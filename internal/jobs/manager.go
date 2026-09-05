// Package jobs runs background work (imports, reindexes, refreshes) on a
// small worker pool with persisted status, progress, and cancellation. It is
// deliberately not a distributed queue; a single process owns it.
package jobs

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

// Status values mirrored by the jobs table CHECK constraint.
const (
	StatusPending   = "pending"
	StatusRunning   = "running"
	StatusDone      = "done"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"
)

// Job is a snapshot of one background task.
type Job struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	BaseID   string `json:"baseId,omitempty"`
	Status   string `json:"status"`
	Progress int    `json:"progress"`
	Total    int    `json:"total"`
	Error    string `json:"error,omitempty"`
}

type task struct {
	job    Job
	ctx    context.Context
	run    func(ctx context.Context, report func(progress int)) error
	cancel context.CancelFunc
}

// Manager owns the worker pool and the job registry.
type Manager struct {
	db      *storage.DB
	mu      sync.Mutex
	tasks   map[string]*task
	queue   chan *task
	workers int
	wg      sync.WaitGroup
}

// New creates a manager with the given worker count.
func New(db *storage.DB, workers int) *Manager {
	if workers < 1 {
		workers = 1
	}
	return &Manager{db: db, tasks: map[string]*task{}, queue: make(chan *task, 256), workers: workers}
}

// Start recovers interrupted jobs, marks stale rows failed, and starts workers.
func (m *Manager) Start(ctx context.Context) error {
	// A previous crash leaves rows running/pending with no live worker;
	// mark them failed so nothing waits forever. Their work is re-derivable
	// (document-level recovery re-queues the actual import state).
	_, err := m.db.Exec(
		`UPDATE jobs SET status = ?, error = ?, updated_at = ? WHERE status IN (?, ?)`,
		StatusFailed, "interrupted by restart", time.Now().UnixMilli(), StatusPending, StatusRunning,
	)
	if err != nil {
		return fmt.Errorf("recover jobs: %w", err)
	}
	for i := 0; i < m.workers; i++ {
		m.wg.Add(1)
		go m.worker(ctx)
	}
	return nil
}

// Stop waits for in-flight tasks (best effort) and drains workers.
func (m *Manager) Stop() {
	close(m.queue)
	m.wg.Wait()
}

// Submit enqueues one task and persists its job row.
func (m *Manager) Submit(kind, baseID string, total int, run func(ctx context.Context, report func(progress int)) error) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	job := Job{ID: id, Kind: kind, BaseID: baseID, Status: StatusPending, Total: total}
	taskCtx, cancel := context.WithCancel(context.Background())
	t := &task{job: job, ctx: taskCtx, run: run, cancel: cancel}
	if err := m.persist(job); err != nil {
		cancel()
		return "", err
	}
	m.mu.Lock()
	m.tasks[id] = t
	m.mu.Unlock()
	select {
	case m.queue <- t:
	default:
		// Queue full: fail the job loudly instead of blocking the caller.
		cancel()
		m.mu.Lock()
		delete(m.tasks, id)
		m.mu.Unlock()
		return "", fmt.Errorf("job queue is full")
	}
	return id, nil
}

func (m *Manager) worker(ctx context.Context) {
	defer m.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case t, ok := <-m.queue:
			if !ok {
				return
			}
			m.runTask(t)
		}
	}
}

func (m *Manager) runTask(t *task) {
	t.job.Status = StatusRunning
	_ = m.persist(t.job)
	report := func(progress int) {
		t.job.Progress = progress
		t.job.Status = StatusRunning
		_ = m.persist(t.job)
	}
	err := t.run(context.WithValue(t.ctx, managerKey{}, m), report)
	switch {
	case err == nil:
		t.job.Status = StatusDone
		t.job.Progress = t.job.Total
	case contextCancelCause(err):
		t.job.Status = StatusCancelled
	default:
		t.job.Status = StatusFailed
		t.job.Error = err.Error()
	}
	_ = m.persist(t.job)
	m.mu.Lock()
	t.cancel()
	delete(m.tasks, t.job.ID)
	m.mu.Unlock()
}

type managerKey struct{}

// FromContext returns the manager embedded in a task context (nil outside jobs).
func FromContext(ctx context.Context) *Manager {
	if m, ok := ctx.Value(managerKey{}).(*Manager); ok {
		return m
	}
	return nil
}

func contextCancelCause(err error) bool {
	if err == nil {
		return false
	}
	return err == context.Canceled || err == context.DeadlineExceeded
}

// Status returns a snapshot of one job.
func (m *Manager) Status(id string) (Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tasks[id]; ok {
		return t.job, true
	}
	var job Job
	var baseID, errText sql.NullString
	var payload []byte
	row := m.db.QueryRow(`SELECT id, kind, base_id, status, progress, total, error, payload FROM jobs WHERE id = ?`, id)
	if err := row.Scan(&job.ID, &job.Kind, &baseID, &job.Status, &job.Progress, &job.Total, &errText, &payload); err != nil {
		return Job{}, false
	}
	job.BaseID = baseID.String
	job.Error = errText.String
	return job, true
}

// Cancel requests cancellation of a running or queued task.
func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	t, ok := m.tasks[id]
	m.mu.Unlock()
	if !ok {
		return false
	}
	t.cancel()
	return true
}

func (m *Manager) persist(job Job) error {
	payload, _ := json.Marshal(map[string]any{})
	var errText any
	if job.Error != "" {
		errText = job.Error
	}
	_, err := m.db.Exec(
		`INSERT INTO jobs (id, kind, base_id, payload, status, progress, total, error, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET status = excluded.status, progress = excluded.progress,
		   total = excluded.total, error = excluded.error, updated_at = excluded.updated_at`,
		job.ID, job.Kind, job.BaseID, payload, job.Status, job.Progress, job.Total, errText,
		time.Now().UnixMilli(), time.Now().UnixMilli(),
	)
	return err
}

func newID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// NewID exposes id generation for other packages (documents, bases, chunks).
func NewID() (string, error) { return newID() }

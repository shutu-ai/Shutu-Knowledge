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
	ID             string `json:"id"`
	Kind           string `json:"kind"`
	BaseID         string `json:"baseId,omitempty"`
	Status         string `json:"status"`
	Progress       int    `json:"progress"`
	Total          int    `json:"total"`
	Phase          string `json:"phase,omitempty"`
	File           string `json:"file,omitempty"`
	CompletedBytes int64  `json:"completedBytes,omitempty"`
	TotalBytes     int64  `json:"totalBytes,omitempty"`
	Error          string `json:"error,omitempty"`
}

// ProgressUpdate carries either a conventional percentage, completed units, or
// byte-level progress from a download-capable subsystem. Completed is used by
// jobs whose total is a count of files/documents; Percent remains for jobs
// whose work is naturally expressed as a percentage.
type ProgressUpdate struct {
	Percent        int
	Completed      int
	Total          int
	Phase          string
	File           string
	CompletedBytes int64
	TotalBytes     int64
}

type persistedProgress struct {
	Phase          string `json:"phase,omitempty"`
	File           string `json:"file,omitempty"`
	CompletedBytes int64  `json:"completedBytes,omitempty"`
	TotalBytes     int64  `json:"totalBytes,omitempty"`
}

type task struct {
	job       Job
	ctx       context.Context
	run       func(ctx context.Context, report func(ProgressUpdate)) error
	cancel    context.CancelFunc
	ioHeavy   bool
	persisted chan error
}

// Manager owns the worker pool and the job registry.
type Manager struct {
	db        *storage.DB
	mu        sync.Mutex
	tasks     map[string]*task
	queue     chan *task
	ioQueue   chan *task
	workers   int
	wg        sync.WaitGroup
	stopOnce  sync.Once
	stopDone  chan struct{}
	onFailure func(kind string)
}

// New creates a manager with the given worker count.
func New(db *storage.DB, workers int) *Manager {
	if workers < 1 {
		workers = 1
	}
	return &Manager{
		db:       db,
		tasks:    map[string]*task{},
		queue:    make(chan *task, 256),
		ioQueue:  make(chan *task, 256),
		workers:  workers,
		stopDone: make(chan struct{}),
	}
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
		go m.worker(ctx, m.queue)
	}
	// Storage-heavy work has its own FIFO queue and one dispatcher. This keeps
	// multiple bases from turning the disk into an unbounded concurrent writer
	// while normal jobs retain the configured worker pool.
	m.wg.Add(1)
	go m.worker(ctx, m.ioQueue)
	return nil
}

// Stop waits for in-flight tasks for a bounded compatibility budget.
func (m *Manager) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	m.StopWithContext(ctx)
}

// StopWithContext cancels accepted tasks, closes the queues once, and waits
// for workers without allowing a non-cooperative legacy task to block process
// shutdown forever. A caller that needs the workers fully drained can retry
// with a later context; the manager retains the same cancellation state.
func (m *Manager) StopWithContext(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	// A task context is independent from the process context so that a request
	// cancellation cannot accidentally cancel accepted work. Shutdown must
	// therefore cancel all accepted tasks before waiting for workers; otherwise
	// a parser or model call can keep the process alive indefinitely.
	m.mu.Lock()
	for _, t := range m.tasks {
		t.cancel()
	}
	m.mu.Unlock()
	m.stopOnce.Do(func() {
		close(m.queue)
		close(m.ioQueue)
		go func() {
			m.wg.Wait()
			close(m.stopDone)
		}()
	})
	select {
	case <-m.stopDone:
	case <-ctx.Done():
	}
}

// Submit enqueues one task and persists its job row.
func (m *Manager) Submit(kind, baseID string, total int, run func(ctx context.Context, report func(progress int)) error) (string, error) {
	return m.SubmitWithProgress(kind, baseID, total, func(ctx context.Context, report func(ProgressUpdate)) error {
		return run(ctx, func(progress int) { report(ProgressUpdate{Percent: progress}) })
	})
}

// SubmitWithProgress enqueues a task whose worker can report phase and
// byte-level progress in addition to the legacy percentage.
func (m *Manager) SubmitWithProgress(kind, baseID string, total int, run func(ctx context.Context, report func(ProgressUpdate)) error) (string, error) {
	return m.submitWithProgress(kind, baseID, total, false, run)
}

// SubmitIOWithProgress enqueues storage-heavy work behind the single IO
// dispatcher. The job remains pending while it waits, so the Web UI can show
// that it is queued instead of implying that disk work has started.
func (m *Manager) SubmitIOWithProgress(kind, baseID string, total int, run func(ctx context.Context, report func(ProgressUpdate)) error) (string, error) {
	return m.submitWithProgress(kind, baseID, total, true, run)
}

func (m *Manager) submitWithProgress(kind, baseID string, total int, ioHeavy bool, run func(ctx context.Context, report func(ProgressUpdate)) error) (string, error) {
	id, err := newID()
	if err != nil {
		return "", err
	}
	job := Job{ID: id, Kind: kind, BaseID: baseID, Status: StatusPending, Total: total}
	if ioHeavy {
		job.Phase = "queued"
	}
	taskCtx, cancel := context.WithCancel(context.Background())
	t := &task{job: job, ctx: taskCtx, run: run, cancel: cancel, ioHeavy: ioHeavy, persisted: make(chan error, 1)}
	m.mu.Lock()
	m.tasks[id] = t
	m.mu.Unlock()
	queue := m.queue
	if ioHeavy {
		queue = m.ioQueue
	}
	select {
	case queue <- t:
	default:
		// Queue full: fail the job loudly instead of blocking the caller.
		cancel()
		m.mu.Lock()
		delete(m.tasks, id)
		m.mu.Unlock()
		return "", fmt.Errorf("job queue is full")
	}
	// Do not make an HTTP caller wait for SQLite's single writer connection.
	// The in-memory task is visible to Status immediately; the worker waits for
	// the durable pending row before it starts work, preserving ordering while
	// allowing a second task to be admitted behind a long write transaction.
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		t.persisted <- m.persist(job)
	}()
	return id, nil
}

func (m *Manager) worker(ctx context.Context, queue <-chan *task) {
	defer m.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case t, ok := <-queue:
			if !ok {
				return
			}
			m.runTask(t)
		}
	}
}

func (m *Manager) runTask(t *task) {
	select {
	case err := <-t.persisted:
		if err != nil {
			m.finishTask(t, fmt.Errorf("persist job: %w", err))
			return
		}
	case <-t.ctx.Done():
		if err := <-t.persisted; err != nil {
			m.finishTask(t, fmt.Errorf("persist job: %w", err))
			return
		}
		m.finishTask(t, t.ctx.Err())
		return
	}
	if err := t.ctx.Err(); err != nil {
		m.finishTask(t, err)
		return
	}
	m.mu.Lock()
	t.job.Status = StatusRunning
	if t.ioHeavy && t.job.Phase == "queued" {
		t.job.Phase = ""
	}
	snapshot := t.job
	m.mu.Unlock()
	_ = m.persist(snapshot)
	report := func(update ProgressUpdate) {
		percent := update.Percent
		if update.TotalBytes > 0 {
			percent = int(float64(update.CompletedBytes) / float64(update.TotalBytes) * 100)
		} else if update.Completed > 0 {
			percent = update.Completed
		}
		m.mu.Lock()
		if update.Completed > 0 {
			t.job.Progress = update.Completed
		} else {
			if percent < 0 {
				percent = 0
			}
			if percent > 100 {
				percent = 100
			}
			t.job.Progress = percent
		}
		if update.Phase != "" {
			t.job.Phase = update.Phase
		}
		if update.File != "" {
			t.job.File = update.File
		}
		if update.TotalBytes > 0 {
			t.job.CompletedBytes = update.CompletedBytes
			t.job.TotalBytes = update.TotalBytes
		} else if update.CompletedBytes > 0 {
			t.job.CompletedBytes = update.CompletedBytes
		}
		if update.Total > 0 {
			t.job.Total = update.Total
		}
		t.job.Status = StatusRunning
		snapshot := t.job
		m.mu.Unlock()
		_ = m.persist(snapshot)
	}
	err := t.run(context.WithValue(t.ctx, managerKey{}, m), report)
	m.finishTask(t, err)
}

func (m *Manager) finishTask(t *task, err error) {
	m.mu.Lock()
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
	snapshot := t.job
	t.cancel()
	delete(m.tasks, t.job.ID)
	m.mu.Unlock()
	_ = m.persist(snapshot)
	if m.onFailure != nil {
		m.onFailure(t.job.Kind)
	}
}

// SetFailureObserver attaches a process-local metrics callback before jobs
// are admitted through the public API.
func (m *Manager) SetFailureObserver(observer func(kind string)) {
	m.onFailure = observer
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
	var progress persistedProgress
	if len(payload) > 0 && json.Unmarshal(payload, &progress) == nil {
		job.Phase = progress.Phase
		job.File = progress.File
		job.CompletedBytes = progress.CompletedBytes
		job.TotalBytes = progress.TotalBytes
	}
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
	payload, _ := json.Marshal(persistedProgress{
		Phase: job.Phase, File: job.File,
		CompletedBytes: job.CompletedBytes, TotalBytes: job.TotalBytes,
	})
	var errText any
	if job.Error != "" {
		errText = job.Error
	}
	_, err := m.db.Exec(
		`INSERT INTO jobs (id, kind, base_id, payload, status, progress, total, error, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET payload = excluded.payload, status = excluded.status, progress = excluded.progress,
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

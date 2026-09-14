package operations

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	sharedscheduler "github.com/shutu-ai/shutu-knowledge/internal/scheduler"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

var (
	ErrNotFound            = errors.New("operation not found")
	ErrUnsupportedCommand  = errors.New("unsupported operation command")
	ErrIdempotencyConflict = errors.New("idempotency key belongs to a different request")
	ErrOperationExpired    = errors.New("operation idempotency binding expired")
	ErrQueueFull           = errors.New("operation queue is full")
	ErrPayloadTooLarge     = errors.New("operation command payload is too large")
)

const (
	DefaultMaxAttempts = 3
	stopTimeout        = 10 * time.Second
)

// testPreDispatchHook is installed only by real process-kill tests. It runs
// after durable claim/admission and immediately before the executor.
var testPreDispatchHook func(Operation)

// SetTestPreDispatchHook installs a package-level process-kill test boundary.
// Production never calls this setter.
func SetTestPreDispatchHook(hook func(Operation)) {
	testPreDispatchHook = hook
}

// Operation is a durable state snapshot exposed to HTTP and Agent callers.
type Operation struct {
	ID                   string          `json:"operationId"`
	Type                 string          `json:"type"`
	CommandSchemaVersion int             `json:"commandSchemaVersion"`
	BaseID               string          `json:"baseId,omitempty"`
	DocumentID           string          `json:"documentId,omitempty"`
	ParentOperationID    string          `json:"parentOperationId,omitempty"`
	State                string          `json:"state"`
	StateRevision        int64           `json:"stateRevision"`
	Phase                string          `json:"phase,omitempty"`
	ResourceClass        string          `json:"resourceClass"`
	Priority             int             `json:"priority"`
	Attempt              int             `json:"attempt"`
	CancelRequested      bool            `json:"cancelRequested"`
	Cancellable          bool            `json:"cancellable"`
	Retryable            bool            `json:"retryable"`
	IdempotencyExpiresAt *int64          `json:"idempotencyExpiresAt,omitempty"`
	CompletedUnits       int             `json:"completedUnits"`
	TotalUnits           *int            `json:"totalUnits,omitempty"`
	CompletedBytes       int64           `json:"completedBytes"`
	TotalBytes           *int64          `json:"totalBytes,omitempty"`
	ErrorCode            string          `json:"errorCode,omitempty"`
	ErrorMessage         string          `json:"errorMessage,omitempty"`
	Result               json.RawMessage `json:"result,omitempty"`
	RequestedAt          int64           `json:"requestedAt"`
	StartedAt            int64           `json:"startedAt,omitempty"`
	FinishedAt           int64           `json:"finishedAt,omitempty"`
}

// Progress is reported in bounded phases by an executor.
type Progress struct {
	Phase          string
	Completed      int
	Total          int
	CompletedBytes int64
	TotalBytes     int64
}

// Request carries a versioned, JSON-serializable command. It has no function
// field: only Register binds a command type to an executable adapter.
type Request struct {
	Type                 string          `json:"type"`
	CommandSchemaVersion int             `json:"commandSchemaVersion"`
	BaseID               string          `json:"baseId"`
	DocumentID           string          `json:"documentId"`
	ParentOperationID    string          `json:"parentOperationId"`
	Payload              json.RawMessage `json:"payload"`
	IdempotencyKey       string          `json:"idempotencyKey"`
	Priority             int             `json:"priority"`
	ResourceClass        string          `json:"resourceClass"`
	TotalUnits           *int            `json:"totalUnits"`
	TotalBytes           *int64          `json:"totalBytes"`
	UploadID             string          `json:"uploadId"`
	// PreallocateDocument lets transport adapters stay idempotent while the
	// service assigns the stable business ID only after de-duplication.
	PreallocateDocument bool `json:"preallocateDocument"`
}

// Executor reconstructs work solely from a persisted operation.
type Executor func(ctx context.Context, op Operation, payload json.RawMessage, report func(Progress)) (any, error)

type activeRun struct {
	cancel context.CancelFunc
}

// ResourceLimits bounds scheduler lanes. Values are explicit and finite so
// imports, maintenance, and model work cannot silently share one global pool.
type ResourceLimits struct {
	IO          int `json:"io"`
	DBWrite     int `json:"dbWrite"`
	Disk        int `json:"disk"`
	Network     int `json:"network"`
	Model       int `json:"model"`
	Maintenance int `json:"maintenance"`
	MaxPerBase  int `json:"maxPerBase"`
}

type scheduler struct {
	limits ResourceLimits
	mu     sync.Mutex
	base   map[string]int
	active map[string]int
}

func newScheduler(limits ResourceLimits) *scheduler {
	limits = normalizeLimits(limits)
	return &scheduler{limits: limits, base: make(map[string]int), active: make(map[string]int)}
}

func schedulerClasses() []string {
	return []string{
		ResourceIO, ResourceDBWrite, ResourceDisk,
		ResourceNetwork, ResourceModel, ResourceMaintenance,
	}
}

func normalizeLimits(limits ResourceLimits) ResourceLimits {
	limits.IO = clampLimit(limits.IO)
	limits.DBWrite = clampLimit(limits.DBWrite)
	limits.Disk = clampLimit(limits.Disk)
	limits.Network = clampLimit(limits.Network)
	limits.Model = clampLimit(limits.Model)
	limits.Maintenance = clampLimit(limits.Maintenance)
	if limits.MaxPerBase < 1 {
		limits.MaxPerBase = 2
	}
	return limits
}

func clampLimit(value int) int {
	if value < 1 {
		return 1
	}
	if value > 64 {
		return 64
	}
	return value
}

func (s *scheduler) reserve(baseID string) bool {
	if baseID == "" {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.base[baseID] >= s.limits.MaxPerBase {
		return false
	}
	s.base[baseID]++
	return true
}

func (s *scheduler) release(baseID string) {
	if baseID == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.base[baseID] > 1 {
		s.base[baseID]--
	} else {
		delete(s.base, baseID)
	}
}

func (s *scheduler) beginRun(resourceClass string) {
	s.mu.Lock()
	s.active[resourceClass]++
	s.mu.Unlock()
}

func (s *scheduler) endRun(resourceClass string) {
	s.mu.Lock()
	if s.active[resourceClass] > 1 {
		s.active[resourceClass]--
	} else {
		delete(s.active, resourceClass)
	}
	s.mu.Unlock()
}

func (s *scheduler) snapshot(queued map[string]int) SchedulerSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	lanes := make(map[string]LaneSnapshot, len(s.active))
	queuedTotal := 0
	activeTotal := 0
	for _, class := range schedulerClasses() {
		queuedCount := queued[class]
		queuedTotal += queuedCount
		activeTotal += s.active[class]
		lanes[class] = LaneSnapshot{Limit: laneLimit(s.limits, class), Active: s.active[class], Queued: queuedCount}
	}
	activeBases := make(map[string]int, len(s.base))
	for base, count := range s.base {
		activeBases[base] = count
	}
	return SchedulerSnapshot{
		Limits: s.limits, Lanes: lanes,
		ActiveBases: activeBases, ActiveTotal: activeTotal, QueuedTotal: queuedTotal,
		MaxPerBase: s.limits.MaxPerBase,
	}
}

func laneLimit(limits ResourceLimits, class string) int {
	switch class {
	case ResourceDBWrite:
		return limits.DBWrite
	case ResourceDisk:
		return limits.Disk
	case ResourceNetwork:
		return limits.Network
	case ResourceModel:
		return limits.Model
	case ResourceMaintenance:
		return limits.Maintenance
	default:
		return limits.IO
	}
}

// LaneSnapshot reports one finite resource pool. Limit is the configured
// concurrency; Active is the number of operations currently holding it.
type LaneSnapshot struct {
	Limit  int `json:"limit"`
	Active int `json:"active"`
	Queued int `json:"queued"`
}

// SchedulerSnapshot is a consistent admission/scheduling view for health and
// capacity acceptance. Queue counts are approximate under concurrent claim.
type SchedulerSnapshot struct {
	Limits      ResourceLimits          `json:"limits"`
	Lanes       map[string]LaneSnapshot `json:"lanes"`
	ActiveBases map[string]int          `json:"activeBases"`
	ActiveTotal int                     `json:"activeTotal"`
	QueuedTotal int                     `json:"queuedTotal"`
	QueueLimit  int                     `json:"queueLimit"`
	MaxPerBase  int                     `json:"maxPerBase"`
}

// Service owns the durable queue and process-local executor registry.
type Service struct {
	db             *storage.DB
	instanceID     string
	workers        int
	maxPending     int
	maxPayload     int
	uploadRoot     string
	idempotencyMS  int64
	limits         ResourceLimits
	scheduler      *scheduler
	modelAdmission sharedscheduler.Admission

	mu       sync.RWMutex
	registry map[string]Executor
	active   map[string]*activeRun
	wakes    map[string]chan struct{}
	acceptMu sync.Mutex
	startMu  sync.Mutex
	started  bool
	wg       sync.WaitGroup
	stopOnce sync.Once
	stop     chan struct{}
}

// New creates an operation service. Executors must be registered before Start.
func New(db *storage.DB, workers int) (*Service, error) {
	return NewWithUploadRoot(db, workers, "")
}

// NewWithUploadRoot enables durable upload staging. An empty root keeps the
// queue usable for tests and text-only deployments.
func NewWithUploadRoot(db *storage.DB, workers int, root string) (*Service, error) {
	if workers < 1 {
		workers = 1
	}
	id, err := newID()
	if err != nil {
		return nil, err
	}
	defaults := ResourceLimits{
		IO: workers, DBWrite: 1, Disk: 1, Network: 1,
		Model: 1, Maintenance: 1, MaxPerBase: 2,
	}
	service := &Service{
		db:            db,
		instanceID:    id,
		workers:       workers,
		maxPending:    1000,
		maxPayload:    1 << 20,
		uploadRoot:    root,
		idempotencyMS: int64(DefaultIdempotencyRetentionHours) * int64(time.Hour.Milliseconds()),
		limits:        defaults,
		registry:      map[string]Executor{},
		active:        map[string]*activeRun{},
		wakes:         make(map[string]chan struct{}),
		stop:          make(chan struct{}),
	}
	for _, class := range schedulerClasses() {
		service.wakes[class] = make(chan struct{}, 64)
	}
	service.scheduler = newScheduler(defaults)
	if err := service.ensureIdempotencySigningKey(context.Background()); err != nil {
		return nil, err
	}
	return service, nil
}

// SetResourceLimits configures lanes before Start so database, disk, model,
// and network work have independent, finite concurrency budgets.
func (s *Service) SetResourceLimits(limits ResourceLimits) {
	s.limits = normalizeLimits(limits)
	s.scheduler = newScheduler(s.limits)
}

// SetQueueLimit bounds durable queued/running commands accepted by Submit.
func (s *Service) SetQueueLimit(limit int) {
	if limit < 1 {
		limit = 1
	}
	if limit > 100000 {
		limit = 100000
	}
	s.maxPending = limit
}

// SetIdempotencyRetentionHours bounds how long a terminal response remains
// addressable by its idempotency key. Zero retains it indefinitely.
func (s *Service) SetIdempotencyRetentionHours(hours int) {
	if hours < 0 {
		hours = 0
	}
	s.idempotencyMS = int64(hours) * int64(time.Hour.Milliseconds())
}

// SetModelAdmission shares the process-wide model budget with interactive
// retrieval. Durable model operations acquire it after their durable claim.
func (s *Service) SetModelAdmission(admission sharedscheduler.Admission) {
	s.modelAdmission = admission
}

// Register binds a command type to its replayable adapter.
func (s *Service) Register(operationType string, executor Executor) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.registry[operationType] = executor
}

// Start reconciles records left by a dead process and starts dispatch workers.
func (s *Service) Start(ctx context.Context) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()
	if s.started {
		return fmt.Errorf("operation service is already started")
	}
	s.started = true
	if err := s.recover(ctx); err != nil {
		s.started = false
		return fmt.Errorf("recover operations: %w", err)
	}
	limits := s.scheduler.limits
	for _, class := range schedulerClasses() {
		count := limits.IO
		switch class {
		case ResourceDBWrite:
			count = limits.DBWrite
		case ResourceDisk:
			count = limits.Disk
		case ResourceNetwork:
			count = limits.Network
		case ResourceModel:
			count = limits.Model
		case ResourceMaintenance:
			count = limits.Maintenance
		}
		for range count {
			s.wg.Add(1)
			go s.worker(ctx, class)
		}
	}
	return nil
}

// Stop cancels local runs and waits for workers. If the executor does not
// return in time, the running row remains for the next-start reconciler.
func (s *Service) Stop() {
	s.StopWithContext(context.Background())
}

// StopWithContext bounds process shutdown so a stuck executor cannot keep the
// application alive forever. Rows left running are recovered on next start.
func (s *Service) StopWithContext(ctx context.Context) {
	s.stopOnce.Do(func() { close(s.stop) })
	s.mu.Lock()
	for _, run := range s.active {
		run.cancel()
	}
	s.mu.Unlock()
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	case <-time.After(stopTimeout):
	}
}

func (s *Service) recover(ctx context.Context) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`UPDATE operations SET state = ?, error_code = 'interrupted',
		error_message = 'worker lost during process restart', state_revision = state_revision + 1,
		finished_at = ?, updated_at = ?
		WHERE state IN ('running', 'cancelling')`,
		StateInterrupted, now(), now()); err != nil {
		return err
	}
	// A persisted cancel intent never re-runs the original business command.
	cancelRows, err := tx.Query(`SELECT id, state_revision FROM operations
		WHERE state = ? AND cancel_requested = 1`, StateInterrupted)
	if err != nil {
		return err
	}
	var cancelled []struct {
		id       string
		revision int64
	}
	for cancelRows.Next() {
		var item struct {
			id       string
			revision int64
		}
		if err := cancelRows.Scan(&item.id, &item.revision); err != nil {
			_ = cancelRows.Close()
			return err
		}
		cancelled = append(cancelled, item)
	}
	if err := cancelRows.Err(); err != nil {
		_ = cancelRows.Close()
		return err
	}
	_ = cancelRows.Close()
	for _, item := range cancelled {
		if _, err := tx.Exec(`UPDATE operations SET state = ?, state_revision = ?,
			finished_at = ?, updated_at = ? WHERE id = ? AND state = ? AND cancel_requested = 1`,
			StateCancelled, item.revision+1, now(), now(), item.id, StateInterrupted); err != nil {
			return err
		}
		if err := insertEvent(ctx, tx, item.id, item.revision+1, "finished", map[string]any{
			"state": StateCancelled, "errorCode": "cancelled",
		}); err != nil {
			return err
		}
	}
	rows, err := tx.Query(`SELECT id, type, attempt, retryable FROM operations WHERE state = ?`, StateInterrupted)
	if err != nil {
		return err
	}
	var replay []string
	for rows.Next() {
		var id, operationType string
		var attempt int
		var retryable bool
		if err := rows.Scan(&id, &operationType, &attempt, &retryable); err != nil {
			_ = rows.Close()
			return err
		}
		s.mu.RLock()
		_, supported := s.registry[operationType]
		s.mu.RUnlock()
		if supported && retryable && attempt < DefaultMaxAttempts {
			replay = append(replay, id)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for _, id := range replay {
		if _, err := tx.Exec(`UPDATE operations SET state = ?, state_revision = state_revision + 1,
			error_code = NULL, error_message = NULL, finished_at = NULL, updated_at = ? WHERE id = ?`,
			StateQueued, now(), id); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := s.expireUploads(ctx); err != nil {
		return err
	}
	if err := s.cleanupUploadTemps(); err != nil {
		return err
	}
	if err := s.ExpireURLCaptures(ctx); err != nil {
		return err
	}
	s.wakeupAll()
	return nil
}

func (s *Service) expireUploads(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM upload_sessions
		WHERE state IN ('uploading','complete') AND expires_at < ?`, now())
	if err != nil {
		return err
	}
	var expired []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		expired = append(expired, id)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()
	for _, id := range expired {
		if _, err := s.db.Exec(`UPDATE upload_sessions SET state = ?, updated_at = ?
			WHERE id = ? AND state IN ('uploading','complete')`, UploadStateExpired, now(), id); err != nil {
			return err
		}
		if path, err := s.uploadPath(id); err == nil {
			_ = os.Remove(path)
		}
	}
	return nil
}

// cleanupUploadTemps removes partial staging temporaries orphaned by a killed
// process. It runs only during startup ownership recovery, before this process
// can create new upload temps.
func (s *Service) cleanupUploadTemps() error {
	if s.uploadRoot == "" {
		return nil
	}
	stale, err := filepath.Glob(filepath.Join(s.uploadRoot, ".*.tmp-*"))
	if err != nil {
		return fmt.Errorf("scan upload staging temps: %w", err)
	}
	for _, path := range stale {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove upload staging temp: %w", err)
		}
	}
	return nil
}

// Submit persists before returning. A lost response can be retried with the
// same idempotency key; this returns the original operation rather than
// creating duplicate work.
func (s *Service) Submit(ctx context.Context, req Request) (Operation, error) {
	if req.CommandSchemaVersion != CommandSchemaV1 {
		return Operation{}, fmt.Errorf("%w: schema %d", ErrUnsupportedCommand, req.CommandSchemaVersion)
	}
	if len(req.Payload) > s.maxPayload {
		return Operation{}, fmt.Errorf("%w (%d > %d bytes)", ErrPayloadTooLarge, len(req.Payload), s.maxPayload)
	}
	if req.ResourceClass == "" {
		req.ResourceClass = "io"
	}
	s.mu.RLock()
	_, supported := s.registry[req.Type]
	s.mu.RUnlock()
	if !supported {
		return Operation{}, fmt.Errorf("%w: %s", ErrUnsupportedCommand, req.Type)
	}
	fingerprint := requestFingerprint(req.Type, req.CommandSchemaVersion, req.Payload)
	req.Priority = schedulingPriority(req.Type, req.Priority)
	// SQLite enforces the same uniqueness invariant across processes. Within
	// this process, serializing the short check-and-insert transaction avoids
	// exposing the raw constraint race to a valid duplicate retry.
	s.acceptMu.Lock()
	defer s.acceptMu.Unlock()
	// An existing key wins before admission and upload lifecycle checks: a
	// retry after response loss must recover the original command even if the
	// queue later filled or the bound upload was released at terminal state.
	if req.IdempotencyKey != "" {
		if op, err := scanOperation(s.db.QueryRow(`SELECT `+operationColumns+` FROM operations WHERE idempotency_key = ?`, req.IdempotencyKey)); err == nil {
			if op.IdempotencyExpiresAt != nil && *op.IdempotencyExpiresAt <= now() {
				return Operation{}, ErrOperationExpired
			}
			var stored []byte
			if err := s.db.QueryRow(`SELECT command_payload FROM operations WHERE id = ?`, op.ID).Scan(&stored); err != nil {
				return Operation{}, err
			}
			if op.Type != req.Type || op.CommandSchemaVersion != req.CommandSchemaVersion ||
				requestFingerprint(op.Type, op.CommandSchemaVersion, stored) != fingerprint ||
				op.BaseID != req.BaseID || op.ParentOperationID != req.ParentOperationID ||
				(req.DocumentID != "" && op.DocumentID != req.DocumentID) {
				return Operation{}, ErrIdempotencyConflict
			}
			return op, nil
		} else if err != sql.ErrNoRows {
			return Operation{}, err
		}
	}
	if req.UploadID != "" {
		upload, err := s.GetUpload(req.UploadID)
		if err != nil {
			return Operation{}, err
		}
		if upload.BaseID != req.BaseID {
			return Operation{}, ErrUploadScopeMismatch
		}
		if upload.State != UploadStateComplete && upload.State != UploadStateBound {
			return Operation{}, ErrUploadNotComplete
		}
	}
	if req.PreallocateDocument {
		documentID, err := newID()
		if err != nil {
			return Operation{}, err
		}
		req.DocumentID = documentID
	}
	id, err := newID()
	if err != nil {
		return Operation{}, err
	}
	requestedAt := now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Operation{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var pending int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM operations WHERE state IN ('queued','running','cancelling')`).Scan(&pending); err != nil {
		return Operation{}, err
	}
	if pending >= s.maxPending {
		return Operation{}, ErrQueueFull
	}
	if _, err := tx.Exec(`INSERT INTO operations
		(id, type, command_schema_version, command_payload, request_fingerprint, idempotency_key,
		 base_id, document_id, parent_operation_id, state, state_revision, resource_class, priority,
		 total_units, total_bytes, requested_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?)`,
		id, req.Type, req.CommandSchemaVersion, string(req.Payload), fingerprint, nullString(req.IdempotencyKey),
		nullString(req.BaseID), nullString(req.DocumentID), nullString(req.ParentOperationID), StateQueued,
		req.ResourceClass, req.Priority, req.TotalUnits, req.TotalBytes, requestedAt, requestedAt); err != nil {
		return Operation{}, err
	}
	if req.UploadID != "" {
		result, err := tx.Exec(`UPDATE upload_sessions SET state = ?, operation_id = ?, updated_at = ?
			WHERE id = ? AND base_id = ? AND state = ? AND (operation_id IS NULL OR operation_id = ?)`,
			UploadStateBound, id, requestedAt, req.UploadID, req.BaseID, UploadStateComplete, id)
		if err != nil {
			return Operation{}, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return Operation{}, err
		}
		if affected != 1 {
			return Operation{}, ErrUploadNotComplete
		}
	}
	if err := insertEvent(ctx, tx, id, 1, "submitted", map[string]any{"fingerprint": fingerprint}); err != nil {
		return Operation{}, err
	}
	if err := tx.Commit(); err != nil {
		return Operation{}, err
	}
	s.wakeupClass(req.ResourceClass)
	return s.Get(id)
}

// FindIdempotencyOperation lets adapters check an existing key before costly
// or state-dependent admission validation. Submit performs the same checks as
// the authoritative path when no operation is found.
func (s *Service) FindIdempotencyOperation(req Request) (Operation, bool, error) {
	if req.IdempotencyKey == "" {
		return Operation{}, false, nil
	}
	fingerprint := requestFingerprint(req.Type, req.CommandSchemaVersion, req.Payload)
	op, err := scanOperation(s.db.QueryRow(
		`SELECT `+operationColumns+` FROM operations WHERE idempotency_key = ?`, req.IdempotencyKey))
	if err == sql.ErrNoRows {
		return Operation{}, false, nil
	}
	if err != nil {
		return Operation{}, false, err
	}
	if op.IdempotencyExpiresAt != nil && *op.IdempotencyExpiresAt <= now() {
		return Operation{}, false, ErrOperationExpired
	}
	var stored []byte
	if err := s.db.QueryRow(`SELECT command_payload FROM operations WHERE id = ?`, op.ID).Scan(&stored); err != nil {
		return Operation{}, false, err
	}
	if op.Type != req.Type || op.CommandSchemaVersion != req.CommandSchemaVersion ||
		requestFingerprint(op.Type, op.CommandSchemaVersion, stored) != fingerprint ||
		op.BaseID != req.BaseID || op.ParentOperationID != req.ParentOperationID ||
		(req.DocumentID != "" && op.DocumentID != req.DocumentID) {
		return Operation{}, false, ErrIdempotencyConflict
	}
	return op, true, nil
}

// Scheduler returns a bounded capacity snapshot. It is intended for health,
// load tests, and acceptance metrics rather than a task-list replacement.
func (s *Service) Scheduler() (SchedulerSnapshot, error) {
	rows, err := s.db.Query(`SELECT resource_class, COUNT(*) FROM operations
		WHERE state = ? GROUP BY resource_class`, StateQueued)
	if err != nil {
		return SchedulerSnapshot{}, err
	}
	defer rows.Close()
	queued := make(map[string]int)
	for rows.Next() {
		var class string
		var count int
		if err := rows.Scan(&class, &count); err != nil {
			return SchedulerSnapshot{}, err
		}
		queued[class] = count
	}
	if err := rows.Err(); err != nil {
		return SchedulerSnapshot{}, err
	}
	snapshot := s.scheduler.snapshot(queued)
	snapshot.QueueLimit = s.maxPending
	return snapshot, nil
}

func (s *Service) worker(ctx context.Context, resourceClass string) {
	defer s.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stop:
			return
		case <-s.wakes[resourceClass]:
		}
		for {
			found, err := s.claimNext(ctx, resourceClass)
			if err != nil || !found {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-s.stop:
				return
			default:
			}
		}
	}
}

func (s *Service) wakeupClass(resourceClass string) {
	wake, ok := s.wakes[resourceClass]
	if !ok {
		wake = s.wakes[ResourceIO]
	}
	select {
	case wake <- struct{}{}:
	default:
	}
}

func (s *Service) wakeupAll() {
	for _, wake := range s.wakes {
		// Recovery can restore one command per configured worker. A single
		// token would wake only the first worker; because claims run
		// synchronously, the second worker could wait while another recovered
		// command stays queued.
		for range 64 {
			select {
			case wake <- struct{}{}:
				continue
			default:
			}
		}
	}
}

func (s *Service) claimNext(ctx context.Context, resourceClass string) (bool, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.Query(`SELECT id, COALESCE(base_id, '') FROM operations
		WHERE state = ? AND resource_class = ?
		ORDER BY priority DESC, requested_at, id LIMIT 32`, StateQueued, resourceClass)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	var id, baseID string
	for rows.Next() {
		var candidateID, candidateBase string
		if err := rows.Scan(&candidateID, &candidateBase); err != nil {
			return false, err
		}
		if !s.scheduler.reserve(candidateBase) {
			continue
		}
		id, baseID = candidateID, candidateBase
		break
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	_ = rows.Close()
	if id == "" {
		return false, nil
	}
	// If the conditional claim loses a race with another process owner, the
	// reservation is handed back so later candidates remain available.
	claimed := false
	defer func() {
		if !claimed {
			s.scheduler.release(baseID)
		}
	}()
	var revision int64
	if err := tx.QueryRow(`SELECT state_revision FROM operations WHERE id = ? AND state = ?`,
		id, StateQueued).Scan(&revision); err != nil {
		return false, err
	}
	result, err := tx.Exec(`UPDATE operations SET state = ?, attempt = attempt + 1,
		state_revision = ?, instance_id = ?, started_at = ?, updated_at = ?
		WHERE id = ? AND state = ?`, StateRunning, revision+1, s.instanceID, now(), now(), id, StateQueued)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected != 1 {
		return true, nil
	}
	if err := insertEvent(ctx, tx, id, revision+1, "claimed", map[string]any{"instanceId": s.instanceID}); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	claimed = true
	// Each dispatcher owns its claimed operation synchronously, so the worker
	// count is the actual concurrency limit rather than just a wake-up count.
	s.run(id)
	return true, nil
}

func (s *Service) run(id string) {
	op, err := s.Get(id)
	if err != nil {
		return
	}
	defer s.scheduler.release(op.BaseID)
	s.scheduler.beginRun(op.ResourceClass)
	defer s.scheduler.endRun(op.ResourceClass)
	s.mu.RLock()
	executor := s.registry[op.Type]
	s.mu.RUnlock()
	if executor == nil {
		_ = s.finish(id, op.Attempt, nil, ErrUnsupportedCommand)
		return
	}
	if testPreDispatchHook != nil {
		testPreDispatchHook(op)
	}
	var payloadBytes []byte
	if err := s.db.QueryRow(`SELECT command_payload FROM operations WHERE id = ?`, id).Scan(&payloadBytes); err != nil {
		_ = s.finish(id, op.Attempt, nil, err)
		return
	}
	payload := json.RawMessage(payloadBytes)
	runCtx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.active[id] = &activeRun{cancel: cancel}
	s.mu.Unlock()
	if op.ResourceClass == ResourceModel && s.modelAdmission != nil {
		releaseAdmission, acquireErr := s.modelAdmission.Acquire(runCtx)
		if acquireErr != nil {
			s.mu.Lock()
			delete(s.active, id)
			s.mu.Unlock()
			cancel()
			_ = s.finish(id, op.Attempt, nil, acquireErr)
			return
		}
		defer releaseAdmission()
	}
	result, runErr := executor(runCtx, op, payload, func(progress Progress) {
		_ = s.report(id, op.Attempt, progress)
	})
	s.mu.Lock()
	delete(s.active, id)
	s.mu.Unlock()
	cancel()
	_ = s.finish(id, op.Attempt, result, runErr)
	if final, err := s.Get(id); err == nil && (final.State == StateSucceeded || final.State == StateCancelled) {
		_ = s.releaseUpload(id)
	}
}

func (s *Service) report(id string, attempt int, progress Progress) error {
	completed := progress.Completed
	var totalUnits any
	if progress.Total > 0 {
		totalUnits = progress.Total
	} else {
		completed = 0
	}
	var totalBytes any
	if progress.TotalBytes > 0 {
		totalBytes = progress.TotalBytes
	}
	_, err := s.db.Exec(`UPDATE operations SET phase = ?, completed_units = ?, total_units = ?,
		completed_bytes = ?, total_bytes = ?, state_revision = state_revision + 1, updated_at = ?
		WHERE id = ? AND state IN ('running','cancelling') AND attempt = ?`,
		progress.Phase, completed, totalUnits, progress.CompletedBytes, totalBytes, now(), id, attempt)
	return err
}

func (s *Service) finish(id string, attempt int, result any, runErr error) error {
	state := StateSucceeded
	errorCode := ""
	errorMessage := ""
	if runErr != nil {
		state = StateFailed
		switch {
		case errors.Is(runErr, context.Canceled), errors.Is(runErr, context.DeadlineExceeded):
			var requested int
			_ = s.db.QueryRow(`SELECT cancel_requested FROM operations WHERE id = ?`, id).Scan(&requested)
			if requested == 1 {
				state = StateCancelled
				errorCode = "cancelled"
				errorMessage = "operation was cancelled"
			} else {
				state = StateInterrupted
				errorCode = "interrupted"
				errorMessage = "worker stopped before completion"
			}
		default:
			errorCode = "operation_failed"
			errorMessage = runErr.Error()
		}
	}
	var resultText any
	if result != nil {
		encoded, err := json.Marshal(result)
		if err != nil {
			state = StateFailed
			errorCode = "result_encode_failed"
			errorMessage = err.Error()
		} else {
			resultText = string(encoded)
		}
	}
	finished := now()
	var idempotencyExpiry any
	if terminal(state) && s.idempotencyMS > 0 {
		idempotencyExpiry = finished + s.idempotencyMS
	}
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var revision int64
	if err := tx.QueryRow(`SELECT state_revision FROM operations
		WHERE id = ? AND state IN (?, ?) AND attempt = ?`,
		id, StateRunning, StateCancelling, attempt).Scan(&revision); err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		return err
	}
	if _, err := tx.Exec(`UPDATE operations SET state = ?, state_revision = ?,
		error_code = ?, error_message = ?, result = ?, finished_at = ?, updated_at = ?,
		idempotency_expires_at = ?
		WHERE id = ? AND state IN (?, ?) AND attempt = ?`,
		state, revision+1, errorCode, errorMessage, resultText, finished, finished,
		idempotencyExpiry, id, StateRunning, StateCancelling, attempt); err != nil {
		return err
	}
	if err := insertEvent(context.Background(), tx, id, revision+1, "finished", map[string]any{
		"state": state, "errorCode": errorCode,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func now() int64 { return time.Now().UnixMilli() }

func newID() (string, error) {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("generate operation id: %w", err)
	}
	return hex.EncodeToString(buf[:]), nil
}

// NewID exposes the operation package's random 128-bit identifier to adapters
// that must preallocate business resource IDs before persistence.
func NewID() (string, error) { return newID() }

func requestFingerprint(operationType string, schema int, payload []byte) string {
	seed := fmt.Sprintf("%s\x00%d\x00", operationType, schema)
	sum := sha256.Sum256(append([]byte(seed), payload...))
	return hex.EncodeToString(sum[:])
}

// schedulingPriority keeps destructive cleanup ahead of ordinary rebuild work
// while pushing low-value maintenance behind interactive commands.
func schedulingPriority(operationType string, priority int) int {
	switch {
	case strings.HasPrefix(operationType, "delete_"):
		if priority < 100 {
			return 100
		}
	case strings.HasPrefix(operationType, "maintenance_"):
		if priority > -100 {
			return -100
		}
	}
	return priority
}

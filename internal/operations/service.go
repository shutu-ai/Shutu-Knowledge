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
	ErrNotFound               = errors.New("operation not found")
	ErrUnsupportedCommand     = errors.New("unsupported operation command")
	ErrIdempotencyConflict    = errors.New("idempotency key belongs to a different request")
	ErrOperationExpired       = errors.New("operation idempotency binding expired")
	ErrQueueFull              = errors.New("operation queue is full")
	ErrPayloadTooLarge        = errors.New("operation command payload is too large")
	ErrResourceBudgetExceeded = errors.New("operation resource budget exceeds configured limit")
	ErrDiskLowWater           = errors.New("database volume is below the configured disk low-water mark")
)

const (
	DefaultMaxAttempts = 3
	stopTimeout        = 10 * time.Second
	priorityAgingMS    = int64(time.Second / time.Millisecond)
	cleanupBatchSize   = 100
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
	ID                    string          `json:"operationId"`
	Type                  string          `json:"type"`
	CommandSchemaVersion  int             `json:"commandSchemaVersion"`
	BaseID                string          `json:"baseId,omitempty"`
	DocumentID            string          `json:"documentId,omitempty"`
	ParentOperationID     string          `json:"parentOperationId,omitempty"`
	PrincipalRef          string          `json:"principalRef,omitempty"`
	ScopeRef              string          `json:"scopeRef,omitempty"`
	InputRef              string          `json:"inputRef,omitempty"`
	InputSHA256           string          `json:"inputSha256,omitempty"`
	SourceVersion         string          `json:"sourceVersion,omitempty"`
	ConfigSnapshotRef     string          `json:"configSnapshotRef,omitempty"`
	ModelSnapshotRef      string          `json:"modelSnapshotRef,omitempty"`
	ExpectedTargetEpoch   *int64          `json:"expectedTargetEpoch,omitempty"`
	ExpectedAncestorEpoch *int64          `json:"expectedAncestorEpoch,omitempty"`
	AllocatedDocumentID   string          `json:"allocatedDocumentId,omitempty"`
	AllocatedGeneration   *int64          `json:"allocatedGeneration,omitempty"`
	State                 string          `json:"state"`
	StateRevision         int64           `json:"stateRevision"`
	Phase                 string          `json:"phase,omitempty"`
	ResourceClass         string          `json:"resourceClass"`
	Priority              int             `json:"priority"`
	Attempt               int             `json:"attempt"`
	CancelRequested       bool            `json:"cancelRequested"`
	Cancellable           bool            `json:"cancellable"`
	Retryable             bool            `json:"retryable"`
	MemoryBytes           int64           `json:"memoryBytes"`
	DiskBytes             int64           `json:"diskBytes"`
	TempBytes             int64           `json:"tempBytes"`
	NextAttemptAt         *int64          `json:"nextAttemptAt,omitempty"`
	RecoveryBasis         string          `json:"recoveryBasis,omitempty"`
	ResultRef             string          `json:"resultRef,omitempty"`
	ResultExpiresAt       *int64          `json:"resultExpiresAt,omitempty"`
	RetainedUntil         *int64          `json:"retainedUntil,omitempty"`
	IdempotencyExpiresAt  *int64          `json:"idempotencyExpiresAt,omitempty"`
	CompletedUnits        int             `json:"completedUnits"`
	TotalUnits            *int            `json:"totalUnits,omitempty"`
	CompletedBytes        int64           `json:"completedBytes"`
	TotalBytes            *int64          `json:"totalBytes,omitempty"`
	ErrorCode             string          `json:"errorCode,omitempty"`
	ErrorMessage          string          `json:"errorMessage,omitempty"`
	Result                json.RawMessage `json:"result,omitempty"`
	RequestedAt           int64           `json:"requestedAt"`
	StartedAt             int64           `json:"startedAt,omitempty"`
	FinishedAt            int64           `json:"finishedAt,omitempty"`
	QueueWaitMS           int64           `json:"queueWaitMs"`
	RunTimeMS             int64           `json:"runTimeMs"`
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
	Type                  string          `json:"type"`
	CommandSchemaVersion  int             `json:"commandSchemaVersion"`
	BaseID                string          `json:"baseId"`
	DocumentID            string          `json:"documentId"`
	ParentOperationID     string          `json:"parentOperationId"`
	PrincipalRef          string          `json:"principalRef"`
	ScopeRef              string          `json:"scopeRef"`
	InputRef              string          `json:"inputRef"`
	InputSHA256           string          `json:"inputSha256"`
	SourceVersion         string          `json:"sourceVersion"`
	ConfigSnapshotRef     string          `json:"configSnapshotRef"`
	ModelSnapshotRef      string          `json:"modelSnapshotRef"`
	ExpectedTargetEpoch   *int64          `json:"expectedTargetEpoch"`
	ExpectedAncestorEpoch *int64          `json:"expectedAncestorEpoch"`
	AllocatedGeneration   *int64          `json:"allocatedGeneration"`
	Payload               json.RawMessage `json:"payload"`
	IdempotencyKey        string          `json:"idempotencyKey"`
	Priority              int             `json:"priority"`
	ResourceClass         string          `json:"resourceClass"`
	TotalUnits            *int            `json:"totalUnits"`
	TotalBytes            *int64          `json:"totalBytes"`
	MemoryBytes           int64           `json:"memoryBytes"`
	DiskBytes             int64           `json:"diskBytes"`
	TempBytes             int64           `json:"tempBytes"`
	UploadID              string          `json:"uploadId"`
	// PreallocateDocument lets transport adapters stay idempotent while the
	// service assigns the stable business ID only after de-duplication.
	PreallocateDocument bool `json:"preallocateDocument"`
}

// Executor reconstructs work solely from a persisted operation.
type Executor func(ctx context.Context, op Operation, payload json.RawMessage, report func(Progress)) (any, error)

// RequestEnricher fills immutable, non-secret replay metadata at the durable
// admission boundary. It runs only for a new idempotency binding; retries of
// an existing key must return the original operation even if its target has
// since been deleted or its current configuration has changed.
type RequestEnricher func(context.Context, Request) (Request, error)

type activeRun struct {
	cancel context.CancelFunc
}

// ResourceLimits bounds scheduler lanes. Values are explicit and finite so
// imports, maintenance, and model work cannot silently share one global pool.
type ResourceLimits struct {
	IO                int   `json:"io"`
	DBWrite           int   `json:"dbWrite"`
	Disk              int   `json:"disk"`
	Network           int   `json:"network"`
	Model             int   `json:"model"`
	Maintenance       int   `json:"maintenance"`
	MaxPerBase        int   `json:"maxPerBase"`
	MemoryBytes       int64 `json:"memoryBytes"`
	DiskBytes         int64 `json:"diskBytes"`
	DiskLowWaterBytes int64 `json:"diskLowWaterBytes"`
	UploadBytes       int64 `json:"uploadBytes"`
	TempBytes         int64 `json:"tempBytes"`
}

// ResourceBudget is the declared peak footprint reserved while an operation
// is executing. It is persisted with the operation so a retry uses the same
// admission contract instead of silently changing shape after restart.
type ResourceBudget struct {
	MemoryBytes int64 `json:"memoryBytes"`
	DiskBytes   int64 `json:"diskBytes"`
	TempBytes   int64 `json:"tempBytes"`
}

func (o Operation) ResourceBudget() ResourceBudget {
	return ResourceBudget{MemoryBytes: o.MemoryBytes, DiskBytes: o.DiskBytes, TempBytes: o.TempBytes}
}

type scheduler struct {
	limits      ResourceLimits
	mu          sync.Mutex
	base        map[string]int
	active      map[string]int
	memoryBytes int64
	diskBytes   int64
	tempBytes   int64
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
	if limits.MemoryBytes <= 0 {
		limits.MemoryBytes = 2 << 30
	}
	if limits.MemoryBytes > 100<<30 {
		limits.MemoryBytes = 100 << 30
	}
	if limits.DiskBytes <= 0 {
		limits.DiskBytes = 10 << 30
	}
	if limits.DiskBytes > 100<<30 {
		limits.DiskBytes = 100 << 30
	}
	if limits.DiskLowWaterBytes <= 0 {
		limits.DiskLowWaterBytes = 256 << 20
	}
	if limits.DiskLowWaterBytes > 100<<30 {
		limits.DiskLowWaterBytes = 100 << 30
	}
	if limits.UploadBytes <= 0 {
		limits.UploadBytes = 512 << 20
	}
	if limits.UploadBytes > 100<<30 {
		limits.UploadBytes = 100 << 30
	}
	if limits.TempBytes <= 0 {
		limits.TempBytes = 1 << 30
	}
	if limits.TempBytes > 100<<30 {
		limits.TempBytes = 100 << 30
	}
	return limits
}

const (
	defaultOperationMemoryBytes = 128 << 20
	defaultOperationDiskBytes   = 256 << 20
	defaultOperationTempBytes   = 64 << 20
)

func defaultResourceBudget(resourceClass string, totalBytes *int64) ResourceBudget {
	budget := ResourceBudget{
		MemoryBytes: defaultOperationMemoryBytes,
		DiskBytes:   defaultOperationDiskBytes,
		TempBytes:   defaultOperationTempBytes,
	}
	switch resourceClass {
	case ResourceModel:
		budget.MemoryBytes = 512 << 20
		budget.DiskBytes = 1 << 30
		budget.TempBytes = 256 << 20
	case ResourceMaintenance:
		budget.MemoryBytes = 64 << 20
		budget.DiskBytes = 512 << 20
		budget.TempBytes = 128 << 20
	}
	if totalBytes != nil && *totalBytes > 0 {
		if scaled, ok := safeDouble(*totalBytes); ok && scaled > budget.DiskBytes {
			budget.DiskBytes = minInt64(scaled, 1<<30)
		}
		if *totalBytes > budget.TempBytes {
			budget.TempBytes = minInt64(*totalBytes, 512<<20)
		}
	}
	return budget
}

func safeDouble(value int64) (int64, bool) {
	if value > (int64(^uint64(0)>>1) / 2) {
		return 0, false
	}
	return value * 2, true
}

func minInt64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}

func (s *Service) resolveResourceBudget(req Request) (ResourceBudget, error) {
	if req.MemoryBytes < 0 || req.DiskBytes < 0 || req.TempBytes < 0 {
		return ResourceBudget{}, fmt.Errorf("%w: resource budget values must be non-negative", ErrResourceBudgetExceeded)
	}
	budget := ResourceBudget{
		MemoryBytes: req.MemoryBytes,
		DiskBytes:   req.DiskBytes,
		TempBytes:   req.TempBytes,
	}
	defaults := defaultResourceBudget(req.ResourceClass, req.TotalBytes)
	if budget.MemoryBytes <= 0 {
		budget.MemoryBytes = defaults.MemoryBytes
	}
	if budget.DiskBytes <= 0 {
		budget.DiskBytes = defaults.DiskBytes
	}
	if budget.TempBytes <= 0 {
		budget.TempBytes = defaults.TempBytes
	}
	if budget.MemoryBytes > s.limits.MemoryBytes ||
		budget.DiskBytes > s.limits.DiskBytes ||
		budget.TempBytes > s.limits.TempBytes {
		return ResourceBudget{}, fmt.Errorf("%w: requested memory=%d disk=%d temp=%d; limits memory=%d disk=%d temp=%d",
			ErrResourceBudgetExceeded, budget.MemoryBytes, budget.DiskBytes, budget.TempBytes,
			s.limits.MemoryBytes, s.limits.DiskBytes, s.limits.TempBytes)
	}
	return budget, nil
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

// effectivePriority gives an older queued operation one priority point per
// second of wait. This is deliberately calculated only at claim time, so the
// persisted request priority remains auditable and bounded.
func effectivePriority(priority int, requestedAt, at int64) int64 {
	if at <= requestedAt || priorityAgingMS <= 0 {
		return int64(priority)
	}
	return int64(priority) + (at-requestedAt)/priorityAgingMS
}

func (s *scheduler) reserve(baseID string, budget ResourceBudget) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if baseID != "" && s.base[baseID] >= s.limits.MaxPerBase {
		return false
	}
	if s.memoryBytes+budget.MemoryBytes > s.limits.MemoryBytes ||
		s.diskBytes+budget.DiskBytes > s.limits.DiskBytes ||
		s.tempBytes+budget.TempBytes > s.limits.TempBytes {
		return false
	}
	if baseID != "" {
		s.base[baseID]++
	}
	s.memoryBytes += budget.MemoryBytes
	s.diskBytes += budget.DiskBytes
	s.tempBytes += budget.TempBytes
	return true
}

func (s *scheduler) release(baseID string, budget ResourceBudget) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if baseID != "" {
		if s.base[baseID] > 1 {
			s.base[baseID]--
		} else {
			delete(s.base, baseID)
		}
	}
	s.memoryBytes -= budget.MemoryBytes
	s.diskBytes -= budget.DiskBytes
	s.tempBytes -= budget.TempBytes
	if s.memoryBytes < 0 {
		s.memoryBytes = 0
	}
	if s.diskBytes < 0 {
		s.diskBytes = 0
	}
	if s.tempBytes < 0 {
		s.tempBytes = 0
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
		MaxPerBase:          s.limits.MaxPerBase,
		ReservedMemoryBytes: s.memoryBytes,
		ReservedDiskBytes:   s.diskBytes,
		ReservedTempBytes:   s.tempBytes,
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
	Limits              ResourceLimits          `json:"limits"`
	Lanes               map[string]LaneSnapshot `json:"lanes"`
	ActiveBases         map[string]int          `json:"activeBases"`
	Resources           ResourceSnapshot        `json:"resources"`
	Operations          OperationMetrics        `json:"operations"`
	ActiveTotal         int                     `json:"activeTotal"`
	QueuedTotal         int                     `json:"queuedTotal"`
	QueueLimit          int                     `json:"queueLimit"`
	MaxPerBase          int                     `json:"maxPerBase"`
	ReservedMemoryBytes int64                   `json:"reservedMemoryBytes"`
	ReservedDiskBytes   int64                   `json:"reservedDiskBytes"`
	ReservedTempBytes   int64                   `json:"reservedTempBytes"`
	Writer              storage.WriterStats     `json:"writer"`
	DiskLowWater        bool                    `json:"diskLowWater"`
}

// OperationTypeStats is a data-free aggregate of durable operation outcomes
// for one command type. Queue and run durations are summed in milliseconds;
// callers can derive averages without exposing payloads or error messages.
type OperationTypeStats struct {
	Total       int64 `json:"total"`
	Rejected    int64 `json:"rejected"`
	Queued      int64 `json:"queued"`
	Running     int64 `json:"running"`
	Cancelling  int64 `json:"cancelling"`
	Succeeded   int64 `json:"succeeded"`
	Failed      int64 `json:"failed"`
	Cancelled   int64 `json:"cancelled"`
	Interrupted int64 `json:"interrupted"`
	Timeouts    int64 `json:"timeouts"`
	QueueWaitMS int64 `json:"queueWaitMs"`
	RunTimeMS   int64 `json:"runTimeMs"`
}

// OperationMetrics is the bounded status/reporting surface for operation
// outcomes. Durable counts survive restart; rejected counts are process-local
// because a rejected command intentionally has no operation row.
type OperationMetrics struct {
	Total    int64                         `json:"total"`
	Rejected int64                         `json:"rejected"`
	ByType   map[string]OperationTypeStats `json:"byType"`
}

// Service owns the durable queue and process-local executor registry.
type Service struct {
	db               *storage.DB
	instanceID       string
	workers          int
	maxPending       int
	maxPayload       int
	uploadRoot       string
	idempotencyMS    int64
	limits           ResourceLimits
	scheduler        *scheduler
	modelAdmission   sharedscheduler.Admission
	resourceSampler  ResourceSampler
	diskFreeSampler  DiskFreeSampler
	requestEnricher  RequestEnricher
	uploadBytesLimit int64
	tempBytesLimit   int64
	tempMu           sync.Mutex
	tempReserved     int64
	metricsMu        sync.Mutex
	rejectedByType   map[string]int64

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
		MemoryBytes: 2 << 30, DiskBytes: 10 << 30, DiskLowWaterBytes: 256 << 20,
		UploadBytes: 512 << 20, TempBytes: 1 << 30,
	}
	service := &Service{
		db:               db,
		instanceID:       id,
		workers:          workers,
		maxPending:       1000,
		maxPayload:       1 << 20,
		uploadRoot:       root,
		idempotencyMS:    int64(DefaultIdempotencyRetentionHours) * int64(time.Hour.Milliseconds()),
		limits:           defaults,
		registry:         map[string]Executor{},
		active:           map[string]*activeRun{},
		wakes:            make(map[string]chan struct{}),
		stop:             make(chan struct{}),
		uploadBytesLimit: defaults.UploadBytes,
		tempBytesLimit:   defaults.TempBytes,
		rejectedByType:   make(map[string]int64),
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
	s.uploadBytesLimit = s.limits.UploadBytes
	s.tempBytesLimit = s.limits.TempBytes
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

// SetResourceSampler attaches the process/storage sampler used by status and
// acceptance reporting. It is configured before Start and is optional for
// small embedded/test services.
func (s *Service) SetResourceSampler(sampler ResourceSampler) {
	s.resourceSampler = sampler
}

// SetDiskFreeSampler attaches the lightweight volume probe used at command
// admission. It is optional for embedded/test services.
func (s *Service) SetDiskFreeSampler(sampler DiskFreeSampler) {
	s.diskFreeSampler = sampler
}

// SetRequestEnricher attaches the application-owned source/config/model
// snapshot resolver used for newly admitted operations.
func (s *Service) SetRequestEnricher(enricher RequestEnricher) {
	s.requestEnricher = enricher
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
	var replay []string
	if err := s.db.WriteTx(ctx, storage.ControlWrite, nil, func(tx *sql.Tx) error {
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
		return nil
	}); err != nil {
		return err
	}
	if err := s.expireUploads(ctx); err != nil {
		return err
	}
	if err := s.releaseTerminalUploads(ctx); err != nil {
		return err
	}
	if err := s.cleanupUploadTemps(); err != nil {
		return err
	}
	if err := s.ExpireURLCaptures(ctx); err != nil {
		return err
	}
	if err := s.expireTerminalOperationPayloads(ctx); err != nil {
		return err
	}
	s.wakeupAll()
	return nil
}

// expireTerminalOperationPayloads releases bulky command inputs and results
// after the idempotency window. The operation row and key remain as a compact
// tombstone so a late retry still receives ErrOperationExpired; retryable
// failures retain their command input until they can no longer be retried.
func (s *Service) expireTerminalOperationPayloads(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, err := s.db.ExecPriority(ctx, storage.ControlWrite, `
			UPDATE operations
			SET command_payload = '', result = NULL, updated_at = ?
			WHERE id IN (
				SELECT id FROM operations
				WHERE idempotency_expires_at IS NOT NULL
				  AND idempotency_expires_at < ?
				  AND (state IN (?, ?) OR (state = ? AND retryable = 0))
				  AND (length(command_payload) > 0 OR result IS NOT NULL)
				ORDER BY idempotency_expires_at, id
				LIMIT ?
			)
			AND (length(command_payload) > 0 OR result IS NOT NULL)`,
			now(), now(), StateSucceeded, StateCancelled, StateFailed, cleanupBatchSize)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected < cleanupBatchSize {
			return nil
		}
	}
}

func (s *Service) expireUploads(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT id FROM upload_sessions
			WHERE state IN ('uploading','complete') AND expires_at < ?
			ORDER BY expires_at, id LIMIT ?`, now(), cleanupBatchSize)
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
			if err := ctx.Err(); err != nil {
				return err
			}
			path, err := s.uploadPath(id)
			if err != nil {
				return fmt.Errorf("resolve expired upload %s: %w", id, err)
			}
			// Remove the bytes before publishing the expired state. If the
			// process dies or removal fails, the durable uploading/complete row
			// remains eligible for a later retry instead of becoming an
			// unrecoverable expired lease with an orphaned staging file.
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove expired upload %s: %w", id, err)
			}
			if _, err := s.db.ExecPriority(ctx, storage.ControlWrite, `UPDATE upload_sessions SET state = ?, updated_at = ?
				WHERE id = ? AND state IN ('uploading','complete')`, UploadStateExpired, now(), id); err != nil {
				return err
			}
		}
		if len(expired) < cleanupBatchSize {
			return nil
		}
	}
}

// releaseTerminalUploads closes the durable input lease for operations that
// cannot run again. It is deliberately part of startup recovery as well as
// the normal run path: a process can exit after the operation reaches a
// terminal state but before the best-effort post-finish cleanup runs.
func (s *Service) releaseTerminalUploads(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		rows, err := s.db.QueryContext(ctx, `SELECT u.id FROM upload_sessions u
			JOIN operations o ON o.id = u.operation_id
			WHERE u.state = ? AND (
				o.state IN (?, ?) OR
				(o.state IN (?, ?) AND (o.retryable = 0 OR o.attempt >= ?))
			)
			ORDER BY u.updated_at, u.id LIMIT ?`, UploadStateBound, StateSucceeded, StateCancelled,
			StateFailed, StateInterrupted, DefaultMaxAttempts, cleanupBatchSize)
		if err != nil {
			return err
		}
		var uploadIDs []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return err
			}
			uploadIDs = append(uploadIDs, id)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()
		for _, id := range uploadIDs {
			if err := ctx.Err(); err != nil {
				return err
			}
			// Remove the file before publishing the released lease. If the
			// process dies between these two steps, startup recovery sees the
			// still-bound terminal lease and retries the idempotent cleanup.
			path, err := s.uploadPath(id)
			if err != nil {
				return fmt.Errorf("resolve terminal upload %s: %w", id, err)
			}
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove terminal upload %s: %w", id, err)
			}
			if _, err := s.db.ExecPriority(ctx, storage.ControlWrite, `UPDATE upload_sessions
				SET state = ?, updated_at = ? WHERE id = ? AND state = ?`,
				UploadStateReleased, now(), id, UploadStateBound); err != nil {
				return err
			}
		}
		if len(uploadIDs) < cleanupBatchSize {
			return nil
		}
	}
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
func (s *Service) Submit(ctx context.Context, req Request) (operation Operation, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	replayed := false
	accepted := false
	defer func() {
		if err != nil && !replayed && !accepted {
			s.recordRejected(req.Type)
		}
	}()
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
	if req.InputRef == "" && req.UploadID != "" {
		req.InputRef = req.UploadID
	}
	if req.InputSHA256 == "" {
		sum := sha256.Sum256(req.Payload)
		req.InputSHA256 = hex.EncodeToString(sum[:])
	}
	fingerprint := requestFingerprint(req)
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
		if op, err := scanOperation(s.db.QueryRowContext(ctx, `SELECT `+operationColumns+` FROM operations WHERE idempotency_key = ?`, req.IdempotencyKey)); err == nil {
			if op.IdempotencyExpiresAt != nil && *op.IdempotencyExpiresAt <= now() {
				return Operation{}, ErrOperationExpired
			}
			var stored []byte
			if err := s.db.QueryRowContext(ctx, `SELECT command_payload FROM operations WHERE id = ?`, op.ID).Scan(&stored); err != nil {
				return Operation{}, err
			}
			storedFingerprint := ""
			if err := s.db.QueryRowContext(ctx, `SELECT request_fingerprint FROM operations WHERE id = ?`, op.ID).Scan(&storedFingerprint); err != nil {
				return Operation{}, err
			}
			if op.Type != req.Type || op.CommandSchemaVersion != req.CommandSchemaVersion ||
				!fingerprintsMatch(storedFingerprint, req, op, stored) ||
				op.BaseID != req.BaseID || op.ParentOperationID != req.ParentOperationID ||
				(req.DocumentID != "" && op.DocumentID != req.DocumentID) {
				return Operation{}, ErrIdempotencyConflict
			}
			replayed = true
			return op, nil
		} else if err != sql.ErrNoRows {
			return Operation{}, err
		}
	}
	if s.requestEnricher != nil {
		req, err = s.requestEnricher(ctx, req)
		if err != nil {
			return Operation{}, err
		}
	}
	if s.diskFreeSampler != nil && s.limits.DiskLowWaterBytes > 0 {
		freeBytes := s.diskFreeSampler()
		if freeBytes > 0 && freeBytes < uint64(s.limits.DiskLowWaterBytes) {
			return Operation{}, fmt.Errorf("%w: free=%d threshold=%d", ErrDiskLowWater, freeBytes, s.limits.DiskLowWaterBytes)
		}
	}
	if req.UploadID != "" {
		upload, err := s.GetUploadContext(ctx, req.UploadID)
		if err != nil {
			return Operation{}, err
		}
		if upload.BaseID != req.BaseID {
			return Operation{}, ErrUploadScopeMismatch
		}
		if upload.State != UploadStateComplete && upload.State != UploadStateBound {
			return Operation{}, ErrUploadNotComplete
		}
		// A completed upload is immutable input. Bind its measured size to the
		// durable command before resolving memory/disk/temp reservations so a
		// large import is not admitted with the small generic operation budget.
		if req.TotalBytes == nil {
			size := upload.SizeBytes
			req.TotalBytes = &size
		}
	}
	budget, err := s.resolveResourceBudget(req)
	if err != nil {
		return Operation{}, err
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
	err = s.db.WriteTx(ctx, storage.ControlWrite, nil, func(tx *sql.Tx) error {
		var pending int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM operations WHERE state IN ('queued','running','cancelling')`).Scan(&pending); err != nil {
			return err
		}
		if pending >= s.maxPending {
			return ErrQueueFull
		}
		if _, err := tx.Exec(`INSERT INTO operations
		(id, type, command_schema_version, command_payload, request_fingerprint, idempotency_key,
		 base_id, document_id, parent_operation_id, principal_ref, scope_ref, input_ref,
		 input_sha256, source_version, config_snapshot_ref, model_snapshot_ref,
		 expected_target_epoch, expected_ancestor_epoch, allocated_document_id,
		 allocated_generation, state, state_revision, resource_class, priority,
		 total_units, total_bytes, memory_bytes, disk_bytes, temp_bytes, requested_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, req.Type, req.CommandSchemaVersion, string(req.Payload), fingerprint, nullString(req.IdempotencyKey),
			nullString(req.BaseID), nullString(req.DocumentID), nullString(req.ParentOperationID),
			nullString(req.PrincipalRef), nullString(req.ScopeRef), nullString(req.InputRef),
			nullString(req.InputSHA256), nullString(req.SourceVersion), nullString(req.ConfigSnapshotRef),
			nullString(req.ModelSnapshotRef), req.ExpectedTargetEpoch, req.ExpectedAncestorEpoch,
			nullString(req.DocumentID), req.AllocatedGeneration, StateQueued,
			req.ResourceClass, req.Priority, req.TotalUnits, req.TotalBytes,
			budget.MemoryBytes, budget.DiskBytes, budget.TempBytes, requestedAt, requestedAt); err != nil {
			return err
		}
		if req.UploadID != "" {
			result, err := tx.Exec(`UPDATE upload_sessions SET state = ?, operation_id = ?, updated_at = ?
			WHERE id = ? AND base_id = ? AND state = ? AND (operation_id IS NULL OR operation_id = ?)`,
				UploadStateBound, id, requestedAt, req.UploadID, req.BaseID, UploadStateComplete, id)
			if err != nil {
				return err
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if affected != 1 {
				return ErrUploadNotComplete
			}
		}
		return insertEvent(ctx, tx, id, 1, "submitted", map[string]any{"fingerprint": fingerprint})
	})
	if err != nil {
		return Operation{}, err
	}
	accepted = true
	s.wakeupClass(req.ResourceClass)
	return s.GetContext(ctx, id)
}

// FindIdempotencyOperation lets adapters check an existing key before costly
// or state-dependent admission validation. Submit performs the same checks as
// the authoritative path when no operation is found.
func (s *Service) FindIdempotencyOperation(req Request) (Operation, bool, error) {
	return s.FindIdempotencyOperationContext(context.Background(), req)
}

// FindIdempotencyOperationContext performs the early idempotency lookup while
// honoring the caller's request context. It is used by HTTP adapters before
// invoking the authoritative Submit path.
func (s *Service) FindIdempotencyOperationContext(ctx context.Context, req Request) (Operation, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if req.IdempotencyKey == "" {
		return Operation{}, false, nil
	}
	if req.InputRef == "" && req.UploadID != "" {
		req.InputRef = req.UploadID
	}
	if req.InputSHA256 == "" {
		sum := sha256.Sum256(req.Payload)
		req.InputSHA256 = hex.EncodeToString(sum[:])
	}
	op, err := scanOperation(s.db.QueryRowContext(ctx,
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
	if err := s.db.QueryRowContext(ctx, `SELECT command_payload FROM operations WHERE id = ?`, op.ID).Scan(&stored); err != nil {
		return Operation{}, false, err
	}
	var storedFingerprint string
	if err := s.db.QueryRowContext(ctx, `SELECT request_fingerprint FROM operations WHERE id = ?`, op.ID).Scan(&storedFingerprint); err != nil {
		return Operation{}, false, err
	}
	if op.Type != req.Type || op.CommandSchemaVersion != req.CommandSchemaVersion ||
		!fingerprintsMatch(storedFingerprint, req, op, stored) ||
		op.BaseID != req.BaseID || op.ParentOperationID != req.ParentOperationID ||
		(req.DocumentID != "" && op.DocumentID != req.DocumentID) {
		return Operation{}, false, ErrIdempotencyConflict
	}
	return op, true, nil
}

// Scheduler returns a bounded capacity snapshot. It is intended for health,
// load tests, and acceptance metrics rather than a task-list replacement.
func (s *Service) Scheduler() (SchedulerSnapshot, error) {
	return s.SchedulerContext(context.Background())
}

// SchedulerContext returns a bounded capacity snapshot while honoring ctx.
func (s *Service) SchedulerContext(ctx context.Context) (SchedulerSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	rows, err := s.db.QueryContext(ctx, `SELECT resource_class, COUNT(*) FROM operations
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
	if s.resourceSampler != nil {
		snapshot.Resources = s.resourceSampler()
		snapshot.DiskLowWater = s.limits.DiskLowWaterBytes > 0 &&
			snapshot.Resources.DiskFreeBytes > 0 &&
			snapshot.Resources.DiskFreeBytes < uint64(s.limits.DiskLowWaterBytes)
	}
	operationMetrics, err := s.OperationMetrics(ctx)
	if err != nil {
		return SchedulerSnapshot{}, err
	}
	snapshot.Operations = operationMetrics
	snapshot.Writer = s.db.WriterStats()
	return snapshot, nil
}

// OperationMetrics returns bounded outcome and timing aggregates grouped by
// operation type. SQL performs the grouping so status requests never load all
// operation rows or command payloads into memory.
func (s *Service) OperationMetrics(ctx context.Context) (OperationMetrics, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	const query = `SELECT type, state, error_code, COUNT(*),
		COALESCE(SUM(CASE
			WHEN started_at IS NOT NULL THEN
				CASE WHEN started_at > requested_at THEN started_at - requested_at ELSE 0 END
			WHEN COALESCE(finished_at, ?) > requested_at
				THEN COALESCE(finished_at, ?) - requested_at
			ELSE 0 END), 0),
		COALESCE(SUM(CASE
			WHEN started_at IS NOT NULL AND COALESCE(finished_at, ?) > started_at
				THEN COALESCE(finished_at, ?) - started_at
			ELSE 0 END), 0)
		FROM operations GROUP BY type, state, error_code`
	nowAt := now()
	rows, err := s.db.QueryContext(ctx, query, nowAt, nowAt, nowAt, nowAt)
	if err != nil {
		return OperationMetrics{}, err
	}
	defer rows.Close()
	metrics := OperationMetrics{ByType: make(map[string]OperationTypeStats)}
	for rows.Next() {
		var operationType, state string
		var errorCode sql.NullString
		var count, queueWaitMS, runTimeMS int64
		if err := rows.Scan(&operationType, &state, &errorCode, &count, &queueWaitMS, &runTimeMS); err != nil {
			return OperationMetrics{}, err
		}
		stats := metrics.ByType[operationType]
		stats.Total += count
		stats.QueueWaitMS += queueWaitMS
		stats.RunTimeMS += runTimeMS
		switch state {
		case StateQueued:
			stats.Queued += count
		case StateRunning:
			stats.Running += count
		case StateCancelling:
			stats.Cancelling += count
		case StateSucceeded:
			stats.Succeeded += count
		case StateFailed:
			stats.Failed += count
		case StateCancelled:
			stats.Cancelled += count
		case StateInterrupted:
			stats.Interrupted += count
		}
		if strings.Contains(strings.ToLower(errorCode.String), "timeout") {
			stats.Timeouts += count
		}
		metrics.ByType[operationType] = stats
		metrics.Total += count
	}
	if err := rows.Err(); err != nil {
		return OperationMetrics{}, err
	}
	s.metricsMu.Lock()
	for operationType, rejected := range s.rejectedByType {
		stats := metrics.ByType[operationType]
		stats.Rejected += rejected
		metrics.ByType[operationType] = stats
		metrics.Rejected += rejected
	}
	s.metricsMu.Unlock()
	return metrics, nil
}

func (s *Service) recordRejected(operationType string) {
	if strings.TrimSpace(operationType) == "" {
		operationType = "unknown"
	}
	s.metricsMu.Lock()
	s.rejectedByType[operationType]++
	s.metricsMu.Unlock()
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
	var id, baseID string
	var budget ResourceBudget
	claimPrepared := false
	err := s.db.WriteTx(ctx, storage.ControlWrite, nil, func(tx *sql.Tx) error {
		claimAt := now()
		rows, err := tx.Query(`SELECT id, COALESCE(base_id, ''), memory_bytes, disk_bytes, temp_bytes FROM operations
			WHERE state = ? AND resource_class = ? AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
			ORDER BY priority + CAST((? - requested_at) / ? AS INTEGER) DESC,
			requested_at, id LIMIT 32`, StateQueued, resourceClass, claimAt, claimAt, priorityAgingMS)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var candidateID, candidateBase string
			var candidateBudget ResourceBudget
			if err := rows.Scan(&candidateID, &candidateBase, &candidateBudget.MemoryBytes,
				&candidateBudget.DiskBytes, &candidateBudget.TempBytes); err != nil {
				return err
			}
			if !s.scheduler.reserve(candidateBase, candidateBudget) {
				continue
			}
			id, baseID, budget = candidateID, candidateBase, candidateBudget
			break
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if id == "" {
			return nil
		}
		// If the conditional claim loses a race with another process owner, the
		// reservation is handed back so later candidates remain available.
		var revision int64
		if err := tx.QueryRow(`SELECT state_revision FROM operations WHERE id = ? AND state = ?`,
			id, StateQueued).Scan(&revision); err != nil {
			return err
		}
		result, err := tx.Exec(`UPDATE operations SET state = ?, attempt = attempt + 1,
			state_revision = ?, instance_id = ?, started_at = ?, next_attempt_at = NULL, updated_at = ?
			WHERE id = ? AND state = ?`, StateRunning, revision+1, s.instanceID, now(), now(), id, StateQueued)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return nil
		}
		if err := insertEvent(ctx, tx, id, revision+1, "claimed", map[string]any{"instanceId": s.instanceID}); err != nil {
			return err
		}
		claimPrepared = true
		return nil
	})
	if err != nil {
		if id != "" && (!claimPrepared || !errors.Is(err, storage.ErrWriteUnknown)) {
			s.scheduler.release(baseID, budget)
		}
		return false, err
	}
	if id == "" {
		return false, nil
	}
	if !claimPrepared {
		s.scheduler.release(baseID, budget)
		return true, nil
	}
	// Each dispatcher owns its claimed operation synchronously, so the worker
	// count is the actual concurrency limit rather than just a wake-up count.
	s.run(ctx, id, baseID, budget)
	return true, nil
}

func (s *Service) run(parentCtx context.Context, id string, claimedBaseID string, claimedBudget ResourceBudget) {
	runCtx, cancel := context.WithCancel(context.Background())
	if parentCtx != nil && parentCtx.Err() != nil {
		cancel()
	}
	select {
	case <-s.stop:
		cancel()
	default:
	}
	s.mu.Lock()
	s.active[id] = &activeRun{cancel: cancel}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.active, id)
		s.mu.Unlock()
		cancel()
	}()

	op, err := s.GetContext(runCtx, id)
	if err != nil {
		finalCtx, finalCancel := operationFinalizationContext(runCtx)
		if finalOp, finalErr := s.GetContext(finalCtx, id); finalErr == nil {
			finishErr := err
			if runCtx.Err() != nil {
				finishErr = runCtx.Err()
			}
			_ = s.finishWorker(finalOp.Attempt, id, nil, finishErr)
		}
		finalCancel()
		s.scheduler.release(claimedBaseID, claimedBudget)
		return
	}
	defer s.scheduler.release(claimedBaseID, claimedBudget)
	s.scheduler.beginRun(op.ResourceClass)
	defer s.scheduler.endRun(op.ResourceClass)
	s.mu.RLock()
	executor := s.registry[op.Type]
	s.mu.RUnlock()
	if executor == nil {
		_ = s.finishWorker(op.Attempt, id, nil, ErrUnsupportedCommand)
		return
	}
	if testPreDispatchHook != nil {
		testPreDispatchHook(op)
	}
	var payloadBytes []byte
	payloadErr := s.db.QueryRowContext(runCtx, `SELECT command_payload FROM operations WHERE id = ?`, id).Scan(&payloadBytes)
	if payloadErr != nil && runCtx.Err() != nil {
		// The command was already durably claimed. If cancellation wins at
		// this narrow boundary, read only that immutable envelope with the
		// bounded finalization budget so the executor still observes the
		// canceled worker context and can converge its terminal marker.
		finalCtx, finalCancel := operationFinalizationContext(runCtx)
		payloadErr = s.db.QueryRowContext(finalCtx, `SELECT command_payload FROM operations WHERE id = ?`, id).Scan(&payloadBytes)
		finalCancel()
	}
	if payloadErr != nil {
		_ = s.finishWorker(op.Attempt, id, nil, payloadErr)
		return
	}
	payload := json.RawMessage(payloadBytes)
	// Cancellation can be durably recorded just before this worker registers
	// itself in active. Re-read the intent after registration so that race does
	// not allow business execution to start with a live context.
	var cancelRequested int
	if err := s.db.QueryRowContext(runCtx, `SELECT cancel_requested FROM operations WHERE id = ?`, id).Scan(&cancelRequested); err == nil && cancelRequested == 1 {
		cancel()
	}
	if op.ResourceClass == ResourceModel && s.modelAdmission != nil {
		releaseAdmission, acquireErr := s.modelAdmission.Acquire(runCtx)
		if acquireErr != nil {
			_ = s.finishWorker(op.Attempt, id, nil, acquireErr)
			return
		}
		defer releaseAdmission()
	}
	result, runErr := executor(runCtx, op, payload, func(progress Progress) {
		_ = s.reportContext(runCtx, id, op.Attempt, progress)
	})
	_ = s.finishWorker(op.Attempt, id, result, runErr)
	finalCtx, finalCancel := operationFinalizationContext(runCtx)
	if final, err := s.GetContext(finalCtx, id); err == nil && terminalInputLease(final) {
		_ = s.releaseUploadContext(finalCtx, id)
	}
	finalCancel()
}

func terminalInputLease(op Operation) bool {
	if op.State == StateSucceeded || op.State == StateCancelled {
		return true
	}
	return (op.State == StateFailed || op.State == StateInterrupted) &&
		(!op.Retryable || op.Attempt >= DefaultMaxAttempts)
}

func (s *Service) report(id string, attempt int, progress Progress) error {
	return s.reportContext(context.Background(), id, attempt, progress)
}

func (s *Service) reportContext(ctx context.Context, id string, attempt int, progress Progress) error {
	if ctx == nil {
		ctx = context.Background()
	}
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
	return s.db.WriteTx(ctx, storage.ControlWrite, nil, func(tx *sql.Tx) error {
		var previousPhase sql.NullString
		var revision int64
		if err := tx.QueryRowContext(ctx, `SELECT phase, state_revision FROM operations
			WHERE id = ? AND state IN ('running','cancelling') AND attempt = ?`, id, attempt).
			Scan(&previousPhase, &revision); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return err
		}
		result, err := tx.ExecContext(ctx, `UPDATE operations SET phase = ?, completed_units = ?, total_units = ?,
			completed_bytes = ?, total_bytes = ?, state_revision = state_revision + 1, updated_at = ?
			WHERE id = ? AND state IN ('running','cancelling') AND attempt = ?`,
			progress.Phase, completed, totalUnits, progress.CompletedBytes, totalBytes, now(), id, attempt)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil || affected == 0 || previousPhase.String == progress.Phase {
			return err
		}
		return insertEvent(ctx, tx, id, revision+1, "phase", map[string]any{
			"phase": progress.Phase, "completed": completed,
			"total": totalUnits, "completedBytes": progress.CompletedBytes,
			"totalBytes": totalBytes,
		})
	})
}

func (s *Service) finish(id string, attempt int, result any, runErr error) error {
	return s.finishContext(context.Background(), id, attempt, result, runErr)
}

// finishWorker gives the terminal transition a second bounded chance when a
// writer queue/lock failure races the executor's completion. The first write
// may have committed even when its caller observed ErrWriteUnknown, so the
// conditional state/attempt predicate in finishContext makes the retry
// idempotent. A live service should not leave a completed business effect in
// running merely because the first finalization response was lost.
func (s *Service) finishWorker(attempt int, id string, result any, runErr error) error {
	if err := s.finishContext(context.Background(), id, attempt, result, runErr); err == nil {
		return nil
	}
	return s.finishContext(context.Background(), id, attempt, result, runErr)
}

func (s *Service) finishContext(parent context.Context, id string, attempt int, result any, runErr error) error {
	finalCtx, cancel := operationFinalizationContext(parent)
	defer cancel()
	state := StateSucceeded
	errorCode := ""
	errorMessage := ""
	recoveryBasis := "executor_complete"
	if runErr != nil {
		state = StateFailed
		switch {
		case errors.Is(runErr, context.Canceled), errors.Is(runErr, context.DeadlineExceeded),
			errors.Is(runErr, storage.ErrWriteUnknown):
			var requested int
			_ = s.db.QueryRowContext(finalCtx, `SELECT cancel_requested FROM operations WHERE id = ?`, id).Scan(&requested)
			if requested == 1 {
				state = StateCancelled
				errorCode = "cancelled"
				errorMessage = "operation was cancelled"
				recoveryBasis = "cancel_intent"
			} else if errors.Is(runErr, context.DeadlineExceeded) {
				state = StateFailed
				errorCode = "timeout"
				errorMessage = "operation deadline exceeded"
				recoveryBasis = "deadline_exceeded"
			} else {
				state = StateInterrupted
				errorCode = "interrupted"
				errorMessage = "worker stopped before completion"
				recoveryBasis = "worker_interrupted"
			}
		default:
			errorCode = "operation_failed"
			errorMessage = SafeErrorMessage(runErr.Error())
			recoveryBasis = "executor_error"
		}
	}
	var resultText any
	retryable := 1
	if result != nil {
		encoded, err := json.Marshal(result)
		if err != nil {
			state = StateFailed
			errorCode = "result_encode_failed"
			errorMessage = SafeErrorMessage(err.Error())
			recoveryBasis = "result_encoding"
			retryable = 0
		} else if len(encoded) > s.maxPayload {
			state = StateFailed
			errorCode = "result_too_large"
			errorMessage = fmt.Sprintf("operation result exceeds %d bytes", s.maxPayload)
			recoveryBasis = "result_retention_limit"
			retryable = 0
		} else {
			resultText = string(encoded)
		}
	}
	finished := now()
	var idempotencyExpiry any
	if terminal(state) && s.idempotencyMS > 0 {
		idempotencyExpiry = finished + s.idempotencyMS
	}
	resultExpiry := idempotencyExpiry
	retainedUntil := idempotencyExpiry
	return s.db.WriteTx(finalCtx, storage.ControlWrite, nil, func(tx *sql.Tx) error {
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
		error_code = ?, error_message = ?, result = ?, retryable = ?, finished_at = ?, updated_at = ?,
		idempotency_expires_at = ?, recovery_basis = ?, result_expires_at = ?, retained_until = ?
		WHERE id = ? AND state IN (?, ?) AND attempt = ?`,
			state, revision+1, errorCode, errorMessage, resultText, retryable, finished, finished,
			idempotencyExpiry, recoveryBasis, resultExpiry, retainedUntil,
			id, StateRunning, StateCancelling, attempt); err != nil {
			return err
		}
		if err := insertEvent(finalCtx, tx, id, revision+1, "finished", map[string]any{
			"state": state, "errorCode": errorCode,
		}); err != nil {
			return err
		}
		return nil
	})
}

const operationFinalizationTimeout = 5 * time.Second

func operationFinalizationContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(parent), operationFinalizationTimeout)
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

func requestFingerprint(req Request) string {
	inputSHA256 := req.InputSHA256
	if inputSHA256 == "" {
		sum := sha256.Sum256(req.Payload)
		inputSHA256 = hex.EncodeToString(sum[:])
	}
	envelope := struct {
		Type                  string          `json:"type"`
		Schema                int             `json:"schema"`
		BaseID                string          `json:"baseId"`
		ParentOperationID     string          `json:"parentOperationId"`
		PrincipalRef          string          `json:"principalRef"`
		ScopeRef              string          `json:"scopeRef"`
		InputRef              string          `json:"inputRef"`
		InputSHA256           string          `json:"inputSha256"`
		SourceVersion         string          `json:"sourceVersion"`
		ConfigSnapshotRef     string          `json:"configSnapshotRef"`
		ModelSnapshotRef      string          `json:"modelSnapshotRef"`
		ExpectedTargetEpoch   *int64          `json:"expectedTargetEpoch"`
		ExpectedAncestorEpoch *int64          `json:"expectedAncestorEpoch"`
		AllocatedGeneration   *int64          `json:"allocatedGeneration"`
		Payload               json.RawMessage `json:"payload"`
	}{
		Type: req.Type, Schema: req.CommandSchemaVersion, BaseID: req.BaseID,
		ParentOperationID: req.ParentOperationID,
		PrincipalRef:      req.PrincipalRef, ScopeRef: req.ScopeRef, InputRef: req.InputRef,
		InputSHA256: inputSHA256, SourceVersion: req.SourceVersion,
		ConfigSnapshotRef: req.ConfigSnapshotRef, ModelSnapshotRef: req.ModelSnapshotRef,
		ExpectedTargetEpoch: req.ExpectedTargetEpoch, ExpectedAncestorEpoch: req.ExpectedAncestorEpoch,
		AllocatedGeneration: req.AllocatedGeneration, Payload: req.Payload,
	}
	encoded, _ := json.Marshal(envelope)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func fingerprintsMatch(stored string, req Request, op Operation, payload []byte) bool {
	if stored == requestFingerprint(req) {
		return true
	}
	// Rows written before migration 0016 used the narrower type/schema/payload
	// fingerprint. Keep those rows addressable while the explicit scope checks
	// above still reject a different target.
	return stored == legacyRequestFingerprint(op.Type, op.CommandSchemaVersion, payload)
}

func legacyRequestFingerprint(operationType string, schema int, payload []byte) string {
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

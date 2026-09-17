package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// WritePriority controls admission order for the single SQLite writer.
// Control writes include command state, cancellation and terminal results;
// maintenance is deliberately lowest priority.
type WritePriority uint8

const (
	ControlWrite WritePriority = iota
	NormalWrite
	MaintenanceWrite
)

var (
	ErrWriterQueueFull = errors.New("sqlite writer queue is full")
	ErrWriterStopped   = errors.New("sqlite writer is stopped")
	ErrWriteUnknown    = errors.New("sqlite write outcome is unknown")
)

const defaultWriterQueueCapacity = 256

type writeRequest struct {
	ctx      context.Context
	priority WritePriority
	kind     writeKind
	fn       func(context.Context) error
	result   chan error
	queuedAt time.Time
}

type writeKind uint8

const (
	mutationWrite writeKind = iota
	transactionWrite
)

// WriterStats is an aggregate, data-free view of writer pressure. Durations
// use Go's monotonic clock and contain no SQL or payload details.
type WriterStats struct {
	Admitted             int64                       `json:"admitted"`
	Rejected             int64                       `json:"rejected"`
	Dropped              int64                       `json:"dropped"`
	Succeeded            int64                       `json:"succeeded"`
	Failed               int64                       `json:"failed"`
	QueueWaitMS          int64                       `json:"queueWaitMs"`
	RunTimeMS            int64                       `json:"runTimeMs"`
	Transactions         int64                       `json:"transactions"`
	TransactionRunTimeMS int64                       `json:"transactionRunTimeMs"`
	DBWaitMS             int64                       `json:"dbWaitMs"`
	DBTransactionMS      int64                       `json:"dbTransactionMs"`
	DBSQLMS              int64                       `json:"dbSqlMs"`
	DBCommitMS           int64                       `json:"dbCommitMs"`
	ByPriority           map[string]WriterClassStats `json:"byPriority"`
}

// WriterClassStats is the same data-free pressure view as WriterStats, scoped
// to one admission class. The aggregate fields above remain the compatibility
// surface for existing health consumers.
type WriterClassStats struct {
	Admitted             int64 `json:"admitted"`
	Rejected             int64 `json:"rejected"`
	Dropped              int64 `json:"dropped"`
	Succeeded            int64 `json:"succeeded"`
	Failed               int64 `json:"failed"`
	QueueWaitMS          int64 `json:"queueWaitMs"`
	RunTimeMS            int64 `json:"runTimeMs"`
	Transactions         int64 `json:"transactions"`
	TransactionRunTimeMS int64 `json:"transactionRunTimeMs"`
	DBWaitMS             int64 `json:"dbWaitMs"`
	DBTransactionMS      int64 `json:"dbTransactionMs"`
	DBSQLMS              int64 `json:"dbSqlMs"`
	DBCommitMS           int64 `json:"dbCommitMs"`
}

type writerCounters struct {
	admitted             atomic.Int64
	rejected             atomic.Int64
	dropped              atomic.Int64
	succeeded            atomic.Int64
	failed               atomic.Int64
	queueWaitMS          atomic.Int64
	runTimeMS            atomic.Int64
	transactions         atomic.Int64
	transactionRunTimeMS atomic.Int64
	dbWaitMS             atomic.Int64
	dbTransactionMS      atomic.Int64
	dbSQLMS              atomic.Int64
	dbCommitMS           atomic.Int64
	control              writerClassCounters
	normal               writerClassCounters
	maintenance          writerClassCounters
}

type writerClassCounters struct {
	admitted             atomic.Int64
	rejected             atomic.Int64
	dropped              atomic.Int64
	succeeded            atomic.Int64
	failed               atomic.Int64
	queueWaitMS          atomic.Int64
	runTimeMS            atomic.Int64
	transactions         atomic.Int64
	transactionRunTimeMS atomic.Int64
	dbWaitMS             atomic.Int64
	dbTransactionMS      atomic.Int64
	dbSQLMS              atomic.Int64
	dbCommitMS           atomic.Int64
}

func (c *writerCounters) class(priority WritePriority) *writerClassCounters {
	switch priority {
	case ControlWrite:
		return &c.control
	case MaintenanceWrite:
		return &c.maintenance
	default:
		return &c.normal
	}
}

func (c *writerCounters) recordAdmitted(priority WritePriority) {
	c.admitted.Add(1)
	c.class(priority).admitted.Add(1)
}

func (c *writerCounters) recordRejected(priority WritePriority) {
	c.rejected.Add(1)
	c.class(priority).rejected.Add(1)
}

func (c *writerCounters) recordDropped(priority WritePriority) {
	c.dropped.Add(1)
	c.class(priority).dropped.Add(1)
}

func (c *writerCounters) recordSucceeded(priority WritePriority) {
	c.succeeded.Add(1)
	c.class(priority).succeeded.Add(1)
}

func (c *writerCounters) recordFailed(priority WritePriority) {
	c.failed.Add(1)
	c.class(priority).failed.Add(1)
}

func (c *writerCounters) recordQueueWait(priority WritePriority, ms int64) {
	c.queueWaitMS.Add(ms)
	c.class(priority).queueWaitMS.Add(ms)
}

func (c *writerCounters) recordRunTime(priority WritePriority, ms int64) {
	c.runTimeMS.Add(ms)
	c.class(priority).runTimeMS.Add(ms)
}

func (c *writerCounters) recordTransactionRunTime(priority WritePriority, ms int64) {
	c.transactions.Add(1)
	c.transactionRunTimeMS.Add(ms)
	class := c.class(priority)
	class.transactions.Add(1)
	class.transactionRunTimeMS.Add(ms)
}

func (c *writerCounters) recordDBWait(priority WritePriority, ms int64) {
	c.dbWaitMS.Add(ms)
	c.class(priority).dbWaitMS.Add(ms)
}

func (c *writerCounters) recordDBTransaction(priority WritePriority, ms int64) {
	c.dbTransactionMS.Add(ms)
	c.class(priority).dbTransactionMS.Add(ms)
}

func (c *writerCounters) recordDBSQL(priority WritePriority, ms int64) {
	c.dbSQLMS.Add(ms)
	c.class(priority).dbSQLMS.Add(ms)
}

func (c *writerCounters) recordDBCommit(priority WritePriority, ms int64) {
	c.dbCommitMS.Add(ms)
	c.class(priority).dbCommitMS.Add(ms)
}

func (c *writerCounters) classStats(priority WritePriority) WriterClassStats {
	class := c.class(priority)
	return WriterClassStats{
		Admitted:             class.admitted.Load(),
		Rejected:             class.rejected.Load(),
		Dropped:              class.dropped.Load(),
		Succeeded:            class.succeeded.Load(),
		Failed:               class.failed.Load(),
		QueueWaitMS:          class.queueWaitMS.Load(),
		RunTimeMS:            class.runTimeMS.Load(),
		Transactions:         class.transactions.Load(),
		TransactionRunTimeMS: class.transactionRunTimeMS.Load(),
		DBWaitMS:             class.dbWaitMS.Load(),
		DBTransactionMS:      class.dbTransactionMS.Load(),
		DBSQLMS:              class.dbSQLMS.Load(),
		DBCommitMS:           class.dbCommitMS.Load(),
	}
}

// Writer serializes database mutations through bounded, priority-aware
// queues. A request is considered accepted only when its callback commits;
// callers whose context expires after admission receive ErrWriteUnknown and
// must reconcile durable state instead of assuming that no mutation occurred.
type Writer struct {
	db          *sql.DB
	control     chan writeRequest
	normal      chan writeRequest
	maintenance chan writeRequest
	stop        chan struct{}
	done        chan struct{}
	stopOnce    sync.Once
	startMu     sync.Mutex
	started     bool
	counters    writerCounters
}

// NewWriter creates a stopped writer. Start must be called before submitting
// work; DB.Open does this for the application writer.
func NewWriter(db *sql.DB, capacity int) *Writer {
	if capacity < 1 {
		capacity = defaultWriterQueueCapacity
	}
	return &Writer{
		db:          db,
		control:     make(chan writeRequest, capacity),
		normal:      make(chan writeRequest, capacity),
		maintenance: make(chan writeRequest, capacity),
		stop:        make(chan struct{}),
		done:        make(chan struct{}),
	}
}

// Stats returns an aggregate snapshot for health and acceptance reporting.
// Individual counters may advance between fields.
func (w *Writer) Stats() WriterStats {
	return WriterStats{
		Admitted:             w.counters.admitted.Load(),
		Rejected:             w.counters.rejected.Load(),
		Dropped:              w.counters.dropped.Load(),
		Succeeded:            w.counters.succeeded.Load(),
		Failed:               w.counters.failed.Load(),
		QueueWaitMS:          w.counters.queueWaitMS.Load(),
		RunTimeMS:            w.counters.runTimeMS.Load(),
		Transactions:         w.counters.transactions.Load(),
		TransactionRunTimeMS: w.counters.transactionRunTimeMS.Load(),
		DBWaitMS:             w.counters.dbWaitMS.Load(),
		DBTransactionMS:      w.counters.dbTransactionMS.Load(),
		DBSQLMS:              w.counters.dbSQLMS.Load(),
		DBCommitMS:           w.counters.dbCommitMS.Load(),
		ByPriority: map[string]WriterClassStats{
			"control":     w.counters.classStats(ControlWrite),
			"normal":      w.counters.classStats(NormalWrite),
			"maintenance": w.counters.classStats(MaintenanceWrite),
		},
	}
}

// Start launches the sole writer loop. It is safe to call once; a second
// start is rejected so ownership is explicit during application startup.
func (w *Writer) Start() error {
	w.startMu.Lock()
	defer w.startMu.Unlock()
	if w.started {
		return fmt.Errorf("sqlite writer is already started")
	}
	select {
	case <-w.stop:
		return ErrWriterStopped
	default:
	}
	w.started = true
	go w.loop()
	return nil
}

// Stop rejects queued work and waits for the in-flight callback to return.
func (w *Writer) Stop(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	w.stopOnce.Do(func() { close(w.stop) })
	// Check before selecting between done and ctx.Done. When the writer has
	// already drained, both channels may be ready and select would otherwise
	// nondeterministically report a successful shutdown for an already-canceled
	// caller.
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Exec executes one bounded mutation through the writer.
func (w *Writer) Exec(ctx context.Context, priority WritePriority, query string, args ...any) (sql.Result, error) {
	type execResult struct {
		result sql.Result
		err    error
	}
	results := make(chan execResult, 1)
	err := w.Do(ctx, priority, func(runCtx context.Context) error {
		result, execErr := w.db.ExecContext(runCtx, query, args...)
		results <- execResult{result: result, err: execErr}
		return execErr
	})
	select {
	case result := <-results:
		return result.result, result.err
	default:
		return nil, err
	}
}

// Tx executes a transaction entirely inside the writer loop. The callback
// must not perform network, model, parsing or unbounded file work.
func (w *Writer) Tx(ctx context.Context, priority WritePriority, opts *sql.TxOptions, fn func(*sql.Tx) error) error {
	return w.do(ctx, priority, transactionWrite, func(runCtx context.Context) error {
		beginStarted := time.Now()
		tx, err := w.db.BeginTx(runCtx, opts)
		w.counters.recordDBWait(priority, time.Since(beginStarted).Milliseconds())
		if err != nil {
			return err
		}
		transactionStarted := time.Now()
		defer func() {
			w.counters.recordDBTransaction(priority, time.Since(transactionStarted).Milliseconds())
		}()
		defer func() { _ = tx.Rollback() }()
		sqlStarted := time.Now()
		if err := fn(tx); err != nil {
			w.counters.recordDBSQL(priority, time.Since(sqlStarted).Milliseconds())
			return err
		}
		w.counters.recordDBSQL(priority, time.Since(sqlStarted).Milliseconds())
		commitStarted := time.Now()
		err = tx.Commit()
		w.counters.recordDBCommit(priority, time.Since(commitStarted).Milliseconds())
		return err
	})
}

// Do admits a callback to a bounded priority queue.
func (w *Writer) Do(ctx context.Context, priority WritePriority, fn func(context.Context) error) error {
	return w.do(ctx, priority, mutationWrite, fn)
}

func (w *Writer) do(ctx context.Context, priority WritePriority, kind writeKind, fn func(context.Context) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	priority = normalizeWritePriority(priority)
	req := writeRequest{ctx: ctx, priority: priority, kind: kind, fn: fn, result: make(chan error, 1), queuedAt: time.Now()}
	queue := w.queue(priority)
	select {
	case <-w.stop:
		w.counters.recordRejected(priority)
		return ErrWriterStopped
	case <-ctx.Done():
		w.counters.recordRejected(priority)
		return ctx.Err()
	case queue <- req:
		w.counters.recordAdmitted(priority)
	default:
		// Admission is deliberately fail-fast. Waiting for queue capacity makes
		// overload invisible to callers and can consume the caller's entire
		// deadline before the work is even accepted.
		w.counters.recordRejected(priority)
		return ErrWriterQueueFull
	}
	select {
	case err := <-req.result:
		return err
	case <-ctx.Done():
		return fmt.Errorf("%w: %v", ErrWriteUnknown, ctx.Err())
	case <-w.stop:
		select {
		case err := <-req.result:
			return err
		default:
			return ErrWriterStopped
		}
	}
}

func (w *Writer) queue(priority WritePriority) chan writeRequest {
	switch priority {
	case ControlWrite:
		return w.control
	case MaintenanceWrite:
		return w.maintenance
	default:
		return w.normal
	}
}

func normalizeWritePriority(priority WritePriority) WritePriority {
	switch priority {
	case ControlWrite, NormalWrite, MaintenanceWrite:
		return priority
	default:
		return NormalWrite
	}
}

func (w *Writer) loop() {
	defer close(w.done)
	controlBurst := 0
	for {
		req, ok := w.next(&controlBurst)
		if !ok {
			w.rejectQueued()
			return
		}
		if err := req.ctx.Err(); err != nil {
			w.counters.recordQueueWait(req.priority, time.Since(req.queuedAt).Milliseconds())
			w.counters.recordFailed(req.priority)
			req.result <- err
			continue
		}
		w.counters.recordQueueWait(req.priority, time.Since(req.queuedAt).Milliseconds())
		runStarted := time.Now()
		err := req.fn(req.ctx)
		runTimeMS := time.Since(runStarted).Milliseconds()
		w.counters.recordRunTime(req.priority, runTimeMS)
		if req.kind == transactionWrite {
			w.counters.recordTransactionRunTime(req.priority, runTimeMS)
		}
		if err != nil {
			w.counters.recordFailed(req.priority)
		} else {
			w.counters.recordSucceeded(req.priority)
		}
		req.result <- err
	}
}

func (w *Writer) next(controlBurst *int) (writeRequest, bool) {
	// Give control writes a bounded burst, then force an opportunity for normal
	// work so cancellation/terminal traffic cannot permanently starve imports.
	if *controlBurst < 8 {
		select {
		case req := <-w.control:
			(*controlBurst)++
			return req, true
		default:
		}
	} else {
		select {
		case req := <-w.normal:
			*controlBurst = 0
			return req, true
		case req := <-w.maintenance:
			*controlBurst = 0
			return req, true
		default:
		}
	}
	select {
	case <-w.stop:
		return writeRequest{}, false
	case req := <-w.control:
		(*controlBurst)++
		return req, true
	case req := <-w.normal:
		*controlBurst = 0
		return req, true
	case req := <-w.maintenance:
		*controlBurst = 0
		return req, true
	}
}

func (w *Writer) rejectQueued() {
	reject := func(queue chan writeRequest) {
		for {
			select {
			case req := <-queue:
				w.counters.recordDropped(req.priority)
				req.result <- ErrWriterStopped
			default:
				return
			}
		}
	}
	reject(w.control)
	reject(w.normal)
	reject(w.maintenance)
}

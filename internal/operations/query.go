package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

const operationColumns = `id, type, command_schema_version, base_id, document_id, parent_operation_id,
	principal_ref, scope_ref, input_ref, input_sha256, source_version, config_snapshot_ref,
	model_snapshot_ref, expected_target_epoch, expected_ancestor_epoch, allocated_document_id,
	allocated_generation, state, state_revision, phase, resource_class, priority, attempt,
	cancel_requested, retryable, completed_units, total_units, completed_bytes, total_bytes,
	error_code, error_message, result, requested_at, started_at, finished_at,
	idempotency_expires_at, memory_bytes, disk_bytes, temp_bytes, next_attempt_at,
	recovery_basis, result_ref, result_expires_at, retained_until`

// ListFilter bounds status and scope queries. Nil slices mean unrestricted;
// explicitly empty slices fail closed.
type ListFilter struct {
	States   []string
	BaseIDs  []string
	DocIDs   []string
	ParentID string
	Limit    int
	Cursor   string
}

// Get returns one operation snapshot.
func (s *Service) Get(id string) (Operation, error) {
	return s.GetContext(context.Background(), id)
}

// GetContext returns one operation snapshot while honoring ctx.
func (s *Service) GetContext(ctx context.Context, id string) (Operation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	op, err := scanOperation(s.db.QueryRowContext(ctx, `SELECT `+operationColumns+` FROM operations WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return Operation{}, ErrNotFound
	}
	return op, err
}

// List returns newest-first operations under a bounded cursor page.
func (s *Service) List(ctx context.Context, filter ListFilter) ([]Operation, string, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	clauses := []string{"1 = 1"}
	var args []any
	clauses, args = appendIDFilter(clauses, args, "state", filter.States)
	clauses, args = appendIDFilter(clauses, args, "base_id", filter.BaseIDs)
	clauses, args = appendIDFilter(clauses, args, "document_id", filter.DocIDs)
	if filter.ParentID != "" {
		clauses = append(clauses, "parent_operation_id = ?")
		args = append(args, filter.ParentID)
	}
	if filter.Cursor != "" {
		cursorAt, cursorID, err := decodeCursor(filter.Cursor)
		if err != nil {
			return nil, "", err
		}
		clauses = append(clauses, "(requested_at < ? OR (requested_at = ? AND id < ?))")
		args = append(args, cursorAt, cursorAt, cursorID)
	}
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, `SELECT `+operationColumns+` FROM operations
		WHERE `+strings.Join(clauses, " AND ")+`
		ORDER BY requested_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []Operation
	for rows.Next() {
		op, err := scanOperation(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, op)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		last := out[len(out)-1]
		next = encodeCursor(last.RequestedAt, last.ID)
	}
	return out, next, nil
}

// Cancel records intent durably before notifying a process-local worker.
func (s *Service) Cancel(id string) (Operation, error) {
	return s.CancelContext(context.Background(), id)
}

// CancelContext records intent durably before notifying a process-local
// worker, honoring cancellation while reading and writing the control state.
func (s *Service) CancelContext(ctx context.Context, id string) (Operation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	op, err := s.GetContext(ctx, id)
	if err != nil {
		return Operation{}, err
	}
	if terminal(op.State) {
		return op, nil
	}
	changed := false
	err = s.db.WriteTx(ctx, storage.ControlWrite, nil, func(tx *sql.Tx) error {
		nextRevision := op.StateRevision + 1
		result, err := tx.ExecContext(ctx, `UPDATE operations SET cancel_requested = 1, state = ?,
		state_revision = ?, updated_at = ?
		WHERE id = ? AND state IN ('queued','running') AND state_revision = ?`,
			StateCancelling, nextRevision, now(), id, op.StateRevision)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return nil
		}
		changed = true
		if err := insertEvent(ctx, tx, id, nextRevision, "cancel_requested", nil); err != nil {
			return err
		}
		if op.State == StateQueued {
			if _, err := tx.ExecContext(ctx, `UPDATE operations SET state = ?,
			state_revision = ?, finished_at = ?, updated_at = ?
			WHERE id = ? AND state = ? AND cancel_requested = 1`,
				StateCancelled, nextRevision+1, now(), now(), id, StateCancelling); err != nil {
				return err
			}
			if err := insertEvent(ctx, tx, id, nextRevision+1, "finished", map[string]any{
				"state": StateCancelled, "errorCode": "cancelled",
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Operation{}, err
	}
	if !changed {
		return s.GetContext(ctx, id)
	}
	s.mu.RLock()
	run := s.active[id]
	s.mu.RUnlock()
	if run != nil {
		run.cancel()
	}
	return s.GetContext(ctx, id)
}

// Retry returns a failed or interrupted operation to the durable queue.
func (s *Service) Retry(id string) (Operation, error) {
	return s.RetryContext(context.Background(), id)
}

// RetryContext returns a failed or interrupted operation to the durable queue,
// honoring cancellation while reading and writing the control state.
func (s *Service) RetryContext(ctx context.Context, id string) (Operation, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	op, err := s.GetContext(ctx, id)
	if err != nil {
		return Operation{}, err
	}
	if op.State != StateFailed && op.State != StateInterrupted {
		return Operation{}, fmt.Errorf("operation state %s cannot be retried", op.State)
	}
	if !op.Retryable {
		return Operation{}, fmt.Errorf("operation is not retryable")
	}
	if op.Attempt >= DefaultMaxAttempts {
		return Operation{}, fmt.Errorf("operation reached the maximum of %d attempts", DefaultMaxAttempts)
	}
	revision := op.StateRevision + 1
	err = s.db.WriteTx(ctx, storage.ControlWrite, nil, func(tx *sql.Tx) error {
		retryAt := now()
		if _, err := tx.ExecContext(ctx, `UPDATE operations SET state = ?, cancel_requested = 0,
		state_revision = ?, error_code = NULL, error_message = NULL,
		result = NULL, finished_at = NULL, next_attempt_at = ?, updated_at = ?
		WHERE id = ? AND state = ?`,
			StateQueued, revision, retryAt, retryAt, id, op.State); err != nil {
			return err
		}
		if err := insertEvent(ctx, tx, id, revision, "retry_queued", map[string]any{
			"attempt": op.Attempt,
		}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return Operation{}, err
	}
	s.wakeupClass(op.ResourceClass)
	return s.GetContext(ctx, id)
}

type operationEvent struct {
	ID        int64           `json:"id"`
	Operation string          `json:"operationId"`
	Revision  int64           `json:"stateRevision"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	CreatedAt int64           `json:"createdAt"`
}

func (s *Service) Events(ctx context.Context, id string, after int64, limit int) ([]operationEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, operation_id, revision, kind, payload, created_at
		FROM operation_events WHERE operation_id = ? AND id > ? ORDER BY id LIMIT ?`, id, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []operationEvent
	for rows.Next() {
		var event operationEvent
		var payload []byte
		if err := rows.Scan(&event.ID, &event.Operation, &event.Revision, &event.Kind, &payload, &event.CreatedAt); err != nil {
			return nil, err
		}
		event.Payload = payload
		out = append(out, event)
	}
	return out, rows.Err()
}

type rowScanner interface {
	Scan(...any) error
}

func scanOperation(row rowScanner) (Operation, error) {
	var op Operation
	var baseID, documentID, parentID sql.NullString
	var principalRef, scopeRef, inputRef, inputSHA256, sourceVersion sql.NullString
	var configSnapshotRef, modelSnapshotRef, allocatedDocumentID sql.NullString
	var phase, errorCode, errorMessage, recoveryBasis, resultRef sql.NullString
	var expectedTargetEpoch, expectedAncestorEpoch, allocatedGeneration sql.NullInt64
	var totalUnits, totalBytes sql.NullInt64
	var startedAt, finishedAt sql.NullInt64
	var idempotencyExpiresAt sql.NullInt64
	var memoryBytes, diskBytes, tempBytes sql.NullInt64
	var nextAttemptAt, resultExpiresAt, retainedUntil sql.NullInt64
	var result []byte
	var cancelRequested, retryable int
	err := row.Scan(&op.ID, &op.Type, &op.CommandSchemaVersion, &baseID, &documentID, &parentID,
		&principalRef, &scopeRef, &inputRef, &inputSHA256, &sourceVersion, &configSnapshotRef,
		&modelSnapshotRef, &expectedTargetEpoch, &expectedAncestorEpoch, &allocatedDocumentID,
		&allocatedGeneration, &op.State, &op.StateRevision, &phase, &op.ResourceClass, &op.Priority, &op.Attempt,
		&cancelRequested, &retryable, &op.CompletedUnits, &totalUnits, &op.CompletedBytes, &totalBytes,
		&errorCode, &errorMessage, &result, &op.RequestedAt, &startedAt, &finishedAt,
		&idempotencyExpiresAt, &memoryBytes, &diskBytes, &tempBytes, &nextAttemptAt,
		&recoveryBasis, &resultRef, &resultExpiresAt, &retainedUntil)
	if err != nil {
		return Operation{}, err
	}
	op.BaseID = baseID.String
	op.DocumentID = documentID.String
	op.ParentOperationID = parentID.String
	op.PrincipalRef = principalRef.String
	op.ScopeRef = scopeRef.String
	op.InputRef = inputRef.String
	op.InputSHA256 = inputSHA256.String
	op.SourceVersion = sourceVersion.String
	op.ConfigSnapshotRef = configSnapshotRef.String
	op.ModelSnapshotRef = modelSnapshotRef.String
	op.AllocatedDocumentID = allocatedDocumentID.String
	op.Phase = phase.String
	op.CancelRequested = cancelRequested != 0
	op.Cancellable = (op.State == StateQueued || op.State == StateRunning) && !op.CancelRequested
	op.Retryable = retryable != 0
	if totalUnits.Valid {
		value := int(totalUnits.Int64)
		op.TotalUnits = &value
	}
	if totalBytes.Valid {
		value := totalBytes.Int64
		op.TotalBytes = &value
	}
	op.ErrorCode = errorCode.String
	op.ErrorMessage = errorMessage.String
	op.StartedAt = startedAt.Int64
	op.FinishedAt = finishedAt.Int64
	if op.StartedAt > 0 {
		op.QueueWaitMS = maxDurationMS(op.StartedAt - op.RequestedAt)
		end := op.FinishedAt
		if end == 0 {
			end = now()
		}
		op.RunTimeMS = maxDurationMS(end - op.StartedAt)
	} else if op.FinishedAt > 0 {
		// Queued cancellation never claims the operation, so its complete
		// lifetime is the admission wait rather than executor runtime.
		op.QueueWaitMS = maxDurationMS(op.FinishedAt - op.RequestedAt)
	}
	if idempotencyExpiresAt.Valid {
		value := idempotencyExpiresAt.Int64
		op.IdempotencyExpiresAt = &value
	}
	op.Result = result
	op.MemoryBytes = memoryBytes.Int64
	op.DiskBytes = diskBytes.Int64
	op.TempBytes = tempBytes.Int64
	if expectedTargetEpoch.Valid {
		value := expectedTargetEpoch.Int64
		op.ExpectedTargetEpoch = &value
	}
	if expectedAncestorEpoch.Valid {
		value := expectedAncestorEpoch.Int64
		op.ExpectedAncestorEpoch = &value
	}
	if allocatedGeneration.Valid {
		value := allocatedGeneration.Int64
		op.AllocatedGeneration = &value
	}
	if nextAttemptAt.Valid {
		value := nextAttemptAt.Int64
		op.NextAttemptAt = &value
	}
	op.RecoveryBasis = recoveryBasis.String
	op.ResultRef = resultRef.String
	if resultExpiresAt.Valid {
		value := resultExpiresAt.Int64
		op.ResultExpiresAt = &value
	}
	if retainedUntil.Valid {
		value := retainedUntil.Int64
		op.RetainedUntil = &value
	}
	return op, nil
}

func maxDurationMS(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}

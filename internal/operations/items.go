package operations

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

var ErrOperationItemCommitted = errors.New("operation item is already committed")

const (
	ItemPending   = "pending"
	ItemCommitted = "committed"
	ItemSkipped   = "skipped"
	ItemFailed    = "failed"
	ItemCancelled = "cancelled"
)

// OperationItem is the durable per-item outcome used by batch executors.
// commit_marker is monotonic: once a business effect is committed, a retry
// cannot move that item back to pending or execute it a second time.
type OperationItem struct {
	ID           int64           `json:"id"`
	OperationID  string          `json:"operationId"`
	ItemKey      string          `json:"itemKey"`
	Attempt      int             `json:"attempt"`
	State        string          `json:"state"`
	Committed    bool            `json:"committed"`
	Result       json.RawMessage `json:"result,omitempty"`
	ErrorCode    string          `json:"errorCode,omitempty"`
	ErrorMessage string          `json:"errorMessage,omitempty"`
	CommittedAt  int64           `json:"committedAt,omitempty"`
	UpdatedAt    int64           `json:"updatedAt"`
}

// MarkItem records one batch item's durable outcome. It is idempotent for the
// same attempt and refuses to overwrite a committed business effect.
func (s *Service) MarkItem(ctx context.Context, operationID string, attempt int,
	itemKey, state string, result any, errorCode, errorMessage string,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	prepared, err := s.prepareItem(operationID, attempt, itemKey, state, result, errorCode, errorMessage)
	if err != nil {
		return err
	}
	return s.db.WriteTx(ctx, storage.ControlWrite, nil, func(tx *sql.Tx) error {
		return markItemTx(tx, prepared)
	})
}

// MarkItemTx records one item using a caller-owned business transaction. It
// is used by application adapters that atomically publish a document effect
// and its durable commit marker.
func (s *Service) MarkItemTx(tx *sql.Tx, operationID string, attempt int,
	itemKey, state string, result any, errorCode, errorMessage string,
) error {
	if tx == nil {
		return fmt.Errorf("operation item transaction is required")
	}
	prepared, err := s.prepareItem(operationID, attempt, itemKey, state, result, errorCode, errorMessage)
	if err != nil {
		return err
	}
	return markItemTx(tx, prepared)
}

// GetItem returns one durable item without scanning the operation's other
// items. Executors use it before a retry so a business effect whose marker was
// committed before a worker crash is not executed a second time.
func (s *Service) GetItem(ctx context.Context, operationID, itemKey string) (OperationItem, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var item OperationItem
	var committed int
	var result []byte
	var errorCode, errorMessage sql.NullString
	var committedAt sql.NullInt64
	err := s.db.QueryRowContext(ctx, `SELECT id, operation_id, item_key,
		attempt, state, commit_marker, result, error_code, error_message,
		committed_at, updated_at FROM operation_items
		WHERE operation_id = ? AND item_key = ?`, operationID, itemKey).Scan(
		&item.ID, &item.OperationID, &item.ItemKey, &item.Attempt, &item.State,
		&committed, &result, &errorCode, &errorMessage, &committedAt, &item.UpdatedAt)
	if err != nil {
		return OperationItem{}, err
	}
	item.Committed = committed != 0
	item.Result = result
	item.ErrorCode = errorCode.String
	item.ErrorMessage = errorMessage.String
	item.CommittedAt = committedAt.Int64
	return item, nil
}

type preparedItem struct {
	operationID string
	itemKey     string
	attempt     int
	state       string
	encoded     any
	errorCode   string
	errorMsg    string
	committed   bool
	committedAt any
	updatedAt   int64
}

func (s *Service) prepareItem(operationID string, attempt int, itemKey, state string,
	result any, errorCode, errorMessage string,
) (preparedItem, error) {
	if strings.TrimSpace(operationID) == "" || strings.TrimSpace(itemKey) == "" {
		return preparedItem{}, fmt.Errorf("operation item requires operationId and itemKey")
	}
	if len(itemKey) > 512 {
		return preparedItem{}, fmt.Errorf("operation item key is too long")
	}
	switch state {
	case ItemPending, ItemCommitted, ItemSkipped, ItemFailed, ItemCancelled:
	default:
		return preparedItem{}, fmt.Errorf("invalid operation item state %q", state)
	}
	if attempt < 0 {
		return preparedItem{}, fmt.Errorf("operation item attempt cannot be negative")
	}
	var encoded any
	if result != nil {
		bytes, err := json.Marshal(result)
		if err != nil {
			return preparedItem{}, fmt.Errorf("encode operation item result: %w", err)
		}
		if len(bytes) > s.maxPayload {
			return preparedItem{}, ErrPayloadTooLarge
		}
		encoded = string(bytes)
	}
	if len(errorCode) > 256 {
		errorCode = errorCode[:256]
	}
	if len(errorMessage) > 4096 {
		errorMessage = errorMessage[:4096]
	}
	errorMessage = SafeErrorMessage(errorMessage)
	committed := state == ItemCommitted
	var committedAt any
	if committed {
		committedAt = now()
	}
	return preparedItem{
		operationID: operationID, itemKey: itemKey, attempt: attempt, state: state,
		encoded: encoded, errorCode: errorCode, errorMsg: errorMessage,
		committed: committed, committedAt: committedAt, updatedAt: now(),
	}, nil
}

func markItemTx(tx *sql.Tx, item preparedItem) error {
	var existingAttempt int
	var existingState string
	var existingCommitted int
	err := tx.QueryRow(`SELECT attempt, state, commit_marker FROM operation_items
		WHERE operation_id = ? AND item_key = ?`, item.operationID, item.itemKey).
		Scan(&existingAttempt, &existingState, &existingCommitted)
	if err == nil {
		if existingCommitted != 0 {
			if item.committed {
				return nil
			}
			return ErrOperationItemCommitted
		}
		if existingAttempt > item.attempt {
			return nil
		}
		_, err = tx.Exec(`UPDATE operation_items SET attempt = ?, state = ?,
			commit_marker = ?, result = COALESCE(?, result), error_code = ?, error_message = ?,
			committed_at = ?, updated_at = ? WHERE operation_id = ? AND item_key = ?`,
			item.attempt, item.state, boolInt(item.committed), item.encoded,
			nullString(item.errorCode), nullString(item.errorMsg), item.committedAt,
			item.updatedAt, item.operationID, item.itemKey)
		return err
	}
	if err != sql.ErrNoRows {
		return err
	}
	_, err = tx.Exec(`INSERT INTO operation_items
		(operation_id, item_key, attempt, state, commit_marker, result,
		 error_code, error_message, committed_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, item.operationID, item.itemKey,
		item.attempt, item.state, boolInt(item.committed), item.encoded,
		nullString(item.errorCode), nullString(item.errorMsg), item.committedAt,
		item.updatedAt)
	return err
}

// ListItems returns a bounded, stable item page for a durable operation.
func (s *Service) ListItems(ctx context.Context, operationID string, limit int) ([]OperationItem, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if limit <= 0 {
		limit = 200
	}
	if limit > 500 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, operation_id, item_key,
		attempt, state, commit_marker, result, error_code, error_message,
		committed_at, updated_at FROM operation_items
		WHERE operation_id = ? ORDER BY id LIMIT ?`, operationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OperationItem
	for rows.Next() {
		var item OperationItem
		var committed int
		var result []byte
		var errorCode, errorMessage sql.NullString
		var committedAt sql.NullInt64
		if err := rows.Scan(&item.ID, &item.OperationID, &item.ItemKey, &item.Attempt,
			&item.State, &committed, &result, &errorCode, &errorMessage,
			&committedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.Committed = committed != 0
		item.Result = result
		item.ErrorCode = errorCode.String
		item.ErrorMessage = errorMessage.String
		item.CommittedAt = committedAt.Int64
		out = append(out, item)
	}
	return out, rows.Err()
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

package operations

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func terminal(state string) bool {
	switch state {
	case StateSucceeded, StateFailed, StateCancelled:
		return true
	default:
		return false
	}
}

func nullString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func insertEvent(ctx context.Context, tx *sql.Tx, operationID string, revision int64, kind string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO operation_events (operation_id, revision, kind, payload, created_at)
		VALUES (?, ?, ?, ?, ?)`, operationID, revision, kind, string(encoded), now())
	return err
}

func appendIDFilter(clauses []string, args []any, column string, values []string) ([]string, []any) {
	if values == nil {
		return clauses, args
	}
	if len(values) == 0 {
		return append(clauses, "0 = 1"), args
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(values)), ",")
	for _, value := range values {
		args = append(args, value)
	}
	return append(clauses, column+" IN ("+placeholders+")"), args
}

func encodeCursor(at int64, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(at, 10) + "\x00" + id))
}

func decodeCursor(value string) (int64, string, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return 0, "", fmt.Errorf("invalid operation cursor")
	}
	parts := strings.SplitN(string(data), "\x00", 2)
	if len(parts) != 2 {
		return 0, "", fmt.Errorf("invalid operation cursor")
	}
	at, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("invalid operation cursor")
	}
	return at, parts[1], nil
}

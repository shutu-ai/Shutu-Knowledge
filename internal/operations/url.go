package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

const MaxURLCaptureBytes = 10 << 20

var (
	ErrURLCaptureNotFound = errors.New("url capture not found")
	ErrURLCaptureState    = errors.New("url capture is not usable")
	ErrURLTooLarge        = errors.New("url response exceeds the configured size limit")
)

// URL states. `ready` is retained for retry; `consumed` clears bytes after the
// business effect is safely published.
const (
	URLStateReady    = "ready"
	URLStateConsumed = "consumed"
	URLStateFailed   = "failed"
)

type URLCapture struct {
	ID          string `json:"id"`
	OperationID string `json:"operationId"`
	RawURL      string `json:"rawUrl"`
	FinalURL    string `json:"finalUrl"`
	ContentType string `json:"contentType,omitempty"`
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"sizeBytes"`
	State       string `json:"state"`
	Body        []byte `json:"-"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
	ExpiresAt   int64  `json:"expiresAt"`
}

// EnsureURLCapture stores the first successful fetch for an operation. If a
// capture already exists, its immutable bytes are returned unchanged.
func (s *Service) EnsureURLCapture(ctx context.Context, operationID, rawURL, finalURL, contentType string, body []byte) (URLCapture, error) {
	if len(body) > MaxURLCaptureBytes {
		return URLCapture{}, fmt.Errorf("%w (%d bytes)", ErrURLTooLarge, MaxURLCaptureBytes)
	}
	if existing, err := s.GetURLCapture(ctx, operationID); err == nil {
		if existing.State != URLStateReady || len(existing.Body) == 0 {
			return URLCapture{}, fmt.Errorf("%w: %s", ErrURLCaptureState, existing.State)
		}
		return existing, nil
	} else if err != ErrURLCaptureNotFound {
		return URLCapture{}, err
	}
	sum := sha256.Sum256(body)
	id, err := newID()
	if err != nil {
		return URLCapture{}, err
	}
	current := now()
	capture := URLCapture{
		ID: id, OperationID: operationID, RawURL: rawURL, FinalURL: finalURL,
		ContentType: contentType, SHA256: hex.EncodeToString(sum[:]),
		SizeBytes: int64(len(body)), State: URLStateReady, Body: append([]byte(nil), body...),
		CreatedAt: current, UpdatedAt: current,
		ExpiresAt: current + int64(7*24*time.Hour/time.Millisecond),
	}
	_, err = s.db.ExecPriority(ctx, storage.ControlWrite, `INSERT INTO url_captures
		(id, operation_id, raw_url, final_url, content_type, sha256, size_bytes, state,
		 body, created_at, updated_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		capture.ID, capture.OperationID, capture.RawURL, capture.FinalURL, capture.ContentType,
		capture.SHA256, capture.SizeBytes, capture.State, capture.Body,
		capture.CreatedAt, capture.UpdatedAt, capture.ExpiresAt)
	if err != nil {
		return URLCapture{}, err
	}
	return capture, nil
}

func (s *Service) GetURLCapture(ctx context.Context, operationID string) (URLCapture, error) {
	var capture URLCapture
	var rawURL, finalURL, contentType, checksum sql.NullString
	var body []byte
	err := s.db.QueryRowContext(ctx, `SELECT id, operation_id, raw_url, final_url,
		content_type, sha256, size_bytes, state, body, created_at, updated_at, expires_at
		FROM url_captures WHERE operation_id = ?`, operationID).Scan(
		&capture.ID, &capture.OperationID, &rawURL, &finalURL, &contentType, &checksum,
		&capture.SizeBytes, &capture.State, &body, &capture.CreatedAt, &capture.UpdatedAt,
		&capture.ExpiresAt)
	if err == sql.ErrNoRows {
		return URLCapture{}, ErrURLCaptureNotFound
	}
	if err != nil {
		return URLCapture{}, err
	}
	capture.RawURL = rawURL.String
	capture.FinalURL = finalURL.String
	capture.ContentType = contentType.String
	capture.SHA256 = checksum.String
	capture.Body = body
	return capture, nil
}

// ConsumeURLCapture clears bulky bytes after the document publish has
// succeeded, while retaining the audit reference and digest.
func (s *Service) ConsumeURLCapture(ctx context.Context, operationID string) error {
	_, err := s.db.ExecPriority(ctx, storage.ControlWrite, `UPDATE url_captures SET state = ?, body = NULL, updated_at = ?
		WHERE operation_id = ? AND state = ?`, URLStateConsumed, now(), operationID, URLStateReady)
	return err
}

// ExpireURLCaptures clears retry bodies at the retention boundary. Consumed
// rows were cleared on success; failed rows retain source through expiry.
func (s *Service) ExpireURLCaptures(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, err := s.db.ExecPriority(ctx, storage.ControlWrite, `UPDATE url_captures
			SET state = ?, body = NULL, updated_at = ?
			WHERE id IN (
				SELECT id FROM url_captures
				WHERE state IN ('ready','failed') AND expires_at < ?
				ORDER BY expires_at, id
				LIMIT ?
			) AND state IN ('ready','failed')`, URLStateConsumed, now(), now(), cleanupBatchSize)
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

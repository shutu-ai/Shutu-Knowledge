package operations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaxUploadBytes is the P1 static admission budget for one staged input.
const MaxUploadBytes = 100 << 20

var (
	ErrUploadNotFound      = errors.New("upload session not found")
	ErrUploadState         = errors.New("upload session state is not valid for this request")
	ErrUploadNotComplete   = errors.New("upload session is not complete")
	ErrUploadScopeMismatch = errors.New("upload session belongs to a different knowledge base")
	ErrUploadSizeMismatch  = errors.New("uploaded size does not match the declared size")
	ErrUploadHashMismatch  = errors.New("uploaded hash does not match the declared hash")
	ErrUploadTooLarge      = errors.New("upload exceeds the configured size limit")
	ErrUploadStorageNotSet = errors.New("upload staging storage is not configured")
)

type Upload struct {
	ID                string `json:"uploadId"`
	BaseID            string `json:"baseId"`
	ParentDirectoryID string `json:"parentDirectoryId,omitempty"`
	FileName          string `json:"fileName"`
	State             string `json:"state"`
	ExpectedSize      *int64 `json:"expectedSize,omitempty"`
	ExpectedSHA256    string `json:"expectedSha256,omitempty"`
	SizeBytes         int64  `json:"sizeBytes"`
	SHA256            string `json:"sha256,omitempty"`
	OperationID       string `json:"operationId,omitempty"`
	CreatedAt         int64  `json:"createdAt"`
	UpdatedAt         int64  `json:"updatedAt"`
	ExpiresAt         int64  `json:"expiresAt"`
}

type UploadCreate struct {
	BaseID            string `json:"baseId"`
	ParentDirectoryID string `json:"parentDirectoryId"`
	FileName          string `json:"fileName"`
	ExpectedSize      *int64 `json:"expectedSize"`
	ExpectedSHA256    string `json:"expectedSha256"`
}

// Publish hooks are test-only synchronization points for forced process-kill
// drills at the exact staging rename boundary. Production leaves them unset.
var (
	testUploadBeforePublish func()
	testUploadAfterPublish  func()
)

func (s *Service) CreateUpload(ctx context.Context, create UploadCreate) (Upload, error) {
	if s.uploadRoot == "" {
		return Upload{}, ErrUploadStorageNotSet
	}
	if strings.TrimSpace(create.BaseID) == "" || strings.TrimSpace(create.FileName) == "" {
		return Upload{}, fmt.Errorf("baseId and fileName are required")
	}
	if create.ExpectedSize != nil && (*create.ExpectedSize < 0 || *create.ExpectedSize > MaxUploadBytes) {
		return Upload{}, fmt.Errorf("%w (%d bytes)", ErrUploadTooLarge, MaxUploadBytes)
	}
	if create.ExpectedSHA256 != "" && len(create.ExpectedSHA256) != 64 {
		return Upload{}, fmt.Errorf("expectedSha256 must be a SHA-256 hex digest")
	}
	id, err := newID()
	if err != nil {
		return Upload{}, err
	}
	current := now()
	session := Upload{
		ID: id, BaseID: create.BaseID, ParentDirectoryID: create.ParentDirectoryID,
		FileName: strings.TrimSpace(create.FileName), State: UploadStateUploading,
		ExpectedSize: create.ExpectedSize, ExpectedSHA256: create.ExpectedSHA256,
		CreatedAt: current, UpdatedAt: current,
		ExpiresAt: current + int64(24*time.Hour/time.Millisecond),
	}
	path, err := s.uploadPath(id)
	if err != nil {
		return Upload{}, err
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO upload_sessions
		(id, base_id, parent_directory_id, file_name, state, expected_size, expected_sha256,
		 staging_path, created_at, updated_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID, session.BaseID, nullString(session.ParentDirectoryID), session.FileName,
		session.State, session.ExpectedSize, nullString(session.ExpectedSHA256),
		path, session.CreatedAt, session.UpdatedAt, session.ExpiresAt); err != nil {
		return Upload{}, err
	}
	return session, nil
}

// PutUploadContent streams bytes to same-volume staging and records digest
// metadata. Binding remains impossible until the explicit complete transition.
func (s *Service) PutUploadContent(ctx context.Context, id string, body io.Reader) (Upload, error) {
	if s.uploadRoot == "" {
		return Upload{}, ErrUploadStorageNotSet
	}
	session, err := s.GetUpload(id)
	if err != nil {
		return Upload{}, err
	}
	if session.State != UploadStateUploading {
		return Upload{}, fmt.Errorf("%w: %s", ErrUploadState, session.State)
	}
	stagingPath, err := s.uploadPath(id)
	if err != nil {
		return Upload{}, err
	}
	// Stage in the same directory and publish by rename. Never unlink the old
	// staging file first: an unbound retry may safely replace it, but a crash
	// must not expose a missing or truncated durable input.
	temp, err := os.CreateTemp(s.uploadRoot, "."+id+".tmp-*")
	if err != nil {
		return Upload{}, fmt.Errorf("create upload staging: %w", err)
	}
	tempPath := temp.Name()
	digest := sha256.New()
	file := temp
	size, copyErr := io.Copy(io.MultiWriter(file, digest), io.LimitReader(body, MaxUploadBytes+1))
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr == nil {
		copyErr = syncErr
	}
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr == nil && size > MaxUploadBytes {
		copyErr = fmt.Errorf("%w (%d bytes)", ErrUploadTooLarge, MaxUploadBytes)
	}
	if copyErr == nil && session.ExpectedSize != nil && size != *session.ExpectedSize {
		copyErr = fmt.Errorf("%w: got %d want %d", ErrUploadSizeMismatch, size, *session.ExpectedSize)
	}
	checksum := hex.EncodeToString(digest.Sum(nil))
	if copyErr == nil && session.ExpectedSHA256 != "" && checksum != session.ExpectedSHA256 {
		copyErr = ErrUploadHashMismatch
	}
	if copyErr != nil {
		_ = os.Remove(tempPath)
		return Upload{}, copyErr
	}
	if testUploadBeforePublish != nil {
		testUploadBeforePublish()
	}
	if err := os.Rename(tempPath, stagingPath); err != nil {
		_ = os.Remove(tempPath)
		return Upload{}, err
	}
	if testUploadAfterPublish != nil {
		testUploadAfterPublish()
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE upload_sessions SET size_bytes = ?, sha256 = ?,
		updated_at = ? WHERE id = ? AND state = ?`, size, checksum, now(), id, UploadStateUploading); err != nil {
		_ = os.Remove(stagingPath)
		return Upload{}, err
	}
	return s.GetUpload(id)
}

func (s *Service) CompleteUpload(ctx context.Context, id string) (Upload, error) {
	session, err := s.GetUpload(id)
	if err != nil {
		return Upload{}, err
	}
	if session.State == UploadStateComplete || session.State == UploadStateBound {
		return session, nil
	}
	if session.State != UploadStateUploading {
		return Upload{}, fmt.Errorf("%w: %s", ErrUploadState, session.State)
	}
	if session.SizeBytes <= 0 || session.SHA256 == "" {
		return Upload{}, ErrUploadNotComplete
	}
	if session.ExpectedSize != nil && session.SizeBytes != *session.ExpectedSize {
		return Upload{}, ErrUploadSizeMismatch
	}
	if session.ExpectedSHA256 != "" && session.SHA256 != session.ExpectedSHA256 {
		return Upload{}, ErrUploadHashMismatch
	}
	if _, err := s.uploadPath(id); err != nil {
		return Upload{}, err
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE upload_sessions SET state = ?, updated_at = ?
		WHERE id = ? AND state = ?`, UploadStateComplete, now(), id, UploadStateUploading); err != nil {
		return Upload{}, err
	}
	return s.GetUpload(id)
}

func (s *Service) GetUpload(id string) (Upload, error) {
	var session Upload
	var parentDirectory, expectedSHA, sha, operationID sql.NullString
	var expectedSize sql.NullInt64
	err := s.db.QueryRow(`SELECT id, base_id, parent_directory_id, file_name, state,
		expected_size, expected_sha256, size_bytes, sha256, operation_id, created_at, updated_at, expires_at
		FROM upload_sessions WHERE id = ?`, id).Scan(
		&session.ID, &session.BaseID, &parentDirectory, &session.FileName, &session.State,
		&expectedSize, &expectedSHA, &session.SizeBytes, &sha, &operationID,
		&session.CreatedAt, &session.UpdatedAt, &session.ExpiresAt)
	if err == sql.ErrNoRows {
		return Upload{}, ErrUploadNotFound
	}
	if err != nil {
		return Upload{}, err
	}
	session.ParentDirectoryID = parentDirectory.String
	session.ExpectedSHA256 = expectedSHA.String
	session.SHA256 = sha.String
	session.OperationID = operationID.String
	if expectedSize.Valid {
		value := expectedSize.Int64
		session.ExpectedSize = &value
	}
	return session, nil
}

// UploadForOperation validates that a bound immutable input belongs to this
// operation and returns its stable staging path.
func (s *Service) UploadForOperation(operationID, uploadID string) (Upload, string, error) {
	session, err := s.GetUpload(uploadID)
	if err != nil {
		return Upload{}, "", err
	}
	if session.State != UploadStateBound || session.OperationID != operationID {
		return Upload{}, "", fmt.Errorf("%w: upload is not bound to operation", ErrUploadState)
	}
	path, err := s.uploadPath(uploadID)
	if err != nil {
		return Upload{}, "", err
	}
	return session, path, nil
}

func (s *Service) releaseUpload(operationID string) error {
	var uploadID string
	err := s.db.QueryRow(`SELECT id FROM upload_sessions WHERE operation_id = ? AND state = ?`,
		operationID, UploadStateBound).Scan(&uploadID)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	path, err := s.uploadPath(uploadID)
	if err != nil {
		return err
	}
	if _, err := s.db.Exec(`UPDATE upload_sessions SET state = ?, updated_at = ?
		WHERE id = ? AND state = ?`, UploadStateReleased, now(), uploadID, UploadStateBound); err != nil {
		return err
	}
	_ = os.Remove(path)
	return nil
}

func (s *Service) uploadPath(id string) (string, error) {
	if s.uploadRoot == "" {
		return "", ErrUploadStorageNotSet
	}
	if _, err := hex.DecodeString(id); err != nil || len(id) != 32 {
		return "", ErrUploadNotFound
	}
	if err := os.MkdirAll(s.uploadRoot, 0o700); err != nil {
		return "", fmt.Errorf("create upload staging: %w", err)
	}
	return filepath.Join(s.uploadRoot, id+".staging"), nil
}

package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

// store is the SQL access layer over the shared SQLite database.
type store struct {
	db *storage.DB

	statsMu    sync.Mutex
	statsCache map[string]statsCacheEntry
	statsNow   func() time.Time
	statsTTL   time.Duration
}

// queryRunner lets bounded search pipelines execute every read on one WAL
// snapshot, either a dedicated read connection or a read transaction.
type queryRunner interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func newStore(db *storage.DB) *store {
	return &store{
		db:       db,
		statsNow: time.Now,
		statsTTL: time.Second,
	}
}

type statsCacheEntry struct {
	stats   Stats
	expires time.Time
}

func (s *store) invalidateStatsCache() {
	s.statsMu.Lock()
	s.statsCache = nil
	s.statsMu.Unlock()
}

// ── bases ────────────────────────────────────────────────────────────────────

const baseColumns = `id, name, description, grp, config, created_at, updated_at, lifecycle_state, mutation_epoch`

// retiredGenerationRetentionMS is a bounded grace window for separately issued
// historical requests. It does not replace a live SQLite read snapshot, which
// already protects every lane within one search request.
const retiredGenerationRetentionMS = int64(5 * time.Minute / time.Millisecond)

func scanBase(row interface{ Scan(...any) error }) (Base, error) {
	var b Base
	var configText string
	var group sql.NullString
	if err := row.Scan(&b.ID, &b.Name, &b.Description, &group, &configText, &b.CreatedAt, &b.UpdatedAt,
		&b.LifecycleState, &b.MutationEpoch); err != nil {
		return Base{}, err
	}
	b.Group = group.String
	if configText != "" {
		if err := json.Unmarshal([]byte(configText), &b.Config); err != nil {
			return Base{}, fmt.Errorf("base %s config: %w", b.ID, err)
		}
	}
	return b, nil
}

func (s *store) putBase(b Base) error {
	if b.LifecycleState == "" {
		b.LifecycleState = LifecycleActive
	}
	if b.MutationEpoch == 0 {
		b.MutationEpoch = 1
	}
	configText, err := json.Marshal(b.Config)
	if err != nil {
		return fmt.Errorf("marshal base config: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(
		`INSERT INTO bases (id, name, description, grp, config, created_at, updated_at, lifecycle_state, mutation_epoch)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET name = excluded.name, description = excluded.description,
		   grp = excluded.grp, config = excluded.config, updated_at = excluded.updated_at
		 WHERE EXISTS (SELECT 1 FROM bases current_base WHERE current_base.id = excluded.id
		   AND current_base.lifecycle_state = 'active')`,
		b.ID, b.Name, b.Description, b.Group, string(configText), b.CreatedAt, b.UpdatedAt,
		b.LifecycleState, b.MutationEpoch,
	); err != nil {
		return err
	}
	var state string
	if err := tx.QueryRow(`SELECT lifecycle_state FROM bases WHERE id = ?`, b.ID).Scan(&state); err != nil {
		return err
	}
	if state != "active" {
		return ErrConflict
	}
	return tx.Commit()
}

func (s *store) getBase(id string) (Base, error) {
	b, err := scanBase(s.db.QueryRow(`SELECT `+baseColumns+` FROM bases WHERE id = ? AND lifecycle_state = ?`, id, LifecycleActive))
	if err == sql.ErrNoRows {
		return Base{}, ErrNotFound
	}
	return b, err
}

func (s *store) listBases() ([]Base, error) {
	rows, err := s.db.Query(`SELECT `+baseColumns+` FROM bases WHERE lifecycle_state = ? ORDER BY created_at, id`, LifecycleActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Base
	for rows.Next() {
		b, err := scanBase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *store) deleteBase(id string) error {
	_, err := s.db.Exec(`DELETE FROM bases WHERE id = ?`, id)
	return err
}

// ── documents ────────────────────────────────────────────────────────────────

const documentColumns = `id, base_id, title, source_type, file_name, mime_type, url, parent_directory_id,
	source_path, content_hash, raw_file_path, raw_text, char_count, token_count, chunk_count,
	embedding_model, embedding_ready, status, phase, progress, incomplete, error_code, error_message, created_at, updated_at, title_locked,
	lifecycle_state, mutation_epoch, source_version, active_index_generation, desired_index_generation, index_state`

const documentMetadataColumns = `id, base_id, title, source_type, file_name, mime_type, url, parent_directory_id,
	source_path, content_hash, raw_file_path, char_count, token_count, chunk_count,
	embedding_model, embedding_ready, status, phase, progress, incomplete, error_code, error_message, created_at, updated_at, title_locked,
	lifecycle_state, mutation_epoch, source_version, active_index_generation, desired_index_generation, index_state`

func scanDocument(row interface{ Scan(...any) error }) (Document, error) {
	var d Document
	var fileName, mimeType, url, parentDir, sourcePath, contentHash, rawFilePath, rawText, embeddingModel sql.NullString
	var tokenCount, updatedAt sql.NullInt64
	var phase, errorCode, errorMessage sql.NullString
	var desiredGeneration sql.NullInt64
	var incomplete, embeddingReady, titleLocked int
	err := row.Scan(&d.ID, &d.BaseID, &d.Title, &d.SourceType, &fileName, &mimeType, &url, &parentDir,
		&sourcePath, &contentHash, &rawFilePath, &rawText, &d.CharCount, &tokenCount, &d.ChunkCount,
		&embeddingModel, &embeddingReady, &d.Status, &phase, &d.Progress, &incomplete, &errorCode, &errorMessage, &d.CreatedAt, &updatedAt,
		&titleLocked, &d.LifecycleState, &d.MutationEpoch, &d.SourceVersion, &d.ActiveIndexGen,
		&desiredGeneration, &d.IndexState)
	if err != nil {
		return Document{}, err
	}
	d.FileName = fileName.String
	d.MimeType = mimeType.String
	d.URL = url.String
	d.ParentDirectoryID = parentDir.String
	d.SourcePath = sourcePath.String
	d.ContentHash = contentHash.String
	d.RawFilePath = rawFilePath.String
	d.RawText = rawText.String
	d.EmbeddingModel = embeddingModel.String
	d.EmbeddingReady = embeddingReady != 0
	d.TokenCount = int(tokenCount.Int64)
	d.UpdatedAt = updatedAt.Int64
	d.Phase = phase.String
	d.Incomplete = incomplete != 0
	d.TitleLocked = titleLocked != 0
	d.ErrorCode = errorCode.String
	d.ErrorMessage = errorMessage.String
	d.DesiredIndexGen = desiredGeneration.Int64
	d.HasDesiredIndexGen = desiredGeneration.Valid
	return d, nil
}

func scanDocumentMetadata(row interface{ Scan(...any) error }) (Document, error) {
	var d Document
	var fileName, mimeType, url, parentDir, sourcePath, contentHash, rawFilePath, embeddingModel sql.NullString
	var tokenCount, updatedAt sql.NullInt64
	var phase, errorCode, errorMessage sql.NullString
	var desiredGeneration sql.NullInt64
	var incomplete, embeddingReady, titleLocked int
	err := row.Scan(&d.ID, &d.BaseID, &d.Title, &d.SourceType, &fileName, &mimeType, &url, &parentDir,
		&sourcePath, &contentHash, &rawFilePath, &d.CharCount, &tokenCount, &d.ChunkCount,
		&embeddingModel, &embeddingReady, &d.Status, &phase, &d.Progress, &incomplete, &errorCode, &errorMessage, &d.CreatedAt, &updatedAt,
		&titleLocked, &d.LifecycleState, &d.MutationEpoch, &d.SourceVersion, &d.ActiveIndexGen,
		&desiredGeneration, &d.IndexState)
	if err != nil {
		return Document{}, err
	}
	d.FileName = fileName.String
	d.MimeType = mimeType.String
	d.URL = url.String
	d.ParentDirectoryID = parentDir.String
	d.SourcePath = sourcePath.String
	d.ContentHash = contentHash.String
	d.RawFilePath = rawFilePath.String
	d.EmbeddingModel = embeddingModel.String
	d.EmbeddingReady = embeddingReady != 0
	d.TokenCount = int(tokenCount.Int64)
	d.UpdatedAt = updatedAt.Int64
	d.Phase = phase.String
	d.Incomplete = incomplete != 0
	d.TitleLocked = titleLocked != 0
	d.ErrorCode = errorCode.String
	d.ErrorMessage = errorMessage.String
	d.DesiredIndexGen = desiredGeneration.Int64
	d.HasDesiredIndexGen = desiredGeneration.Valid
	return d, nil
}

func (s *store) putDocument(d Document) error {
	tx, err := s.db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := upsertDocument(context.Background(), tx, d); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.invalidateStatsCache()
	return nil
}

type documentExecer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// fenceDocumentScope rejects new and existing rows whose base or parent chain
// is deleting. Running in the same write transaction as the upsert closes the
// scan/delete/new-child-ID race required by C06.
func fenceDocumentScope(ctx context.Context, runner documentExecer, d Document) error {
	var baseCount int
	if err := runner.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM bases WHERE id = ? AND lifecycle_state = ?`,
		d.BaseID, LifecycleActive).Scan(&baseCount); err != nil {
		return err
	}
	if baseCount == 0 {
		return ErrConflict
	}
	if d.ParentDirectoryID == "" {
		return nil
	}
	var total, fenced int
	if err := runner.QueryRowContext(ctx, `WITH RECURSIVE ancestors(id, parent_id) AS (
			SELECT id, parent_directory_id FROM documents WHERE id = ?
			UNION ALL
			SELECT doc.id, doc.parent_directory_id FROM documents doc
			JOIN ancestors parent ON doc.id = parent.parent_id
		) SELECT COUNT(*), COALESCE(SUM(doc.lifecycle_state <> ?), 0)
		  FROM ancestors ancestor JOIN documents doc ON doc.id = ancestor.id`,
		d.ParentDirectoryID, LifecycleActive).Scan(&total, &fenced); err != nil {
		return err
	}
	if total == 0 || fenced != 0 {
		return ErrConflict
	}
	return nil
}

func upsertDocument(ctx context.Context, runner documentExecer, d Document) error {
	var tokenCount, updatedAt any
	if d.TokenCount > 0 {
		tokenCount = d.TokenCount
	}
	if d.UpdatedAt > 0 {
		updatedAt = d.UpdatedAt
	}
	var phase, errorCode, errorMessage, embeddingModel any
	if d.Phase != "" {
		phase = d.Phase
	}
	if d.ErrorCode != "" {
		errorCode = d.ErrorCode
	}
	if d.ErrorMessage != "" {
		errorMessage = d.ErrorMessage
	}
	if d.EmbeddingModel != "" {
		embeddingModel = d.EmbeddingModel
	}
	rawText := any(nil)
	if d.RawText != "" {
		rawText = d.RawText
	}
	incomplete := 0
	if d.Incomplete {
		incomplete = 1
	}
	titleLocked := 0
	if d.TitleLocked {
		titleLocked = 1
	}
	embeddingReady := 0
	if d.EmbeddingReady {
		embeddingReady = 1
	}
	var desiredGeneration any
	if d.HasDesiredIndexGen {
		desiredGeneration = d.DesiredIndexGen
	}
	if d.LifecycleState == "" {
		d.LifecycleState = LifecycleActive
	}
	if d.MutationEpoch == 0 {
		d.MutationEpoch = 1
	}
	if d.SourceVersion == 0 {
		d.SourceVersion = 1
	}
	if d.IndexState == "" {
		d.IndexState = IndexStateActive
	}
	if err := fenceDocumentScope(ctx, runner, d); err != nil {
		return err
	}
	result, err := runner.ExecContext(ctx,
		`INSERT INTO documents (id, base_id, title, source_type, file_name, mime_type, url, parent_directory_id,
		   source_path, content_hash, raw_file_path, raw_text, char_count, token_count, chunk_count,
		   embedding_model, embedding_ready, status, phase, progress, incomplete, error_code, error_message, created_at, updated_at, title_locked,
		   lifecycle_state, mutation_epoch, source_version, active_index_generation, desired_index_generation, index_state)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET title = excluded.title, file_name = excluded.file_name,
		   mime_type = excluded.mime_type, url = excluded.url, parent_directory_id = excluded.parent_directory_id,
		   source_path = excluded.source_path, content_hash = excluded.content_hash,
		   raw_file_path = excluded.raw_file_path, raw_text = excluded.raw_text,
		   char_count = excluded.char_count, token_count = excluded.token_count,
		   chunk_count = excluded.chunk_count, embedding_model = excluded.embedding_model,
		   embedding_ready = excluded.embedding_ready, status = excluded.status, phase = excluded.phase,
		   progress = excluded.progress, incomplete = excluded.incomplete,
		   error_code = excluded.error_code, error_message = excluded.error_message,
		   updated_at = excluded.updated_at, title_locked = excluded.title_locked,
		   lifecycle_state = excluded.lifecycle_state, mutation_epoch = excluded.mutation_epoch,
		   source_version = excluded.source_version, active_index_generation = excluded.active_index_generation,
		   desired_index_generation = excluded.desired_index_generation, index_state = excluded.index_state
		 WHERE EXISTS (SELECT 1 FROM documents current_doc JOIN bases current_base
		   ON current_base.id = current_doc.base_id
		   WHERE current_doc.id = excluded.id
		     AND current_doc.lifecycle_state = 'active' AND current_base.lifecycle_state = 'active')`,
		d.ID, d.BaseID, d.Title, d.SourceType, d.FileName, d.MimeType, d.URL, d.ParentDirectoryID,
		d.SourcePath, d.ContentHash, d.RawFilePath, rawText, d.CharCount, tokenCount, d.ChunkCount,
		embeddingModel, embeddingReady, d.Status, phase, d.Progress, incomplete, errorCode, errorMessage, d.CreatedAt, updatedAt, titleLocked,
		d.LifecycleState, d.MutationEpoch, d.SourceVersion, d.ActiveIndexGen, desiredGeneration, d.IndexState,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		// The UPSERT predicate found a tombstone/inactive scope after the
		// conflict target was resolved. Callers must observe the fence
		// instead of believing a stale publication or move succeeded.
		return ErrConflict
	}
	return err
}

func (s *store) getDocument(id string) (Document, error) {
	d, err := scanDocument(s.db.QueryRow(`SELECT `+documentColumns+` FROM documents WHERE id = ? AND lifecycle_state = ?`, id, LifecycleActive))
	if err == sql.ErrNoRows {
		return Document{}, ErrNotFound
	}
	return d, err
}

// getDocumentIncludingDeleting is for delete-operation recovery: a tombstone
// must remain findable so cleanup can resume, while normal reads exclude it.
func (s *store) getDocumentIncludingDeleting(id string) (Document, error) {
	d, err := scanDocument(s.db.QueryRow(`SELECT `+documentColumns+` FROM documents WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return Document{}, ErrNotFound
	}
	return d, err
}

func (s *store) listDocuments(baseID string) ([]Document, error) {
	rows, err := s.db.Query(`SELECT `+documentColumns+` FROM documents WHERE base_id = ? AND lifecycle_state = ? ORDER BY created_at, id`, baseID, LifecycleActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectDocuments(rows)
}

func (s *store) listDocumentMetadata(baseID string) ([]Document, error) {
	return s.listDocumentMetadataContext(context.Background(), baseID)
}

func (s *store) listDocumentMetadataContext(ctx context.Context, baseID string) ([]Document, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+documentMetadataColumns+` FROM documents WHERE base_id = ? AND lifecycle_state = ? ORDER BY created_at, id`, baseID, LifecycleActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectDocumentMetadata(rows)
}

func (s *store) getDocumentMetadataContext(ctx context.Context, id string) (Document, error) {
	d, err := scanDocumentMetadata(s.db.QueryRowContext(ctx,
		`SELECT `+documentMetadataColumns+` FROM documents
		 WHERE id = ? AND lifecycle_state = ?`, id, LifecycleActive))
	if err == sql.ErrNoRows {
		return Document{}, ErrNotFound
	}
	return d, err
}

func (s *store) listChildDocumentMetadataContext(ctx context.Context, baseID, parentID string, limit, offset int) ([]Document, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM documents
		 WHERE base_id = ? AND COALESCE(parent_directory_id, '') = ? AND lifecycle_state = ?
		   AND (? <> '' OR source_type <> 'url')`,
		baseID, parentID, LifecycleActive, parentID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+documentMetadataColumns+` FROM documents
		 WHERE base_id = ? AND COALESCE(parent_directory_id, '') = ? AND lifecycle_state = ?
		   AND (? <> '' OR source_type <> 'url')
		 ORDER BY CASE WHEN source_type = 'directory' THEN 0 ELSE 1 END, created_at, id
		 LIMIT ? OFFSET ?`,
		baseID, parentID, LifecycleActive, parentID, limit, offset)
	if err != nil {
		return nil, total, err
	}
	defer rows.Close()
	docs, err := collectDocumentMetadata(rows)
	return docs, total, err
}

func (s *store) documentPathMetadataContext(ctx context.Context, baseID, documentID string) ([]Document, error) {
	var path []Document
	currentID := documentID
	for i := 0; i < 64 && currentID != ""; i++ {
		document, err := s.getDocumentMetadataContext(ctx, currentID)
		if err != nil {
			return nil, err
		}
		if document.BaseID != baseID {
			return nil, ErrNotFound
		}
		path = append([]Document{document}, path...)
		currentID = document.ParentDirectoryID
	}
	if currentID != "" {
		return nil, ErrConflict
	}
	return path, nil
}

func (s *store) listAllDocuments() ([]Document, error) {
	rows, err := s.db.Query(`SELECT `+documentColumns+` FROM documents WHERE lifecycle_state = ?`, LifecycleActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectDocuments(rows)
}

func (s *store) listAllDocumentMetadata() ([]Document, error) {
	rows, err := s.db.Query(`SELECT `+documentMetadataColumns+` FROM documents WHERE lifecycle_state = ?`, LifecycleActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectDocumentMetadata(rows)
}

type storageDocumentRef struct {
	ID          string
	RawFilePath string
	ChunkCount  int
}

func (s *store) recoverInterrupted(updatedAt int64, pendingStatus, processingStatus, failedStatus, interruptedCode, interruptedMessage string) (resumed, failed int, err error) {
	result, err := s.db.Exec(`UPDATE documents SET status = ?, incomplete = 1, updated_at = ?
		WHERE source_type <> 'directory' AND status IN (?, ?)
		  AND (COALESCE(raw_text, '') <> '' OR COALESCE(raw_file_path, '') <> '')`,
		pendingStatus, updatedAt, pendingStatus, processingStatus)
	if err != nil {
		return 0, 0, err
	}
	resumed64, err := result.RowsAffected()
	if err != nil {
		return 0, 0, err
	}
	result, err = s.db.Exec(`UPDATE documents SET status = ?, incomplete = 0,
		error_code = ?, error_message = ?, updated_at = ?
		WHERE source_type <> 'directory' AND status IN (?, ?)
		  AND COALESCE(raw_text, '') = '' AND COALESCE(raw_file_path, '') = ''`,
		failedStatus, interruptedCode, interruptedMessage, updatedAt, pendingStatus, processingStatus)
	if err != nil {
		return int(resumed64), 0, err
	}
	failed64, err := result.RowsAffected()
	if err != nil {
		return int(resumed64), 0, err
	}
	// Directory containers do not have a source payload that can be resumed by
	// the document importer. If their scan job disappeared with the process,
	// leave an explicit failed state instead of exposing a phantom active task
	// forever in indexing-status.
	result, err = s.db.Exec(`UPDATE documents SET status = ?, incomplete = 0,
		error_code = ?, error_message = ?, updated_at = ?
		WHERE source_type = 'directory' AND status IN (?, ?)`,
		failedStatus, interruptedCode, interruptedMessage, updatedAt, pendingStatus, processingStatus)
	if err != nil {
		return int(resumed64), int(failed64), err
	}
	directoryFailed64, err := result.RowsAffected()
	if err != nil {
		return int(resumed64), int(failed64), err
	}
	return int(resumed64), int(failed64) + int(directoryFailed64), nil
}

// listStorageRefs reads only the fields needed by storage reconciliation.
// In particular, it avoids loading raw_text for every document.
func (s *store) listStorageRefs() ([]storageDocumentRef, error) {
	rows, err := s.db.Query(`SELECT id, COALESCE(raw_file_path, ''), chunk_count FROM documents`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []storageDocumentRef
	for rows.Next() {
		var ref storageDocumentRef
		if err := rows.Scan(&ref.ID, &ref.RawFilePath, &ref.ChunkCount); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// listGenerationRawPaths exposes every immutable raw copy that is still covered
// by a durable generation citation. Retention GC removes mappings first; the
// storage reconciler then sees the file as an ordinary orphan.
func (s *store) listGenerationRawPaths() ([]string, error) {
	rows, err := s.db.Query(`SELECT COALESCE(raw_file_path, '')
		FROM document_generations WHERE raw_file_path <> ''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		out = append(out, path)
	}
	return out, rows.Err()
}

func collectDocuments(rows *sql.Rows) ([]Document, error) {
	var out []Document
	for rows.Next() {
		d, err := scanDocument(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func collectDocumentMetadata(rows *sql.Rows) ([]Document, error) {
	var out []Document
	for rows.Next() {
		d, err := scanDocumentMetadata(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *store) deleteDocument(id string) error {
	if _, err := s.db.Exec(`DELETE FROM documents WHERE id = ?`, id); err != nil {
		return err
	}
	s.invalidateStatsCache()
	return nil
}

func (s *store) countChunksByDoc(docID string) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM chunks c JOIN documents d ON d.id = c.doc_id
		WHERE c.doc_id = ? AND d.lifecycle_state = ? AND c.index_generation = d.active_index_generation`,
		docID, LifecycleActive).Scan(&count)
	return count, err
}

func (s *store) countEmbeddedChunksByDocGeneration(docID string, generation int64) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM chunks
		WHERE doc_id = ? AND index_generation = ? AND embedding IS NOT NULL`,
		docID, generation).Scan(&count)
	return count, err
}

func (s *store) updateChunkCount(docID string, count int) error {
	if _, err := s.db.Exec(`UPDATE documents SET chunk_count = ?, updated_at = ? WHERE id = ?`, count, now(), docID); err != nil {
		return err
	}
	s.invalidateStatsCache()
	return nil
}

func (s *store) findDocumentByTitle(baseID, title string) (Document, error) {
	d, err := scanDocument(s.db.QueryRow(
		`SELECT `+documentColumns+` FROM documents WHERE base_id = ? AND title = ? AND lifecycle_state = ? ORDER BY created_at LIMIT 1`,
		baseID, title, LifecycleActive))
	if err == sql.ErrNoRows {
		return Document{}, ErrNotFound
	}
	return d, err
}

// ── chunks ───────────────────────────────────────────────────────────────────

func documentFenceTx(tx *sql.Tx, docID string, expectedEpoch int64) (Document, error) {
	var d Document
	err := tx.QueryRow(`SELECT d.id, d.base_id, d.mutation_epoch, d.active_index_generation
		FROM documents d JOIN bases b ON b.id = d.base_id
		WHERE d.id = ? AND d.lifecycle_state = ? AND b.lifecycle_state = ?`,
		docID, LifecycleActive, LifecycleActive).Scan(&d.ID, &d.BaseID, &d.MutationEpoch, &d.ActiveIndexGen)
	if err == sql.ErrNoRows {
		return Document{}, ErrConflict
	}
	if err != nil {
		return Document{}, err
	}
	if expectedEpoch != 0 && d.MutationEpoch != expectedEpoch {
		return Document{}, ErrConflict
	}
	var fenced int
	if err := tx.QueryRow(`WITH RECURSIVE ancestors(id, parent_id) AS (
			SELECT id, parent_directory_id FROM documents WHERE id = ?
			UNION ALL
			SELECT d.id, d.parent_directory_id FROM documents d
			JOIN ancestors a ON d.id = a.parent_id
		) SELECT COUNT(*) FROM documents WHERE id IN (SELECT id FROM ancestors) AND lifecycle_state <> ?`,
		docID, LifecycleActive).Scan(&fenced); err != nil {
		return Document{}, err
	}
	if fenced != 0 {
		return Document{}, ErrConflict
	}
	return d, nil
}

func insertChunkTx(ctx context.Context, tx *sql.Tx, c Chunk) error {
	var heading any
	if c.Heading != "" {
		heading = c.Heading
	}
	var hash any
	if c.EmbeddingHash != "" {
		hash = c.EmbeddingHash
	}
	var embedding, model any
	if c.EmbeddingVec != nil {
		embedding = encodeEmbedding(c.EmbeddingVec)
		model = c.EmbeddingModel
	} else if c.StageEmbedding != nil {
		embedding = c.StageEmbedding
		model = c.StageEmbeddingModel
	}
	_, err := tx.ExecContext(ctx,
		`INSERT INTO chunks (id, doc_id, base_id, idx, text, heading, context, embedding, embedding_model, embedding_text_hash, created_at, index_generation)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.DocID, c.BaseID, c.Index, c.Text, heading, c.Context, embedding, model, hash, c.CreatedAt, c.IndexGeneration,
	)
	return err
}

// putChunksReplace stages a new generation without deleting the active one.
// Activation happens only after the caller has validated and embedded the
// staged rows; a crash therefore leaves the old searchable version intact.
func (s *store) putChunksReplace(ctx context.Context, chunks []Chunk, targetModelKey string) (int64, error) {
	if len(chunks) == 0 {
		return 0, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	docID := chunks[0].DocID
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := documentFenceTx(tx, docID, 0)
	if err != nil {
		return 0, err
	}
	// A failed or abandoned attempt can leave an unpublished generation after
	// the authoritative active generation. Remove it before allocating the
	// same generation number again; active and retired-but-cited rows remain.
	if _, err := tx.ExecContext(ctx, `DELETE FROM chunks
		WHERE doc_id = ? AND index_generation > ?`, docID, current.ActiveIndexGen); err != nil {
		return 0, err
	}
	// Carry over stored vectors whose embedding-text hash survives the
	// re-chunk and whose model identity equals the target space. Hash equality
	// alone would let model A vectors enter a model B generation.
	carried := map[string][2]any{}
	legacyQuery := `SELECT embedding_text_hash, embedding, embedding_model FROM chunks
		WHERE doc_id = ? AND index_generation = ? AND embedding IS NOT NULL`
	legacyArgs := []any{docID, current.ActiveIndexGen}
	if targetModelKey != "" {
		legacyQuery += ` AND embedding_model = ?`
		legacyArgs = append(legacyArgs, targetModelKey)
	}
	legacy, err := tx.QueryContext(ctx, legacyQuery, legacyArgs...)
	if err != nil {
		return 0, err
	}
	for legacy.Next() {
		var hash string
		var blob []byte
		var model string
		if err := legacy.Scan(&hash, &blob, &model); err != nil {
			_ = legacy.Close()
			return 0, err
		}
		carried[hash] = [2]any{blob, model}
	}
	if err := legacy.Err(); err != nil {
		_ = legacy.Close()
		return 0, err
	}
	_ = legacy.Close()
	generation := current.ActiveIndexGen + 1
	for i := range chunks {
		chunks[i].IndexGeneration = generation
		chunks[i].ID = fmt.Sprintf("%s:g%d:%d", chunks[i].DocID, generation, chunks[i].Index)
		if stage, ok := carried[chunks[i].EmbeddingHash]; ok {
			chunks[i].StageEmbedding = stage[0].([]byte)
			chunks[i].StageEmbeddingModel = stage[1].(string)
		} else if targetModelKey == "" {
			chunks[i].StageEmbedding = nil
			chunks[i].StageEmbeddingModel = ""
		}
	}
	for _, chunk := range chunks {
		if err := insertChunkTx(ctx, tx, chunk); err != nil {
			return 0, err
		}
	}
	if targetModelKey == "" {
		if _, err := tx.Exec(`UPDATE chunks SET embedding = NULL, embedding_model = NULL
			WHERE doc_id = ? AND index_generation = ?`, docID, generation); err != nil {
			return 0, err
		}
	}
	return generation, tx.Commit()
}

// activateDocumentGeneration is the short transactional switch. The caller has
// already parsed and validated the staged generation; no model or file work is
// done while this write transaction is open.
func (s *store) activateDocumentGeneration(ctx context.Context, docID string, expectedEpoch, generation int64, d Document) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	current, err := documentFenceTx(tx, docID, expectedEpoch)
	if err != nil {
		return err
	}
	if current.ActiveIndexGen+1 != generation {
		return ErrConflict
	}
	d.ID = docID
	d.LifecycleState = LifecycleActive
	d.MutationEpoch = current.MutationEpoch
	d.ActiveIndexGen = generation
	d.DesiredIndexGen = generation
	d.HasDesiredIndexGen = true
	d.IndexState = IndexStateActive
	if err := upsertDocument(ctx, tx, d); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO document_generations
		(doc_id, index_generation, source_version, chunk_count, created_at,
		 raw_file_path, content_hash)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(doc_id, index_generation) DO UPDATE SET
		  source_version = excluded.source_version,
		  chunk_count = excluded.chunk_count,
		  raw_file_path = excluded.raw_file_path,
		  content_hash = excluded.content_hash`,
		d.ID, generation, d.SourceVersion, d.ChunkCount, now(),
		d.RawFilePath, d.ContentHash); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.invalidateStatsCache()
	return nil
}

func (s *store) pruneRetiredGenerations(docID string, activeGeneration int64) ([]string, error) {
	const batchSize = 256
	cutoff := now() - retiredGenerationRetentionMS
	// Live search requests hold their WAL snapshot. This bounded grace also
	// gives a caller time to issue a continuation for a prior generation;
	// after expiry the explicit request receives evidence-expired rather than
	// silently reading a newer version.
	for {
		tx, err := s.db.Begin()
		if err != nil {
			return nil, err
		}
		result, err := tx.Exec(`DELETE FROM chunks WHERE rowid IN (
			SELECT rowid FROM chunks
			WHERE doc_id = ? AND index_generation < ? AND created_at < ? LIMIT ?
		)`, docID, activeGeneration, cutoff, batchSize)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected < batchSize {
			break
		}
	}
	rows, err := s.db.Query(`SELECT COALESCE(raw_file_path, '') FROM document_generations
		WHERE doc_id = ? AND index_generation < ? AND created_at < ?`,
		docID, activeGeneration, cutoff)
	if err != nil {
		return nil, err
	}
	var rawPaths []string
	for rows.Next() {
		var rawPath string
		if err := rows.Scan(&rawPath); err != nil {
			_ = rows.Close()
			return nil, err
		}
		rawPaths = append(rawPaths, rawPath)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	if _, err := s.db.Exec(`DELETE FROM document_generations
		WHERE doc_id = ? AND index_generation < ? AND created_at < ?`,
		docID, activeGeneration, cutoff); err != nil {
		return nil, err
	}
	return rawPaths, nil
}

func (s *store) markDocumentTreeDeleting(docID string) (Document, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Document{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var d Document
	err = tx.QueryRow(`SELECT id, base_id, COALESCE(raw_file_path, ''), mutation_epoch FROM documents
		WHERE id = ? AND lifecycle_state = ?`, docID, LifecycleActive).Scan(
		&d.ID, &d.BaseID, &d.RawFilePath, &d.MutationEpoch)
	if err == sql.ErrNoRows {
		return Document{}, ErrNotFound
	}
	if err != nil {
		return Document{}, err
	}
	if _, err := tx.Exec(`WITH RECURSIVE tree(id) AS (
			SELECT id FROM documents WHERE id = ?
			UNION ALL
			SELECT d.id FROM documents d JOIN tree t ON d.parent_directory_id = t.id
		) UPDATE documents SET lifecycle_state = ?, mutation_epoch = mutation_epoch + 1
		WHERE id IN (SELECT id FROM tree)`, docID, LifecycleDeleting); err != nil {
		return Document{}, err
	}
	if err := tx.Commit(); err != nil {
		return Document{}, err
	}
	s.invalidateStatsCache()
	return d, nil
}

func (s *store) markBaseDeleting(baseID string) (Base, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Base{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var b Base
	if err := tx.QueryRow(`SELECT id, lifecycle_state, mutation_epoch FROM bases
		WHERE id = ? AND lifecycle_state = ?`, baseID, LifecycleActive).Scan(
		&b.ID, &b.LifecycleState, &b.MutationEpoch); err != nil {
		if err == sql.ErrNoRows {
			return Base{}, ErrNotFound
		}
		return Base{}, err
	}
	if _, err := tx.Exec(`UPDATE bases SET lifecycle_state = ?, mutation_epoch = mutation_epoch + 1 WHERE id = ?`,
		LifecycleDeleting, baseID); err != nil {
		return Base{}, err
	}
	if _, err := tx.Exec(`UPDATE documents SET lifecycle_state = ?, mutation_epoch = mutation_epoch + 1
		WHERE base_id = ? AND lifecycle_state = ?`, LifecycleDeleting, baseID, LifecycleActive); err != nil {
		return Base{}, err
	}
	if err := tx.Commit(); err != nil {
		return Base{}, err
	}
	s.invalidateStatsCache()
	return b, nil
}

type cleanupDocumentRef struct {
	ID             string
	RawFilePath    string
	ActiveIndexGen int64
}

func (s *store) listDocumentTreeCleanupRefs(rootID string) ([]cleanupDocumentRef, error) {
	rows, err := s.db.Query(`WITH RECURSIVE tree(id) AS (
			SELECT id FROM documents WHERE id = ?
			UNION ALL
			SELECT d.id FROM documents d JOIN tree t ON d.parent_directory_id = t.id
		) SELECT d.id, COALESCE(d.raw_file_path, ''), d.active_index_generation
		FROM documents d WHERE d.id IN (SELECT id FROM tree)`, rootID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []cleanupDocumentRef
	for rows.Next() {
		var ref cleanupDocumentRef
		if err := rows.Scan(&ref.ID, &ref.RawFilePath, &ref.ActiveIndexGen); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

func (s *store) listBaseCleanupRefs(baseID string) ([]cleanupDocumentRef, error) {
	rows, err := s.db.Query(`SELECT id, COALESCE(raw_file_path, ''), active_index_generation
		FROM documents WHERE base_id = ?`, baseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []cleanupDocumentRef
	for rows.Next() {
		var ref cleanupDocumentRef
		if err := rows.Scan(&ref.ID, &ref.RawFilePath, &ref.ActiveIndexGen); err != nil {
			return nil, err
		}
		out = append(out, ref)
	}
	return out, rows.Err()
}

// startDocumentIngest binds the processing transition and the full ancestor
// lifecycle fence in one transaction. New child rows therefore cannot be
// inserted after an ancestor delete has committed.
func (s *store) startDocumentIngest(d Document) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var baseCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM bases WHERE id = ? AND lifecycle_state = ?`,
		d.BaseID, LifecycleActive).Scan(&baseCount); err != nil {
		return err
	}
	if baseCount == 0 {
		return ErrConflict
	}
	if d.ParentDirectoryID != "" {
		var fenced int
		if err := tx.QueryRow(`WITH RECURSIVE tree(id, parent_id) AS (
				SELECT id, parent_directory_id FROM documents WHERE id = ?
				UNION ALL
				SELECT doc.id, doc.parent_directory_id FROM documents doc
				JOIN tree ancestor ON doc.id = ancestor.parent_id
			) SELECT COUNT(*) FROM documents WHERE id IN (SELECT id FROM tree) AND lifecycle_state <> ?`,
			d.ParentDirectoryID, LifecycleActive).Scan(&fenced); err != nil {
			return err
		}
		if fenced != 0 {
			return ErrConflict
		}
	}
	if err := upsertDocument(context.Background(), tx, d); err != nil {
		return err
	}
	var lifecycle string
	if err := tx.QueryRow(`SELECT lifecycle_state FROM documents WHERE id = ?`, d.ID).Scan(&lifecycle); err != nil {
		return err
	}
	if lifecycle != LifecycleActive {
		return ErrConflict
	}
	return tx.Commit()
}

func (s *store) deleteChunks(docID string) error {
	if _, err := s.db.Exec(`DELETE FROM chunks WHERE doc_id = ?`, docID); err != nil {
		return err
	}
	s.invalidateStatsCache()
	return nil
}

func (s *store) deleteDocumentGeneration(docID string, generation int64) error {
	const batchSize = 256
	for {
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		result, err := tx.Exec(`DELETE FROM chunks WHERE rowid IN (
			SELECT rowid FROM chunks WHERE doc_id = ? AND index_generation = ? LIMIT ?
		)`, docID, generation, batchSize)
		if err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected < batchSize {
			return nil
		}
		s.invalidateStatsCache()
	}
}

// deleteDocumentGenerations removes every retained chunk generation and its
// citation mappings. Returned raw paths are safe for the physical cleaner to
// delete because the authoritative references are gone once this commits.
func (s *store) deleteDocumentGenerations(docID string) ([]string, error) {
	const batchSize = 256
	for {
		tx, err := s.db.Begin()
		if err != nil {
			return nil, err
		}
		result, err := tx.Exec(`DELETE FROM chunks WHERE rowid IN (
			SELECT rowid FROM chunks WHERE doc_id = ? LIMIT ?
		)`, docID, batchSize)
		if err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return nil, err
		}
		if affected < batchSize {
			break
		}
		s.invalidateStatsCache()
	}
	rows, err := s.db.Query(`SELECT COALESCE(raw_file_path, '')
		FROM document_generations WHERE doc_id = ?`, docID)
	if err != nil {
		return nil, err
	}
	var rawPaths []string
	for rows.Next() {
		var rawPath string
		if err := rows.Scan(&rawPath); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if rawPath != "" {
			rawPaths = append(rawPaths, rawPath)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	if _, err := s.db.Exec(`DELETE FROM document_generations WHERE doc_id = ?`, docID); err != nil {
		return nil, err
	}
	s.invalidateStatsCache()
	return rawPaths, nil
}

func (s *store) deleteChunksByBase(baseID string) error {
	if _, err := s.db.Exec(`DELETE FROM chunks WHERE base_id = ?`, baseID); err != nil {
		return err
	}
	s.invalidateStatsCache()
	return nil
}

func (s *store) listChunksByDoc(docID string, limit, offset int) ([]Chunk, error) {
	query := `SELECT c.id, c.doc_id, c.base_id, c.idx, c.text, COALESCE(c.heading, ''), COALESCE(c.context, ''),
	          COALESCE(c.embedding_text_hash, ''), c.created_at
	          FROM chunks c JOIN documents d ON d.id = c.doc_id
	          WHERE c.doc_id = ? AND d.lifecycle_state = ? AND c.index_generation = d.active_index_generation ORDER BY c.idx`
	args := []any{docID, LifecycleActive}
	if limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Chunk
	for rows.Next() {
		var c Chunk
		if err := rows.Scan(&c.ID, &c.DocID, &c.BaseID, &c.Index, &c.Text, &c.Heading, &c.Context, &c.EmbeddingHash, &c.CreatedAt); err != nil {
			return nil, err
		}
		c.EmbeddingText = searchTextOf(c)
		out = append(out, c)
	}
	return out, rows.Err()
}

// statsFor returns a short-lived aggregate snapshot; empty baseID aggregates
// every base. Callers that change document/chunk aggregates must invalidate.
func (s *store) statsFor(baseID string) (Stats, error) {
	s.statsMu.Lock()
	if entry, ok := s.statsCache[baseID]; ok && s.statsNow().Before(entry.expires) {
		s.statsMu.Unlock()
		return entry.stats, nil
	}
	s.statsMu.Unlock()
	stats, err := s.rawStatsFor(baseID)
	if err != nil {
		return Stats{}, err
	}
	s.statsMu.Lock()
	if s.statsCache == nil {
		s.statsCache = make(map[string]statsCacheEntry)
	}
	s.statsCache[baseID] = statsCacheEntry{stats: stats, expires: s.statsNow().Add(s.statsTTL)}
	s.statsMu.Unlock()
	return stats, nil
}

func (s *store) rawStatsFor(baseID string) (Stats, error) {
	stats := Stats{BaseID: baseID}
	baseFilter := ``
	args := []any{LifecycleActive}
	if baseID != "" {
		baseFilter = ` AND base_id = ?`
		args = append(args, baseID)
	}
	if err := s.db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(char_count), 0), COALESCE(SUM(token_count), 0) FROM documents WHERE lifecycle_state = ?`+baseFilter, args...,
	).Scan(&stats.DocumentCount, &stats.CharCount, &stats.TokenCount); err != nil {
		return Stats{}, err
	}
	if err := s.db.QueryRow(
		`SELECT COALESCE(SUM(chunk_count), 0) FROM documents WHERE lifecycle_state = ?`+baseFilter, args...,
	).Scan(&stats.ChunkCount); err != nil {
		return Stats{}, err
	}
	return stats, nil
}

func searchTextOf(c Chunk) string {
	return strings.TrimSpace(c.Context + " " + c.Text)
}

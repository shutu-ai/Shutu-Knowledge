package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/documentir"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

// store is the SQL access layer over the shared SQLite database.
type store struct {
	db *storage.DB

	stageMu    sync.Mutex
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
	return s.putBaseContext(context.Background(), b)
}

func (s *store) putBaseContext(ctx context.Context, b Base) error {
	if ctx == nil {
		ctx = context.Background()
	}
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
	err = s.db.WriteTx(ctx, storage.ControlWrite, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
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
		if err := tx.QueryRowContext(ctx, `SELECT lifecycle_state FROM bases WHERE id = ?`, b.ID).Scan(&state); err != nil {
			return err
		}
		if state != "active" {
			return ErrConflict
		}
		return nil
	})
	return err
}

func (s *store) getBase(id string) (Base, error) {
	return s.getBaseContext(context.Background(), id)
}

func (s *store) getBaseContext(ctx context.Context, id string) (Base, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	b, err := scanBase(s.db.QueryRowContext(ctx,
		`SELECT `+baseColumns+` FROM bases WHERE id = ? AND lifecycle_state = ?`, id, LifecycleActive))
	if err == sql.ErrNoRows {
		return Base{}, ErrNotFound
	}
	return b, err
}

func (s *store) getBaseIncludingDeleting(id string) (Base, error) {
	return s.getBaseIncludingDeletingContext(context.Background(), id)
}

func (s *store) getBaseIncludingDeletingContext(ctx context.Context, id string) (Base, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	b, err := scanBase(s.db.QueryRowContext(ctx, `SELECT `+baseColumns+` FROM bases WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return Base{}, ErrNotFound
	}
	return b, err
}

func (s *store) listBases() ([]Base, error) {
	return s.listBasesContext(context.Background())
}

func (s *store) listBasesContext(ctx context.Context) ([]Base, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+baseColumns+` FROM bases WHERE lifecycle_state = ? ORDER BY created_at, id`, LifecycleActive)
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
	quality_status, quality_score, quality_warnings, quality_partial, extraction_method, pages_total, pages_ocr,
	lifecycle_state, mutation_epoch, source_version, active_index_generation, desired_index_generation, index_state`

const documentMetadataColumns = `id, base_id, title, source_type, file_name, mime_type, url, parent_directory_id,
	source_path, content_hash, raw_file_path, char_count, token_count, chunk_count,
	embedding_model, embedding_ready, status, phase, progress, incomplete, error_code, error_message, created_at, updated_at, title_locked,
	quality_status, quality_score, quality_warnings, quality_partial, extraction_method, pages_total, pages_ocr,
	lifecycle_state, mutation_epoch, source_version, active_index_generation, desired_index_generation, index_state`

func scanDocument(row interface{ Scan(...any) error }) (Document, error) {
	var d Document
	var fileName, mimeType, url, parentDir, sourcePath, contentHash, rawFilePath, rawText, embeddingModel sql.NullString
	var tokenCount, updatedAt sql.NullInt64
	var phase, errorCode, errorMessage sql.NullString
	var desiredGeneration sql.NullInt64
	var incomplete, embeddingReady, titleLocked int
	var qualityStatus, qualityWarnings, extractionMethod sql.NullString
	var qualityScore, pagesTotal, pagesOCR sql.NullFloat64
	var qualityPartial sql.NullInt64
	err := row.Scan(&d.ID, &d.BaseID, &d.Title, &d.SourceType, &fileName, &mimeType, &url, &parentDir,
		&sourcePath, &contentHash, &rawFilePath, &rawText, &d.CharCount, &tokenCount, &d.ChunkCount,
		&embeddingModel, &embeddingReady, &d.Status, &phase, &d.Progress, &incomplete, &errorCode, &errorMessage, &d.CreatedAt, &updatedAt,
		&titleLocked, &qualityStatus, &qualityScore, &qualityWarnings, &qualityPartial, &extractionMethod, &pagesTotal, &pagesOCR,
		&d.LifecycleState, &d.MutationEpoch, &d.SourceVersion, &d.ActiveIndexGen,
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
	scanQualityColumns(&d, qualityStatus, qualityWarnings, qualityPartial, extractionMethod, pagesTotal, pagesOCR, qualityScore)
	return d, nil
}

func scanDocumentMetadata(row interface{ Scan(...any) error }) (Document, error) {
	var d Document
	var fileName, mimeType, url, parentDir, sourcePath, contentHash, rawFilePath, embeddingModel sql.NullString
	var tokenCount, updatedAt sql.NullInt64
	var phase, errorCode, errorMessage sql.NullString
	var desiredGeneration sql.NullInt64
	var incomplete, embeddingReady, titleLocked int
	var qualityStatus, qualityWarnings, extractionMethod sql.NullString
	var qualityScore, pagesTotal, pagesOCR sql.NullFloat64
	var qualityPartial sql.NullInt64
	err := row.Scan(&d.ID, &d.BaseID, &d.Title, &d.SourceType, &fileName, &mimeType, &url, &parentDir,
		&sourcePath, &contentHash, &rawFilePath, &d.CharCount, &tokenCount, &d.ChunkCount,
		&embeddingModel, &embeddingReady, &d.Status, &phase, &d.Progress, &incomplete, &errorCode, &errorMessage, &d.CreatedAt, &updatedAt,
		&titleLocked, &qualityStatus, &qualityScore, &qualityWarnings, &qualityPartial, &extractionMethod, &pagesTotal, &pagesOCR,
		&d.LifecycleState, &d.MutationEpoch, &d.SourceVersion, &d.ActiveIndexGen,
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
	scanQualityColumns(&d, qualityStatus, qualityWarnings, qualityPartial, extractionMethod, pagesTotal, pagesOCR, qualityScore)
	return d, nil
}

// scanQualityColumns attaches the v0.6.2 quality columns to a document.
func scanQualityColumns(d *Document, status, warnings sql.NullString, partial sql.NullInt64, method sql.NullString, pagesTotal, pagesOCR, score sql.NullFloat64) {
	// Legacy rows migrated without quality metadata surface explicitly as
	// UNKNOWN instead of being mistaken for GOOD.
	d.QualityStatus = status.String
	if d.QualityStatus == "" {
		d.QualityStatus = QualityUnknown
	}
	d.QualityWarnings = parseQualityWarnings(warnings.String)
	d.QualityPartial = partial.Int64 != 0
	d.ExtractionMethod = method.String
	d.PagesTotal = int(pagesTotal.Float64)
	d.PagesOCR = int(pagesOCR.Float64)
	d.QualityScore = score.Float64
}

func (s *store) putDocument(d Document) error {
	return s.putDocumentContext(context.Background(), d)
}

func (s *store) putDocumentContext(ctx context.Context, d Document) error {
	if ctx == nil {
		ctx = context.Background()
	}
	err := s.db.WriteTx(ctx, storage.ControlWrite, nil, func(tx *sql.Tx) error {
		return upsertDocument(ctx, tx, d)
	})
	if err != nil {
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
	var previousSourceType, previousContentHash, previousSourcePath, previousRawFilePath string
	var previousSourceVersion int64
	sourceChanged := false
	err := runner.QueryRowContext(ctx, `SELECT source_type, source_version,
		content_hash, source_path, COALESCE(raw_file_path, '') FROM documents WHERE id = ?`, d.ID).
		Scan(&previousSourceType, &previousSourceVersion, &previousContentHash, &previousSourcePath, &previousRawFilePath)
	switch {
	case err == sql.ErrNoRows:
		sourceChanged = true
	case err != nil:
		return err
	default:
		sourceChanged = previousSourceType != d.SourceType ||
			previousSourceVersion != d.SourceVersion ||
			previousContentHash != d.ContentHash ||
			previousSourcePath != d.SourcePath ||
			previousRawFilePath != d.RawFilePath
	}
	result, err := runner.ExecContext(ctx,
		`INSERT INTO documents (id, base_id, title, source_type, file_name, mime_type, url, parent_directory_id,
		   source_path, content_hash, raw_file_path, raw_text, char_count, token_count, chunk_count,
		   embedding_model, embedding_ready, status, phase, progress, incomplete, error_code, error_message, created_at, updated_at, title_locked,
		   quality_status, quality_score, quality_warnings, quality_partial, extraction_method, pages_total, pages_ocr,
		   lifecycle_state, mutation_epoch, source_version, active_index_generation, desired_index_generation, index_state)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET title = excluded.title, file_name = excluded.file_name,
		   quality_status = excluded.quality_status, quality_score = excluded.quality_score,
		   quality_warnings = excluded.quality_warnings, quality_partial = excluded.quality_partial,
		   extraction_method = excluded.extraction_method, pages_total = excluded.pages_total,
		   pages_ocr = excluded.pages_ocr,
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
		d.QualityStatus, d.QualityScore, formatQualityWarnings(d.QualityWarnings), qualityPartialInt(d.QualityPartial), d.ExtractionMethod, d.PagesTotal, d.PagesOCR,
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
	if sourceChanged {
		if _, err := runner.ExecContext(ctx,
			`UPDATE bases SET mutation_epoch = mutation_epoch + 1
			 WHERE id = ? AND lifecycle_state = ?`, d.BaseID, LifecycleActive); err != nil {
			return err
		}
	}
	return err
}

func (s *store) getDocument(id string) (Document, error) {
	return s.getDocumentContext(context.Background(), id)
}

func (s *store) getDocumentContext(ctx context.Context, id string) (Document, error) {
	d, err := scanDocument(s.db.QueryRowContext(ctx,
		`SELECT `+documentColumns+` FROM documents WHERE id = ? AND lifecycle_state = ?`, id, LifecycleActive))
	if err == sql.ErrNoRows {
		return Document{}, ErrNotFound
	}
	return d, err
}

// getDocumentIncludingDeleting is for delete-operation recovery: a tombstone
// must remain findable so cleanup can resume, while normal reads exclude it.
func (s *store) getDocumentIncludingDeleting(id string) (Document, error) {
	return s.getDocumentIncludingDeletingContext(context.Background(), id)
}

func (s *store) getDocumentIncludingDeletingContext(ctx context.Context, id string) (Document, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	d, err := scanDocument(s.db.QueryRowContext(ctx, `SELECT `+documentColumns+` FROM documents WHERE id = ?`, id))
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

func (s *store) listDocumentsAfterContext(ctx context.Context, baseID string,
	afterCreatedAt int64, afterID string, limit int,
) ([]Document, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+documentMetadataColumns+` FROM documents
		WHERE base_id = ? AND lifecycle_state = ?
		  AND (created_at > ? OR (created_at = ? AND id > ?))
		ORDER BY created_at, id LIMIT ?`, baseID, LifecycleActive,
		afterCreatedAt, afterCreatedAt, afterID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectDocumentMetadata(rows)
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

func (s *store) listDocumentMetadataTreeContext(ctx context.Context, baseID, rootID string) ([]Document, error) {
	rows, err := s.db.QueryContext(ctx, `WITH RECURSIVE tree(id) AS (
			SELECT id FROM documents
			 WHERE id = ? AND base_id = ? AND lifecycle_state = ?
			UNION ALL
			SELECT child.id FROM documents child
			 JOIN tree parent ON child.parent_directory_id = parent.id
			 WHERE child.lifecycle_state = ?
		) SELECT `+documentMetadataColumns+` FROM documents
		 WHERE id IN (SELECT id FROM tree) ORDER BY created_at, id`,
		rootID, baseID, LifecycleActive, LifecycleActive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectDocumentMetadata(rows)
}

func (s *store) findDocumentBySourcePathContext(ctx context.Context, baseID, sourcePath string) (Document, error) {
	d, err := scanDocumentMetadata(s.db.QueryRowContext(ctx,
		`SELECT `+documentMetadataColumns+` FROM documents
		 WHERE base_id = ? AND source_path = ? AND lifecycle_state = ?
		 ORDER BY created_at, id LIMIT 1`, baseID, sourcePath, LifecycleActive))
	if err == sql.ErrNoRows {
		return Document{}, ErrNotFound
	}
	return d, err
}

func (s *store) contentHashesPresentContext(ctx context.Context, baseID string, hashes []string) (map[string]bool, error) {
	present := make(map[string]bool, len(hashes))
	if len(hashes) == 0 {
		return present, nil
	}
	placeholders := make([]string, len(hashes))
	args := make([]any, 0, len(hashes)+2)
	args = append(args, baseID, LifecycleActive)
	for index, hash := range hashes {
		placeholders[index] = "?"
		args = append(args, hash)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT content_hash FROM documents
		WHERE base_id = ? AND lifecycle_state = ? AND content_hash IN (`+
		strings.Join(placeholders, ",")+
		`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var hash string
		if err := rows.Scan(&hash); err != nil {
			return nil, err
		}
		present[hash] = true
	}
	return present, rows.Err()
}

func (s *store) listDocumentMetadataPageContext(ctx context.Context, baseID string, limit, offset int) ([]Document, int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM documents WHERE base_id = ? AND lifecycle_state = ?`,
		baseID, LifecycleActive).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+documentMetadataColumns+` FROM documents
		 WHERE base_id = ? AND lifecycle_state = ?
		 ORDER BY created_at, id LIMIT ? OFFSET ?`,
		baseID, LifecycleActive, limit, offset)
	if err != nil {
		return nil, total, err
	}
	defer rows.Close()
	docs, err := collectDocumentMetadata(rows)
	return docs, total, err
}

// listActiveDocumentMetadataContext returns only the bounded set of documents
// that can contribute to the indexing-status control path. Keeping the status
// predicate in SQL avoids copying the whole documents table into the service
// just to discard ready/failed rows afterward.
func (s *store) listActiveDocumentMetadataContext(ctx context.Context, limit int) ([]Document, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 200 {
		limit = 200
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+documentMetadataColumns+` FROM documents
		WHERE lifecycle_state = ? AND status IN (?, ?)
		ORDER BY updated_at, id LIMIT ?`, LifecycleActive, StatusPending, StatusProcessing, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectDocumentMetadata(rows)
}

// countActiveDocumentsContext returns the bounded scalar needed by control
// paths. It deliberately does not materialize document metadata.
func (s *store) countActiveDocumentsContext(ctx context.Context, baseID string) (int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM documents WHERE base_id = ? AND lifecycle_state = ?`,
		baseID, LifecycleActive).Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *store) countActiveNonDirectoryDocumentsContext(ctx context.Context, baseID string) (int, error) {
	var total int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM documents
		 WHERE base_id = ? AND lifecycle_state = ? AND source_type <> ?`,
		baseID, LifecycleActive, "directory").Scan(&total); err != nil {
		return 0, err
	}
	return total, nil
}

func (s *store) countDocumentTreeContext(ctx context.Context, rootID string) (int, error) {
	var total int
	err := s.db.QueryRowContext(ctx, `WITH RECURSIVE tree(id) AS (
		SELECT id FROM documents WHERE id = ? AND lifecycle_state = ?
		UNION ALL
		SELECT child.id FROM documents child JOIN tree parent ON child.parent_directory_id = parent.id
		 WHERE child.lifecycle_state = ?
	) SELECT COUNT(*) FROM tree`, rootID, LifecycleActive, LifecycleActive).Scan(&total)
	return total, err
}

// reindexDocumentWindow captures a stable upper cursor and the initial count
// for a base. Keyset pagination below can then walk the same target window
// without loading the whole base or including documents created later.
func (s *store) reindexDocumentWindow(ctx context.Context, baseID string) (total int, cutoffCreatedAt int64, cutoffID string, err error) {
	total, err = s.countActiveDocumentsContext(ctx, baseID)
	if err != nil || total == 0 {
		return total, 0, "", err
	}
	err = s.db.QueryRowContext(ctx,
		`SELECT created_at, id FROM documents
		 WHERE base_id = ? AND lifecycle_state = ?
		 ORDER BY created_at DESC, id DESC LIMIT 1`,
		baseID, LifecycleActive).Scan(&cutoffCreatedAt, &cutoffID)
	return total, cutoffCreatedAt, cutoffID, err
}

func (s *store) listReindexDocumentBatchContext(ctx context.Context, baseID string, cutoffCreatedAt int64, cutoffID string, afterCreatedAt int64, afterID string, limit int) ([]Document, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+documentMetadataColumns+` FROM documents
		 WHERE base_id = ? AND lifecycle_state = ?
		   AND (created_at < ? OR (created_at = ? AND id <= ?))
		   AND (created_at > ? OR (created_at = ? AND id > ?))
		 ORDER BY created_at, id LIMIT ?`,
		baseID, LifecycleActive,
		cutoffCreatedAt, cutoffCreatedAt, cutoffID,
		afterCreatedAt, afterCreatedAt, afterID, limit)
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

type storageDocumentRef struct {
	ID          string
	RawFilePath string
	ChunkCount  int
}

func (s *store) recoverInterrupted(updatedAt int64, pendingStatus, processingStatus, failedStatus, interruptedCode, interruptedMessage string) (resumed, failed int, err error) {
	return s.recoverInterruptedWithContext(context.Background(), updatedAt, pendingStatus, processingStatus,
		failedStatus, interruptedCode, interruptedMessage)
}

func (s *store) recoverInterruptedWithContext(ctx context.Context, updatedAt int64, pendingStatus, processingStatus, failedStatus, interruptedCode, interruptedMessage string) (resumed, failed int, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	resumed, err = s.recoverDocumentRows(ctx,
		`source_type <> 'directory' AND status IN (?, ?)
		 AND (COALESCE(raw_text, '') <> '' OR COALESCE(raw_file_path, '') <> '')`,
		[]any{pendingStatus, processingStatus},
		`UPDATE documents SET status = ?, incomplete = 1,
		 desired_index_generation = NULL, index_state = ?, updated_at = ?`,
		[]any{pendingStatus, IndexStateActive, updatedAt})
	if err != nil {
		return resumed, 0, err
	}
	failedWithoutSource, err := s.recoverDocumentRows(ctx,
		`source_type <> 'directory' AND status IN (?, ?)
		 AND COALESCE(raw_text, '') = '' AND COALESCE(raw_file_path, '') = ''`,
		[]any{pendingStatus, processingStatus},
		`UPDATE documents SET status = ?, incomplete = 0,
		 desired_index_generation = NULL, index_state = ?,
		 error_code = ?, error_message = ?, updated_at = ?`,
		[]any{failedStatus, IndexStateActive, interruptedCode, interruptedMessage, updatedAt})
	if err != nil {
		return resumed, failedWithoutSource, err
	}
	failed += failedWithoutSource
	// Directory containers do not have a source payload that can be resumed by
	// the document importer. If their scan job disappeared with the process,
	// leave an explicit failed state instead of exposing a phantom active task
	// forever in indexing-status.
	directoryFailed, err := s.recoverDocumentRows(ctx,
		`source_type = 'directory' AND status IN (?, ?)`,
		[]any{pendingStatus, processingStatus},
		`UPDATE documents SET status = ?, incomplete = 0,
		 desired_index_generation = NULL, index_state = ?,
		 error_code = ?, error_message = ?, updated_at = ?`,
		[]any{failedStatus, IndexStateActive, interruptedCode, interruptedMessage, updatedAt})
	if err != nil {
		return resumed, failed + directoryFailed, err
	}
	return resumed, failed + directoryFailed, nil
}

// recoverDocumentRows selects at most one bounded batch of IDs at a time,
// then updates only those IDs through the control writer. The cursor prevents
// rows whose status remains pending from being selected again.
func (s *store) recoverDocumentRows(ctx context.Context, where string, selectArgs []any, updatePrefix string, updateArgs []any) (int, error) {
	const batchSize = 256
	afterID := ""
	updated := 0
	for {
		if err := ctx.Err(); err != nil {
			return updated, err
		}
		selectParams := make([]any, 0, len(selectArgs)+2)
		selectParams = append(selectParams, afterID)
		selectParams = append(selectParams, selectArgs...)
		selectParams = append(selectParams, batchSize)
		rows, err := s.db.QueryContext(ctx, `SELECT id FROM documents WHERE id > ? AND `+where+` ORDER BY id LIMIT ?`, selectParams...)
		if err != nil {
			return updated, err
		}
		ids := make([]string, 0, batchSize)
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				_ = rows.Close()
				return updated, err
			}
			ids = append(ids, id)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return updated, err
		}
		_ = rows.Close()
		if len(ids) == 0 {
			return updated, nil
		}
		placeholders := make([]string, len(ids))
		args := make([]any, 0, len(updateArgs)+len(ids))
		args = append(args, updateArgs...)
		for i, id := range ids {
			placeholders[i] = "?"
			args = append(args, id)
		}
		result, err := s.db.ExecPriority(ctx, storage.ControlWrite,
			updatePrefix+" WHERE id IN ("+strings.Join(placeholders, ",")+")", args...)
		if err != nil {
			return updated, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return updated, err
		}
		updated += int(affected)
		afterID = ids[len(ids)-1]
		if len(ids) < batchSize {
			return updated, nil
		}
	}
}

// visitStorageRefs reads only the fields needed by storage reconciliation.
// In particular, it avoids loading raw_text or materializing every document.
func (s *store) visitStorageRefs(ctx context.Context, visit func(storageDocumentRef) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if visit == nil {
		return fmt.Errorf("storage reference visitor is required")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, COALESCE(raw_file_path, ''), chunk_count FROM documents`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ref storageDocumentRef
		if err := rows.Scan(&ref.ID, &ref.RawFilePath, &ref.ChunkCount); err != nil {
			return err
		}
		if err := visit(ref); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (s *store) countStorageRefs(ctx context.Context) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM documents`).Scan(&count)
	return count, err
}

// visitGenerationRawPaths exposes immutable raw copies that are still covered
// by a durable generation citation. Retention GC removes mappings first; the
// storage reconciler then sees the file as an ordinary orphan.
func (s *store) visitGenerationRawPaths(ctx context.Context, visit func(string) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if visit == nil {
		return fmt.Errorf("generation path visitor is required")
	}
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(raw_file_path, '')
		FROM document_generations WHERE raw_file_path <> ''`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return err
		}
		if err := visit(path); err != nil {
			return err
		}
	}
	return rows.Err()
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
	return s.countChunksByDocContext(context.Background(), docID)
}

func (s *store) countChunksByDocContext(ctx context.Context, docID string) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks c JOIN documents d ON d.id = c.doc_id
		WHERE c.doc_id = ? AND d.lifecycle_state = ? AND c.index_generation = d.active_index_generation`,
		docID, LifecycleActive).Scan(&count)
	return count, err
}

func (s *store) countEmbeddedChunksByDocGeneration(docID string, generation int64) (int, error) {
	return s.countEmbeddedChunksByDocGenerationContext(context.Background(), docID, generation)
}

func (s *store) countEmbeddedChunksByDocGenerationContext(ctx context.Context, docID string, generation int64) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM chunks
		WHERE doc_id = ? AND index_generation = ? AND embedding IS NOT NULL`,
		docID, generation).Scan(&count)
	return count, err
}

func (s *store) updateChunkCount(docID string, count int) error {
	return s.updateChunkCountWithContext(context.Background(), docID, count)
}

func (s *store) updateChunkCountWithContext(ctx context.Context, docID string, count int) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := s.db.ExecPriority(ctx, storage.ControlWrite, `UPDATE documents SET chunk_count = ?, updated_at = ? WHERE id = ?`, count, now(), docID); err != nil {
		return err
	}
	s.invalidateStatsCache()
	return nil
}

func (s *store) findDocumentByTitle(baseID, title string) (Document, error) {
	return s.findDocumentByTitleContext(context.Background(), baseID, title)
}

func (s *store) findDocumentByTitleContext(ctx context.Context, baseID, title string) (Document, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	d, err := scanDocument(s.db.QueryRowContext(ctx,
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
	var desiredGeneration sql.NullInt64
	var indexState string
	err := tx.QueryRow(`SELECT d.id, d.base_id, d.mutation_epoch, d.active_index_generation,
		 d.desired_index_generation, d.index_state
		FROM documents d JOIN bases b ON b.id = d.base_id
		WHERE d.id = ? AND d.lifecycle_state = ? AND b.lifecycle_state = ?`,
		docID, LifecycleActive, LifecycleActive).Scan(
		&d.ID, &d.BaseID, &d.MutationEpoch, &d.ActiveIndexGen, &desiredGeneration, &indexState)
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
	d.DesiredIndexGen = desiredGeneration.Int64
	d.HasDesiredIndexGen = desiredGeneration.Valid
	d.IndexState = indexState
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
	if err != nil {
		return err
	}
	for order, nodeID := range c.NodeIDs {
		if strings.TrimSpace(nodeID) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO chunk_node_links
			(doc_id, index_generation, chunk_id, node_id, link_order)
			VALUES (?, ?, ?, ?, ?)`, c.DocID, c.IndexGeneration, c.ID, nodeID, order); err != nil {
			return err
		}
	}
	return nil
}

func insertDocumentIRTx(ctx context.Context, tx *sql.Tx, ir *documentir.Document, docID string, generation int64) error {
	if ir == nil {
		return nil
	}
	if ir.DocumentID != docID {
		return fmt.Errorf("IR document ID %q does not match %q", ir.DocumentID, docID)
	}
	if err := insertDocumentMetadataTx(ctx, tx, ir, docID, generation); err != nil {
		return err
	}
	if err := insertDocumentNodesTx(ctx, tx, ir, docID, generation, ir.Nodes); err != nil {
		return err
	}
	return insertDocumentRelationshipsTx(ctx, tx, ir, docID, generation)
}

func insertDocumentMetadataTx(ctx context.Context, tx *sql.Tx, ir *documentir.Document, docID string, generation int64) error {
	parseConfig, _ := json.Marshal(ir.ParseConfig)
	_, err := tx.ExecContext(ctx, `INSERT INTO document_parse_metadata
		(doc_id, index_generation, ir_version, parser, parser_version, parse_config)
		VALUES (?, ?, ?, ?, ?, ?)`, docID, generation, ir.IRVersion, ir.Parser, ir.ParserVersion, string(parseConfig))
	return err
}

func insertDocumentNodesTx(ctx context.Context, tx *sql.Tx, ir *documentir.Document, docID string, generation int64, nodes []documentir.Node) error {
	if len(nodes) == 0 {
		return nil
	}
	nodeStmt, err := tx.PrepareContext(ctx, `INSERT INTO document_nodes
		(doc_id, index_generation, node_id, parent_node_id, node_type, node_order,
			 text, heading_path, source_anchor, page_number, slide_number, sheet_name,
			 bbox, metadata, parser, parser_version, confidence)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer nodeStmt.Close()
	for _, node := range nodes {
		heading, _ := json.Marshal(node.HeadingPath)
		anchor, _ := json.Marshal(node.SourceAnchor)
		metadata, _ := json.Marshal(node.Metadata)
		var bbox any
		if node.BBox != nil {
			data, _ := json.Marshal(node.BBox)
			bbox = string(data)
		}
		if _, err := nodeStmt.ExecContext(ctx,
			docID, generation, node.ID, irNullableString(node.ParentID), node.Type, node.Order,
			node.Text, string(heading), string(anchor), irNullableInt(node.PageNumber), irNullableInt(node.SlideNumber), irNullableString(node.SheetName),
			bbox, string(metadata), node.Parser, node.ParserVersion, node.Confidence); err != nil {
			return err
		}
	}
	return nil
}

func insertDocumentRelationshipsTx(ctx context.Context, tx *sql.Tx, ir *documentir.Document, docID string, generation int64) error {
	if len(ir.Relationships) == 0 {
		return nil
	}
	relationStmt, err := tx.PrepareContext(ctx, `INSERT INTO document_relationships
		(doc_id, index_generation, relationship_id, from_node_id, to_node_id,
			 relationship_type, relationship_order) VALUES (?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer relationStmt.Close()
	for _, relation := range ir.Relationships {
		if _, err := relationStmt.ExecContext(ctx,
			docID, generation, relation.ID, relation.FromNodeID, relation.ToNodeID, relation.Type, relation.Order); err != nil {
			return err
		}
	}
	return nil
}
func irNullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func irNullableInt(value int) any {
	if value == 0 {
		return nil
	}
	return value
}

// putChunksReplace stages a new generation without deleting the active one.
// Activation happens only after the caller has validated and embedded the
// staged rows; a crash therefore leaves the old searchable version intact.
func (s *store) putChunksReplace(ctx context.Context, chunks []Chunk, targetModelKey string, irs ...*documentir.Document) (int64, error) {
	const batchSize = 256
	if len(chunks) == 0 {
		return 0, nil
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	docID := chunks[0].DocID
	var ir *documentir.Document
	if len(irs) > 0 {
		ir = irs[0]
	}
	s.stageMu.Lock()
	defer s.stageMu.Unlock()

	var generation int64
	var expectedEpoch int64
	var activeGeneration int64
	carried := map[string][2]any{}
	err := s.db.WriteTx(ctx, storage.ControlWrite, nil, func(tx *sql.Tx) error {
		current, err := documentFenceTx(tx, docID, 0)
		if err != nil {
			return err
		}
		if current.IndexState == IndexStateBuilding {
			return ErrConflict
		}
		expectedEpoch = current.MutationEpoch
		activeGeneration = current.ActiveIndexGen
		// Carry over stored vectors whose embedding-text hash survives the
		// re-chunk and whose model identity equals the target space. Hash equality
		// alone would let model A vectors enter a model B generation.
		legacyQuery := `SELECT embedding_text_hash, embedding, embedding_model FROM chunks
		WHERE doc_id = ? AND index_generation = ? AND embedding IS NOT NULL`
		legacyArgs := []any{docID, current.ActiveIndexGen}
		if targetModelKey != "" {
			legacyQuery += ` AND embedding_model = ?`
			legacyArgs = append(legacyArgs, targetModelKey)
		}
		legacy, err := tx.QueryContext(ctx, legacyQuery, legacyArgs...)
		if err != nil {
			return err
		}
		for legacy.Next() {
			var hash string
			var blob []byte
			var model string
			if err := legacy.Scan(&hash, &blob, &model); err != nil {
				_ = legacy.Close()
				return err
			}
			carried[hash] = [2]any{blob, model}
		}
		if err := legacy.Err(); err != nil {
			_ = legacy.Close()
			return err
		}
		_ = legacy.Close()
		generation = current.ActiveIndexGen + 1
		result, err := tx.Exec(`UPDATE documents SET desired_index_generation = ?,
			index_state = ?, updated_at = ?
			WHERE id = ? AND lifecycle_state = ? AND mutation_epoch = ?`,
			generation, IndexStateBuilding, now(), docID, LifecycleActive, expectedEpoch)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrConflict
		}
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
		return nil
	})
	if err != nil {
		return 0, err
	}
	// A failed or abandoned attempt can leave a large unpublished generation.
	// Clean it in bounded transactions; the old single control transaction
	// could exceed its budget before deleting hundreds of thousands of rows.
	if err := s.clearStagedGenerationsAfter(ctx, docID, activeGeneration); err != nil {
		_ = s.clearStagedGeneration(context.Background(), docID, expectedEpoch, generation)
		return 0, err
	}
	const irNodeBatchSize = 50000
	if ir != nil {
		if err := s.db.WriteTx(ctx, storage.NormalWrite, nil, func(tx *sql.Tx) error {
			current, err := documentFenceTx(tx, docID, expectedEpoch)
			if err != nil {
				return err
			}
			if !current.HasDesiredIndexGen || current.DesiredIndexGen != generation || current.IndexState != IndexStateBuilding {
				return ErrConflict
			}
			if err := insertDocumentMetadataTx(ctx, tx, ir, docID, generation); err != nil {
				return err
			}
			end := minInt(irNodeBatchSize, len(ir.Nodes))
			return insertDocumentNodesTx(ctx, tx, ir, docID, generation, ir.Nodes[:end])
		}); err != nil {
			if !errors.Is(err, storage.ErrWriteUnknown) {
				_ = s.clearStagedGeneration(context.Background(), docID, expectedEpoch, generation)
			}
			return 0, err
		}
		for start := irNodeBatchSize; start < len(ir.Nodes); start += irNodeBatchSize {
			end := minInt(start+irNodeBatchSize, len(ir.Nodes))
			if err := s.db.WriteTx(ctx, storage.NormalWrite, nil, func(tx *sql.Tx) error {
				current, err := documentFenceTx(tx, docID, expectedEpoch)
				if err != nil {
					return err
				}
				if !current.HasDesiredIndexGen || current.DesiredIndexGen != generation || current.IndexState != IndexStateBuilding {
					return ErrConflict
				}
				return insertDocumentNodesTx(ctx, tx, ir, docID, generation, ir.Nodes[start:end])
			}); err != nil {
				if !errors.Is(err, storage.ErrWriteUnknown) {
					_ = s.clearStagedGeneration(context.Background(), docID, expectedEpoch, generation)
				}
				return 0, err
			}
		}
		if len(ir.Relationships) > 0 {
			if err := s.db.WriteTx(ctx, storage.NormalWrite, nil, func(tx *sql.Tx) error {
				current, err := documentFenceTx(tx, docID, expectedEpoch)
				if err != nil {
					return err
				}
				if !current.HasDesiredIndexGen || current.DesiredIndexGen != generation || current.IndexState != IndexStateBuilding {
					return ErrConflict
				}
				return insertDocumentRelationshipsTx(ctx, tx, ir, docID, generation)
			}); err != nil {
				if !errors.Is(err, storage.ErrWriteUnknown) {
					_ = s.clearStagedGeneration(context.Background(), docID, expectedEpoch, generation)
				}
				return 0, err
			}
		}
	}
	for start := 0; start < len(chunks); start += batchSize {
		end := minInt(start+batchSize, len(chunks))
		batch := chunks[start:end]
		err := s.db.WriteTx(ctx, storage.NormalWrite, nil, func(tx *sql.Tx) error {
			current, err := documentFenceTx(tx, docID, expectedEpoch)
			if err != nil {
				return err
			}
			if !current.HasDesiredIndexGen || current.DesiredIndexGen != generation || current.IndexState != IndexStateBuilding {
				return ErrConflict
			}
			for _, chunk := range batch {
				if err := insertChunkTx(ctx, tx, chunk); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			if !errors.Is(err, storage.ErrWriteUnknown) {
				_ = s.clearStagedGeneration(context.Background(), docID, expectedEpoch, generation)
			}
			return 0, err
		}
	}
	if targetModelKey == "" {
		err = s.db.WriteTx(ctx, storage.NormalWrite, nil, func(tx *sql.Tx) error {
			current, err := documentFenceTx(tx, docID, expectedEpoch)
			if err != nil {
				return err
			}
			if !current.HasDesiredIndexGen || current.DesiredIndexGen != generation {
				return ErrConflict
			}
			_, err = tx.Exec(`UPDATE chunks SET embedding = NULL, embedding_model = NULL
				WHERE doc_id = ? AND index_generation = ?`, docID, generation)
			return err
		})
		if err != nil {
			if !errors.Is(err, storage.ErrWriteUnknown) {
				_ = s.clearStagedGeneration(context.Background(), docID, expectedEpoch, generation)
			}
			return 0, err
		}
	}
	_ = carried
	return generation, nil
}

func (s *store) clearStagedGenerationsAfter(ctx context.Context, docID string, activeGeneration int64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	var generations []byte
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(group_concat(distinct_generation), '') FROM (
		SELECT DISTINCT index_generation AS distinct_generation FROM chunks
		WHERE doc_id = ? AND index_generation > ?
		UNION
		SELECT DISTINCT index_generation FROM document_nodes
		WHERE doc_id = ? AND index_generation > ?
	)`, docID, activeGeneration, docID, activeGeneration).Scan(&generations); err != nil {
		return err
	}
	if len(generations) > 0 {
		for _, part := range strings.Split(string(generations), ",") {
			generation, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
			if err != nil {
				return err
			}
			if err := s.clearGenerationData(ctx, docID, generation); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *store) clearGenerationData(ctx context.Context, docID string, generation int64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	const batchSize = 5000
	// Children first so an interrupted cleanup never leaves links for deleted
	// chunks. Each table is drained independently in bounded transactions.
	for _, table := range []string{"chunk_node_links", "document_nodes", "document_relationships", "document_parse_metadata", "chunks"} {
		for {
			var affected int64
			err := s.db.WriteTx(ctx, storage.NormalWrite, nil, func(tx *sql.Tx) error {
				result, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE rowid IN (
					SELECT rowid FROM `+table+`
					WHERE doc_id = ? AND index_generation = ? LIMIT ?
				)`, docID, generation, batchSize)
				if err != nil {
					return err
				}
				affected, err = result.RowsAffected()
				return err
			})
			if err != nil {
				return err
			}
			if affected < batchSize {
				break
			}
		}
	}
	return nil
}

func (s *store) clearStagedGeneration(ctx context.Context, docID string, expectedEpoch, generation int64) error {
	if err := s.clearGenerationData(ctx, docID, generation); err != nil {
		return err
	}
	return s.db.WriteTx(ctx, storage.ControlWrite, nil, func(tx *sql.Tx) error {
		result, err := tx.Exec(`UPDATE documents SET desired_index_generation = NULL,
			index_state = ?, updated_at = ?
			WHERE id = ? AND lifecycle_state = ? AND mutation_epoch = ?
			AND desired_index_generation = ?`, IndexStateActive, now(), docID,
			LifecycleActive, expectedEpoch, generation)
		if err != nil {
			return err
		}
		if affected, err := result.RowsAffected(); err != nil {
			return err
		} else if affected != 1 {
			return ErrConflict
		}
		return nil
	})
}

// activateDocumentGeneration is the short transactional switch. The caller has
// already parsed and validated the staged generation; no model or file work is
// done while this write transaction is open.
func (s *store) activateDocumentGeneration(ctx context.Context, docID string, expectedEpoch, generation int64, d Document) error {
	err := s.db.WriteTx(ctx, storage.ControlWrite, nil, func(tx *sql.Tx) error {
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
		if hook := commitHookFromContext(ctx); hook != nil {
			if err := hook(tx); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	s.invalidateStatsCache()
	return nil
}

func (s *store) pruneRetiredGenerations(docID string, activeGeneration int64) ([]string, error) {
	return s.pruneRetiredGenerationsWithContext(context.Background(), docID, activeGeneration)
}

func (s *store) pruneRetiredGenerationsWithContext(ctx context.Context, docID string, activeGeneration int64) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	const batchSize = 256
	cutoff := now() - retiredGenerationRetentionMS
	// Live search requests hold their WAL snapshot. This bounded grace also
	// gives a caller time to issue a continuation for a prior generation;
	// after expiry the explicit request receives evidence-expired rather than
	// silently reading a newer version.
	for {
		var affected int64
		err := s.db.WriteTx(ctx, storage.MaintenanceWrite, nil, func(tx *sql.Tx) error {
			result, err := tx.Exec(`DELETE FROM chunks WHERE rowid IN (
			SELECT rowid FROM chunks
			WHERE doc_id = ? AND index_generation < ? AND created_at < ? LIMIT ?
		)`, docID, activeGeneration, cutoff, batchSize)
			if err != nil {
				return err
			}
			affected, err = result.RowsAffected()
			return err
		})
		if err != nil {
			return nil, err
		}
		if affected < batchSize {
			break
		}
	}
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(raw_file_path, '') FROM document_generations
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
	if _, err := s.db.ExecPriority(ctx, storage.MaintenanceWrite, `DELETE FROM document_generations
		WHERE doc_id = ? AND index_generation < ? AND created_at < ?`,
		docID, activeGeneration, cutoff); err != nil {
		return nil, err
	}
	return rawPaths, nil
}

func (s *store) markDocumentTreeDeleting(docID string) (Document, error) {
	return s.markDocumentTreeDeletingWithContext(context.Background(), docID)
}

func (s *store) markDocumentTreeDeletingWithContext(ctx context.Context, docID string) (Document, error) {
	var d Document
	err := s.db.WriteTx(ctx, storage.ControlWrite, nil, func(tx *sql.Tx) error {
		err := tx.QueryRow(`SELECT id, base_id, COALESCE(raw_file_path, ''), mutation_epoch FROM documents
		WHERE id = ? AND lifecycle_state = ?`, docID, LifecycleActive).Scan(
			&d.ID, &d.BaseID, &d.RawFilePath, &d.MutationEpoch)
		if err == sql.ErrNoRows {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`WITH RECURSIVE tree(id) AS (
			SELECT id FROM documents WHERE id = ?
			UNION ALL
			SELECT d.id FROM documents d JOIN tree t ON d.parent_directory_id = t.id
		) UPDATE documents SET lifecycle_state = ?, mutation_epoch = mutation_epoch + 1
		WHERE id IN (SELECT id FROM tree)`, docID, LifecycleDeleting); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE bases SET mutation_epoch = mutation_epoch + 1
			WHERE id = ? AND lifecycle_state = ?`, d.BaseID, LifecycleActive); err != nil {
			return err
		}
		if hook := commitHookFromContext(ctx); hook != nil {
			if err := hook(tx); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Document{}, err
	}
	s.invalidateStatsCache()
	return d, nil
}

func (s *store) markBaseDeleting(baseID string) (Base, error) {
	return s.markBaseDeletingWithContext(context.Background(), baseID)
}

func (s *store) markBaseDeletingWithContext(ctx context.Context, baseID string) (Base, error) {
	var b Base
	err := s.db.WriteTx(ctx, storage.ControlWrite, nil, func(tx *sql.Tx) error {
		if err := tx.QueryRow(`SELECT id, lifecycle_state, mutation_epoch FROM bases
		WHERE id = ? AND lifecycle_state = ?`, baseID, LifecycleActive).Scan(
			&b.ID, &b.LifecycleState, &b.MutationEpoch); err != nil {
			if err == sql.ErrNoRows {
				return ErrNotFound
			}
			return err
		}
		if _, err := tx.Exec(`UPDATE bases SET lifecycle_state = ?, mutation_epoch = mutation_epoch + 1 WHERE id = ?`,
			LifecycleDeleting, baseID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE documents SET lifecycle_state = ?, mutation_epoch = mutation_epoch + 1
		WHERE base_id = ? AND lifecycle_state = ?`, LifecycleDeleting, baseID, LifecycleActive); err != nil {
			return err
		}
		if hook := commitHookFromContext(ctx); hook != nil {
			if err := hook(tx); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
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

const cleanupDocumentPageSize = 128

func (s *store) listDocumentTreeCleanupRefs(rootID string) ([]cleanupDocumentRef, error) {
	return s.listDocumentTreeCleanupRefsContext(context.Background(), rootID, "", 0)
}

func (s *store) listDocumentTreeCleanupRefsContext(ctx context.Context, rootID, afterID string, limit int) ([]cleanupDocumentRef, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if limit <= 0 || limit > cleanupDocumentPageSize {
		limit = cleanupDocumentPageSize
	}
	rows, err := s.db.QueryContext(ctx, `WITH RECURSIVE tree(id) AS (
			SELECT id FROM documents WHERE id = ?
			UNION ALL
			SELECT d.id FROM documents d JOIN tree t ON d.parent_directory_id = t.id
		) SELECT d.id, COALESCE(d.raw_file_path, ''), d.active_index_generation
		FROM documents d WHERE d.id IN (SELECT id FROM tree) AND d.id > ?
		ORDER BY d.id LIMIT ?`, rootID, afterID, limit)
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
	return s.listBaseCleanupRefsContext(context.Background(), baseID, "", 0)
}

func (s *store) listBaseCleanupRefsContext(ctx context.Context, baseID, afterID string, limit int) ([]cleanupDocumentRef, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if limit <= 0 || limit > cleanupDocumentPageSize {
		limit = cleanupDocumentPageSize
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, COALESCE(raw_file_path, ''), active_index_generation
		FROM documents WHERE base_id = ? AND id > ? ORDER BY id LIMIT ?`, baseID, afterID, limit)
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
	return s.startDocumentIngestContext(context.Background(), d)
}

func (s *store) startDocumentIngestContext(ctx context.Context, d Document) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return s.db.WriteTx(ctx, storage.ControlWrite, nil, func(tx *sql.Tx) error {
		var baseCount int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM bases WHERE id = ? AND lifecycle_state = ?`,
			d.BaseID, LifecycleActive).Scan(&baseCount); err != nil {
			return err
		}
		if baseCount == 0 {
			return ErrConflict
		}
		if d.ParentDirectoryID != "" {
			var fenced int
			if err := tx.QueryRowContext(ctx, `WITH RECURSIVE tree(id, parent_id) AS (
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
		if err := upsertDocument(ctx, tx, d); err != nil {
			return err
		}
		var lifecycle string
		if err := tx.QueryRowContext(ctx, `SELECT lifecycle_state FROM documents WHERE id = ?`, d.ID).Scan(&lifecycle); err != nil {
			return err
		}
		if lifecycle != LifecycleActive {
			return ErrConflict
		}
		return nil
	})
}

func (s *store) deleteChunks(docID string) error {
	if _, err := s.db.Exec(`DELETE FROM chunks WHERE doc_id = ?`, docID); err != nil {
		return err
	}
	for _, table := range []string{"chunk_node_links", "document_relationships", "document_nodes", "document_parse_metadata", "derived_knowledge"} {
		if _, err := s.db.Exec(`DELETE FROM `+table+` WHERE doc_id = ?`, docID); err != nil {
			return err
		}
	}
	s.invalidateStatsCache()
	return nil
}

func (s *store) deleteDocumentGeneration(docID string, generation int64) error {
	return s.deleteDocumentGenerationWithContext(context.Background(), docID, generation)
}

func (s *store) deleteDocumentGenerationWithContext(ctx context.Context, docID string, generation int64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	const batchSize = 256
	for {
		var affected int64
		err := s.db.WriteTx(ctx, storage.ControlWrite, nil, func(tx *sql.Tx) error {
			result, err := tx.Exec(`DELETE FROM chunks WHERE rowid IN (
			SELECT rowid FROM chunks WHERE doc_id = ? AND index_generation = ? LIMIT ?
		)`, docID, generation, batchSize)
			if err != nil {
				return err
			}
			affected, err = result.RowsAffected()
			return err
		})
		if err != nil {
			return err
		}
		if affected < batchSize {
			for _, table := range []string{"chunk_node_links", "document_relationships", "document_nodes", "document_parse_metadata"} {
				if _, cleanupErr := s.db.ExecPriority(ctx, storage.ControlWrite,
					`DELETE FROM `+table+` WHERE doc_id = ? AND index_generation = ?`, docID, generation); cleanupErr != nil {
					return cleanupErr
				}
			}
			return nil
		}
		s.invalidateStatsCache()
	}
}

// deleteDocumentGenerations removes every retained chunk generation and its
// citation mappings. Returned raw paths are safe for the physical cleaner to
// delete because the authoritative references are gone once this commits.
func (s *store) deleteDocumentGenerations(docID string) ([]string, error) {
	return s.deleteDocumentGenerationsWithContext(context.Background(), docID)
}

func (s *store) deleteDocumentGenerationsWithContext(ctx context.Context, docID string) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	const batchSize = 256
	for {
		var affected int64
		err := s.db.WriteTx(ctx, storage.ControlWrite, nil, func(tx *sql.Tx) error {
			result, err := tx.Exec(`DELETE FROM chunks WHERE rowid IN (
			SELECT rowid FROM chunks WHERE doc_id = ? LIMIT ?
		)`, docID, batchSize)
			if err != nil {
				return err
			}
			affected, err = result.RowsAffected()
			return err
		})
		if err != nil {
			return nil, err
		}
		if affected < batchSize {
			break
		}
		s.invalidateStatsCache()
	}
	rows, err := s.db.QueryContext(ctx, `SELECT COALESCE(raw_file_path, '')
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
	if _, err := s.db.ExecPriority(ctx, storage.ControlWrite, `DELETE FROM document_generations WHERE doc_id = ?`, docID); err != nil {
		return nil, err
	}
	for _, table := range []string{"chunk_node_links", "document_relationships", "document_nodes", "document_parse_metadata", "derived_knowledge"} {
		if _, err := s.db.ExecPriority(ctx, storage.ControlWrite, `DELETE FROM `+table+` WHERE doc_id = ?`, docID); err != nil {
			return nil, err
		}
	}
	s.invalidateStatsCache()
	return rawPaths, nil
}

func (s *store) deleteChunksByBase(baseID string) error {
	return s.deleteChunksByBaseWithContext(context.Background(), baseID)
}

func (s *store) deleteChunksByBaseWithContext(ctx context.Context, baseID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	const batchSize = 256
	for {
		var affected int64
		err := s.db.WriteTx(ctx, storage.ControlWrite, nil, func(tx *sql.Tx) error {
			result, err := tx.Exec(`DELETE FROM chunks WHERE rowid IN (
				SELECT rowid FROM chunks WHERE base_id = ? LIMIT ?
			)`, baseID, batchSize)
			if err != nil {
				return err
			}
			affected, err = result.RowsAffected()
			return err
		})
		if err != nil {
			return err
		}
		if affected == 0 {
			for _, table := range []string{"chunk_node_links", "document_relationships", "document_nodes", "document_parse_metadata", "derived_knowledge"} {
				if _, cleanupErr := s.db.ExecPriority(ctx, storage.ControlWrite, `DELETE FROM `+table+` WHERE doc_id IN (SELECT id FROM documents WHERE base_id = ?)`, baseID); cleanupErr != nil {
					return cleanupErr
				}
			}
			return nil
		}
		s.invalidateStatsCache()
		if affected < batchSize {
			continue
		}
	}
}

func (s *store) listChunksByDoc(docID string, limit, offset int) ([]Chunk, error) {
	return s.listChunksByDocContext(context.Background(), docID, limit, offset)
}

func (s *store) listChunksByDocContext(ctx context.Context, docID string, limit, offset int) ([]Chunk, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	query := `SELECT c.id, c.doc_id, c.base_id, c.idx, c.text, COALESCE(c.heading, ''), COALESCE(c.context, ''),
	          COALESCE(c.embedding_text_hash, ''), c.created_at
	          FROM chunks c JOIN documents d ON d.id = c.doc_id
	          WHERE c.doc_id = ? AND d.lifecycle_state = ? AND c.index_generation = d.active_index_generation ORDER BY c.idx`
	args := []any{docID, LifecycleActive}
	if limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
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
	return s.statsForContext(context.Background(), baseID)
}

func (s *store) statsForContext(ctx context.Context, baseID string) (Stats, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Stats{}, err
	}
	s.statsMu.Lock()
	if entry, ok := s.statsCache[baseID]; ok && s.statsNow().Before(entry.expires) {
		s.statsMu.Unlock()
		return entry.stats, nil
	}
	s.statsMu.Unlock()
	stats, err := s.rawStatsForContext(ctx, baseID)
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
	return s.rawStatsForContext(context.Background(), baseID)
}

func (s *store) rawStatsForContext(ctx context.Context, baseID string) (Stats, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	stats := Stats{BaseID: baseID}
	baseFilter := ``
	args := []any{LifecycleActive}
	if baseID != "" {
		baseFilter = ` AND base_id = ?`
		args = append(args, baseID)
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*), COALESCE(SUM(char_count), 0), COALESCE(SUM(token_count), 0) FROM documents WHERE lifecycle_state = ?`+baseFilter, args...,
	).Scan(&stats.DocumentCount, &stats.CharCount, &stats.TokenCount); err != nil {
		return Stats{}, err
	}
	if err := s.db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(chunk_count), 0) FROM documents WHERE lifecycle_state = ?`+baseFilter, args...,
	).Scan(&stats.ChunkCount); err != nil {
		return Stats{}, err
	}
	return stats, nil
}

func searchTextOf(c Chunk) string {
	return strings.TrimSpace(c.Context + " " + c.Text)
}

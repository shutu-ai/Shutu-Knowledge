package knowledge

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

// store is the SQL access layer over the shared SQLite database.
type store struct {
	db *storage.DB
}

func newStore(db *storage.DB) *store { return &store{db: db} }

// ── bases ────────────────────────────────────────────────────────────────────

const baseColumns = `id, name, description, grp, config, created_at, updated_at`

func scanBase(row interface{ Scan(...any) error }) (Base, error) {
	var b Base
	var configText string
	var group sql.NullString
	if err := row.Scan(&b.ID, &b.Name, &b.Description, &group, &configText, &b.CreatedAt, &b.UpdatedAt); err != nil {
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
	configText, err := json.Marshal(b.Config)
	if err != nil {
		return fmt.Errorf("marshal base config: %w", err)
	}
	_, err = s.db.Exec(
		`INSERT INTO bases (id, name, description, grp, config, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET name = excluded.name, description = excluded.description,
		   grp = excluded.grp, config = excluded.config, updated_at = excluded.updated_at`,
		b.ID, b.Name, b.Description, b.Group, string(configText), b.CreatedAt, b.UpdatedAt,
	)
	return err
}

func (s *store) getBase(id string) (Base, error) {
	b, err := scanBase(s.db.QueryRow(`SELECT `+baseColumns+` FROM bases WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return Base{}, ErrNotFound
	}
	return b, err
}

func (s *store) listBases() ([]Base, error) {
	rows, err := s.db.Query(`SELECT ` + baseColumns + ` FROM bases ORDER BY created_at, id`)
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
	status, phase, progress, incomplete, error_code, error_message, created_at, updated_at, title_locked`

func scanDocument(row interface{ Scan(...any) error }) (Document, error) {
	var d Document
	var fileName, mimeType, url, parentDir, sourcePath, contentHash, rawFilePath, rawText sql.NullString
	var tokenCount, updatedAt sql.NullInt64
	var phase, errorCode, errorMessage sql.NullString
	var incomplete, titleLocked int
	err := row.Scan(&d.ID, &d.BaseID, &d.Title, &d.SourceType, &fileName, &mimeType, &url, &parentDir,
		&sourcePath, &contentHash, &rawFilePath, &rawText, &d.CharCount, &tokenCount, &d.ChunkCount,
		&d.Status, &phase, &d.Progress, &incomplete, &errorCode, &errorMessage, &d.CreatedAt, &updatedAt,
		&titleLocked)
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
	d.TokenCount = int(tokenCount.Int64)
	d.UpdatedAt = updatedAt.Int64
	d.Phase = phase.String
	d.Incomplete = incomplete != 0
	d.TitleLocked = titleLocked != 0
	d.ErrorCode = errorCode.String
	d.ErrorMessage = errorMessage.String
	return d, nil
}

func (s *store) putDocument(d Document) error {
	var tokenCount, updatedAt any
	if d.TokenCount > 0 {
		tokenCount = d.TokenCount
	}
	if d.UpdatedAt > 0 {
		updatedAt = d.UpdatedAt
	}
	var phase, errorCode, errorMessage any
	if d.Phase != "" {
		phase = d.Phase
	}
	if d.ErrorCode != "" {
		errorCode = d.ErrorCode
	}
	if d.ErrorMessage != "" {
		errorMessage = d.ErrorMessage
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
	_, err := s.db.Exec(
		`INSERT INTO documents (id, base_id, title, source_type, file_name, mime_type, url, parent_directory_id,
		   source_path, content_hash, raw_file_path, raw_text, char_count, token_count, chunk_count,
		   status, phase, progress, incomplete, error_code, error_message, created_at, updated_at, title_locked)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET title = excluded.title, file_name = excluded.file_name,
		   mime_type = excluded.mime_type, url = excluded.url, parent_directory_id = excluded.parent_directory_id,
		   source_path = excluded.source_path, content_hash = excluded.content_hash,
		   raw_file_path = excluded.raw_file_path, raw_text = excluded.raw_text,
		   char_count = excluded.char_count, token_count = excluded.token_count,
		   chunk_count = excluded.chunk_count, status = excluded.status, phase = excluded.phase,
		   progress = excluded.progress, incomplete = excluded.incomplete,
		   error_code = excluded.error_code, error_message = excluded.error_message,
		   updated_at = excluded.updated_at, title_locked = excluded.title_locked`,
		d.ID, d.BaseID, d.Title, d.SourceType, d.FileName, d.MimeType, d.URL, d.ParentDirectoryID,
		d.SourcePath, d.ContentHash, d.RawFilePath, rawText, d.CharCount, tokenCount, d.ChunkCount,
		d.Status, phase, d.Progress, incomplete, errorCode, errorMessage, d.CreatedAt, updatedAt, titleLocked,
	)
	return err
}

func (s *store) getDocument(id string) (Document, error) {
	d, err := scanDocument(s.db.QueryRow(`SELECT `+documentColumns+` FROM documents WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return Document{}, ErrNotFound
	}
	return d, err
}

func (s *store) listDocuments(baseID string) ([]Document, error) {
	rows, err := s.db.Query(`SELECT `+documentColumns+` FROM documents WHERE base_id = ? ORDER BY created_at, id`, baseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectDocuments(rows)
}

func (s *store) listAllDocuments() ([]Document, error) {
	rows, err := s.db.Query(`SELECT ` + documentColumns + ` FROM documents`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return collectDocuments(rows)
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

func (s *store) deleteDocument(id string) error {
	_, err := s.db.Exec(`DELETE FROM documents WHERE id = ?`, id)
	return err
}

func (s *store) findDocumentByTitle(baseID, title string) (Document, error) {
	d, err := scanDocument(s.db.QueryRow(
		`SELECT `+documentColumns+` FROM documents WHERE base_id = ? AND title = ? ORDER BY created_at LIMIT 1`, baseID, title))
	if err == sql.ErrNoRows {
		return Document{}, ErrNotFound
	}
	return d, err
}

// ── chunks ───────────────────────────────────────────────────────────────────

func (s *store) putChunksReplace(chunks []Chunk) error {
	if len(chunks) == 0 {
		return nil
	}
	docID := chunks[0].DocID
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// Carry over stored vectors whose embedding-text hash survives the
	// re-chunk: a reindex must not silently destroy reusable vectors.
	carried := map[string][2]any{}
	legacy, err := tx.Query(`SELECT embedding_text_hash, embedding, embedding_model FROM chunks WHERE doc_id = ? AND embedding IS NOT NULL`, docID)
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
	if _, err := tx.Exec(`DELETE FROM chunks WHERE doc_id = ?`, docID); err != nil {
		return err
	}
	for _, c := range chunks {
		var heading any
		if c.Heading != "" {
			heading = c.Heading
		}
		var hash any
		if c.EmbeddingHash != "" {
			hash = c.EmbeddingHash
		}
		var embedding, model any
		if carried, ok := carried[c.EmbeddingHash]; ok {
			embedding = carried[0]
			model = carried[1]
		}
		if c.EmbeddingVec != nil {
			embedding = encodeEmbedding(c.EmbeddingVec)
			model = c.EmbeddingModel
		}
		if _, err := tx.Exec(
			`INSERT INTO chunks (id, doc_id, base_id, idx, text, heading, context, embedding, embedding_model, embedding_text_hash, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			c.ID, c.DocID, c.BaseID, c.Index, c.Text, heading, c.Context, embedding, model, hash, c.CreatedAt,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *store) deleteChunks(docID string) error {
	_, err := s.db.Exec(`DELETE FROM chunks WHERE doc_id = ?`, docID)
	return err
}

func (s *store) deleteChunksByBase(baseID string) error {
	_, err := s.db.Exec(`DELETE FROM chunks WHERE base_id = ?`, baseID)
	return err
}

func (s *store) listChunksByDoc(docID string, limit, offset int) ([]Chunk, error) {
	query := `SELECT id, doc_id, base_id, idx, text, COALESCE(heading, ''), COALESCE(context, ''),
	          COALESCE(embedding_text_hash, ''), created_at
	          FROM chunks WHERE doc_id = ? ORDER BY idx`
	args := []any{docID}
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

// statsFor computes aggregate counts; empty baseID aggregates every base.
func (s *store) statsFor(baseID string) (Stats, error) {
	stats := Stats{BaseID: baseID}
	baseFilter := ``
	args := []any{}
	if baseID != "" {
		baseFilter = ` WHERE base_id = ?`
		args = append(args, baseID)
	}
	if err := s.db.QueryRow(
		`SELECT COUNT(*), COALESCE(SUM(char_count), 0), COALESCE(SUM(token_count), 0) FROM documents`+baseFilter, args...,
	).Scan(&stats.DocumentCount, &stats.CharCount, &stats.TokenCount); err != nil {
		return Stats{}, err
	}
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM chunks`+baseFilter, args...,
	).Scan(&stats.ChunkCount); err != nil {
		return Stats{}, err
	}
	return stats, nil
}

func searchTextOf(c Chunk) string {
	return strings.TrimSpace(c.Context + " " + c.Text)
}

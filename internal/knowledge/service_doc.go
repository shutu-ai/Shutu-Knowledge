package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/shutu-ai/shutu-knowledge/internal/chunk"
	"github.com/shutu-ai/shutu-knowledge/internal/parser"
)

// ConflictError reports same-name import conflicts (surfaced as 409 by web).
type ConflictError struct {
	Conflicts []string
}

func (e *ConflictError) Error() string {
	return "conflicting documents already exist: " + strings.Join(e.Conflicts, ", ")
}

// ChunkOptions resolves the effective chunking settings for a base.
type ChunkOptions struct {
	Smart             bool
	Separator         string
	Size              int
	Overlap           int
	Semantic          bool
	TokenLimit        int
	SemanticThreshold float64
}

func (s *Service) chunkOptions(cfg BaseConfig) ChunkOptions {
	opts := ChunkOptions{
		Smart:             s.global.Chunking.Smart,
		Separator:         s.global.Chunking.Separator,
		Size:              s.global.Chunking.Size,
		Overlap:           s.global.Chunking.Overlap,
		Semantic:          s.global.Chunking.Semantic,
		TokenLimit:        s.global.Chunking.TokenLimit,
		SemanticThreshold: s.global.Chunking.SemanticThreshold,
	}
	if cfg.SmartChunk != nil {
		opts.Smart = *cfg.SmartChunk
	}
	if cfg.ChunkSeparator != "" {
		opts.Separator = cfg.ChunkSeparator
	}
	if cfg.ChunkSize > 0 {
		opts.Size = cfg.ChunkSize
	}
	if cfg.ChunkOverlap > 0 {
		opts.Overlap = cfg.ChunkOverlap
	}
	if cfg.SemanticChunk != nil {
		opts.Semantic = *cfg.SemanticChunk
	}
	if cfg.ChunkTokenLimit > 0 {
		opts.TokenLimit = cfg.ChunkTokenLimit
	}
	if cfg.SemanticThreshold > 0 {
		opts.SemanticThreshold = cfg.SemanticThreshold
	}
	return opts
}

// ListDocuments returns summaries for one base.
func (s *Service) ListDocuments(baseID string) ([]DocumentSummary, error) {
	docs, err := s.store.listDocuments(baseID)
	if err != nil {
		return nil, err
	}
	out := make([]DocumentSummary, 0, len(docs))
	for _, doc := range docs {
		out = append(out, summarize(doc))
	}
	return out, nil
}

// ListChunks returns a page of a document's chunks.
func (s *Service) ListChunks(docID string, limit, offset int) ([]Chunk, error) {
	if _, err := s.store.getDocument(docID); err != nil {
		return nil, err
	}
	return s.store.listChunksByDoc(docID, limit, offset)
}

func summarize(d Document) DocumentSummary {
	return DocumentSummary{
		ID: d.ID, BaseID: d.BaseID, Title: d.Title, SourceType: d.SourceType,
		FileName: d.FileName, URL: d.URL, ParentDirID: d.ParentDirectoryID,
		CharCount: d.CharCount, TokenCount: d.TokenCount, ChunkCount: d.ChunkCount,
		Status: d.Status, Phase: d.Phase, Progress: d.Progress,
		ErrorCode: d.ErrorCode, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
	}
}

// GetDocument returns the full document; chunks included on request.
func (s *Service) GetDocument(id string, includeChunks bool) (Document, []Chunk, error) {
	doc, err := s.store.getDocument(id)
	if err != nil {
		return Document{}, nil, err
	}
	var chunks []Chunk
	if includeChunks {
		chunks, err = s.store.listChunksByDoc(id, 0, 0)
		if err != nil {
			return Document{}, nil, err
		}
	}
	return doc, chunks, nil
}

// RenameDocument updates the title.
func (s *Service) RenameDocument(id, title string) (Document, error) {
	doc, err := s.store.getDocument(id)
	if err != nil {
		return Document{}, err
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return Document{}, fmt.Errorf("document title is required")
	}
	doc.Title = title
	doc.UpdatedAt = now()
	if err := s.store.putDocument(doc); err != nil {
		return Document{}, err
	}
	return doc, nil
}

// DeleteDocument removes one document with its chunks and raw source.
func (s *Service) DeleteDocument(id string) error {
	doc, err := s.store.getDocument(id)
	if err != nil {
		return err
	}
	if err := s.store.deleteChunks(doc.ID); err != nil {
		return err
	}
	if doc.RawFilePath != "" {
		if err := s.raw.Delete(doc.RawFilePath); err != nil {
			return err
		}
	}
	return s.store.deleteDocument(doc.ID)
}

// DeleteDocuments removes several documents.
func (s *Service) DeleteDocuments(ids []string) (int, error) {
	deleted := 0
	for _, id := range ids {
		if err := s.DeleteDocument(id); err != nil {
			if err == ErrNotFound {
				continue
			}
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

// AddTextDocument imports a text note.
func (s *Service) AddTextDocument(ctx context.Context, baseID, title, content string) (Document, error) {
	base, err := s.store.getBase(baseID)
	if err != nil {
		return Document{}, err
	}
	content = chunk.Normalize(content)
	if content == "" {
		return Document{}, fmt.Errorf("document content is empty")
	}
	if title = strings.TrimSpace(title); title == "" {
		title = "untitled"
	}
	doc := s.newDocument(baseID, title, "text")
	doc.RawText = content
	if err := s.ingest(ctx, &doc, base.Config, nil); err != nil {
		return Document{}, err
	}
	return doc, nil
}

// AddFileDocument imports an uploaded file (conflict handled by caller).
func (s *Service) AddFileDocument(ctx context.Context, baseID, fileName string, data []byte, parentDirID string) (Document, error) {
	base, err := s.store.getBase(baseID)
	if err != nil {
		return Document{}, err
	}
	if fileName = strings.TrimSpace(fileName); fileName == "" {
		return Document{}, fmt.Errorf("file name is required")
	}
	doc := s.newDocument(baseID, fileName, "file")
	doc.FileName = fileName
	doc.ParentDirectoryID = parentDirID
	if err := s.ingest(ctx, &doc, base.Config, data); err != nil {
		return Document{}, err
	}
	return doc, nil
}

func hashText(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// AddFilesItem is one file of a batch import.
type AddFilesItem struct {
	FileName      string
	ContentBase64 string
}

// AcceptedFile reports one successfully imported (or skipped) file.
type AcceptedFile struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Skipped bool   `json:"skipped,omitempty"`
}

// AddFilesResult is the batch outcome.
type AddFilesResult struct {
	Status    string         `json:"status"` // conflicts | added
	Conflicts []string       `json:"conflicts,omitempty"`
	Accepted  []AcceptedFile `json:"accepted,omitempty"`
}

// MaxBatchFiles bounds one batch import.
const MaxBatchFiles = 20

// AddFiles imports a batch of base64 files with server-side conflict
// detection: conflict=detect refuses the whole batch when any name collides;
// rename/replace resolve collisions per file; duplicates by content hash are
// skipped.
func (s *Service) AddFiles(ctx context.Context, baseID string, items []AddFilesItem, conflict, parentDirID string) (AddFilesResult, error) {
	if _, err := s.store.getBase(baseID); err != nil {
		return AddFilesResult{}, err
	}
	if len(items) == 0 {
		return AddFilesResult{}, fmt.Errorf("no files provided")
	}
	if len(items) > MaxBatchFiles {
		return AddFilesResult{}, fmt.Errorf("batch too large (%d > %d files)", len(items), MaxBatchFiles)
	}
	if conflict == "" {
		conflict = "rename"
	}
	switch conflict {
	case "detect", "rename", "replace", "keep":
	default:
		return AddFilesResult{}, fmt.Errorf("invalid conflict strategy %q", conflict)
	}

	type decoded struct {
		name string
		data []byte
	}
	files := make([]decoded, 0, len(items))
	for _, item := range items {
		data, err := base64.StdEncoding.DecodeString(item.ContentBase64)
		if err != nil {
			return AddFilesResult{}, fmt.Errorf("file %s: invalid base64 content", item.FileName)
		}
		files = append(files, decoded{name: item.FileName, data: data})
	}

	if conflict == "detect" {
		names := make([]string, 0, len(files))
		for _, f := range files {
			names = append(names, f.name)
		}
		if conflicts := s.DetectConflicts(baseID, names); len(conflicts) > 0 {
			return AddFilesResult{Status: "conflicts", Conflicts: conflicts}, nil
		}
	}

	result := AddFilesResult{Status: "added", Accepted: make([]AcceptedFile, 0, len(files))}
	for _, f := range files {
		title := f.name
		if conflict == "replace" {
			if existing, err := s.store.findDocumentByTitle(baseID, title); err == nil {
				if err := s.DeleteDocument(existing.ID); err != nil {
					return result, err
				}
			}
		} else if conflict == "rename" {
			if _, err := s.store.findDocumentByTitle(baseID, title); err == nil {
				title = s.RenameAvailable(baseID, title)
			}
		}
		// Content-hash duplicate detection: identical bytes already stored
		// in this base are skipped regardless of strategy.
		sum := sha256.Sum256(f.data)
		hash := hex.EncodeToString(sum[:])
		if s.hasContentHash(baseID, hash) {
			result.Accepted = append(result.Accepted, AcceptedFile{Title: title, Skipped: true})
			continue
		}
		doc, err := s.AddFileDocument(ctx, baseID, title, f.data, parentDirID)
		if err != nil {
			return result, err
		}
		result.Accepted = append(result.Accepted, AcceptedFile{ID: doc.ID, Title: doc.Title})
	}
	return result, nil
}

func (s *Service) hasContentHash(baseID, hash string) bool {
	docs, err := s.store.listDocuments(baseID)
	if err != nil {
		return false
	}
	for _, doc := range docs {
		if doc.ContentHash == hash {
			return true
		}
	}
	return false
}

// DetectConflicts returns base file names that already exist.
func (s *Service) DetectConflicts(baseID string, fileNames []string) []string {
	var conflicts []string
	for _, name := range fileNames {
		if _, err := s.store.findDocumentByTitle(baseID, name); err == nil {
			conflicts = append(conflicts, name)
		}
	}
	return conflicts
}

// RenameAvailable suggests the first free "name_1" style title.
func (s *Service) RenameAvailable(baseID, fileName string) string {
	ext := filepath.Ext(fileName)
	stem := strings.TrimSuffix(fileName, ext)
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s_%d%s", stem, i, ext)
		if _, err := s.store.findDocumentByTitle(baseID, candidate); err == ErrNotFound {
			return candidate
		}
	}
}

// ReindexDocument re-reads the raw source, re-parses, and re-chunks.
func (s *Service) ReindexDocument(ctx context.Context, id string) (Document, error) {
	doc, err := s.store.getDocument(id)
	if err != nil {
		return Document{}, err
	}
	base, err := s.store.getBase(doc.BaseID)
	if err != nil {
		return Document{}, err
	}
	var data []byte
	if doc.SourceType == "file" {
		if doc.RawFilePath != "" {
			if data, err = s.raw.Read(doc.RawFilePath); err != nil {
				return Document{}, err
			}
		}
		if len(data) == 0 {
			return Document{}, fmt.Errorf("raw source is missing; reindex requires the stored source")
		}
	} else if doc.RawText == "" {
		return Document{}, fmt.Errorf("document has no recoverable source text")
	}
	if err := s.ingest(ctx, &doc, base.Config, data); err != nil {
		return Document{}, err
	}
	return doc, nil
}

// ReindexBase reindexes every document of a base as one background job.
func (s *Service) ReindexBase(ctx context.Context, baseID string) (string, error) {
	if s.jobMgr == nil {
		return "", fmt.Errorf("job manager is not running")
	}
	if _, err := s.store.getBase(baseID); err != nil {
		return "", err
	}
	docs, err := s.store.listDocuments(baseID)
	if err != nil {
		return "", err
	}
	total := len(docs)
	return s.jobMgr.Submit("reindex_base", baseID, total, func(jobCtx context.Context, report func(int)) error {
		for i, doc := range docs {
			select {
			case <-jobCtx.Done():
				return jobCtx.Err()
			default:
			}
			if doc.SourceType == "directory" {
				report(i + 1)
				continue
			}
			if _, err := s.ReindexDocument(jobCtx, doc.ID); err != nil {
				// A single failure must not hide the rest (per-file errors
				// stay on the document row).
				doc.Status = StatusFailed
				doc.ErrorCode = ErrParseFailed
				doc.ErrorMessage = err.Error()
				doc.UpdatedAt = now()
				_ = s.store.putDocument(doc)
			}
			report(i + 1)
		}
		return nil
	})
}

// RecoverInterrupted finalizes documents left mid-import by a crash:
// recoverable ones (raw source present) are marked for reindex, hopeless
// placeholders become failed.
func (s *Service) RecoverInterrupted(ctx context.Context) (resumed int, failed int, err error) {
	docs, err := s.store.listAllDocuments()
	if err != nil {
		return 0, 0, err
	}
	for _, doc := range docs {
		if doc.SourceType == "directory" {
			continue
		}
		switch doc.Status {
		case StatusPending, StatusProcessing:
			hasSource := doc.RawText != "" || doc.RawFilePath != ""
			if hasSource {
				doc.Status = StatusPending
				doc.Incomplete = true
				doc.UpdatedAt = now()
				if err := s.store.putDocument(doc); err != nil {
					return resumed, failed, err
				}
				resumed++
				continue
			}
			doc.Status = StatusFailed
			doc.ErrorCode = ErrInterrupted
			doc.ErrorMessage = "import was interrupted before the source was stored"
			doc.UpdatedAt = now()
			if err := s.store.putDocument(doc); err != nil {
				return resumed, failed, err
			}
			failed++
		}
	}
	return resumed, failed, nil
}

func (s *Service) newDocument(baseID, title, sourceType string) Document {
	id, _ := newID()
	return Document{
		ID: id, BaseID: baseID, Title: title, SourceType: sourceType,
		Status: StatusPending, CreatedAt: now(),
	}
}

// ingest runs parse -> chunk -> persist with status transitions. File bytes
// are persisted to the raw store first ("import means copy"); a failure in
// later steps leaves the raw copy for recovery.
func (s *Service) ingest(ctx context.Context, doc *Document, cfg BaseConfig, fileBytes []byte) error {
	doc.Status = StatusProcessing
	doc.Phase = PhaseParsing
	doc.Progress = 0
	doc.Incomplete = true
	doc.ErrorCode = ""
	doc.ErrorMessage = ""
	doc.UpdatedAt = now()
	if err := s.store.putDocument(*doc); err != nil {
		return err
	}

	if doc.SourceType == "file" && len(fileBytes) > 0 {
		rel, err := s.raw.Write(doc.BaseID, doc.ID, "."+parser.ExtensionOf(doc.FileName), fileBytes)
		if err != nil {
			return s.failDocument(doc, ErrParseFailed, err)
		}
		doc.RawFilePath = rel
		sum := sha256.Sum256(fileBytes)
		doc.ContentHash = hex.EncodeToString(sum[:])
	}

	text := doc.RawText
	if doc.SourceType == "file" {
		parsed, err := s.parsers.Parse(doc.FileName, fileBytes)
		if err != nil {
			return s.failDocument(doc, ErrParseFailed, err)
		}
		if parsed.Title != "" && doc.Title == doc.FileName {
			doc.Title = parsed.Title
		}
		text = parsed.Text
	}

	text = chunk.Normalize(text)
	if text == "" {
		return s.failDocument(doc, ErrParseFailed, fmt.Errorf("contains no extractable text"))
	}
	doc.RawText = text
	doc.CharCount = len([]rune(text))
	doc.TokenCount = chunk.EstimateTokens(text)

	opts := s.chunkOptions(cfg)
	var pieces []chunk.Piece
	if opts.Semantic {
		pieces = chunk.SemanticSegments(text, opts.Separator)
	} else {
		pieces = chunk.Chunk(text, opts.Size, opts.Overlap, chunk.Options{Smart: &opts.Smart, Separator: opts.Separator})
	}
	pieces = chunk.RefineByTokenLimit(pieces, opts.TokenLimit, nil)

	// Build chunks with retrieval context (title + heading path) and the
	// embedding-text hash the vector phase will reuse.
	rows := make([]Chunk, 0, len(pieces))
	for i, piece := range pieces {
		contextText := strings.TrimSpace(doc.Title)
		if piece.Heading != "" {
			contextText = strings.TrimSpace(contextText + " " + piece.Heading)
		}
		c := Chunk{
			ID:        fmt.Sprintf("%s:%d", doc.ID, i),
			DocID:     doc.ID,
			BaseID:    doc.BaseID,
			Index:     i,
			Text:      piece.Text,
			Heading:   piece.Heading,
			Context:   contextText,
			CreatedAt: now(),
		}
		c.EmbeddingText = strings.TrimSpace(c.Context + " " + c.Text)
		c.EmbeddingHash = hashText(c.EmbeddingText)
		rows = append(rows, c)
	}
	if len(rows) == 0 {
		return s.failDocument(doc, ErrParseFailed, fmt.Errorf("chunker produced no chunks"))
	}
	if err := s.store.putChunksReplace(rows); err != nil {
		return s.failDocument(doc, ErrParseFailed, err)
	}

	// Vector phase: with an active embedding provider, chunks are embedded
	// with library-wide hash reuse; a failure degrades the document to
	// lexical-only instead of dropping the imported text. Degradation is a
	// successful import with a recorded error code (reference behavior);
	// only a cancellation leaves the document resumable.
	if s.global.Embedding.Provider != "none" {
		doc.Status = StatusProcessing
		doc.Phase = PhaseEmbedding
		doc.Progress = 0
		doc.UpdatedAt = now()
		if err := s.store.putDocument(*doc); err != nil {
			return err
		}
		if code, cause := s.embedChunks(ctx, doc, rows); code != "" {
			if code == ErrInterrupted {
				// Keep the document resumable for the startup recovery pass.
				doc.Status = StatusProcessing
				doc.Phase = PhaseEmbedding
				doc.Incomplete = true
			} else {
				doc.Status = StatusReady
				doc.Phase = ""
				doc.ErrorCode = code
				doc.ErrorMessage = cause.Error()
				doc.Incomplete = false
			}
			doc.UpdatedAt = now()
			if err := s.store.putDocument(*doc); err != nil {
				return err
			}
			return nil
		}
	}

	doc.ChunkCount = len(rows)
	doc.Status = StatusReady
	doc.Phase = ""
	doc.Progress = 100
	doc.Incomplete = false
	doc.UpdatedAt = now()
	return s.store.putDocument(*doc)
}

// embedChunks embeds the stored chunks in batches, reusing stored vectors by
// embedding-text hash, and persists each landed batch (crash-safe). Returns
// a stable error code when the document degrades to lexical-only.
func (s *Service) embedChunks(ctx context.Context, doc *Document, rows []Chunk) (string, error) {
	modelKey := s.embedder.ModelKey()
	hashes := make([]string, 0, len(rows))
	textByHash := map[string]string{}
	for _, row := range rows {
		hashes = append(hashes, row.EmbeddingHash)
		textByHash[row.EmbeddingHash] = row.EmbeddingText
	}
	reuse := s.store.ListEmbeddingVectorsByHashes(hashes, modelKey)
	var pendingHashes []string
	for _, hash := range hashes {
		if _, ok := reuse[hash]; !ok {
			pendingHashes = append(pendingHashes, hash)
		}
	}
	dimension := 0
	batchSize := s.global.Embedding.Batch
	progressStep := 100 / (len(rows) + 1)
	landed := 0
	report := func() {
		landed++
		doc.Progress = minInt(99, landed*progressStep)
		doc.UpdatedAt = now()
		_ = s.store.putDocument(*doc)
	}
	for start := 0; start < len(pendingHashes); start += batchSize {
		end := minInt(start+batchSize, len(pendingHashes))
		batch := pendingHashes[start:end]
		texts := make([]string, 0, len(batch))
		for _, hash := range batch {
			texts = append(texts, textByHash[hash])
		}
		vectors, err := s.embedder.Embed(ctx, texts)
		if err != nil {
			if ctx.Err() != nil {
				return ErrInterrupted, ctx.Err()
			}
			return ErrEmbeddingProvider, err
		}
		if len(vectors) != len(batch) {
			return ErrEmbeddingProvider, fmt.Errorf("provider returned %d vectors for %d inputs", len(vectors), len(batch))
		}
		if dimension == 0 {
			dimension = len(vectors[0])
		}
		byHash := map[string][]float64{}
		for i, vector := range vectors {
			if len(vector) != dimension {
				return ErrDimensionMismatch, fmt.Errorf("vector width %d differs from stored %d", len(vector), dimension)
			}
			byHash[batch[i]] = vector
		}
		if err := s.store.PutChunkVectors(doc.ID, modelKey, byHash); err != nil {
			return ErrEmbeddingProvider, err
		}
		report()
	}
	doc.Progress = 100
	return "", nil
}

func (s *Service) failDocument(doc *Document, code string, cause error) error {
	doc.Status = StatusFailed
	doc.Phase = ""
	doc.ErrorCode = code
	doc.ErrorMessage = cause.Error()
	doc.Incomplete = false
	doc.UpdatedAt = now()
	if err := s.store.putDocument(*doc); err != nil {
		return err
	}
	return fmt.Errorf("%s: %w", code, cause)
}

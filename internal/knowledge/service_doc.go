package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/shutu-ai/shutu-knowledge/internal/caption"
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
		SourcePath: d.SourcePath,
		CharCount:  d.CharCount, TokenCount: d.TokenCount, ChunkCount: d.ChunkCount,
		Status: d.Status, Phase: d.Phase, Progress: d.Progress,
		ErrorCode: d.ErrorCode, ErrorMessage: d.ErrorMessage,
		CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
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

// RawFile is original source content prepared for HTTP download/preview.
type RawFile struct {
	Bytes    []byte
	FileName string
	MimeType string
}

// GetRawFile returns the stored original bytes for a file document.
func (s *Service) GetRawFile(id string) (*RawFile, error) {
	doc, err := s.store.getDocument(id)
	if err != nil {
		return nil, err
	}
	if doc.RawFilePath == "" {
		return nil, fmt.Errorf("%w: document has no raw source", ErrNotFound)
	}
	data, err := s.raw.Read(doc.RawFilePath)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: raw source is missing", ErrNotFound)
	}
	fileName := doc.FileName
	if fileName == "" {
		fileName = doc.Title
	}
	mimeType := doc.MimeType
	if mimeType == "" {
		mimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(fileName)))
	}
	return &RawFile{Bytes: data, FileName: fileName, MimeType: mimeType}, nil
}

// IndexingStatus is one currently active import/index job.
type IndexingStatus struct {
	DocID    string `json:"docId"`
	BaseID   string `json:"baseId"`
	Title    string `json:"title"`
	Phase    string `json:"phase,omitempty"`
	Progress int    `json:"progress"`
}

// IndexingStatus reports active imports across every base.
func (s *Service) IndexingStatus() ([]IndexingStatus, error) {
	docs, err := s.store.listAllDocuments()
	if err != nil {
		return nil, err
	}
	out := make([]IndexingStatus, 0)
	for _, doc := range docs {
		if doc.Status != StatusPending && doc.Status != StatusProcessing {
			continue
		}
		out = append(out, IndexingStatus{
			DocID: doc.ID, BaseID: doc.BaseID, Title: doc.Title,
			Phase: doc.Phase, Progress: doc.Progress,
		})
	}
	return out, nil
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
	doc.TitleLocked = true
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
	if doc.SourceType == "directory" {
		children, err := s.store.listDocuments(doc.BaseID)
		if err != nil {
			return err
		}
		for _, child := range children {
			if child.ParentDirectoryID != doc.ID {
				continue
			}
			if err := s.DeleteDocument(child.ID); err != nil && err != ErrNotFound {
				return err
			}
		}
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

// plannedFile is one decoded batch member with a title fixed before workers
// start. Fixing titles and duplicate decisions up front prevents concurrent
// imports from racing for the same name.
type plannedFile struct {
	title     string
	fileName  string
	data      []byte
	hash      string
	duplicate bool
}

// AddFiles imports a batch of base64 files with server-side conflict
// detection: conflict=detect refuses the whole batch when any name collides;
// rename/replace resolve collisions per file; duplicates by content hash are
// skipped. Ingestion runs with at most five workers.
func (s *Service) AddFiles(ctx context.Context, baseID string, items []AddFilesItem, conflict, parentDirID string) (AddFilesResult, error) {
	base, err := s.store.getBase(baseID)
	if err != nil {
		return AddFilesResult{}, err
	}
	if len(items) == 0 {
		return AddFilesResult{}, fmt.Errorf("no files provided")
	}
	if len(items) > MaxBatchFiles {
		return AddFilesResult{}, fmt.Errorf("batch too large (%d > %d files)", len(items), MaxBatchFiles)
	}
	if conflict == "" {
		conflict = ResolveBaseConfig(s.global, base.Config).ConflictStrategy
	}
	if conflict == "" {
		conflict = "rename"
	}
	switch conflict {
	case "detect", "rename", "replace", "keep":
	default:
		return AddFilesResult{}, fmt.Errorf("invalid conflict strategy %q", conflict)
	}

	s.batchMu.Lock()
	plans, err := s.planFileBatch(ctx, baseID, items, conflict)
	s.batchMu.Unlock()
	if err != nil {
		var conflictErr *ConflictError
		if errors.As(err, &conflictErr) {
			return AddFilesResult{Status: "conflicts", Conflicts: conflictErr.Conflicts}, nil
		}
		return AddFilesResult{}, err
	}

	result := AddFilesResult{Status: "added", Accepted: make([]AcceptedFile, len(plans))}
	completed := make([]bool, len(plans))
	workerCount := maxBatchWorkers
	if len(plans) < workerCount {
		workerCount = len(plans)
	}
	jobs := make(chan int)
	workerErr := make(chan error, workerCount)
	var workers sync.WaitGroup
	for worker := 0; worker < workerCount; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				plan := plans[index]
				if plan.duplicate {
					result.Accepted[index] = AcceptedFile{Title: plan.title, Skipped: true}
					completed[index] = true
					continue
				}
				doc, err := s.AddFileDocument(ctx, baseID, plan.title, plan.data, parentDirID)
				if err != nil {
					workerErr <- err
					return
				}
				result.Accepted[index] = AcceptedFile{ID: doc.ID, Title: doc.Title}
				completed[index] = true
			}
		}()
	}
	for index := range plans {
		select {
		case jobs <- index:
		case err := <-workerErr:
			close(jobs)
			workers.Wait()
			return result, err
		}
	}
	close(jobs)
	workers.Wait()
	select {
	case err := <-workerErr:
		return result, err
	default:
	}
	for index := range result.Accepted {
		if !completed[index] {
			result.Accepted[index] = AcceptedFile{Title: plans[index].title}
		}
	}
	return result, nil
}

const maxBatchWorkers = 5

func (s *Service) planFileBatch(ctx context.Context, baseID string, items []AddFilesItem, conflict string) ([]plannedFile, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	type decodedFile struct {
		name string
		data []byte
		hash string
	}
	files := make([]decodedFile, 0, len(items))
	seenNames := map[string]bool{}
	for _, item := range items {
		data, err := base64.StdEncoding.DecodeString(item.ContentBase64)
		if err != nil {
			return nil, fmt.Errorf("file %s: invalid base64 content", item.FileName)
		}
		name := strings.TrimSpace(item.FileName)
		if name == "" {
			return nil, fmt.Errorf("file name is required")
		}
		if seenNames[name] {
			return nil, fmt.Errorf("file %s appears more than once in the batch", name)
		}
		seenNames[name] = true
		sum := sha256.Sum256(data)
		files = append(files, decodedFile{name: name, data: data, hash: hex.EncodeToString(sum[:])})
	}

	if conflict == "detect" {
		names := make([]string, 0, len(files))
		for _, f := range files {
			names = append(names, f.name)
		}
		if conflicts := s.DetectConflicts(baseID, names); len(conflicts) > 0 {
			return nil, &ConflictError{Conflicts: conflicts}
		}
	}

	// Existing hashes must remain visible to planning. Hashes created by the
	// workers are serialized by SQLite; a race with another import can create
	// a legitimate duplicate and will be reconciled by later operations.
	existingHashes := map[string]bool{}
	docs, err := s.store.listDocuments(baseID)
	if err != nil {
		return nil, err
	}
	for _, doc := range docs {
		existingHashes[doc.ContentHash] = true
	}
	plans := make([]plannedFile, 0, len(files))
	usedTitles := map[string]bool{}
	for _, f := range files {
		title := f.name
		if conflict == "replace" {
			if existing, err := s.store.findDocumentByTitle(baseID, title); err == nil {
				if err := s.DeleteDocument(existing.ID); err != nil {
					return nil, err
				}
			}
		} else if conflict == "rename" {
			if _, err := s.store.findDocumentByTitle(baseID, title); err == nil {
				title = s.RenameAvailable(baseID, title)
			}
		}
		duplicate := existingHashes[f.hash]
		existingHashes[f.hash] = true
		if usedTitles[title] {
			title = s.RenameAvailable(baseID, title)
		}
		usedTitles[title] = true
		plans = append(plans, plannedFile{
			title: title, fileName: f.name, data: f.data, hash: f.hash, duplicate: duplicate,
		})
	}
	return plans, nil
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
	importStarted := Now()
	defer func() {
		s.recordMetric(func(m *MetricsSnapshot) {
			m.Imports++
			m.ImportDurationMS += durationMS(importStarted)
		})
	}()
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
		parseStarted := Now()
		parsedText, parsedTitle, err := s.parseFileContent(ctx, doc, cfg, fileBytes)
		s.recordMetric(func(m *MetricsSnapshot) { m.ParseDurationMS += durationMS(parseStarted) })
		if err != nil {
			return s.failDocument(doc, ErrParseFailed, err)
		}
		if parsedTitle != "" && !doc.TitleLocked {
			doc.Title = parsedTitle
		}
		text = s.appendImageCaptions(ctx, doc, fileBytes, parsedText)
	}

	text = chunk.Normalize(text)
	if text == "" {
		return s.failDocument(doc, ErrParseFailed, fmt.Errorf("contains no extractable text"))
	}
	doc.RawText = text
	doc.CharCount = len([]rune(text))
	doc.TokenCount = chunk.EstimateTokens(text)

	opts := s.chunkOptions(cfg)
	embeddingStarted := Now()
	pieces, inlineVectors := s.buildPieces(ctx, text, doc, opts)
	s.recordMetric(func(m *MetricsSnapshot) { m.EmbeddingDurationMS += durationMS(embeddingStarted) })

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
		if inlineVectors != nil {
			if vector, ok := inlineVectors[i]; ok {
				c.EmbeddingVec = vector
				c.EmbeddingModel = s.EmbeddingModelKey()
			}
		}
		rows = append(rows, c)
	}
	if len(rows) == 0 {
		return s.failDocument(doc, ErrParseFailed, fmt.Errorf("chunker produced no chunks"))
	}
	if err := s.store.putChunksReplace(rows); err != nil {
		return s.failDocument(doc, ErrParseFailed, err)
	}
	s.recordMetric(func(m *MetricsSnapshot) { m.ChunkCount += int64(len(rows)) })

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
		embeddingStarted = Now()
		code, cause := s.embedChunks(ctx, doc, rows)
		embeddingElapsed := durationMS(embeddingStarted)
		s.recordMetric(func(m *MetricsSnapshot) { m.EmbeddingDurationMS += embeddingElapsed })
		if code != "" {
			s.recordMetric(func(m *MetricsSnapshot) { m.ModelErrors++ })
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

// parseFileContent runs the format-specific extraction chain for file
// sources: optional MinerU remote processing (PDF, per-base), the local
// parser registry, and the OCR modes (native first, OCR fallback, forced
// OCR). OCR failure never destroys already-extracted text.
func (s *Service) parseFileContent(ctx context.Context, doc *Document, cfg BaseConfig, data []byte) (string, string, error) {
	cfg = ResolveBaseConfig(s.global, cfg)
	isPDF := strings.EqualFold(parser.ExtensionOf(doc.FileName), "pdf")
	isImage := isOCRImageExtension(parser.ExtensionOf(doc.FileName))

	// MinerU remote processing first when configured; any failure falls
	// back to the local chain.
	if isPDF && cfg.Processor == "mineru" && strings.TrimSpace(cfg.MineruAPIKey) != "" {
		markdown, err := parser.ExtractPDFWithMineru(ctx, doc.FileName, data, parser.MineruSettings{
			APIKey:  cfg.MineruAPIKey,
			APIHost: cfg.MineruAPIHost,
		})
		if err == nil && strings.TrimSpace(markdown) != "" {
			return markdown, "", nil
		}
	}

	parsed, parseErr := s.parsers.Parse(doc.FileName, data)
	nativeAvailable := parseErr == nil && strings.TrimSpace(parsed.Text) != ""
	// The parser may still provide fragmented native text while requesting a
	// healthier OCR pass (upstream's text-layer health behavior).
	nativeOK := nativeAvailable && !parsed.NeedsOCR

	mode := s.resolveOCRMode(cfg)
	ocrUsable := (isPDF || isImage) && s.ocr != nil && s.ocr.Available()
	contentUsable := isPDF && s.content != nil && s.content.Available()
	runOCR := func() (string, bool) {
		if !ocrUsable {
			return "", false
		}
		if isPDF {
			if renderedText, ok := s.runRenderedPageOCR(ctx, data); ok {
				return renderedText, true
			}
		} else if text, ok := s.runOCRImage(ctx, data); ok {
			return text, true
		}
		format := parser.ExtensionOf(doc.FileName)
		if format == "" {
			format = "pdf"
		}
		text, err := s.ocr.Run(ctx, format, data)
		trimmed := strings.TrimSpace(text)
		if err == nil && trimmed != "" {
			return postprocessOCRText(trimmed), true
		}
		if err != nil || trimmed == "" {
			// Scanned PDFs commonly carry page images even when the primary
			// helper cannot rasterize the PDF envelope itself. Try those
			// bounded embedded rasters before giving up; each image is passed
			// with an explicit PNG format hint.
			if rasterText, rasterErr := s.runEmbeddedRasterOCR(ctx, data); rasterErr == nil && strings.TrimSpace(rasterText) != "" {
				return rasterText, true
			}
			return "", false
		}
		return text, true
	}
	runContentConverter := func() (string, bool) {
		if !contentUsable {
			return "", false
		}
		markdown, err := s.content.Run(ctx, "pdf", data)
		if err != nil || strings.TrimSpace(markdown) == "" {
			return "", false
		}
		return markdown, true
	}

	if mode == "forced" {
		if text, ok := runOCR(); ok {
			return text, parsed.Title, nil
		}
		if text, ok := runContentConverter(); ok {
			return text, parsed.Title, nil
		}
		if nativeAvailable {
			return parsed.Text, parsed.Title, nil
		}
		// Forced OCR failed without any native text: surface the failure.
		return "", "", fmt.Errorf("forced OCR failed")
	}
	if nativeOK {
		return parsed.Text, parsed.Title, nil
	}
	if !nativeAvailable {
		// Upstream asks the content-signature reader first when the primary
		// PDF parser produced no text at all; OCR is its next fallback.
		if text, ok := runContentConverter(); ok {
			return text, parsed.Title, nil
		}
		if text, ok := runOCR(); ok {
			return text, parsed.Title, nil
		}
	} else {
		// A fragmented layer is still native evidence, so try OCR before
		// replacing it with a converter's reconstruction.
		if text, ok := runOCR(); ok {
			return text, parsed.Title, nil
		}
		if text, ok := runContentConverter(); ok {
			return text, parsed.Title, nil
		}
	}
	if nativeAvailable {
		return parsed.Text, parsed.Title, nil
	}
	if parseErr != nil {
		return "", "", parseErr
	}
	return parsed.Text, parsed.Title, fmt.Errorf("contains no extractable text")
}

func isOCRImageExtension(extension string) bool {
	switch strings.ToLower(strings.TrimSpace(extension)) {
	case "png", "jpg", "jpeg":
		return true
	default:
		return false
	}
}

func (s *Service) runEmbeddedRasterOCR(ctx context.Context, data []byte) (string, error) {
	images, err := parser.ExtractPDFImages(data, s.pdfImageOptions(ctx)...)
	if err != nil {
		return "", err
	}
	pageTexts := make(map[int][]string)
	for _, image := range images {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if len(image.PNG) == 0 {
			continue
		}
		text, ok := s.runOCRImage(ctx, image.PNG)
		if !ok {
			continue
		}
		if processed := postprocessOCRText(strings.TrimSpace(text)); processed != "" {
			pageTexts[image.Page] = append(pageTexts[image.Page], processed)
		}
	}
	if len(pageTexts) == 0 {
		return "", fmt.Errorf("embedded raster OCR produced no text")
	}
	pages := make([]int, 0, len(pageTexts))
	for page := range pageTexts {
		pages = append(pages, page)
	}
	sort.Ints(pages)
	pageParts := make([]string, 0, len(pages))
	for _, page := range pages {
		pageParts = append(pageParts, strings.Join(pageTexts[page], "\n"))
	}
	return strings.Join(pageParts, "\n\n"), nil
}

// runRenderedPageOCR asks the optional deployment-provided rasterizer for
// complete PDF pages first. Renderer, validation, and per-page OCR failures
// are isolated so ingestion can continue through the older PDF-envelope and
// embedded-raster fallbacks.
func (s *Service) runRenderedPageOCR(ctx context.Context, data []byte) (string, bool) {
	if !s.ocrRendererAvailable() {
		return "", false
	}
	output, err := s.ocrRenderer.DecodeLimit(ctx, "pdf", data, parser.MaxOCRRenderOutputBytes)
	if err != nil {
		return "", false
	}
	pages, err := parser.ParseRenderedPDFPages(output)
	if err != nil {
		return "", false
	}
	pageTexts := make(map[int]string, len(pages))
	orderedPages := make([]int, 0, len(pages))
	for _, page := range pages {
		if err := ctx.Err(); err != nil {
			return "", false
		}
		text, ok := s.runOCRImage(ctx, page.PNG)
		if !ok {
			continue
		}
		processed := postprocessOCRText(strings.TrimSpace(text))
		pageTexts[page.Page] = processed
		orderedPages = append(orderedPages, page.Page)
	}
	if len(orderedPages) == 0 {
		return "", false
	}
	pageParts := make([]string, 0, len(orderedPages))
	for _, page := range orderedPages {
		pageParts = append(pageParts, pageTexts[page])
	}
	return strings.Join(pageParts, "\n\n"), true
}

// appendImageCaptions enriches searchable text with best-effort vision model
// descriptions. Caption failures intentionally leave parsed text untouched.
func (s *Service) appendImageCaptions(ctx context.Context, doc *Document, data []byte, text string) string {
	if !strings.EqualFold(parser.ExtensionOf(doc.FileName), "pdf") {
		return text
	}
	captionCfg := caption.Config{
		Provider:         s.global.Captioning.Provider,
		Model:            s.global.Captioning.Model,
		BaseURL:          s.global.Captioning.BaseURL,
		APIKey:           s.global.Captioning.APIKey,
		EmbeddingBaseURL: s.global.Embedding.BaseURL,
	}
	if captionCfg.Provider == "off" || strings.TrimSpace(captionCfg.Model) == "" {
		return text
	}
	captionOptions := s.captionOptionsOrEmpty()
	if captionOptions.ExtractImages == nil {
		captionOptions.ExtractImages = func(source []byte) ([]parser.PDFImage, error) {
			return parser.ExtractPDFImages(source, s.pdfImageOptions(ctx)...)
		}
	}
	result := caption.PDFImages(ctx, data, captionCfg, captionOptions)
	if result.Failures > 0 {
		s.recordMetric(func(m *MetricsSnapshot) { m.ModelErrors++ })
	}
	if result.Text == "" {
		return text
	}
	return text + result.Text
}

func (s *Service) captionOptionsOrEmpty() caption.Options {
	if s.captionOptions != nil {
		return *s.captionOptions
	}
	return caption.Options{}
}

// resolveOCRMode: per-base override, else the global mode (default auto).
func (s *Service) resolveOCRMode(cfg BaseConfig) string {
	mode := strings.TrimSpace(cfg.OCRMode)
	if mode == "" {
		mode = strings.TrimSpace(s.global.OCR.Mode)
	}
	switch mode {
	case "forced", "off":
		return mode
	default:
		return "auto"
	}
}

// buildPieces chunks the text; the semantic path merges adjacent embedded
// segments and returns per-piece mean vectors. Any provider failure falls
// back to the structural chunker (semantic chunking must never block import).
func (s *Service) buildPieces(ctx context.Context, text string, doc *Document, opts ChunkOptions) ([]chunk.Piece, map[int][]float64) {
	if !opts.Semantic || s.global.Embedding.Provider == "none" {
		return s.structuralPieces(text, opts), nil
	}
	segments := chunk.SemanticSegments(text, opts.Separator)
	if len(segments) == 0 {
		return nil, nil
	}
	texts := make([]string, 0, len(segments))
	for _, segment := range segments {
		contextText := strings.TrimSpace(doc.Title)
		if segment.Heading != "" {
			contextText = strings.TrimSpace(contextText + " " + segment.Heading)
		}
		texts = append(texts, strings.TrimSpace(contextText+" "+segment.Text))
	}
	vectors, err := s.embedder.Embed(ctx, texts)
	if err != nil || len(vectors) != len(segments) {
		return s.structuralPieces(text, opts), nil
	}
	merged := chunk.MergeSemanticSegments(segments, vectors, opts.Size, opts.SemanticThreshold)
	pieces := make([]chunk.Piece, 0, len(merged))
	inline := map[int][]float64{}
	for i, segment := range merged {
		pieces = append(pieces, segment.Piece)
		if segment.Vector != nil {
			inline[i] = segment.Vector
		}
	}
	return pieces, inline
}

func (s *Service) structuralPieces(text string, opts ChunkOptions) []chunk.Piece {
	pieces := chunk.Chunk(text, opts.Size, opts.Overlap, chunk.Options{Smart: &opts.Smart, Separator: opts.Separator})
	return chunk.RefineByTokenLimit(pieces, opts.TokenLimit, nil)
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
	reusedForDocument := map[string][]float64{}
	for _, hash := range hashes {
		if vector, ok := reuse[hash]; ok {
			// Hash reuse is library-wide, but the vector still has to be
			// materialized on this document before vector search can see it.
			reusedForDocument[hash] = vector
		} else {
			pendingHashes = append(pendingHashes, hash)
		}
	}
	if len(reusedForDocument) > 0 {
		if err := s.store.PutChunkVectors(doc.ID, modelKey, reusedForDocument); err != nil {
			return ErrEmbeddingProvider, err
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

package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/shutu-ai/shutu-knowledge/internal/caption"
	"github.com/shutu-ai/shutu-knowledge/internal/chunk"
	"github.com/shutu-ai/shutu-knowledge/internal/documentir"
	"github.com/shutu-ai/shutu-knowledge/internal/embedding"
	"github.com/shutu-ai/shutu-knowledge/internal/parser"
	"github.com/shutu-ai/shutu-knowledge/internal/semantic"
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
	return s.ListDocumentsContext(context.Background(), baseID)
}

// ListDocumentsContext is the cancellable API path used by Web requests.
// Large databases must not leave an abandoned reader occupying the pool after
// the browser navigates away or its request times out.
func (s *Service) ListDocumentsContext(ctx context.Context, baseID string) ([]DocumentSummary, error) {
	docs, err := s.store.listDocumentMetadataContext(ctx, baseID)
	if err != nil {
		return nil, err
	}
	modelKey := ""
	if providers, providerErr := s.providersForBaseContext(ctx, baseID); providerErr != nil {
		return nil, providerErr
	} else if providers.embeddingActive && providers.embedder != nil {
		modelKey = providers.embedder.ModelKey()
	}
	out := make([]DocumentSummary, 0, len(docs))
	for _, doc := range docs {
		summary := summarize(doc)
		summary.EmbeddingReady = modelKey != "" && doc.EmbeddingReady && doc.EmbeddingModel == modelKey
		out = append(out, summary)
	}
	return out, nil
}

// ListDocumentsPageContext provides a bounded flat-list compatibility view.
func (s *Service) ListDocumentsPageContext(ctx context.Context, baseID string, limit, offset int) (DocumentListPage, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	docs, total, err := s.store.listDocumentMetadataPageContext(ctx, baseID, limit, offset)
	if err != nil {
		return DocumentListPage{}, err
	}
	modelKey := ""
	if providers, providerErr := s.providersForBaseContext(ctx, baseID); providerErr != nil {
		return DocumentListPage{}, providerErr
	} else if providers.embeddingActive && providers.embedder != nil {
		modelKey = providers.embedder.ModelKey()
	}
	out := make([]DocumentSummary, 0, len(docs))
	for _, doc := range docs {
		summary := summarize(doc)
		summary.EmbeddingReady = modelKey != "" && doc.EmbeddingReady && doc.EmbeddingModel == modelKey
		out = append(out, summary)
	}
	return DocumentListPage{
		Documents: out, Total: total, Limit: limit, Offset: offset,
		HasMore: offset+len(out) < total,
	}, nil
}

// CountActiveDocuments returns the scalar count used by operation receipts.
// Callers that need the rows should use a bounded page API instead.
func (s *Service) CountActiveDocuments(ctx context.Context, baseID string) (int, error) {
	return s.store.countActiveDocumentsContext(ctx, baseID)
}

// CountDocumentTree returns a scalar subtree size for operation progress. It
// uses a recursive SQL count instead of loading every sibling in the base.
func (s *Service) CountDocumentTree(ctx context.Context, rootID string) (int, error) {
	return s.store.countDocumentTreeContext(ctx, rootID)
}

// ReindexIdempotencyKey derives a stable equivalent-operation key from the
// current target/source/config snapshot. An empty documentIDs slice means the
// whole base; a non-empty slice is treated as an order-independent batch.
// The key contains only a digest, so configuration credentials never leave
// this process even though they participate in the snapshot.
func (s *Service) ReindexIdempotencyKey(ctx context.Context, baseID string, documentIDs []string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	base, err := s.store.getBaseContext(ctx, baseID)
	if err != nil {
		return "", err
	}
	resolved := ResolveBaseConfig(s.global, base.Config)
	configBytes, err := json.Marshal(resolved)
	if err != nil {
		return "", fmt.Errorf("marshal reindex config snapshot: %w", err)
	}
	hash := sha256.New()
	writePart := func(value string) {
		_, _ = fmt.Fprintf(hash, "%d:", len(value))
		_, _ = hash.Write([]byte(value))
	}
	writePart("reindex-equivalent-v1")
	writePart(baseID)
	writePart(string(configBytes))
	writePart(resolved.EmbeddingProvider)
	writePart(resolved.EmbeddingModel)
	if len(documentIDs) == 0 {
		writePart("base")
		// The base epoch is advanced atomically with source identity changes and
		// subtree deletion. It is the bounded aggregate identity for a whole-base
		// reindex; do not scan every document while an HTTP receipt is being built.
		writePart(fmt.Sprintf("%d", base.MutationEpoch))
	} else {
		writePart("documents")
		documents := make([]Document, 0, len(documentIDs))
		for _, documentID := range documentIDs {
			document, err := s.store.getDocumentContext(ctx, documentID)
			if err != nil {
				return "", err
			}
			if document.BaseID != baseID {
				return "", ErrConflict
			}
			documents = append(documents, document)
		}
		sort.Slice(documents, func(i, j int) bool { return documents[i].ID < documents[j].ID })
		for _, document := range documents {
			writeReindexDocumentIdentity(writePart, summarize(document))
		}
	}
	return "reindex.auto." + hex.EncodeToString(hash.Sum(nil)), nil
}

func writeReindexDocumentIdentity(writePart func(string), document DocumentSummary) {
	writePart(document.ID)
	writePart(document.SourceType)
	writePart(fmt.Sprintf("%d", document.SourceVersion))
	writePart(fmt.Sprintf("%d", document.MutationEpoch))
	writePart(fmt.Sprintf("%d", document.ActiveIndexGen))
	writePart(document.ContentHash)
	writePart(document.SourcePath)
	writePart(document.RawFilePath)
}

// ForEachReindexDocumentBatch visits a fixed target window in bounded keyset
// pages. The upper cursor is captured before the first page, so documents
// created while the operation runs are left for a later explicit reindex.
func (s *Service) ForEachReindexDocumentBatch(ctx context.Context, baseID string, batchSize int, visit func(start, total int, documents []DocumentSummary) error) error {
	if batchSize <= 0 {
		batchSize = 50
	}
	if batchSize > 200 {
		batchSize = 200
	}
	if _, err := s.store.getBaseContext(ctx, baseID); err != nil {
		return err
	}
	total, cutoffCreatedAt, cutoffID, err := s.store.reindexDocumentWindow(ctx, baseID)
	if err != nil {
		return err
	}
	if total == 0 {
		return visit(0, 0, nil)
	}
	var afterCreatedAt int64
	var afterID string
	for start := 0; start < total; {
		docs, err := s.store.listReindexDocumentBatchContext(ctx, baseID, cutoffCreatedAt, cutoffID, afterCreatedAt, afterID, batchSize)
		if err != nil {
			return err
		}
		if len(docs) == 0 {
			break
		}
		page := make([]DocumentSummary, 0, len(docs))
		for _, doc := range docs {
			page = append(page, summarize(doc))
		}
		if err := visit(start, total, page); err != nil {
			return err
		}
		last := docs[len(docs)-1]
		afterCreatedAt, afterID = last.CreatedAt, last.ID
		start += len(docs)
	}
	return nil
}

// ListDocumentChildrenContext returns one bounded directory page. Directory
// navigation is lazy; a large base never requires loading its full metadata
// tree just to render the current folder.
func (s *Service) ListDocumentChildrenContext(ctx context.Context, baseID, parentID string, limit, offset int) (DocumentChildrenPage, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	if parentID != "" {
		parent, err := s.store.getDocumentMetadataContext(ctx, parentID)
		if err != nil {
			return DocumentChildrenPage{}, err
		}
		if parent.BaseID != baseID || parent.SourceType != "directory" {
			return DocumentChildrenPage{}, ErrNotFound
		}
	}
	docs, total, err := s.store.listChildDocumentMetadataContext(ctx, baseID, parentID, limit, offset)
	if err != nil {
		return DocumentChildrenPage{}, err
	}
	modelKey := ""
	if providers, providerErr := s.providersForBaseContext(ctx, baseID); providerErr != nil {
		return DocumentChildrenPage{}, providerErr
	} else if providers.embeddingActive && providers.embedder != nil {
		modelKey = providers.embedder.ModelKey()
	}
	documents := make([]DocumentSummary, 0, len(docs))
	for _, document := range docs {
		summary := summarize(document)
		summary.EmbeddingReady = modelKey != "" && document.EmbeddingReady && document.EmbeddingModel == modelKey
		documents = append(documents, summary)
	}
	var breadcrumbs []DocumentSummary
	if parentID == "" {
		breadcrumbs = []DocumentSummary{}
	} else {
		path, pathErr := s.store.documentPathMetadataContext(ctx, baseID, parentID)
		if pathErr != nil {
			return DocumentChildrenPage{}, pathErr
		}
		breadcrumbs = make([]DocumentSummary, 0, len(path))
		for _, document := range path {
			breadcrumbs = append(breadcrumbs, summarize(document))
		}
	}
	return DocumentChildrenPage{
		ParentID: parentID, Documents: documents, Total: total,
		Limit: limit, Offset: offset, HasMore: offset+len(documents) < total,
		Breadcrumbs: breadcrumbs,
	}, nil
}

// ListChunks returns a page of a document's chunks.
func (s *Service) ListChunks(docID string, limit, offset int) ([]Chunk, error) {
	return s.ListChunksContext(context.Background(), docID, limit, offset)
}

// ListChunksContext keeps document validation and bounded chunk reads inside
// the caller's cancellation boundary.
func (s *Service) ListChunksContext(ctx context.Context, docID string, limit, offset int) ([]Chunk, error) {
	if _, err := s.store.getDocumentContext(ctx, docID); err != nil {
		return nil, err
	}
	return s.store.listChunksByDocContext(ctx, docID, limit, offset)
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
		SourceVersion: d.SourceVersion, MutationEpoch: d.MutationEpoch,
		ActiveIndexGen: d.ActiveIndexGen,
		ContentHash:    d.ContentHash, RawFilePath: d.RawFilePath,
	}
}

// GetDocument returns the full document; chunks included on request.
func (s *Service) GetDocument(id string, includeChunks bool) (Document, []Chunk, error) {
	return s.GetDocumentWithContext(context.Background(), id, includeChunks)
}

// GetDocumentWithContext keeps operation-worker reads inside the caller's
// cancellation boundary. The compatibility GetDocument wrapper remains for
// non-operation callers.
func (s *Service) GetDocumentWithContext(ctx context.Context, id string, includeChunks bool) (Document, []Chunk, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	doc, err := s.store.getDocumentContext(ctx, id)
	if err != nil {
		return Document{}, nil, err
	}
	var chunks []Chunk
	if includeChunks {
		chunks, err = s.store.listChunksByDocContext(ctx, id, 0, 0)
		if err != nil {
			return Document{}, nil, err
		}
	}
	return doc, chunks, nil
}

// GetDocumentIR returns the active generation's structured document view.
// Legacy documents imported before 0.3 return a valid empty IR rather than
// exposing an implementation-specific storage error.
func (s *Service) GetDocumentIR(ctx context.Context, id string) (documentir.Document, error) {
	return s.store.listDocumentIR(ctx, id)
}

func (s *Service) GetDerivedKnowledge(ctx context.Context, id string) ([]DerivedKnowledge, error) {
	return s.store.listDerivedKnowledge(ctx, id)
}

// GetDocumentIncludingDeleting is restricted to operation recovery paths. A
// tombstoned document is hidden from ordinary reads but must remain visible to
// a retry that is finishing physical cleanup.
func (s *Service) GetDocumentIncludingDeleting(id string) (Document, error) {
	return s.GetDocumentIncludingDeletingWithContext(context.Background(), id)
}

// GetDocumentIncludingDeletingWithContext is used by delete recovery while
// retaining the caller's cancellation boundary.
func (s *Service) GetDocumentIncludingDeletingWithContext(ctx context.Context, id string) (Document, error) {
	return s.store.getDocumentIncludingDeletingContext(ctx, id)
}

// RawFile is original source content prepared for HTTP download/preview.
type RawFile struct {
	Bytes           []byte
	FileName        string
	MimeType        string
	IndexGeneration int64
	SourceVersion   int64
}

// RawCitationOptions pins a raw download to the identity emitted by a search
// hit. Nil fields preserve the current-document compatibility path.
type RawCitationOptions struct {
	SourceVersion   *int64
	IndexGeneration *int64
}

// GetRawFile returns the stored original bytes for a file document.
func (s *Service) GetRawFile(id string) (*RawFile, error) {
	return s.GetRawFileForCitation(id, RawCitationOptions{})
}

// GetRawFileForCitation serves raw bytes only when the caller's generation and
// source version identify the currently published document. The raw path can
// be replaced by a later re-index, so a historical mapping is never enough:
// returning current bytes under an old citation would mix evidence.
func (s *Service) GetRawFileForCitation(id string, opts RawCitationOptions) (*RawFile, error) {
	return s.GetRawFileForCitationContext(context.Background(), id, opts)
}

// GetRawFileForCitationContext keeps generation validation and raw-byte reads
// inside the caller's cancellation boundary. The compatibility wrapper above
// remains for non-HTTP callers.
func (s *Service) GetRawFileForCitationContext(ctx context.Context, id string, opts RawCitationOptions) (*RawFile, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if (opts.IndexGeneration == nil) != (opts.SourceVersion == nil) {
		return nil, fmt.Errorf("indexGeneration and sourceVersion must be supplied together")
	}
	var identity generationIdentity
	var current Document
	if opts.IndexGeneration != nil {
		var err error
		identity, err = s.store.generationByID(ctx, s.store.db.ReadDB(),
			id, *opts.IndexGeneration)
		if err != nil {
			return nil, err
		}
		if identity.SourceVersion != *opts.SourceVersion {
			return nil, ErrHistoricalEvidenceExpired
		}
		// Historical bytes never bypass a committed tombstone. Once the
		// current document is invisible, fail with the citation contract
		// instead of exposing ordinary not-found semantics.
		current, err = s.store.getDocumentContext(ctx, id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				return nil, ErrHistoricalEvidenceExpired
			}
			return nil, err
		}
	} else {
		var err error
		current, err = s.store.getDocumentContext(ctx, id)
		if err != nil {
			return nil, err
		}
		identity = generationIdentity{
			Generation: current.ActiveIndexGen, SourceVersion: current.SourceVersion,
			RawFilePath: current.RawFilePath, ContentHash: current.ContentHash,
		}
	}
	if identity.RawFilePath == "" {
		return nil, fmt.Errorf("%w: document has no raw source", ErrNotFound)
	}
	diskReadStarted := Now()
	data, err := s.raw.ReadContext(ctx, identity.RawFilePath)
	s.recordMetric(func(m *MetricsSnapshot) { m.DiskReadMS += durationMS(diskReadStarted) })
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("%w: raw source is missing", ErrNotFound)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != identity.ContentHash {
		// A legacy shared path can be replaced before a new generation is
		// activated; immutable paths detect corruption or accidental writes.
		return nil, ErrHistoricalEvidenceExpired
	}
	fileName := current.FileName
	if fileName == "" {
		fileName = current.Title
	}
	mimeType := current.MimeType
	if mimeType == "" {
		mimeType = mime.TypeByExtension(strings.ToLower(filepath.Ext(fileName)))
	}
	return &RawFile{
		Bytes: data, FileName: fileName, MimeType: mimeType,
		IndexGeneration: identity.Generation, SourceVersion: identity.SourceVersion,
	}, nil
}

// rawPathIsVersioned distinguishes the pre-generation shared layout from the
// immutable per-source-version layout.
func rawPathIsVersioned(rawPath string) bool {
	return strings.Contains(filepath.ToSlash(rawPath), "/.generations/")
}

// preserveLegacyRawGeneration copies an old shared raw file into the immutable
// layout before a rebuild can replace it. The content hash must already match
// the published document; otherwise the historical citation is already unsafe.
func (s *Service) preserveLegacyRawGeneration(ctx context.Context, doc *Document, data []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if doc.SourceType != "file" || doc.RawFilePath == "" || rawPathIsVersioned(doc.RawFilePath) ||
		doc.SourceVersion <= 0 || doc.ContentHash == "" {
		return nil
	}
	if len(data) == 0 {
		var err error
		data, err = s.raw.ReadContext(ctx, doc.RawFilePath)
		if err != nil {
			return err
		}
	}
	if len(data) == 0 {
		return nil
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != doc.ContentHash {
		return nil
	}
	rel, err := s.raw.WriteVersion(doc.BaseID, doc.ID, doc.SourceVersion,
		"."+parser.ExtensionOf(doc.FileName), data)
	if err != nil {
		return err
	}
	if err := s.store.setGenerationRawPathContext(ctx, doc.ID, doc.ActiveIndexGen, rel); err != nil {
		return err
	}
	doc.RawFilePath = rel
	return nil
}

// IndexingStatus is one currently active import/index job.
type IndexingStatus struct {
	DocID    string `json:"docId"`
	BaseID   string `json:"baseId"`
	Title    string `json:"title"`
	Phase    string `json:"phase,omitempty"`
	Progress int    `json:"progress"`
}

const maxIndexingStatusItems = 200

// IndexingStatus reports a bounded snapshot of active imports across every
// base. The compatibility wrapper retains the original non-context API.
func (s *Service) IndexingStatus() ([]IndexingStatus, error) {
	return s.IndexingStatusContext(context.Background())
}

// IndexingStatusContext is the cancellable, bounded status path used by Web
// requests. The limit is enforced by the SQL query before rows are materialized.
func (s *Service) IndexingStatusContext(ctx context.Context) ([]IndexingStatus, error) {
	docs, err := s.store.listActiveDocumentMetadataContext(ctx, maxIndexingStatusItems)
	if err != nil {
		return nil, err
	}
	out := make([]IndexingStatus, 0)
	for _, doc := range docs {
		out = append(out, IndexingStatus{
			DocID: doc.ID, BaseID: doc.BaseID, Title: doc.Title,
			Phase: doc.Phase, Progress: doc.Progress,
		})
	}
	return out, nil
}

// RenameDocument updates the title.
func (s *Service) RenameDocument(id, title string) (Document, error) {
	return s.RenameDocumentWithContext(context.Background(), id, title)
}

// RenameDocumentWithContext binds the document read and update to the caller.
func (s *Service) RenameDocumentWithContext(ctx context.Context, id, title string) (Document, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	doc, err := s.store.getDocumentContext(ctx, id)
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
	if err := s.store.putDocumentContext(ctx, doc); err != nil {
		return Document{}, err
	}
	return doc, nil
}

// deleteDocumentTree first commits the logical delete fence for the target and
// every descendant, then performs bounded physical cleanup. A cleanup failure
// leaves lifecycle_state=deleting rather than exposing or resurrecting rows.
// Once that fence commits, cancellation cannot restore the tree; an
// interrupted cleanup remains deleting and the next durable attempt resumes
// from its bounded page/chunk cursor.
func (s *Service) deleteDocumentTree(ctx context.Context, rootID string, onDeleted func()) (int, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	root, err := s.store.getDocumentContext(ctx, rootID)
	switch {
	case err == nil:
		if _, err := s.store.markDocumentTreeDeletingWithContext(ctx, rootID); err != nil {
			return 0, err
		}
	case errors.Is(err, ErrNotFound):
		root, err = s.store.getDocumentIncludingDeletingContext(ctx, rootID)
		if err != nil {
			return 0, err
		}
		if root.LifecycleState != LifecycleDeleting {
			return 0, ErrNotFound
		}
	default:
		return 0, err
	}
	removed := 0
	afterID := ""
	for {
		refs, err := s.store.listDocumentTreeCleanupRefsContext(ctx, root.ID, afterID, cleanupDocumentPageSize)
		if err != nil {
			return removed, err
		}
		if len(refs) == 0 {
			break
		}
		for _, ref := range refs {
			if err := ctx.Err(); err != nil {
				return removed, err
			}
			if err := s.markSemanticDocumentDeleted(ctx, root.BaseID, ref.ID); err != nil {
				return removed, err
			}
			rawPaths, err := s.store.deleteDocumentGenerationsWithContext(ctx, ref.ID)
			if err != nil {
				return removed, err
			}
			activeIsGenerationRaw := false
			for _, rawPath := range rawPaths {
				if rawPath == ref.RawFilePath {
					activeIsGenerationRaw = true
				}
				if err := s.raw.Delete(rawPath); err != nil {
					return removed, err
				}
			}
			if ref.RawFilePath != "" {
				if activeIsGenerationRaw {
					removed++
					if onDeleted != nil {
						onDeleted()
					}
					continue
				}
				if err := s.raw.Delete(ref.RawFilePath); err != nil {
					return removed, err
				}
			}
			removed++
			if onDeleted != nil {
				onDeleted()
			}
		}
		afterID = refs[len(refs)-1].ID
		if len(refs) < cleanupDocumentPageSize {
			break
		}
	}
	if _, err := s.semanticStore.GetActiveCompilation(ctx, root.BaseID); err == nil {
		// Delete propagation is synchronous when semantic memory is active.
		// If the deterministic replacement fails, retire the old generation
		// rather than expose provenance to deleted evidence; the durable queue
		// remains available for a later full rebuild.
		if _, compileErr := s.CompileSemanticMemory(ctx, root.BaseID); compileErr != nil {
			if retireErr := s.semanticStore.RetireActiveCompilation(ctx, root.BaseID); retireErr != nil && !errors.Is(retireErr, semantic.ErrNotFound) {
				return removed, retireErr
			}
		}
	} else if !errors.Is(err, semantic.ErrNotFound) {
		return removed, err
	}
	return removed, nil
}

// DeleteDocument marks the document subtree deleting before cleanup. The
// logical effect is committed before this call returns.
func (s *Service) DeleteDocument(id string) error {
	_, err := s.deleteDocumentTree(context.Background(), id, nil)
	return err
}

// DeleteDocumentWithProgress behaves like DeleteDocument and invokes onDeleted
// after each tombstoned document's physical data has been removed. Callers may
// use it for durable progress; cancellation remains authoritative only before
// the logical fence commits.
func (s *Service) DeleteDocumentWithProgress(ctx context.Context, id string, onDeleted func()) (int, error) {
	return s.deleteDocumentTree(ctx, id, onDeleted)
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
	id, err := newID()
	if err != nil {
		return Document{}, err
	}
	return s.AddTextDocumentWithID(ctx, baseID, id, title, content)
}

// AddTextDocumentWithID lets a durable operation preallocate the business ID.
// If its business effect already committed before the terminal state was
// written, a restart returns the same document instead of importing again.
func (s *Service) AddTextDocumentWithID(ctx context.Context, baseID, id, title, content string) (Document, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	base, err := s.store.getBaseContext(ctx, baseID)
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
	if id == "" {
		id, err = newID()
		if err != nil {
			return Document{}, err
		}
	}
	doc, err := s.store.getDocumentContext(ctx, id)
	if err == nil {
		if doc.Status == StatusReady {
			return doc, nil
		}
	} else if !errors.Is(err, ErrNotFound) {
		return Document{}, err
	} else {
		doc = s.newDocument(baseID, title, "text")
	}
	doc.ID = id
	doc.BaseID = baseID
	doc.Title = title
	doc.SourceType = "text"
	doc.Status = StatusPending
	doc.RawText = content
	if doc.CreatedAt == 0 {
		doc.CreatedAt = now()
	}
	if err := s.ingest(ctx, &doc, base.Config, nil); err != nil {
		return Document{}, err
	}
	return doc, nil
}

// AddFileDocument imports an uploaded file (conflict handled by caller).
func (s *Service) AddFileDocument(ctx context.Context, baseID, fileName string, data []byte, parentDirID string) (Document, error) {
	id, err := newID()
	if err != nil {
		return Document{}, err
	}
	return s.AddFileDocumentWithID(ctx, baseID, id, fileName, data, parentDirID)
}

// AddFileDocumentWithID gives a durable operation ownership of the business ID
// before execution. After a publish/crash race, a ready document is returned
// rather than imported a second time.
func (s *Service) AddFileDocumentWithID(ctx context.Context, baseID, id, fileName string, data []byte, parentDirID string) (Document, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	base, err := s.store.getBaseContext(ctx, baseID)
	if err != nil {
		return Document{}, err
	}
	if fileName = strings.TrimSpace(fileName); fileName == "" {
		return Document{}, fmt.Errorf("file name is required")
	}
	if len(data) > MaxIngestFileBytes {
		return Document{}, fmt.Errorf("file exceeds %d MB limit", MaxIngestFileBytes>>20)
	}
	if id == "" {
		id, err = newID()
		if err != nil {
			return Document{}, err
		}
	}
	doc, err := s.store.getDocumentContext(ctx, id)
	if err == nil {
		if doc.Status == StatusReady {
			return doc, nil
		}
	} else if !errors.Is(err, ErrNotFound) {
		return Document{}, err
	} else {
		doc = s.newDocument(baseID, fileName, "file")
	}
	doc.ID = id
	doc.BaseID = baseID
	doc.Title = fileName
	doc.SourceType = "file"
	doc.FileName = fileName
	doc.ParentDirectoryID = parentDirID
	doc.Status = StatusPending
	if doc.CreatedAt == 0 {
		doc.CreatedAt = now()
	}
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
	DocumentID    string `json:"documentId,omitempty"`
	FileName      string `json:"fileName"`
	ContentBase64 string `json:"contentBase64"`
	// Resolved fields are populated only by the durable operation adapter.
	// They are intentionally excluded from the HTTP command payload; the
	// Knowledge service uses them to preserve an already committed batch item
	// without replaying its business effect.
	Resolved        bool   `json:"-"`
	ResolvedSkipped bool   `json:"-"`
	ResolvedTitle   string `json:"-"`
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
	documentID string
	title      string
	fileName   string
	data       []byte
	hash       string
	duplicate  bool
	resolved   bool
	resSkipped bool
}

// AddFiles imports a batch of base64 files with server-side conflict
// detection: conflict=detect refuses the whole batch when any name collides;
// rename/replace resolve collisions per file; duplicates by content hash are
// skipped. The request workers are bounded at five, while storage-heavy
// ingest pipelines are capped separately at two by NewService.
func (s *Service) AddFiles(ctx context.Context, baseID string, items []AddFilesItem, conflict, parentDirID string) (AddFilesResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	base, err := s.store.getBaseContext(ctx, baseID)
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
	var resultMu sync.Mutex
	for worker := 0; worker < workerCount; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				plan := plans[index]
				if plan.resolved {
					resultMu.Lock()
					result.Accepted[index] = AcceptedFile{
						ID: plan.documentID, Title: plan.title, Skipped: plan.resSkipped,
					}
					completed[index] = true
					resultMu.Unlock()
					continue
				}
				if plan.duplicate {
					resultMu.Lock()
					result.Accepted[index] = AcceptedFile{Title: plan.title, Skipped: true}
					completed[index] = true
					resultMu.Unlock()
					continue
				}
				itemCtx := ctx
				if factory := itemCommitHookFactoryFromContext(ctx); factory != nil {
					itemCtx = WithCommitHook(ctx, factory(fmt.Sprintf("%d:%s", index, plan.fileName)))
				}
				var doc Document
				var err error
				if plan.documentID != "" {
					doc, err = s.AddFileDocumentWithID(itemCtx, baseID, plan.documentID, plan.title, plan.data, parentDirID)
				} else {
					doc, err = s.AddFileDocument(itemCtx, baseID, plan.title, plan.data, parentDirID)
				}
				if err != nil {
					workerErr <- err
					return
				}
				resultMu.Lock()
				result.Accepted[index] = AcceptedFile{ID: doc.ID, Title: doc.Title}
				completed[index] = true
				resultMu.Unlock()
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
		name          string
		data          []byte
		hash          string
		documentID    string
		resolved      bool
		resSkipped    bool
		resolvedTitle string
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
		files = append(files, decodedFile{
			name: name, data: data, hash: hex.EncodeToString(sum[:]),
			documentID: item.DocumentID, resolved: item.Resolved,
			resSkipped: item.ResolvedSkipped, resolvedTitle: item.ResolvedTitle,
		})
	}

	if conflict == "detect" {
		names := make([]string, 0, len(files))
		for _, f := range files {
			names = append(names, f.name)
		}
		if conflicts, err := s.DetectConflictsContext(ctx, baseID, names); err != nil {
			return nil, err
		} else if len(conflicts) > 0 {
			return nil, &ConflictError{Conflicts: conflicts}
		}
	}

	// Existing hashes must remain visible to planning. Hashes created by the
	// workers are serialized by SQLite; a race with another import can create
	// a legitimate duplicate and will be reconciled by later operations.
	hashes := make([]string, 0, len(files))
	for _, file := range files {
		hashes = append(hashes, file.hash)
	}
	existingHashes, err := s.store.contentHashesPresentContext(ctx, baseID, hashes)
	if err != nil {
		return nil, err
	}
	plans := make([]plannedFile, 0, len(files))
	usedTitles := map[string]bool{}
	for _, f := range files {
		title := f.name
		if f.resolvedTitle != "" {
			title = f.resolvedTitle
		}
		if f.resolved {
			// A resolved durable item must not participate in conflict
			// resolution: doing so could delete its already-published document
			// before the worker reaches the resolved fast path. Keep its hash and
			// title in the planning index so later items still deduplicate
			// against the committed result.
			existingHashes[f.hash] = true
			usedTitles[title] = true
			plans = append(plans, plannedFile{
				documentID: f.documentID,
				title:      title,
				fileName:   f.name,
				data:       f.data,
				hash:       f.hash,
				resolved:   true,
				resSkipped: f.resSkipped,
			})
			continue
		}
		titleCollision := false
		if conflict == "replace" {
			if existing, err := s.store.findDocumentByTitleContext(ctx, baseID, title); err == nil {
				titleCollision = true
				deleteCtx := ctx
				if factory := itemCommitHookFactoryFromContext(ctx); factory != nil {
					// Replacing an existing title is a second durable business
					// effect. Give it its own marker so a crash between the old
					// delete and the new document publish can be replayed without
					// mistaking the replacement for a committed import.
					deleteCtx = WithCommitHook(ctx, factory("replace:"+existing.ID))
				}
				if _, err := s.DeleteDocumentWithProgress(deleteCtx, existing.ID, nil); err != nil && !errors.Is(err, ErrNotFound) {
					return nil, err
				}
			}
		} else if conflict == "rename" {
			if _, err := s.store.findDocumentByTitleContext(ctx, baseID, title); err == nil {
				var renameErr error
				title, renameErr = s.RenameAvailableContext(ctx, baseID, title)
				if renameErr != nil {
					return nil, renameErr
				}
			}
		}
		duplicate := existingHashes[f.hash]
		// A replace collision deliberately removes the old title before the
		// worker runs. The old content hash is therefore not a reason to skip
		// this item: doing so would leave the replacement title absent when the
		// replacement bytes are identical to the old bytes.
		if titleCollision {
			duplicate = false
		}
		existingHashes[f.hash] = true
		if usedTitles[title] {
			var renameErr error
			title, renameErr = s.RenameAvailableContext(ctx, baseID, title)
			if renameErr != nil {
				return nil, renameErr
			}
		}
		usedTitles[title] = true
		plans = append(plans, plannedFile{
			documentID: f.documentID,
			title:      title, fileName: f.name, data: f.data, hash: f.hash,
			duplicate: duplicate, resolved: f.resolved,
			resSkipped: f.resSkipped,
		})
	}
	return plans, nil
}

func (s *Service) hasContentHash(baseID, hash string) bool {
	present, err := s.hasContentHashContext(context.Background(), baseID, hash)
	return err == nil && present
}

func (s *Service) hasContentHashContext(ctx context.Context, baseID, hash string) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var present int
	err := s.store.db.QueryRowContext(ctx, `SELECT 1 FROM documents
		WHERE base_id = ? AND lifecycle_state = ? AND content_hash = ? LIMIT 1`,
		baseID, LifecycleActive, hash).Scan(&present)
	return present == 1, err
}

// DetectConflicts returns base file names that already exist.
func (s *Service) DetectConflicts(baseID string, fileNames []string) []string {
	conflicts, _ := s.DetectConflictsContext(context.Background(), baseID, fileNames)
	return conflicts
}

// DetectConflictsContext is the cancellable conflict lookup used by import
// workers. Unlike the legacy helper it returns unexpected database errors.
func (s *Service) DetectConflictsContext(ctx context.Context, baseID string, fileNames []string) ([]string, error) {
	var conflicts []string
	for _, name := range fileNames {
		if _, err := s.store.findDocumentByTitleContext(ctx, baseID, name); err == nil {
			conflicts = append(conflicts, name)
		} else if err != ErrNotFound {
			return conflicts, err
		}
	}
	return conflicts, nil
}

// FindDocumentByTitle exposes title conflict lookup to operation adapters.
func (s *Service) FindDocumentByTitle(baseID, title string) (Document, error) {
	return s.store.findDocumentByTitle(baseID, title)
}

// FindDocumentByTitleContext is the cancellable title conflict lookup used by
// operation workers.
func (s *Service) FindDocumentByTitleContext(ctx context.Context, baseID, title string) (Document, error) {
	return s.store.findDocumentByTitleContext(ctx, baseID, title)
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

// RenameAvailableContext returns the first free title without allowing an
// operation worker to wait on an uncancellable database lookup.
func (s *Service) RenameAvailableContext(ctx context.Context, baseID, fileName string) (string, error) {
	ext := filepath.Ext(fileName)
	stem := strings.TrimSuffix(fileName, ext)
	for i := 1; ; i++ {
		candidate := fmt.Sprintf("%s_%d%s", stem, i, ext)
		if _, err := s.store.findDocumentByTitleContext(ctx, baseID, candidate); err == ErrNotFound {
			return candidate, nil
		} else if err != nil {
			return "", err
		}
	}
}

// ReindexDocument re-reads the raw source, re-parses, and re-chunks.
func (s *Service) ReindexDocument(ctx context.Context, id string) (Document, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	doc, err := s.store.getDocumentContext(ctx, id)
	if err != nil {
		return Document{}, err
	}
	base, err := s.store.getBaseContext(ctx, doc.BaseID)
	if err != nil {
		return Document{}, err
	}
	var data []byte
	if doc.SourceType == "file" {
		if doc.RawFilePath != "" {
			if data, err = s.raw.ReadContext(ctx, doc.RawFilePath); err != nil {
				return Document{}, err
			}
		}
		// Older directory-import failures could lose RawFilePath while the
		// source path remained tracked. Fall back to that live source so those
		// documents can be recovered without another directory rescan.
		if len(data) == 0 && doc.SourcePath != "" {
			data, err = readBoundedSourceFile(doc.SourcePath)
			if err != nil {
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

func readBoundedSourceFile(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read live source: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, MaxIngestFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read live source: %w", err)
	}
	if len(data) > MaxIngestFileBytes {
		return nil, fmt.Errorf("file exceeds %d MB limit", MaxIngestFileBytes>>20)
	}
	return data, nil
}

// RecoverInterrupted finalizes documents left mid-import by a crash:
// recoverable ones (raw source present) are marked for reindex, hopeless
// placeholders become failed.
func (s *Service) RecoverInterrupted(ctx context.Context) (resumed int, failed int, err error) {
	if err := ctx.Err(); err != nil {
		return 0, 0, err
	}
	return s.store.recoverInterruptedWithContext(ctx, now(), StatusPending, StatusProcessing, StatusFailed,
		ErrInterrupted, "import was interrupted before the source was stored")
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
	cfg = ResolveBaseConfig(s.global, cfg)
	providers, err := s.providersForBaseContext(ctx, doc.BaseID)
	if err != nil {
		return err
	}
	if doc.SourceType == "file" && len(fileBytes) > 0 {
		if err := s.preserveLegacyRawGeneration(ctx, doc, fileBytes); err != nil {
			return err
		}
	}
	previous := *doc
	publishedVectors := 0
	if previous.ID != "" {
		var err error
		publishedVectors, err = s.store.countEmbeddedChunksByDocGenerationContext(ctx, previous.ID, previous.ActiveIndexGen)
		if err != nil {
			return err
		}
	}
	queueWaitStarted := Now()
	if s.ingestSlots != nil {
		select {
		case s.ingestSlots <- struct{}{}:
			defer func() { <-s.ingestSlots }()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	s.recordMetric(func(m *MetricsSnapshot) { m.QueueWaitMS += durationMS(queueWaitStarted) })
	importStarted := Now()
	defer func() {
		s.recordMetric(func(m *MetricsSnapshot) {
			m.Imports++
			elapsed := durationMS(importStarted)
			m.ImportDurationMS += elapsed
			m.RunTimeMS += elapsed
		})
		s.observePeakRSS()
	}()
	s.observePeakRSS()
	doc.Status = StatusProcessing
	doc.Phase = PhaseParsing
	doc.Progress = 0
	doc.Incomplete = true
	doc.ErrorCode = ""
	doc.ErrorMessage = ""
	doc.EmbeddingReady = false
	doc.EmbeddingModel = ""
	doc.UpdatedAt = now()
	if err := s.store.startDocumentIngestContext(ctx, *doc); err != nil {
		return err
	}

	if doc.SourceType == "file" && len(fileBytes) > 0 {
		rel, err := s.raw.WriteVersion(doc.BaseID, doc.ID, doc.SourceVersion+1,
			"."+parser.ExtensionOf(doc.FileName), fileBytes)
		if err != nil {
			return s.failDocumentContext(ctx, doc, ErrParseFailed, err)
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
			return s.failDocumentContext(ctx, doc, ErrParseFailed, err)
		}
		if parsedTitle != "" && !doc.TitleLocked {
			doc.Title = parsedTitle
		}
		text = s.appendImageCaptions(ctx, doc, fileBytes, parsedText)
	}

	text = chunk.Normalize(text)
	if text == "" {
		return s.failDocumentContext(ctx, doc, ErrParseFailed, fmt.Errorf("contains no extractable text"))
	}
	opts := s.chunkOptions(cfg)
	irBuildStarted := Now()
	if doc.IR == nil || len(doc.IR.Nodes) == 0 {
		parserName := "text"
		if doc.SourceType == "file" {
			parserName = parser.ExtensionOf(doc.FileName)
		}
		doc.IR = documentir.FromText(doc.Title, text, parserName, "builtin-v1")
	}
	doc.IR.Title = doc.Title
	doc.IR.ParseConfig = map[string]string{
		"ocrMode":   s.resolveOCRMode(cfg),
		"processor": strings.TrimSpace(cfg.Processor),
		"chunkMode": map[bool]string{true: "smart", false: "delimiter"}[opts.Smart],
	}
	if err := doc.IR.BindDocument(doc.ID); err != nil {
		return s.failDocumentContext(ctx, doc, ErrParseFailed, err)
	}
	irBytes := 0
	if encoded, err := doc.IR.MarshalJSONStable(); err == nil {
		irBytes = len(encoded)
	}
	s.recordMetric(func(m *MetricsSnapshot) {
		m.IRBuildDurationMS += durationMS(irBuildStarted)
		m.IRStorageBytes += int64(irBytes)
	})
	s.observePeakRSS()
	s.recordMetric(func(m *MetricsSnapshot) {
		if m.ParserSelections == nil {
			m.ParserSelections = map[string]int64{}
		}
		parserName := doc.IR.Parser
		if parserName == "" {
			parserName = "unknown"
		}
		m.ParserSelections[parserName]++
		m.NodeCount += int64(len(doc.IR.Nodes))
		for _, node := range doc.IR.Nodes {
			if node.Type == documentir.TypeTable || node.Type == documentir.TypeTableCell {
				m.TableCount++
			}
			if node.Type == documentir.TypeFigure {
				m.FigureCount++
			}
		}
	})
	doc.RawText = text
	doc.CharCount = len([]rune(text))
	doc.TokenCount = chunk.EstimateTokens(text)

	chunkStarted := Now()
	pieces, inlineVectors := s.buildPieces(ctx, text, doc, opts, providers.embedder)

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
		c.NodeIDs, c.SourceAnchor = nodesForPiece(doc.IR, piece.Text, piece.Heading)
		if structural := structuralContext(doc.IR, c.NodeIDs); structural != "" {
			c.Context = strings.TrimSpace(c.Context + " " + structural)
		}
		c.EmbeddingText = strings.TrimSpace(c.Context + " " + c.Text)
		c.EmbeddingHash = hashText(c.EmbeddingText)
		if inlineVectors != nil {
			if vector, ok := inlineVectors[i]; ok {
				c.EmbeddingVec = vector
				c.EmbeddingModel = providers.embedder.ModelKey()
			}
		}
		rows = append(rows, c)
	}
	if len(rows) == 0 {
		return s.failDocumentContext(ctx, doc, ErrParseFailed, fmt.Errorf("chunker produced no chunks"))
	}
	s.recordMetric(func(m *MetricsSnapshot) { m.ChunkDurationMS += durationMS(chunkStarted) })
	s.observePeakRSS()
	targetModelKey := ""
	if providers.embeddingActive && providers.embedder != nil && providers.embedder.ModelKey() != "none" {
		targetModelKey = providers.embedder.ModelKey()
	}
	dbTransactionStarted := Now()
	generation, err := s.store.putChunksReplace(ctx, rows, targetModelKey, doc.IR)
	s.recordMetric(func(m *MetricsSnapshot) {
		m.DBTransactionMS += durationMS(dbTransactionStarted)
	})
	if err != nil {
		return s.failDocumentContext(ctx, doc, ErrParseFailed, err)
	}
	staged, err := s.store.getDocumentContext(ctx, doc.ID)
	if err != nil {
		return s.failDocumentContext(ctx, doc, ErrParseFailed, err)
	}
	expectedEpoch := staged.MutationEpoch
	doc.DesiredIndexGen = generation
	doc.HasDesiredIndexGen = true
	doc.IndexState = IndexStateBuilding
	s.recordMetric(func(m *MetricsSnapshot) { m.ChunkCount += int64(len(rows)) })

	// Vector phase: with an active embedding provider, chunks are embedded
	// with library-wide hash reuse; a failure degrades the document to
	// lexical-only instead of dropping the imported text. Degradation is a
	// successful import with a recorded error code (reference behavior);
	// only a cancellation leaves the document resumable.
	if providers.embeddingActive {
		doc.Status = StatusProcessing
		doc.Phase = PhaseEmbedding
		doc.Progress = 0
		doc.UpdatedAt = now()
		if err := s.store.putDocumentContext(ctx, *doc); err != nil {
			return err
		}
		embeddingStarted := Now()
		code, cause := s.embedChunks(ctx, doc, rows, providers.embedder)
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
			if err := s.store.putDocumentContext(ctx, *doc); err != nil {
				return err
			}
			if publishedVectors > 0 {
				// A previously published vector index remains authoritative
				// when a reindex cannot produce vectors. The staged lexical
				// version stays unactivated for later bounded cleanup.
				*doc = previous
				doc.Status = StatusReady
				doc.ErrorCode = code
				doc.ErrorMessage = cause.Error()
				doc.UpdatedAt = now()
				return s.store.putDocumentContext(ctx, *doc)
			}
			if code == ErrInterrupted {
				return nil
			}
			doc.SourceVersion++
			if err := s.store.activateDocumentGeneration(ctx, doc.ID, expectedEpoch, generation, *doc); err != nil {
				return s.failDocumentContext(ctx, doc, code, err)
			}
			doc.ActiveIndexGen = generation
			rawPaths, pruneErr := s.store.pruneRetiredGenerationsWithContext(ctx, doc.ID, generation)
			if pruneErr != nil {
				return pruneErr
			}
			for _, rawPath := range rawPaths {
				if rawPath == "" {
					continue
				}
				if err := s.raw.Delete(rawPath); err != nil {
					return err
				}
			}
			if err := s.refreshDerivedKnowledge(ctx, doc); err != nil {
				return err
			}
			if err := s.markSemanticDocumentUpdated(ctx, doc); err != nil {
				return err
			}
			return nil
		}
		s.observePeakRSS()
	}

	doc.ChunkCount = len(rows)
	if providers.embeddingActive && providers.embedder != nil {
		doc.EmbeddingReady = true
		doc.EmbeddingModel = providers.embedder.ModelKey()
	}
	doc.Status = StatusReady
	doc.Phase = ""
	doc.Progress = 100
	doc.Incomplete = false
	doc.UpdatedAt = now()
	doc.SourceVersion++
	if err := s.store.activateDocumentGeneration(ctx, doc.ID, expectedEpoch, generation, *doc); err != nil {
		return s.failDocumentContext(ctx, doc, ErrParseFailed, err)
	}
	doc.ActiveIndexGen = generation
	rawPaths, err := s.store.pruneRetiredGenerationsWithContext(ctx, doc.ID, generation)
	if err != nil {
		return err
	}
	for _, rawPath := range rawPaths {
		if rawPath == "" {
			continue
		}
		if err := s.raw.Delete(rawPath); err != nil {
			return err
		}
	}
	if err := s.refreshDerivedKnowledge(ctx, doc); err != nil {
		return err
	}
	if err := s.markSemanticDocumentUpdated(ctx, doc); err != nil {
		return err
	}
	return nil
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
			doc.IR = documentir.FromText("", markdown, "mineru", "remote")
			return markdown, "", nil
		}
	}

	parsed, parseErr := s.parsers.ParseContext(ctx, doc.FileName, data)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", "", ctxErr
	}
	nativeAvailable := parseErr == nil && strings.TrimSpace(parsed.Text) != ""
	if parsed.IR != nil {
		doc.IR = parsed.IR
	}
	// The parser may still provide fragmented native text while requesting a
	// healthier OCR pass (upstream's text-layer health behavior).
	nativeOK := nativeAvailable && !parsed.NeedsOCR

	mode := s.resolveOCRMode(cfg)
	fallbackUsed := mode == "forced" || parseErr != nil || !nativeOK
	defer func() {
		if fallbackUsed {
			s.recordMetric(func(m *MetricsSnapshot) { m.ParserFallbacks++ })
		}
	}()
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
			doc.IR = documentir.FromText(parsed.Title, text, "ocr", "builtin-v1")
			return text, parsed.Title, nil
		}
		if text, ok := runContentConverter(); ok {
			doc.IR = documentir.FromText(parsed.Title, text, "content-converter", "builtin-v1")
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
			doc.IR = documentir.FromText(parsed.Title, text, "ocr", "builtin-v1")
			return text, parsed.Title, nil
		}
		if text, ok := runContentConverter(); ok {
			doc.IR = documentir.FromText(parsed.Title, text, "content-converter", "builtin-v1")
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
func (s *Service) buildPieces(ctx context.Context, text string, doc *Document, opts ChunkOptions, providers ...embedding.Provider) ([]chunk.Piece, map[int][]float64) {
	embedder := s.embedder
	if len(providers) > 0 && providers[0] != nil {
		embedder = providers[0]
	}
	if !opts.Semantic || embedder == nil || embedder.ModelKey() == "none" {
		return s.structureAwarePieces(text, doc.IR, opts), nil
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
	vectors, err := embedder.Embed(ctx, texts)
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

// structureAwarePieces keeps hard page/slide/sheet/table boundaries intact
// before applying the existing paragraph/token chunker. It is deliberately a
// small projection, not a second chunking engine.
func (s *Service) structureAwarePieces(text string, ir *documentir.Document, opts ChunkOptions) []chunk.Piece {
	if ir == nil {
		return s.structuralPieces(text, opts)
	}
	hasPageOrSlide := false
	for _, node := range ir.Nodes {
		if node.Type == documentir.TypePage || node.Type == documentir.TypeSlide {
			hasPageOrSlide = true
			break
		}
	}
	useLeafStructure := ir.Parser == "docx" || ir.Parser == "epub"
	var pieces []chunk.Piece
	for _, node := range ir.Nodes {
		include := false
		switch node.Type {
		case documentir.TypePage, documentir.TypeSlide, documentir.TypeSheet,
			documentir.TypeTable, documentir.TypeTableRow, documentir.TypeFigure,
			documentir.TypeCaption, documentir.TypeFootnote:
			include = true
		case documentir.TypeSection, documentir.TypeHeading, documentir.TypeParagraph,
			documentir.TypeBlock, documentir.TypeListItem:
			// Page/slide nodes are already aggregate reading-order units. Office
			// and text documents without those containers retain their leaf
			// paragraph/list/heading boundaries here.
			include = !hasPageOrSlide && useLeafStructure
		}
		if !include {
			continue
		}
		if node.Type == documentir.TypeSheet {
			hasRows := false
			for _, child := range ir.Nodes {
				if child.ParentID == node.ID && (child.Type == documentir.TypeTable || child.Type == documentir.TypeTableRow) {
					hasRows = true
					break
				}
			}
			if hasRows {
				continue
			}
		}
		if strings.TrimSpace(node.Text) == "" {
			continue
		}
		heading := strings.Join(node.HeadingPath, " / ")
		if heading == "" {
			switch node.Type {
			case documentir.TypePage:
				heading = fmt.Sprintf("Page %d", node.PageNumber)
			case documentir.TypeSlide:
				heading = fmt.Sprintf("Slide %d", node.SlideNumber)
			case documentir.TypeSheet:
				heading = node.SheetName
			case documentir.TypeTableRow:
				heading = node.SourceAnchor.CellRange
			case documentir.TypeTable:
				heading = "Table"
			}
		}
		for _, piece := range chunk.Chunk(node.Text, opts.Size, opts.Overlap, chunk.Options{Smart: &opts.Smart, Separator: opts.Separator}) {
			piece.Heading = strings.TrimSpace(heading)
			pieces = append(pieces, piece)
		}
	}
	if len(pieces) == 0 {
		return s.structuralPieces(text, opts)
	}
	return chunk.RefineByTokenLimit(pieces, opts.TokenLimit, nil)
}

func nodesForPiece(ir *documentir.Document, text, heading string) ([]string, documentir.SourceAnchor) {
	if ir == nil {
		return nil, documentir.SourceAnchor{}
	}
	target := strings.TrimSpace(chunk.Normalize(text))
	var ids []string
	var anchor documentir.SourceAnchor
	bestAnchorScore := -1
	seen := map[string]bool{}
	for _, node := range ir.Nodes {
		if node.Type == documentir.TypeDocument || strings.TrimSpace(node.Text) == "" {
			continue
		}
		nodeText := strings.TrimSpace(chunk.Normalize(node.Text))
		match := nodeText != "" && (strings.Contains(nodeText, target) || strings.Contains(target, nodeText))
		if !match && heading != "" && strings.Contains(strings.Join(node.HeadingPath, " "), strings.TrimSpace(heading)) {
			match = true
		}
		if !match || seen[node.ID] {
			continue
		}
		seen[node.ID] = true
		ids = append(ids, node.ID)
		score := sourceAnchorScore(node.Type, node.Metadata)
		if anchor.Kind == "" || score > bestAnchorScore {
			anchor = node.SourceAnchor
			bestAnchorScore = score
		}
		if len(ids) >= 8 {
			break
		}
	}
	if len(ids) == 0 {
		for _, node := range ir.Nodes {
			if node.Type == documentir.TypeDocument {
				continue
			}
			ids = append(ids, node.ID)
			anchor = node.SourceAnchor
			break
		}
	}
	return ids, anchor
}

// structuralContext adds a bounded parent/header projection to the retrieval
// representation. The chunk text remains the source evidence; this context
// only helps a row/cell query retain the table heading and header row.
func structuralContext(ir *documentir.Document, ids []string) string {
	if ir == nil || len(ids) == 0 {
		return ""
	}
	byID := make(map[string]documentir.Node, len(ir.Nodes))
	for _, node := range ir.Nodes {
		byID[node.ID] = node
	}
	parts := make([]string, 0, 4)
	seen := map[string]bool{}
	appendNode := func(node documentir.Node) {
		value := strings.TrimSpace(node.Text)
		if value == "" || node.Type == documentir.TypeDocument || seen[node.ID] {
			return
		}
		seen[node.ID] = true
		parts = append(parts, value)
	}
	for _, id := range ids {
		node, ok := byID[id]
		if !ok {
			continue
		}
		if len(node.HeadingPath) > 0 {
			parts = append(parts, "Section: "+strings.Join(node.HeadingPath, " / "))
		}
		for parent := node.ParentID; parent != ""; {
			ancestor, exists := byID[parent]
			if !exists {
				break
			}
			if ancestor.Type == documentir.TypeHeading || ancestor.Type == documentir.TypeSection || ancestor.Type == documentir.TypeTable {
				appendNode(ancestor)
			}
			parent = ancestor.ParentID
		}
		if node.Type == documentir.TypeTableCell || node.Type == documentir.TypeTableRow {
			if node.Type == documentir.TypeTableCell {
				if parent, exists := byID[node.ParentID]; exists {
					appendNode(parent)
				}
			}
			row := node
			if row.Type == documentir.TypeTableCell {
				row = byID[row.ParentID]
			}
			for _, candidate := range ir.Nodes {
				if candidate.ParentID == row.ParentID && candidate.Type == documentir.TypeTableRow && candidate.Order < row.Order {
					appendNode(candidate)
					break
				}
			}
		}
		if len(parts) >= 4 {
			break
		}
	}
	value := strings.TrimSpace(strings.Join(parts, " | "))
	if len([]rune(value)) > 600 {
		value = string([]rune(value)[:600])
	}
	return value
}

// embedChunks embeds the stored chunks in batches, reusing stored vectors by
// embedding-text hash, and persists each landed batch (crash-safe). Returns
// a stable error code when the document degrades to lexical-only.
func (s *Service) embedChunks(ctx context.Context, doc *Document, rows []Chunk, providers ...embedding.Provider) (string, error) {
	embedder := s.embedder
	if len(providers) > 0 && providers[0] != nil {
		embedder = providers[0]
	}
	if embedder == nil || embedder.ModelKey() == "none" {
		return "", nil
	}
	modelKey := embedder.ModelKey()
	hashes := make([]string, 0, len(rows))
	textByHash := map[string]string{}
	for _, row := range rows {
		hashes = append(hashes, row.EmbeddingHash)
		textByHash[row.EmbeddingHash] = row.EmbeddingText
	}
	reuse, err := s.store.ListEmbeddingVectorsByHashesContext(ctx, hashes, modelKey)
	if err != nil {
		return ErrEmbeddingProvider, err
	}
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
		if err := s.store.PutChunkVectorsContext(ctx, doc.ID, rows[0].IndexGeneration, modelKey, reusedForDocument); err != nil {
			return ErrEmbeddingProvider, err
		}
	}
	dimension := 0
	batchSize := s.global.Embedding.Batch
	progressStep := 100 / (len(rows) + 1)
	landed := 0
	report := func() {
		if ctx.Err() != nil {
			return
		}
		landed++
		doc.Progress = minInt(99, landed*progressStep)
		doc.UpdatedAt = now()
		_ = s.store.putDocumentContext(ctx, *doc)
	}
	for start := 0; start < len(pendingHashes); start += batchSize {
		end := minInt(start+batchSize, len(pendingHashes))
		batch := pendingHashes[start:end]
		texts := make([]string, 0, len(batch))
		for _, hash := range batch {
			texts = append(texts, textByHash[hash])
		}
		vectors, err := embedder.Embed(ctx, texts)
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
		if err := s.store.PutChunkVectorsContext(ctx, doc.ID, rows[0].IndexGeneration, modelKey, byHash); err != nil {
			return ErrEmbeddingProvider, err
		}
		report()
	}
	doc.Progress = 100
	return "", nil
}

func (s *Service) failDocument(doc *Document, code string, cause error) error {
	return s.failDocumentContext(context.Background(), doc, code, cause)
}

func (s *Service) failDocumentContext(ctx context.Context, doc *Document, code string, cause error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	doc.Status = StatusFailed
	doc.Phase = ""
	doc.ErrorCode = code
	doc.ErrorMessage = cause.Error()
	doc.Incomplete = false
	doc.UpdatedAt = now()
	if err := s.store.putDocumentContext(ctx, *doc); err != nil {
		return err
	}
	return fmt.Errorf("%s: %w", code, cause)
}

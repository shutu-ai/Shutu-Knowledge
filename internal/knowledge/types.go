// Package knowledge implements the Knowledge domain: bases, documents,
// chunks, lifecycle, groups, and the enabled scope. Plain JSON-serializable
// values only — no Agent or storage types leak out of this package.
package knowledge

import (
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/documentir"
)

// BaseConfig carries per-base overrides; empty fields inherit the global
// configuration at resolve time.
type BaseConfig struct {
	EmbeddingProvider    string `json:"embeddingProvider,omitempty"`
	EmbeddingBaseURL     string `json:"embeddingBaseUrl,omitempty"`
	EmbeddingModel       string `json:"embeddingModel,omitempty"`
	EmbeddingAPIKey      string `json:"embeddingApiKey,omitempty"`
	EmbeddingAPIKeySet   bool   `json:"embeddingApiKeySet,omitempty"`
	ClearEmbeddingAPIKey bool   `json:"clearEmbeddingApiKey,omitempty"`
	RerankEnabled        *bool  `json:"rerankEnabled,omitempty"`
	RerankModel          string `json:"rerankModel,omitempty"`
	RerankBaseURL        string `json:"rerankBaseUrl,omitempty"`
	RerankAPIKey         string `json:"rerankApiKey,omitempty"`
	RerankAPIKeySet      bool   `json:"rerankApiKeySet,omitempty"`
	ClearRerankAPIKey    bool   `json:"clearRerankApiKey,omitempty"`

	SmartChunk        *bool   `json:"smartChunk,omitempty"`
	ChunkSeparator    string  `json:"chunkSeparator,omitempty"`
	ChunkSize         int     `json:"chunkSize,omitempty"`
	ChunkOverlap      int     `json:"chunkOverlap,omitempty"`
	SemanticChunk     *bool   `json:"semanticChunk,omitempty"`
	SemanticThreshold float64 `json:"semanticChunkThreshold,omitempty"`
	ChunkTokenLimit   int     `json:"chunkTokenLimit,omitempty"`

	TopK            int     `json:"topK,omitempty"`
	SearchMode      string  `json:"searchMode,omitempty"`
	SimilarityMin   float64 `json:"similarityThreshold,omitempty"`
	MMR             *bool   `json:"mmr,omitempty"`
	MMRDiversity    float64 `json:"mmrDiversity,omitempty"`
	RRFVectorWeight float64 `json:"rrfVectorWeight,omitempty"`
	SiblingChunks   *int    `json:"siblingChunks,omitempty"`

	ConflictStrategy string `json:"conflictStrategy,omitempty"` // keep | replace | rename
	URLRefreshHours  int    `json:"urlRefreshHours,omitempty"`
	AutoRetrieve     *bool  `json:"autoRetrieve,omitempty"`
	// AutoRetrieveMax is a pointer so an explicit zero can exclude a base.
	AutoRetrieveMax   *int   `json:"autoRetrieveWeight,omitempty"`
	Processor         string `json:"documentProcessorProvider,omitempty"` // builtin | mineru
	MineruAPIKey      string `json:"mineruApiKey,omitempty"`
	MineruAPIKeySet   bool   `json:"mineruApiKeySet,omitempty"`
	ClearMineruAPIKey bool   `json:"clearMineruApiKey,omitempty"`
	MineruAPIHost     string `json:"mineruApiHost,omitempty"`
	// OCRMode: auto (native first, OCR fallback) | forced | off; empty
	// inherits the global mode.
	OCRMode string `json:"ocrMode,omitempty"`
}

// Redacted returns an API-safe copy: credentials are removed and only their
// configured-state flags remain. Clear flags are never echoed.
func (c BaseConfig) Redacted() BaseConfig {
	c.EmbeddingAPIKeySet = c.EmbeddingAPIKey != ""
	c.EmbeddingAPIKey = ""
	c.ClearEmbeddingAPIKey = false
	c.RerankAPIKeySet = c.RerankAPIKey != ""
	c.RerankAPIKey = ""
	c.ClearRerankAPIKey = false
	c.MineruAPIKeySet = c.MineruAPIKey != ""
	c.MineruAPIKey = ""
	c.ClearMineruAPIKey = false
	return c
}

// Base is one knowledge base.
type Base struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Group       string     `json:"group,omitempty"`
	Config      BaseConfig `json:"config,omitempty"`
	CreatedAt   int64      `json:"createdAt"`
	UpdatedAt   int64      `json:"updatedAt"`
	// LifecycleState is the P1 delete fence. It is intentionally separate
	// from business status and is persisted before cleanup begins.
	LifecycleState string `json:"-"`
	MutationEpoch  int64  `json:"-"`
}

// Document lifecycle statuses.
const (
	StatusPending    = "pending"
	StatusProcessing = "processing"
	StatusReady      = "ready"
	StatusFailed     = "failed"
	StatusStale      = "stale"
)

// Processing phases while status is processing.
const (
	PhaseParsing   = "parsing"
	PhaseEmbedding = "embedding"
	PhaseScanning  = "scanning"
)

// Document error codes (stable, UI-localizable).
const (
	ErrInterrupted       = "interrupted"
	ErrDimensionMismatch = "dimension_mismatch"
	ErrParseFailed       = "parse_failed"
	ErrEmbeddingProvider = "embedding_provider"
	ErrSourceMissing     = "source_missing"
)

// Document is one imported document inside a base.
type Document struct {
	ID                string `json:"id"`
	BaseID            string `json:"baseId"`
	Title             string `json:"title"`
	SourceType        string `json:"sourceType"` // text | file | url | directory
	FileName          string `json:"fileName,omitempty"`
	MimeType          string `json:"mimeType,omitempty"`
	URL               string `json:"url,omitempty"`
	ParentDirectoryID string `json:"parentDirectoryId,omitempty"`
	SourcePath        string `json:"sourcePath,omitempty"`
	ContentHash       string `json:"contentHash,omitempty"`
	RawFilePath       string `json:"rawFilePath,omitempty"`
	RawText           string `json:"-"`
	// IR is runtime state; its durable generation-scoped form is stored in
	// document_nodes and published with the same fence as chunks.
	IR *documentir.Document `json:"-"`
	// TitleLocked is internal state: user-named titles survive source
	// refresh, while source-derived titles follow metadata changes.
	TitleLocked    bool   `json:"-"`
	CharCount      int    `json:"charCount"`
	TokenCount     int    `json:"tokenCount,omitempty"`
	ChunkCount     int    `json:"chunkCount"`
	EmbeddingReady bool   `json:"-"`
	EmbeddingModel string `json:"-"`
	Status         string `json:"status"`
	Phase          string `json:"phase,omitempty"`
	Progress       int    `json:"progress"`
	Incomplete     bool   `json:"incomplete,omitempty"`
	ErrorCode      string `json:"errorCode,omitempty"`
	ErrorMessage   string `json:"errorMessage,omitempty"`
	// Extraction quality visibility (v0.6.2). QualityStatus is '' for legacy
	// rows and is surfaced as UNKNOWN by the API layer.
	QualityStatus   string   `json:"qualityStatus,omitempty"`
	QualityScore    float64  `json:"qualityScore,omitempty"`
	QualityWarnings []string `json:"qualityWarnings,omitempty"`
	QualityPartial  bool     `json:"qualityPartial,omitempty"`
	// ExtractionMethod records the winning candidate: native, reassembled,
	// fallback, ocr, or mixed.
	ExtractionMethod string `json:"extractionMethod,omitempty"`
	PagesTotal       int    `json:"pagesTotal,omitempty"`
	PagesOCR         int    `json:"pagesOcr,omitempty"`
	CreatedAt        int64  `json:"createdAt"`
	UpdatedAt        int64  `json:"updatedAt,omitempty"`
	// Version/fence state retained on every document. Generation zero is the
	// compatibility identity assigned to all pre-migration rows.
	LifecycleState     string `json:"-"`
	MutationEpoch      int64  `json:"-"`
	SourceVersion      int64  `json:"-"`
	ActiveIndexGen     int64  `json:"-"`
	DesiredIndexGen    int64  `json:"-"`
	HasDesiredIndexGen bool   `json:"-"`
	IndexState         string `json:"-"`
}

// Chunk is one stored chunk. Phase 2 stores text + metadata; embedding and
// embeddingModel are filled by the retrieval phase.
type Chunk struct {
	ID            string `json:"id"`
	DocID         string `json:"docId"`
	BaseID        string `json:"baseId"`
	Index         int    `json:"index"`
	Text          string `json:"text"`
	Heading       string `json:"heading,omitempty"`
	Context       string `json:"context,omitempty"`
	EmbeddingText string `json:"-"`
	EmbeddingHash string `json:"-"`
	// EmbeddingVec/EmbeddingModel are set on the semantic-chunking insert
	// path; the regular phase-3 path persists vectors via PutChunkVectors.
	EmbeddingVec    []float64               `json:"-"`
	EmbeddingModel  string                  `json:"-"`
	CreatedAt       int64                   `json:"-"`
	HasEmbedding    bool                    `json:"-"`
	IndexGeneration int64                   `json:"-"`
	SourceVersion   int64                   `json:"-"`
	NodeIDs         []string                `json:"-"`
	NodeTypes       []string                `json:"-"`
	SourceAnchor    documentir.SourceAnchor `json:"-"`
	// Stage values are used only while inserting a new generation before it
	// becomes active. They keep reusable vectors attached to the new rows.
	StageEmbedding      []byte `json:"-"`
	StageEmbeddingModel string `json:"-"`
}

// CitationV2 is an additive source anchor. Legacy callers can continue using
// document/chunk IDs while newer callers get the most precise parser anchor.
type CitationV2 struct {
	Document   string           `json:"document,omitempty"`
	DocumentID string           `json:"documentId,omitempty"`
	Page       int              `json:"page,omitempty"`
	Slide      int              `json:"slide,omitempty"`
	Sheet      string           `json:"sheet,omitempty"`
	Section    string           `json:"section,omitempty"`
	NodeID     string           `json:"nodeId,omitempty"`
	ChunkID    string           `json:"chunkId,omitempty"`
	BBox       *documentir.BBox `json:"bbox,omitempty"`
	CellRange  string           `json:"cellRange,omitempty"`
	Snippet    string           `json:"snippet,omitempty"`
}

// DocumentSummary is the list view of a document.
type DocumentSummary struct {
	ID               string `json:"id"`
	BaseID           string `json:"baseId"`
	Title            string `json:"title"`
	SourceType       string `json:"sourceType"`
	FileName         string `json:"fileName,omitempty"`
	URL              string `json:"url,omitempty"`
	ParentDirID      string `json:"parentDirectoryId,omitempty"`
	SourcePath       string `json:"sourcePath,omitempty"`
	CharCount        int    `json:"charCount"`
	TokenCount       int    `json:"tokenCount,omitempty"`
	ChunkCount       int    `json:"chunkCount"`
	EmbeddingReady   bool   `json:"embeddingReady"`
	Status           string `json:"status"`
	Phase            string `json:"phase,omitempty"`
	Progress         int    `json:"progress"`
	ErrorCode        string `json:"errorCode,omitempty"`
	ErrorMessage     string `json:"errorMessage,omitempty"`
	QualityStatus    string `json:"qualityStatus,omitempty"`
	QualityPartial   bool   `json:"qualityPartial,omitempty"`
	ExtractionMethod string `json:"extractionMethod,omitempty"`
	PagesTotal       int    `json:"pagesTotal,omitempty"`
	PagesOCR         int    `json:"pagesOcr,omitempty"`
	CreatedAt        int64  `json:"createdAt"`
	UpdatedAt        int64  `json:"updatedAt,omitempty"`
	// These fields participate in equivalent-operation keys but are never
	// serialized in list responses.
	SourceVersion  int64  `json:"-"`
	MutationEpoch  int64  `json:"-"`
	ActiveIndexGen int64  `json:"-"`
	ContentHash    string `json:"-"`
	RawFilePath    string `json:"-"`
}

// DocumentChildrenPage is one bounded directory view. Breadcrumbs are ordered
// from the base root to the requested parent and never include raw source text.
type DocumentChildrenPage struct {
	ParentID    string            `json:"parentId,omitempty"`
	Documents   []DocumentSummary `json:"documents"`
	Total       int               `json:"total"`
	Limit       int               `json:"limit"`
	Offset      int               `json:"offset"`
	HasMore     bool              `json:"hasMore"`
	Breadcrumbs []DocumentSummary `json:"breadcrumbs"`
}

// DocumentListPage is the bounded compatibility form of the legacy flat
// document list. It intentionally has no tree breadcrumbs.
type DocumentListPage struct {
	Documents []DocumentSummary `json:"documents"`
	Total     int               `json:"total"`
	Limit     int               `json:"limit"`
	Offset    int               `json:"offset"`
	HasMore   bool              `json:"hasMore"`
}

// BaseSummary is the list view of a base.
type BaseSummary struct {
	Base
	DocumentCount int   `json:"documentCount"`
	ChunkCount    int   `json:"chunkCount"`
	CharCount     int64 `json:"charCount"`
	TokenCount    int64 `json:"tokenCount"`
}

// Stats aggregates one base (or all bases when BaseID is empty).
type Stats struct {
	BaseID        string `json:"baseId,omitempty"`
	DocumentCount int    `json:"documentCount"`
	ChunkCount    int    `json:"chunkCount"`
	CharCount     int64  `json:"charCount"`
	TokenCount    int64  `json:"tokenCount"`
	Embedded      bool   `json:"embedded"`
	Dimensions    int    `json:"embeddingDimensions,omitempty"`
	StaleChunks   int    `json:"staleChunkCount,omitempty"`
}

// Now is the clock used for timestamps (overridable in tests).
var Now = time.Now

// EnabledScopeState is the complete invocation switch state. Explicit is
// false only before the first scope write; a saved empty BaseIDs slice is a
// fail-closed pinned scope, not "all bases".
type EnabledScopeState struct {
	Enabled  bool     `json:"enabled"`
	BaseIDs  []string `json:"enabledBaseIds"`
	Explicit bool     `json:"-"`
}

// Evidence accessors adapt Chunk to the evidence composer interface.
func (c Chunk) EvidenceID() string      { return c.ID }
func (c Chunk) EvidenceDocID() string   { return c.DocID }
func (c Chunk) EvidenceBaseID() string  { return c.BaseID }
func (c Chunk) EvidenceIndex() int      { return c.Index }
func (c Chunk) EvidenceText() string    { return c.Text }
func (c Chunk) EvidenceHeading() string { return c.Heading }

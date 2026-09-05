// Package knowledge implements the Knowledge domain: bases, documents,
// chunks, lifecycle, groups, and the enabled scope. Plain JSON-serializable
// values only — no Agent or storage types leak out of this package.
package knowledge

import "time"

// BaseConfig carries per-base overrides; empty fields inherit the global
// configuration at resolve time.
type BaseConfig struct {
	EmbeddingProvider string `json:"embeddingProvider,omitempty"`
	EmbeddingBaseURL  string `json:"embeddingBaseUrl,omitempty"`
	EmbeddingModel    string `json:"embeddingModel,omitempty"`
	EmbeddingAPIKey   string `json:"embeddingApiKey,omitempty"`
	RerankModel       string `json:"rerankModel,omitempty"`
	RerankBaseURL     string `json:"rerankBaseUrl,omitempty"`
	RerankAPIKey      string `json:"rerankApiKey,omitempty"`

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
	AutoRetrieveMax  int    `json:"autoRetrieveWeight,omitempty"`
	Processor        string `json:"documentProcessorProvider,omitempty"` // builtin | mineru
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
)

// Document error codes (stable, UI-localizable).
const (
	ErrInterrupted       = "interrupted"
	ErrDimensionMismatch = "dimension_mismatch"
	ErrParseFailed       = "parse_failed"
	ErrEmbeddingProvider = "embedding_provider"
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
	CharCount         int    `json:"charCount"`
	TokenCount        int    `json:"tokenCount,omitempty"`
	ChunkCount        int    `json:"chunkCount"`
	Status            string `json:"status"`
	Phase             string `json:"phase,omitempty"`
	Progress          int    `json:"progress"`
	Incomplete        bool   `json:"incomplete,omitempty"`
	ErrorCode         string `json:"errorCode,omitempty"`
	ErrorMessage      string `json:"errorMessage,omitempty"`
	CreatedAt         int64  `json:"createdAt"`
	UpdatedAt         int64  `json:"updatedAt,omitempty"`
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
	CreatedAt     int64  `json:"-"`
	HasEmbedding  bool   `json:"-"`
}

// DocumentSummary is the list view of a document.
type DocumentSummary struct {
	ID          string `json:"id"`
	BaseID      string `json:"baseId"`
	Title       string `json:"title"`
	SourceType  string `json:"sourceType"`
	FileName    string `json:"fileName,omitempty"`
	URL         string `json:"url,omitempty"`
	ParentDirID string `json:"parentDirectoryId,omitempty"`
	CharCount   int    `json:"charCount"`
	TokenCount  int    `json:"tokenCount,omitempty"`
	ChunkCount  int    `json:"chunkCount"`
	Status      string `json:"status"`
	Phase       string `json:"phase,omitempty"`
	Progress    int    `json:"progress"`
	ErrorCode   string `json:"errorCode,omitempty"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt,omitempty"`
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

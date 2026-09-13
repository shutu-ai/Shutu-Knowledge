package knowledge

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"sync"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/caption"
	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/embedding"
	"github.com/shutu-ai/shutu-knowledge/internal/jobs"
	"github.com/shutu-ai/shutu-knowledge/internal/parser"
	"github.com/shutu-ai/shutu-knowledge/internal/rerank"
	"github.com/shutu-ai/shutu-knowledge/internal/runtime"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
)

// ErrNotFound is returned for missing bases/documents.
var ErrNotFound = errors.New("not found")

// Service is the Knowledge Core facade: bases, documents, lifecycle.
type Service struct {
	store        *store
	raw          *storage.RawFileStore
	parsers      *parser.Registry
	ocr          parser.HelperRunner
	ocrRenderer  parser.PageRenderer
	content      parser.HelperRunner
	imageDecoder parser.ExecHelper
	global       config.Config
	jobMgr       *jobs.Manager
	embedder     embedding.Provider
	reranker     rerank.Provider
	runtime      runtime.Caller
	// ocrArtifacts maps the shared model bundle to the optional runtime
	// helper contract; nil means the helper owns its artifact resolution.
	ocrArtifacts func() (path string, ready bool)
	// captionOptions is normally nil; tests may inject HTTP/extractor fakes.
	captionOptions *caption.Options
	// batchMu serializes batch planning so title/hash decisions see one
	// consistent snapshot before bounded ingestion workers start.
	batchMu     sync.Mutex
	ingestSlots chan struct{}
	metricsMu   sync.Mutex
	metrics     MetricsSnapshot
}

// NewService builds the service over the shared database.
func NewService(db *storage.DB, raw *storage.RawFileStore, global config.Config, jobMgr *jobs.Manager) *Service {
	var registryOptions []parser.Option
	if strings.TrimSpace(global.Helpers.LegacyOffice) != "" {
		registryOptions = append(registryOptions, parser.WithLegacyHelper(parser.ExecHelper{
			Template:  global.Helpers.LegacyOffice,
			TimeoutMS: 120_000,
		}))
	}
	ingestWorkers := global.Jobs.ImportWorkers
	if ingestWorkers < 1 {
		ingestWorkers = 1
	}
	// Two concurrent ingest pipelines retain useful provider parallelism while
	// preventing several chunk/vector rewrites from saturating the disk.
	if ingestWorkers > 2 {
		ingestWorkers = 2
	}
	service := &Service{
		store:       newStore(db),
		raw:         raw,
		parsers:     parser.NewRegistry(registryOptions...),
		global:      global,
		jobMgr:      jobMgr,
		ingestSlots: make(chan struct{}, ingestWorkers),
	}
	if strings.TrimSpace(global.Helpers.ContentConverter) != "" {
		service.content = parser.ExecHelper{
			Template:  global.Helpers.ContentConverter,
			TimeoutMS: 120_000,
		}
	}
	if strings.TrimSpace(global.Helpers.ImageDecoder) != "" {
		service.imageDecoder = parser.ExecHelper{
			Template:  global.Helpers.ImageDecoder,
			TimeoutMS: global.Helpers.ImageDecoderTimeoutMS,
		}
	}
	service.setOCRRenderer(parser.ExecHelper{
		Template:  global.OCR.RenderHelper,
		TimeoutMS: global.OCR.RenderTimeoutMS,
	})
	service.selectOCR()
	service.applyConfiguredProviders()
	return service
}

func (s *Service) setOCRRenderer(helper parser.ExecHelper) {
	if strings.TrimSpace(helper.Template) != "" {
		s.ocrRenderer = helper
		return
	}
	s.ocrRenderer = nil
}

func (s *Service) ocrRendererAvailable() bool {
	return s.ocrRenderer != nil && s.ocrRenderer.Available()
}

// SetRuntime attaches the optional isolated ML process manager. It is called
// after construction and again whenever runtime commands change.
func (s *Service) SetRuntime(manager runtime.Caller) {
	s.runtime = manager
	s.applyConfiguredProviders()
	if strings.TrimSpace(s.global.OCR.RenderHelper) == "" {
		s.ocrRenderer = parser.NewRuntimeRenderer(manager)
	}
	s.selectOCR()
}

// SetOCRArtifactProvider wires model-cache state into the isolated OCR
// helper. Artifact readiness is distinct from helper process readiness.
func (s *Service) SetOCRArtifactProvider(provider func() (string, bool)) {
	s.ocrArtifacts = provider
	s.selectOCR()
}

// SetLegacyOfficeRunner installs the built-in LibreOffice adapter or removes
// the legacy parser when the runtime is unavailable. The parser registry is
// the single source of truth for both API capability reporting and ingestion.
func (s *Service) SetLegacyOfficeRunner(runner parser.HelperRunner) {
	if runner == nil {
		runner = parser.NewRuntimeOfficeHelper(s.runtime)
		if runner == nil {
			runner = parser.NewLibreOfficeHelper()
		}
	}
	s.parsers.SetLegacyHelper(runner)
}

func (s *Service) selectOCR() {
	s.ocr = nil
	primary := s.primaryOCR()
	if primary == nil {
		if strings.TrimSpace(s.global.OCR.FallbackHelper) != "" {
			s.ocr = parser.ExecHelper{Template: s.global.OCR.FallbackHelper, TimeoutMS: s.global.OCR.TimeoutMS}
		}
		return
	}
	if strings.TrimSpace(s.global.OCR.FallbackHelper) != "" {
		s.ocr = parser.FallbackHelper{
			Primary: primary,
			Fallback: parser.ExecHelper{
				Template:  s.global.OCR.FallbackHelper,
				TimeoutMS: s.global.OCR.TimeoutMS,
			},
		}
		return
	}
	s.ocr = primary
}

func (s *Service) primaryOCR() parser.HelperRunner {
	if helper := parser.NewRuntimeHelperWithArtifacts(s.runtime, s.ocrArtifacts); helper != nil && helper.Available() {
		return helper
	}
	if strings.TrimSpace(s.global.OCR.Helper) != "" {
		return parser.ExecHelper{Template: s.global.OCR.Helper, TimeoutMS: s.global.OCR.TimeoutMS}
	}
	return nil
}

// applyConfiguredProviders builds providers from config (tests override via
// SetProviders).
func (s *Service) applyConfiguredProviders() {
	providers := s.providersForConfig(BaseConfig{
		EmbeddingProvider: s.global.Embedding.Provider,
		EmbeddingBaseURL:  s.global.Embedding.BaseURL,
		EmbeddingModel:    s.global.Embedding.Model,
		EmbeddingAPIKey:   s.global.Embedding.APIKey,
		RerankEnabled:     boolOverride(s.global.Rerank.Enabled),
		RerankModel:       s.global.Rerank.Model,
		RerankBaseURL:     s.global.Rerank.BaseURL,
		RerankAPIKey:      s.global.Rerank.APIKey,
	})
	s.embedder = providers.embedder
	s.reranker = providers.reranker
}

type providerSet struct {
	embedder        embedding.Provider
	reranker        rerank.Provider
	embeddingActive bool
	rerankerActive  bool
}

func (s *Service) providersForConfig(cfg BaseConfig) providerSet {
	provider := strings.TrimSpace(cfg.EmbeddingProvider)
	var embedder embedding.Provider
	embeddingActive := provider != "" && provider != "none"
	if provider == "local" {
		if s.runtime != nil && s.runtime.Configured(runtime.CapabilityEmbedding) {
			embedder = embedding.NewLocal(embedding.LocalConfig{Runtime: s.runtime, Model: strings.TrimPrefix(strings.TrimSpace(cfg.EmbeddingModel), "local:")})
		} else {
			embeddingActive = false
			embedder = embedding.New(embedding.Config{Provider: "none"})
		}
	} else {
		embedder = embedding.New(embedding.Config{
			Provider: provider,
			BaseURL:  cfg.EmbeddingBaseURL,
			Model:    cfg.EmbeddingModel,
			APIKey:   cfg.EmbeddingAPIKey,
		})
	}

	rerankActive := cfg.RerankEnabled != nil && *cfg.RerankEnabled && strings.TrimSpace(cfg.RerankModel) != ""
	var reranker rerank.Provider
	if rerankActive {
		model := strings.TrimPrefix(strings.TrimSpace(cfg.RerankModel), "local:")
		if strings.TrimSpace(cfg.RerankBaseURL) == "" && s.runtime != nil && s.runtime.Configured(runtime.CapabilityRerank) {
			reranker = rerank.NewLocal(s.runtime, model)
		} else if strings.TrimSpace(cfg.RerankBaseURL) != "" {
			reranker = rerank.New(rerank.Config{
				BaseURL: cfg.RerankBaseURL, Model: cfg.RerankModel, APIKey: cfg.RerankAPIKey,
				Timeout: time.Duration(s.global.Rerank.TimeoutMS) * time.Millisecond,
			})
		} else {
			rerankActive = false
		}
	}
	return providerSet{embedder: embedder, reranker: reranker, embeddingActive: embeddingActive, rerankerActive: rerankActive}
}

func (s *Service) providersForBase(baseID string) (providerSet, error) {
	base, err := s.store.getBase(baseID)
	if err != nil {
		return providerSet{}, err
	}
	cfg := ResolveBaseConfig(s.global, base.Config)
	// Start with the injected/global providers, then replace only the
	// dimensions explicitly overridden by this base. Besides avoiding needless
	// provider construction, this preserves injected providers for callers that
	// configure only reranking (and vice versa).
	providers := providerSet{
		embedder:        s.embedder,
		reranker:        s.reranker,
		embeddingActive: s.global.Embedding.Provider != "none" && s.embedder != nil && s.embedder.ModelKey() != "none",
		rerankerActive:  s.reranker != nil,
	}
	configured := s.providersForConfig(cfg)
	if hasEmbeddingOverride(base.Config) {
		providers.embedder = configured.embedder
		providers.embeddingActive = configured.embeddingActive
	}
	if hasRerankOverride(base.Config) {
		providers.reranker = configured.reranker
		providers.rerankerActive = configured.rerankerActive
	}
	return providers, nil
}

func (s *Service) searchProviders(baseIDs []string) ([]string, map[string]providerSet, bool, error) {
	ids := baseIDs
	if ids == nil {
		bases, err := s.store.listBases()
		if err != nil {
			return nil, nil, false, err
		}
		ids = make([]string, 0, len(bases))
		for _, base := range bases {
			ids = append(ids, base.ID)
		}
	}
	providers := make(map[string]providerSet, len(ids))
	active := false
	for _, id := range ids {
		set, err := s.providersForBase(id)
		if err != nil {
			return nil, nil, false, err
		}
		providers[id] = set
		active = active || set.embeddingActive
	}
	return ids, providers, active, nil
}

func hasEmbeddingOverride(cfg BaseConfig) bool {
	return strings.TrimSpace(cfg.EmbeddingProvider) != "" || strings.TrimSpace(cfg.EmbeddingBaseURL) != "" ||
		strings.TrimSpace(cfg.EmbeddingModel) != "" || cfg.EmbeddingAPIKey != ""
}

func hasRerankOverride(cfg BaseConfig) bool {
	return cfg.RerankEnabled != nil || strings.TrimSpace(cfg.RerankModel) != "" || strings.TrimSpace(cfg.RerankBaseURL) != "" || cfg.RerankAPIKey != ""
}

func boolOverride(value bool) *bool { return &value }

// SetProviders overrides the model providers (tests, future local helpers).
func (s *Service) SetProviders(embedder embedding.Provider, reranker rerank.Provider) {
	if embedder != nil {
		s.embedder = embedder
	}
	if reranker != nil {
		s.reranker = reranker
	}
}

// SetGlobalConfig applies persisted runtime config and rebuilds providers.
// Existing vectors are intentionally retained; Stats reports model drift.
func (s *Service) SetGlobalConfig(cfg config.Config) {
	s.global = cfg.Normalized()
	s.applyConfiguredProviders()
	if strings.TrimSpace(s.global.Helpers.LegacyOffice) != "" {
		s.parsers.SetLegacyHelper(parser.ExecHelper{
			Template:  s.global.Helpers.LegacyOffice,
			TimeoutMS: 120_000,
		})
	} else {
		s.SetLegacyOfficeRunner(nil)
	}
	if strings.TrimSpace(s.global.Helpers.ContentConverter) != "" {
		s.content = parser.ExecHelper{
			Template:  s.global.Helpers.ContentConverter,
			TimeoutMS: 120_000,
		}
	} else {
		s.content = nil
	}
	if strings.TrimSpace(s.global.Helpers.ImageDecoder) != "" {
		s.imageDecoder = parser.ExecHelper{
			Template:  s.global.Helpers.ImageDecoder,
			TimeoutMS: s.global.Helpers.ImageDecoderTimeoutMS,
		}
	} else {
		s.imageDecoder = parser.ExecHelper{}
	}
	s.setOCRRenderer(parser.ExecHelper{
		Template:  s.global.OCR.RenderHelper,
		TimeoutMS: s.global.OCR.RenderTimeoutMS,
	})
	if strings.TrimSpace(s.global.OCR.RenderHelper) == "" {
		s.ocrRenderer = parser.NewRuntimeRenderer(s.runtime)
	}
	s.selectOCR()
}

func (s *Service) pdfImageOptions(ctx context.Context) []parser.PDFImageOption {
	if strings.TrimSpace(s.global.Helpers.ImageDecoder) == "" || !s.imageDecoder.Available() {
		return nil
	}
	return []parser.PDFImageOption{
		parser.WithPDFImageContext(ctx),
		parser.WithPDFImageDecoder(s.imageDecoder.Decode),
	}
}

// ProbeEmbeddingDimensions verifies a provider/model before configuration is
// saved. It intentionally does not mutate the active provider or vector space.
func (s *Service) ProbeEmbeddingDimensions(ctx context.Context, provider, baseURL, model, apiKey string) (int, error) {
	if strings.TrimSpace(provider) == "" {
		provider = s.global.Embedding.Provider
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = s.global.Embedding.BaseURL
	}
	if strings.TrimSpace(model) == "" {
		model = s.global.Embedding.Model
	}
	if strings.TrimSpace(apiKey) == "" {
		apiKey = s.global.Embedding.APIKey
	}
	if provider == "none" {
		return 0, fmt.Errorf("no embedding provider configured")
	}
	if provider == "local" {
		probe := embedding.NewLocal(embedding.LocalConfig{Runtime: s.runtime, Model: model})
		vectors, err := probe.Embed(ctx, []string{"dimension probe"})
		if err != nil {
			return 0, err
		}
		if len(vectors) != 1 || len(vectors[0]) == 0 {
			return 0, fmt.Errorf("local embedding returned an empty vector")
		}
		return len(vectors[0]), nil
	}
	probe := embedding.New(embedding.Config{
		Provider: provider, BaseURL: baseURL, Model: model, APIKey: apiKey,
		Timeout: 30 * time.Second,
	})
	vectors, err := probe.Embed(ctx, []string{"dimension probe"})
	if err != nil {
		return 0, err
	}
	if len(vectors) != 1 || len(vectors[0]) == 0 {
		return 0, fmt.Errorf("embedding returned an empty vector")
	}
	return len(vectors[0]), nil
}

// ProbeRerank verifies that a reranker accepts a small representative request
// and returns one score per candidate. It never changes the active provider or
// persists any self-test state.
func (s *Service) ProbeRerank(ctx context.Context, baseURL, model, apiKey string) ([]float64, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return nil, fmt.Errorf("rerank model is empty")
	}
	var provider rerank.Provider
	if strings.TrimSpace(baseURL) == "" && s.runtime != nil && s.runtime.Configured(runtime.CapabilityRerank) {
		provider = rerank.NewLocal(s.runtime, strings.TrimPrefix(model, "local:"))
	} else if strings.TrimSpace(baseURL) != "" {
		provider = rerank.New(rerank.Config{
			BaseURL: baseURL, Model: model, APIKey: apiKey,
			Timeout: 30 * time.Second,
		})
	} else {
		return nil, fmt.Errorf("rerank runtime or base URL is not configured")
	}
	scores, err := provider.Rerank(ctx, "knowledge configuration probe", []string{
		"This candidate discusses knowledge retrieval configuration.",
		"This candidate discusses an unrelated topic.",
	})
	if err != nil {
		return nil, err
	}
	if len(scores) != 2 {
		return nil, fmt.Errorf("rerank returned %d scores for 2 candidates", len(scores))
	}
	return scores, nil
}

// ProbeCaption sends a tiny valid PNG to the configured vision endpoint. The
// probe is deliberately independent from document ingestion and does not save
// the returned caption.
func (s *Service) ProbeCaption(ctx context.Context, provider, baseURL, model, apiKey string) (string, error) {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = s.global.Captioning.Provider
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = s.global.Captioning.BaseURL
	}
	if strings.TrimSpace(model) == "" {
		model = s.global.Captioning.Model
	}
	if strings.TrimSpace(apiKey) == "" {
		apiKey = s.global.Captioning.APIKey
	}
	if provider == "off" {
		return "", fmt.Errorf("caption provider is disabled")
	}
	// Use a small but meaningful chart instead of a blank 1x1 pixel. A blank
	// probe makes a healthy vision endpoint look broken because the model can
	// only truthfully answer that there is nothing to describe.
	png := sampleCaptionPNG()
	return caption.CaptionImage(ctx, png, caption.Config{
		Provider: provider, Model: model, BaseURL: baseURL, APIKey: apiKey,
		EmbeddingBaseURL: s.global.Embedding.BaseURL,
	}, caption.Options{})
}

// sampleCaptionPNG creates a deterministic chart-like image for the caption
// probe. It must contain visible content so a successful vision call does not
// look like a failure merely because the probe image is blank.
func sampleCaptionPNG() []byte {
	const width, height = 512, 320
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.White)
		}
	}
	setRect := func(x0, y0, x1, y1 int, c color.Color) {
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				img.Set(x, y, c)
			}
		}
	}
	grid := color.RGBA{R: 220, G: 225, B: 232, A: 255}
	axis := color.RGBA{R: 70, G: 80, B: 95, A: 255}
	for _, y := range []int{60, 110, 160, 210, 260} {
		setRect(55, y, 470, y+2, grid)
	}
	setRect(55, 55, 58, 270, axis)
	setRect(55, 267, 470, 270, axis)
	for _, bar := range []struct {
		x0, x1, y int
		c         color.RGBA
	}{
		{90, 145, 185, color.RGBA{R: 65, G: 132, B: 244, A: 255}},
		{175, 230, 145, color.RGBA{R: 52, G: 168, B: 83, A: 255}},
		{260, 315, 105, color.RGBA{R: 245, G: 166, B: 35, A: 255}},
		{345, 400, 75, color.RGBA{R: 217, G: 72, B: 72, A: 255}},
	} {
		setRect(bar.x0, bar.y, bar.x1, 267, bar.c)
	}
	trend := color.RGBA{R: 31, G: 41, B: 55, A: 255}
	points := [][2]int{{90, 220}, {145, 205}, {175, 180}, {230, 155}, {260, 135}, {315, 115}, {345, 95}, {400, 70}}
	for _, point := range points {
		setRect(point[0]-3, point[1]-3, point[0]+4, point[1]+4, trend)
	}
	for i := 0; i < len(points)-1; i++ {
		a, b := points[i], points[i+1]
		for x := a[0]; x <= b[0]; x++ {
			y := a[1] + (b[1]-a[1])*(x-a[0])/(b[0]-a[0])
			setRect(x, y-1, x+2, y+2, trend)
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		return nil
	}
	return out.Bytes()
}

// GlobalConfig returns the effective runtime configuration.
func (s *Service) GlobalConfig() config.Config { return s.global }

// EmbeddingModelKey reports the active vector-space identity ("" when none).
func (s *Service) EmbeddingModelKey() string {
	if s.global.Embedding.Provider == "none" {
		return ""
	}
	return s.embedder.ModelKey()
}

// baseConfigOrEmpty returns one base's overrides (empty on any error).
func (s *Service) baseConfigOrEmpty(baseID string) BaseConfig {
	base, err := s.store.getBase(baseID)
	if err != nil {
		return BaseConfig{}
	}
	return base.Config
}

func now() int64 { return Now().UnixMilli() }

func newID() (string, error) { return jobs.NewID() }

// ResolveBaseConfig layers a base's fields over Knowledge's global defaults.
// Scalar zero values are inherited by long-standing API convention; pointers
// preserve explicit false/zero overrides. The returned copy is only for
// runtime resolution and is never used as stored API state.
func ResolveBaseConfig(global config.Config, cfg BaseConfig) BaseConfig {
	resolved := cfg
	if strings.TrimSpace(resolved.EmbeddingProvider) == "" {
		resolved.EmbeddingProvider = global.Embedding.Provider
	}
	if strings.TrimSpace(resolved.EmbeddingBaseURL) == "" && resolved.EmbeddingProvider != "local" {
		resolved.EmbeddingBaseURL = global.Embedding.BaseURL
	}
	if strings.TrimSpace(resolved.EmbeddingModel) == "" {
		resolved.EmbeddingModel = global.Embedding.Model
	}
	if resolved.EmbeddingAPIKey == "" {
		resolved.EmbeddingAPIKey = global.Embedding.APIKey
	}
	if resolved.RerankEnabled == nil {
		enabled := global.Rerank.Enabled
		// Older model-page saves had no enabled field. A stored model selection
		// is an explicit opt-in and must remain active after this field exists.
		if strings.TrimSpace(resolved.RerankModel) != "" || strings.TrimSpace(resolved.RerankBaseURL) != "" {
			enabled = true
		}
		resolved.RerankEnabled = &enabled
	}
	if strings.TrimSpace(resolved.RerankModel) == "" {
		resolved.RerankModel = global.Rerank.Model
	}
	if strings.TrimSpace(resolved.RerankBaseURL) == "" && strings.TrimSpace(cfg.RerankModel) == "" {
		resolved.RerankBaseURL = global.Rerank.BaseURL
	}
	if resolved.RerankAPIKey == "" {
		resolved.RerankAPIKey = global.Rerank.APIKey
	}
	if strings.TrimSpace(resolved.Processor) == "" {
		resolved.Processor = global.Processing.Provider
	}
	if resolved.MineruAPIKey == "" {
		resolved.MineruAPIKey = global.Processing.APIKey
	}
	if resolved.MineruAPIHost == "" {
		resolved.MineruAPIHost = global.Processing.APIHost
	}
	if resolved.ConflictStrategy == "" {
		resolved.ConflictStrategy = global.Workflow.ConflictStrategy
	}
	if resolved.URLRefreshHours <= 0 {
		resolved.URLRefreshHours = global.Workflow.URLRefreshHours
	}
	if resolved.AutoRetrieve == nil {
		enabled := global.AutoRetrieve.Enabled
		resolved.AutoRetrieve = &enabled
	}
	if resolved.AutoRetrieveMax == nil {
		weight := global.AutoRetrieve.Weight
		resolved.AutoRetrieveMax = &weight
	}
	if *resolved.AutoRetrieveMax < 0 || *resolved.AutoRetrieveMax > 5 {
		weight := min(max(*resolved.AutoRetrieveMax, 0), 5)
		resolved.AutoRetrieveMax = &weight
	}
	return resolved
}

// CreateBase creates a knowledge base.
func (s *Service) CreateBase(name, description, group string, cfg BaseConfig) (Base, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Base{}, fmt.Errorf("base name is required")
	}
	id, err := newID()
	if err != nil {
		return Base{}, err
	}
	base := Base{ID: id, Name: name, Description: description, Group: group, Config: cfg, CreatedAt: now(), UpdatedAt: now()}
	if err := s.store.putBase(base); err != nil {
		return Base{}, err
	}
	return base, nil
}

// RestoreBase rebuilds source documents in a new base, optionally switching
// the embedding/provider configuration before the rebuild.
func (s *Service) RestoreBase(ctx context.Context, sourceBaseID, name string, cfg *BaseConfig) (Base, error) {
	source, err := s.store.getBase(sourceBaseID)
	if err != nil {
		return Base{}, err
	}
	targetConfig := source.Config
	if cfg != nil {
		targetConfig = *cfg
	}
	if strings.TrimSpace(name) == "" {
		name = source.Name + " (restored)"
	}
	base, err := s.CreateBase(name, source.Description, source.Group, targetConfig)
	if err != nil {
		return Base{}, err
	}

	docs, err := s.store.listDocuments(sourceBaseID)
	if err != nil {
		return base, err
	}
	for _, sourceDoc := range docs {
		if sourceDoc.SourceType == "directory" {
			continue
		}
		if err := s.restoreDocument(ctx, base, sourceDoc); err != nil {
			return base, err
		}
	}
	return base, nil
}

func (s *Service) restoreDocument(ctx context.Context, base Base, source Document) error {
	text := source.RawText
	var fileBytes []byte
	var err error
	if source.SourceType == "file" {
		if source.RawFilePath != "" {
			fileBytes, err = s.raw.Read(source.RawFilePath)
			if err != nil {
				return err
			}
		}
		if len(fileBytes) == 0 {
			return fmt.Errorf("raw source for %s is missing", source.ID)
		}
	} else if text == "" {
		chunks, chunkErr := s.store.listChunksByDoc(source.ID, 0, 0)
		if chunkErr != nil {
			return chunkErr
		}
		pieces := make([]string, 0, len(chunks))
		for _, c := range chunks {
			pieces = append(pieces, c.Text)
		}
		text = strings.Join(pieces, "\n\n")
	}
	if strings.TrimSpace(text) == "" && len(fileBytes) == 0 {
		return nil
	}

	id, err := newID()
	if err != nil {
		return err
	}
	doc := source
	doc.ID = id
	doc.BaseID = base.ID
	doc.RawFilePath = ""
	doc.ContentHash = ""
	doc.TitleLocked = true
	doc.CreatedAt = now()
	doc.UpdatedAt = 0
	doc.Status = StatusPending
	doc.Phase = ""
	doc.Progress = 0
	doc.Incomplete = false
	doc.ErrorCode = ""
	doc.ErrorMessage = ""
	return s.ingest(ctx, &doc, base.Config, fileBytes)
}

// RenameBase updates name/description/group/config.
func (s *Service) RenameBase(id string, name, description, group *string, cfg *BaseConfig) (Base, error) {
	base, err := s.store.getBase(id)
	if err != nil {
		return Base{}, err
	}
	if name != nil && strings.TrimSpace(*name) != "" {
		base.Name = strings.TrimSpace(*name)
	}
	if description != nil {
		base.Description = *description
	}
	if group != nil {
		base.Group = *group
	}
	if cfg != nil {
		base.Config = *cfg
	}
	base.UpdatedAt = now()
	if err := s.store.putBase(base); err != nil {
		return Base{}, err
	}
	return base, nil
}

// DeleteBase removes the base with its documents, chunks, and raw files.
func (s *Service) DeleteBase(id string) error {
	if _, err := s.store.getBase(id); err != nil {
		return err
	}
	docs, err := s.store.listDocumentMetadata(id)
	if err != nil {
		return err
	}
	for _, doc := range docs {
		if err := s.store.deleteChunks(doc.ID); err != nil {
			return err
		}
		if err := s.store.deleteDocument(doc.ID); err != nil {
			return err
		}
	}
	if err := s.store.deleteChunksByBase(id); err != nil {
		return err
	}
	if err := s.raw.DeleteBase(id); err != nil {
		return err
	}
	return s.store.deleteBase(id)
}

// GetBase returns one base.
func (s *Service) GetBase(id string) (Base, error) { return s.store.getBase(id) }

// ListBases returns summaries with document/chunk counts.
func (s *Service) ListBases() ([]BaseSummary, error) {
	bases, err := s.store.listBases()
	if err != nil {
		return nil, err
	}
	out := make([]BaseSummary, 0, len(bases))
	for _, base := range bases {
		stats, err := s.store.statsFor(base.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, BaseSummary{Base: base, DocumentCount: stats.DocumentCount, ChunkCount: stats.ChunkCount, CharCount: stats.CharCount, TokenCount: stats.TokenCount})
	}
	return out, nil
}

// Stats returns aggregate stats for a base or all bases.
func (s *Service) Stats(baseID string) (Stats, error) {
	stats, err := s.store.statsFor(baseID)
	if err != nil {
		return Stats{}, err
	}
	// The global overview only needs document/chunk aggregates. Do not scan
	// every vector blob just to render the home page: large local indexes can
	// contain millions of chunks, and the vector diagnostics below are meant
	// for a specific base where they can be inspected on demand.
	if baseID == "" {
		return stats, nil
	}
	modelKey := s.EmbeddingModelKey()
	if providers, providerErr := s.providersForBase(baseID); providerErr == nil && providers.embeddingActive && providers.embedder != nil {
		modelKey = providers.embedder.ModelKey()
	} else {
		modelKey = ""
	}
	if modelKey == "" {
		return stats, nil
	}
	counts, err := s.store.VectorModelCounts(baseID)
	if err != nil {
		return Stats{}, err
	}
	embedded := 0
	stale := 0
	for model, count := range counts {
		embedded += count
		if model != modelKey {
			stale += count
		}
	}
	stats.Embedded = embedded > 0
	stats.StaleChunks = stale
	if dimensions, err := s.store.VectorDimensions(baseID); err == nil {
		stats.Dimensions = dimensions
	}
	return stats, nil
}

// ListGroups returns the persisted plus implicit group names.
func (s *Service) ListGroups() ([]string, error) {
	var out []string
	if err := s.kvGet("groups", &out); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	unique := make([]string, 0, len(out))
	for _, g := range out {
		if g != "" && !seen[g] {
			seen[g] = true
			unique = append(unique, g)
		}
	}
	bases, err := s.store.listBases()
	if err != nil {
		return nil, err
	}
	for _, b := range bases {
		if b.Group != "" && !seen[b.Group] {
			seen[b.Group] = true
			unique = append(unique, b.Group)
		}
	}
	return unique, nil
}

// CreateGroup registers an empty group.
func (s *Service) CreateGroup(name string) ([]string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("group name is required")
	}
	groups, err := s.ListGroups()
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		if g == name {
			return groups, nil
		}
	}
	next := append(groups, name)
	return next, s.kvSet("groups", next)
}

// RenameGroup renames a group and every base inside it.
func (s *Service) RenameGroup(from, to string) ([]string, error) {
	groups, err := s.ListGroups()
	if err != nil {
		return nil, err
	}
	next := make([]string, 0, len(groups))
	for _, g := range groups {
		if g == from {
			g = to
		}
		next = append(next, g)
	}
	bases, err := s.store.listBases()
	if err != nil {
		return nil, err
	}
	for _, b := range bases {
		if b.Group == from {
			b.Group = to
			b.UpdatedAt = now()
			if err := s.store.putBase(b); err != nil {
				return nil, err
			}
		}
	}
	return next, s.kvSet("groups", next)
}

// DeleteGroup removes a group; member bases become ungrouped.
func (s *Service) DeleteGroup(name string) error {
	groups, err := s.ListGroups()
	if err != nil {
		return err
	}
	next := make([]string, 0, len(groups))
	for _, g := range groups {
		if g != name {
			next = append(next, g)
		}
	}
	bases, err := s.store.listBases()
	if err != nil {
		return err
	}
	for _, b := range bases {
		if b.Group == name {
			b.Group = ""
			b.UpdatedAt = now()
			if err := s.store.putBase(b); err != nil {
				return err
			}
		}
	}
	return s.kvSet("groups", next)
}

// EnabledScope returns the invocation switch and the pinned base ids.
func (s *Service) EnabledScope() (bool, []string, error) {
	state, err := s.EnabledScopeState()
	if err != nil {
		return false, nil, err
	}
	return state.Enabled, state.BaseIDs, nil
}

// EnabledScopeState distinguishes the default all-bases state from an
// explicitly saved empty pinned scope, which must match zero bases.
func (s *Service) EnabledScopeState() (EnabledScopeState, error) {
	var scope struct {
		Enabled        bool     `json:"enabled"`
		EnabledBaseIDs []string `json:"enabledBaseIds"`
	}
	if !s.kvHas("scope") {
		return EnabledScopeState{Enabled: true}, nil
	}
	if err := s.kvGet("scope", &scope); err != nil {
		return EnabledScopeState{}, err
	}
	if scope.EnabledBaseIDs == nil {
		scope.EnabledBaseIDs = []string{}
	}
	return EnabledScopeState{Enabled: scope.Enabled, BaseIDs: scope.EnabledBaseIDs, Explicit: true}, nil
}

// SetEnabledScope updates the invocation switch and/or pinned base ids.
func (s *Service) SetEnabledScope(enabled *bool, baseIDs *[]string) error {
	var scope struct {
		Enabled        bool     `json:"enabled"`
		EnabledBaseIDs []string `json:"enabledBaseIds"`
	}
	scope.Enabled = true
	if err := s.kvGet("scope", &scope); err != nil {
		return err
	}
	if enabled != nil {
		scope.Enabled = *enabled
	}
	if baseIDs != nil {
		scope.EnabledBaseIDs = *baseIDs
	}
	if scope.EnabledBaseIDs == nil {
		scope.EnabledBaseIDs = []string{}
	}
	return s.kvSet("scope", scope)
}

// ── key-value storage ────────────────────────────────────────────────────────

func (s *Service) kvGet(key string, out any) error {
	var value string
	err := s.store.db.QueryRow(`SELECT value FROM kv WHERE key = ?`, key).Scan(&value)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return err
	}
	if value == "" {
		return nil
	}
	return json.Unmarshal([]byte(value), out)
}

func (s *Service) kvSet(key string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.store.db.Exec(
		`INSERT INTO kv (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, string(data),
	)
	return err
}

// kvHas reports whether a key exists.
func (s *Service) kvHas(key string) bool {
	var value string
	err := s.store.db.QueryRow(`SELECT value FROM kv WHERE key = ?`, key).Scan(&value)
	return err == nil
}

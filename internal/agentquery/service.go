// Package agentquery is the protocol-independent query application layer for
// Agent-facing callers. REST, Extension v1, and MCP adapters should map their
// transport DTOs to this package rather than inventing separate query
// semantics. It composes existing Knowledge Core capabilities and never
// bypasses Evidence as the authority for factual claims.
package agentquery

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
	"github.com/shutu-ai/shutu-knowledge/internal/schemamodel"
	"github.com/shutu-ai/shutu-knowledge/internal/semantic"
)

// Query modes in the shared application contract. Evidence remains the only
// authoritative factual lane; semantic output is a separately labeled lane.
type Mode string

const (
	ModeEvidence Mode = "evidence"
	ModeSemantic Mode = "semantic"
	ModeHybrid   Mode = "hybrid"
	ModeAuto     Mode = "auto"
)

// Snapshot selectors. Semantic supports immutable generation pinning now;
// evidence pinning will be introduced with the evidence snapshot contract.
type Generation string

const GenerationActive Generation = "active"

// DocumentVersionPolicy is the requested temporal scope. Current is the only
// policy supported by the evidence lane today; semantic supports as-of, range,
// and all.
type DocumentVersionPolicy string

const (
	VersionCurrent DocumentVersionPolicy = "current"
	VersionAsOf    DocumentVersionPolicy = "as_of"
	VersionRange   DocumentVersionPolicy = "range"
	VersionAll     DocumentVersionPolicy = "all"
)

// VersionPolicy carries explicit temporal bounds. Unknown or unsupported
// bounds are rejected rather than silently interpreted by query routing.
type VersionPolicy struct {
	Type        DocumentVersionPolicy `json:"type,omitempty"`
	AsOf        string                `json:"asOf,omitempty"`
	FromVersion string                `json:"fromVersion,omitempty"`
	ToVersion   string                `json:"toVersion,omitempty"`
}

// ErrorCode is the stable machine-readable application error taxonomy used by
// all transport adapters. Transport adapters may add protocol details, but must
// preserve these codes.
type ErrorCode string

const (
	ErrInvalidArgument    ErrorCode = "INVALID_ARGUMENT"
	ErrPermissionDenied   ErrorCode = "PERMISSION_DENIED"
	ErrBaseNotFound       ErrorCode = "BASE_NOT_FOUND"
	ErrSemanticNotReady   ErrorCode = "SEMANTIC_NOT_READY"
	ErrGenerationNotFound ErrorCode = "GENERATION_NOT_FOUND"
	ErrVersionUnsupported ErrorCode = "VERSION_UNSUPPORTED"
	ErrUnitNotFound       ErrorCode = "UNIT_NOT_FOUND"
)

// Error is a stable shared-contract error. BaseID is populated only when the
// error is scoped to one known request base.
type Error struct {
	Code    ErrorCode `json:"code"`
	BaseID  string    `json:"baseId,omitempty"`
	Message string    `json:"message"`
	Cause   error     `json:"-"`
}

func (e *Error) Error() string {
	message := e.Message
	if e.Cause != nil {
		message = fmt.Sprintf("%s: %v", message, e.Cause)
	}
	if e.BaseID == "" {
		return string(e.Code) + ": " + message
	}
	return string(e.Code) + " (" + e.BaseID + "): " + message
}

func (e *Error) Unwrap() error {
	return e.Cause
}

func invalidArgument(format string, args ...any) error {
	return &Error{Code: ErrInvalidArgument, Message: fmt.Sprintf(format, args...)}
}

func scopedError(code ErrorCode, baseID, message string, cause error) error {
	return &Error{Code: code, BaseID: baseID, Message: message, Cause: cause}
}

// Diagnostic is a bounded, stable application-layer diagnostic. It is not a
// substitute for a transport error when the requested mode cannot run.
type Diagnostic struct {
	Code     string `json:"code"`
	BaseID   string `json:"baseId,omitempty"`
	Message  string `json:"message"`
	Severity string `json:"severity,omitempty"`
}

// Request is the shared query shape. Explicit BaseIDs authorize/limit the
// call; when omitted, the existing enabled Knowledge scope decides the scope.
type Request struct {
	Query             string                  `json:"query"`
	ExtraQueries      []string                `json:"extraQueries,omitempty"`
	BaseIDs           []string                `json:"baseIds,omitempty"`
	Generation        Generation              `json:"generation,omitempty"`
	VersionPolicy     VersionPolicy           `json:"versionPolicy,omitempty"`
	Mode              Mode                    `json:"mode,omitempty"`
	EvidenceMode      string                  `json:"evidenceMode,omitempty"`
	SemanticKinds     []semantic.UnitKind     `json:"semanticKinds,omitempty"`
	TopK              int                     `json:"topK,omitempty"`
	Filter            *knowledge.SearchFilter `json:"filter,omitempty"`
	Debug             bool                    `json:"debug,omitempty"`
	IncludeStructured bool                    `json:"includeStructured,omitempty"`
}

// Result is the shared response envelope. Evidence is authoritative; semantic
// and structured lanes are independent, explicitly labeled result types.
type Result struct {
	Query             string                  `json:"query"`
	Mode              Mode                    `json:"mode"`
	Requested         Mode                    `json:"requestedMode,omitempty"`
	Scope             []string                `json:"scope"`
	Plan              *semantic.QueryPlan     `json:"plan,omitempty"`
	Evidence          *knowledge.SearchResult `json:"evidence,omitempty"`
	Semantic          []BaseSemanticResult    `json:"semantic,omitempty"`
	Structured        []BaseStructuredResult  `json:"structured,omitempty"`
	Snapshots         []Snapshot              `json:"snapshots,omitempty"`
	EvidenceSnapshots []BaseEvidenceSnapshot  `json:"evidenceSnapshots,omitempty"`
	Diagnostics       []Diagnostic            `json:"diagnostics,omitempty"`
	Degraded          bool                    `json:"degraded,omitempty"`
}

// BaseSemanticResult scopes semantic activation to one base and snapshot.
type BaseSemanticResult struct {
	BaseID     string                  `json:"baseId"`
	Generation int64                   `json:"generation"`
	Response   semantic.SearchResponse `json:"response"`
}

// BaseStructuredResult scopes structured schema matches to one base.
type BaseStructuredResult struct {
	BaseID   string                           `json:"baseId"`
	Response schemamodel.SchemaSearchResponse `json:"response"`
}

// Snapshot records the immutable semantic generation used by this result.
type Snapshot struct {
	BaseID             string `json:"baseId"`
	SemanticGeneration int64  `json:"semanticGeneration,omitempty"`
	SemanticSource     string `json:"semanticSource,omitempty"`
}

// BaseEvidenceSnapshot exposes the per-hit evidence generation identities in
// one base. SQLite currently has per-document active generations, not a single
// base-wide index generation; reporting these exact identities avoids making
// up an aggregate generation that cannot be audited.
type BaseEvidenceSnapshot struct {
	BaseID          string `json:"baseId"`
	DocumentID      string `json:"documentId"`
	IndexGeneration int64  `json:"indexGeneration"`
	SourceVersion   int64  `json:"sourceVersion"`
}

// ContextRequest is the shared bounded context shape. A multi-base request
// produces one independently scoped package per base; packages are never
// merged into a fake cross-base generation.
type ContextRequest struct {
	Query         string        `json:"query"`
	BaseIDs       []string      `json:"baseIds,omitempty"`
	Generation    Generation    `json:"generation,omitempty"`
	VersionPolicy VersionPolicy `json:"versionPolicy,omitempty"`
	TokenBudget   int           `json:"tokenBudget,omitempty"`
}

// ContextResult contains one package per authorized base.
type ContextResult struct {
	Query       string               `json:"query"`
	Scope       []string             `json:"scope"`
	Packages    []BaseContextPackage `json:"packages"`
	Diagnostics []Diagnostic         `json:"diagnostics,omitempty"`
	Degraded    bool                 `json:"degraded,omitempty"`
}

// BaseContextPackage pairs the existing semantic ContextPackage with its scope.
type BaseContextPackage struct {
	BaseID     string                  `json:"baseId"`
	Generation int64                   `json:"generation"`
	Package    semantic.ContextPackage `json:"package"`
}

// BaseCapability is the per-base discovery record used by tool adapters.
type BaseCapability struct {
	BaseID               string `json:"baseId"`
	Name                 string `json:"name"`
	DocumentCount        int    `json:"documentCount"`
	ChunkCount           int    `json:"chunkCount"`
	SemanticReady        bool   `json:"semanticReady"`
	SemanticGeneration   int64  `json:"semanticGeneration,omitempty"`
	StructuredReady      bool   `json:"structuredReady"`
	StructuredModelCount int    `json:"structuredModelCount,omitempty"`
}

// Capabilities is a bounded discovery response for adapters.
type Capabilities struct {
	Bases []BaseCapability `json:"bases"`
	Modes []Mode           `json:"modes"`
}

// Service composes Knowledge Core capabilities behind one shared contract.
type Service struct {
	knowledge *knowledge.Service
}

// New binds the query application to a Knowledge Core service.
func New(knowledgeService *knowledge.Service) *Service {
	if knowledgeService == nil {
		panic("agentquery: knowledge service is required")
	}
	return &Service{knowledge: knowledgeService}
}

// Search executes one shared-contract query. Semantic failures do not silently
// turn semantic mode into evidence mode; hybrid records a degraded diagnostic.
func (s *Service) Search(ctx context.Context, request Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	request.Mode = normalizeMode(request.Mode)
	request.Generation = normalizeGeneration(request.Generation)
	if strings.TrimSpace(request.Query) == "" {
		return Result{}, invalidArgument("query is required")
	}
	if request.Generation != GenerationActive && request.Generation != "" {
		if _, err := strconv.ParseInt(string(request.Generation), 10, 64); err != nil {
			return Result{}, &Error{Code: ErrInvalidArgument, Message: fmt.Sprintf("generation must be %q or a positive integer", GenerationActive), Cause: err}
		}
	}
	temporal, err := validateVersionPolicy(request.VersionPolicy)
	if err != nil {
		return Result{}, err
	}

	scope, err := s.resolveScope(ctx, request.BaseIDs)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		Query: request.Query, Mode: request.Mode, Requested: request.Mode, Scope: scope,
	}

	if request.Mode == ModeAuto {
		plan := s.knowledge.PlanKnowledgeQuery(request.Query)
		result.Plan = &plan
	}

	var evidence *knowledge.SearchResult
	if request.Mode != ModeSemantic {
		evidence, err = s.searchEvidence(ctx, request, scope)
		if err != nil {
			return Result{}, err
		}
		result.Evidence = evidence
		result.EvidenceSnapshots = evidenceSnapshots(evidence.Generations)
		if temporal.Intent != semantic.TemporalNone && temporal.Intent != semantic.TemporalCurrent {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Code:     "VERSION_POLICY_DEGRADED",
				Message:  "evidence retrieval currently resolves active documents only; semantic lane was filtered by version policy",
				Severity: "warning",
			})
			result.Degraded = true
		}
	}

	if request.Mode != ModeEvidence {
		semanticResults, snapshots, diagnostics := s.searchSemantic(ctx, request, temporal, scope)
		result.Semantic = semanticResults
		result.Snapshots = append(result.Snapshots, snapshots...)
		result.Diagnostics = append(result.Diagnostics, diagnostics...)
		result.Degraded = request.Mode == ModeHybrid && len(semanticResults) == 0
		if request.Mode == ModeSemantic && len(semanticResults) == 0 {
			return result, &Error{Code: ErrSemanticNotReady, Message: "no requested base has an active or pinned semantic generation"}
		}
	}

	if request.IncludeStructured {
		structured, diagnostics := s.searchStructured(ctx, request, scope)
		result.Structured = structured
		result.Diagnostics = append(result.Diagnostics, diagnostics...)
	}

	if request.Mode == ModeAuto {
		result.Mode = ModeHybrid
		if len(result.Semantic) == 0 {
			result.Mode = ModeEvidence
		}
	}
	return result, nil
}

// Context compiles one bounded package per requested base. Missing semantic
// state is reported, never replaced by an evidence-only package.
func (s *Service) Context(ctx context.Context, request ContextRequest) (ContextResult, error) {
	if err := ctx.Err(); err != nil {
		return ContextResult{}, err
	}
	request.Generation = normalizeGeneration(request.Generation)
	if strings.TrimSpace(request.Query) == "" {
		return ContextResult{}, invalidArgument("query is required")
	}
	temporal, err := validateVersionPolicy(request.VersionPolicy)
	if err != nil {
		return ContextResult{}, err
	}
	pinnedGeneration := int64(0)
	if request.Generation != GenerationActive {
		parsed, parseErr := strconv.ParseInt(string(request.Generation), 10, 64)
		if parseErr != nil || parsed <= 0 {
			return ContextResult{}, &Error{Code: ErrInvalidArgument, Message: fmt.Sprintf("generation must be %q or a positive integer", GenerationActive), Cause: parseErr}
		}
		pinnedGeneration = parsed
	}
	scope, err := s.resolveScope(ctx, request.BaseIDs)
	if err != nil {
		return ContextResult{}, err
	}
	out := ContextResult{Query: request.Query, Scope: scope}
	for _, baseID := range scope {
		var pkg semantic.ContextPackage
		var err error
		if pinnedGeneration > 0 {
			pkg, err = s.knowledge.CompileKnowledgeContextByIDWithPolicy(ctx, baseID, pinnedGeneration, request.Query, request.TokenBudget, &temporal)
		} else {
			pkg, err = s.knowledge.CompileKnowledgeContextWithPolicy(ctx, baseID, request.Query, request.TokenBudget, &temporal)
		}
		if errors.Is(err, knowledge.ErrNotFound) {
			out.Diagnostics = append(out.Diagnostics, Diagnostic{
				Code: "SEMANTIC_NOT_READY", BaseID: baseID,
				Message: "no active semantic compilation", Severity: "warning",
			})
			out.Degraded = true
			continue
		}
		if err != nil {
			return out, fmt.Errorf("compile context for base %s: %w", baseID, err)
		}
		out.Packages = append(out.Packages, BaseContextPackage{
			BaseID: baseID, Generation: pkg.Generation, Package: pkg,
		})
	}
	return out, nil
}

// ContextForBase preserves the existing single-base Extension contract while
// routing through the shared application layer. Missing semantic state remains
// a NotFound error instead of an evidence-only degraded package.
func (s *Service) ContextForBase(ctx context.Context, baseID, query string, tokenBudget int) (semantic.ContextPackage, error) {
	result, err := s.Context(ctx, ContextRequest{
		Query: query, BaseIDs: []string{baseID}, TokenBudget: tokenBudget,
	})
	if err != nil {
		return semantic.ContextPackage{}, err
	}
	if len(result.Packages) == 1 {
		return result.Packages[0].Package, nil
	}
	return semantic.ContextPackage{}, knowledge.ErrNotFound
}

// Capabilities returns discovery data and per-base semantic readiness. It is
// read-only and deliberately does not expose internal base IDs that are not in
// the enabled scope.
func (s *Service) Capabilities(ctx context.Context) (Capabilities, error) {
	state, err := s.knowledge.EnabledScopeStateContext(ctx)
	if err != nil {
		return Capabilities{}, err
	}
	if !state.Enabled {
		return Capabilities{Modes: modes()}, nil
	}
	bases, err := s.knowledge.ListBasesContext(ctx)
	if err != nil {
		return Capabilities{}, err
	}
	allowed := map[string]bool{}
	for _, id := range state.BaseIDs {
		allowed[id] = true
	}
	cap := Capabilities{Modes: modes()}
	for _, base := range bases {
		if state.Explicit && len(state.BaseIDs) > 0 && !allowed[base.ID] {
			continue
		}
		item := BaseCapability{
			BaseID: base.ID, Name: base.Name,
			DocumentCount: base.DocumentCount, ChunkCount: base.ChunkCount,
		}
		if count, err := s.knowledge.StructuredModelCount(ctx, base.ID); err == nil {
			item.StructuredModelCount = count
			item.StructuredReady = count > 0
		} else {
			return cap, fmt.Errorf("read structured capability for base %s: %w", base.ID, err)
		}
		if compilation, err := s.knowledge.GetSemanticCompilation(ctx, base.ID); err == nil {
			item.SemanticReady = true
			item.SemanticGeneration = compilation.Generation
		} else if !errors.Is(err, knowledge.ErrNotFound) {
			return cap, fmt.Errorf("read semantic capability for base %s: %w", base.ID, err)
		}
		cap.Bases = append(cap.Bases, item)
	}
	return cap, nil
}

func evidenceSnapshots(generations []knowledge.SearchGeneration) []BaseEvidenceSnapshot {
	seen := make(map[BaseEvidenceSnapshot]bool)
	out := make([]BaseEvidenceSnapshot, 0, len(generations))
	for _, generation := range generations {
		item := BaseEvidenceSnapshot{
			BaseID: generation.BaseID, DocumentID: generation.DocID,
			IndexGeneration: generation.IndexGeneration, SourceVersion: generation.SourceVersion,
		}
		if seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].BaseID != out[j].BaseID {
			return out[i].BaseID < out[j].BaseID
		}
		if out[i].DocumentID != out[j].DocumentID {
			return out[i].DocumentID < out[j].DocumentID
		}
		if out[i].IndexGeneration != out[j].IndexGeneration {
			return out[i].IndexGeneration < out[j].IndexGeneration
		}
		return out[i].SourceVersion < out[j].SourceVersion
	})
	return out
}

func (s *Service) searchEvidence(ctx context.Context, request Request, scope []string) (*knowledge.SearchResult, error) {
	req := knowledge.SearchRequest{
		Query: request.Query, Queries: request.ExtraQueries, BaseIDs: scope,
		TopK: request.TopK, Mode: evidenceKnowledgeMode(request.Mode, request.EvidenceMode), Debug: request.Debug,
	}
	// A one-base scope retains the existing BaseID behavior for compatibility.
	if len(scope) == 1 {
		req.BaseID = scope[0]
		req.BaseIDs = nil
	}
	result, err := s.knowledge.Search(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("evidence search: %w", err)
	}
	return &result, nil
}

func (s *Service) searchSemantic(ctx context.Context, request Request, temporal semantic.TemporalQuery, scope []string) ([]BaseSemanticResult, []Snapshot, []Diagnostic) {
	results := make([]BaseSemanticResult, 0, len(scope))
	snapshots := make([]Snapshot, 0, len(scope))
	diagnostics := make([]Diagnostic, 0)
	for _, baseID := range scope {
		compilation, source, err := s.semanticSnapshot(ctx, baseID, request.Generation)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{
				Code: "SEMANTIC_NOT_READY", BaseID: baseID,
				Message: err.Error(), Severity: "warning",
			})
			continue
		}
		response, err := semantic.SearchCompilation(compilation, semantic.SearchOptions{
			Query: request.Query, TopK: request.TopK, Kinds: request.SemanticKinds, Temporal: &temporal,
		})
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{
				Code: "SEMANTIC_SEARCH_FAILED", BaseID: baseID,
				Message: err.Error(), Severity: "warning",
			})
			continue
		}
		results = append(results, BaseSemanticResult{
			BaseID: baseID, Generation: compilation.Generation, Response: response,
		})
		snapshots = append(snapshots, Snapshot{
			BaseID: baseID, SemanticGeneration: compilation.Generation, SemanticSource: source,
		})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].BaseID < results[j].BaseID })
	return results, snapshots, diagnostics
}

func (s *Service) semanticSnapshot(ctx context.Context, baseID string, generation Generation) (semantic.Compilation, string, error) {
	if generation == GenerationActive || generation == "" {
		compilation, err := s.knowledge.GetSemanticCompilation(ctx, baseID)
		if err != nil {
			return semantic.Compilation{}, "", scopedError(ErrSemanticNotReady, baseID, "no active semantic compilation", nil)
		}
		return compilation, "active", nil
	}
	number, err := strconv.ParseInt(string(generation), 10, 64)
	if err != nil || number <= 0 {
		return semantic.Compilation{}, "", &Error{Code: ErrGenerationNotFound, BaseID: baseID, Message: fmt.Sprintf("invalid semantic generation %q", generation)}
	}
	compilation, err := s.knowledge.GetSemanticCompilationByID(ctx, baseID, number)
	if err != nil {
		return semantic.Compilation{}, "", scopedError(ErrGenerationNotFound, baseID, fmt.Sprintf("semantic generation %d is unavailable", number), nil)
	}
	return compilation, "pinned", nil
}

func (s *Service) searchStructured(ctx context.Context, request Request, scope []string) ([]BaseStructuredResult, []Diagnostic) {
	results := make([]BaseStructuredResult, 0, len(scope))
	diagnostics := make([]Diagnostic, 0)
	for _, baseID := range scope {
		response, err := s.knowledge.SearchSchema(ctx, baseID, request.Query, request.TopK)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{
				Code: "STRUCTURED_SEARCH_FAILED", BaseID: baseID,
				Message: err.Error(), Severity: "warning",
			})
			continue
		}
		results = append(results, BaseStructuredResult{BaseID: baseID, Response: response})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].BaseID < results[j].BaseID })
	return results, diagnostics
}

// ResolveScope exposes the shared scope resolution to protocol adapters that
// need the authorized base list before issuing a query. It applies the same
// authorization rules as Search.
func (s *Service) ResolveScope(ctx context.Context, requested []string) ([]string, error) {
	return s.resolveScope(ctx, requested)
}

// resolveScope starts from the existing enabled scope and intersects explicit
// base IDs. Unknown IDs are rejected so an authorization mistake is visible.
func (s *Service) resolveScope(ctx context.Context, requested []string) ([]string, error) {
	state, err := s.knowledge.EnabledScopeStateContext(ctx)
	if err != nil {
		return nil, err
	}
	if !state.Enabled {
		return []string{}, nil
	}
	bases, err := s.knowledge.ListBasesContext(ctx)
	if err != nil {
		return nil, err
	}
	existing := make(map[string]bool, len(bases))
	all := make([]string, 0, len(bases))
	for _, base := range bases {
		existing[base.ID] = true
		all = append(all, base.ID)
	}
	allowed := map[string]bool{}
	explicitAllowed := len(state.BaseIDs) > 0
	for _, id := range state.BaseIDs {
		allowed[id] = true
	}
	var selected []string
	seen := map[string]bool{}
	for _, id := range requested {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		if !existing[id] {
			return nil, scopedError(ErrBaseNotFound, id, "knowledge base is not available", nil)
		}
		if explicitAllowed && !allowed[id] {
			return nil, scopedError(ErrPermissionDenied, id, "knowledge base is not enabled", nil)
		}
		seen[id] = true
		selected = append(selected, id)
	}
	if len(selected) > 0 {
		sort.Strings(selected)
		return selected, nil
	}
	if state.Explicit {
		selected = append(selected, state.BaseIDs...)
		sort.Strings(selected)
		return selected, nil
	}
	sort.Strings(all)
	return all, nil
}

func normalizeMode(mode Mode) Mode {
	switch mode {
	case ModeEvidence, ModeSemantic, ModeHybrid, ModeAuto:
		return mode
	default:
		return ModeAuto
	}
}

func normalizeGeneration(generation Generation) Generation {
	if generation == "" {
		return GenerationActive
	}
	return generation
}

func validateVersionPolicy(policy VersionPolicy) (semantic.TemporalQuery, error) {
	normalized := policy
	if normalized.Type == "" {
		normalized.Type = VersionCurrent
	}
	switch normalized.Type {
	case VersionCurrent:
		if normalized.AsOf != "" || normalized.FromVersion != "" || normalized.ToVersion != "" {
			return semantic.TemporalQuery{}, invalidArgument("current version policy accepts no version bounds")
		}
		return semantic.TemporalQuery{Intent: semantic.TemporalCurrent, Confidence: 1}, nil
	case VersionAll:
		if normalized.AsOf != "" || normalized.FromVersion != "" || normalized.ToVersion != "" {
			return semantic.TemporalQuery{}, invalidArgument("all version policy accepts no version bounds")
		}
		return semantic.TemporalQuery{Intent: semantic.TemporalEvolution, Confidence: 1}, nil
	case VersionAsOf:
		if strings.TrimSpace(normalized.AsOf) == "" {
			return semantic.TemporalQuery{}, invalidArgument("as_of version policy requires asOf")
		}
		if normalized.FromVersion != "" || normalized.ToVersion != "" {
			return semantic.TemporalQuery{}, invalidArgument("as_of version policy does not accept range bounds")
		}
		identity, ok := semantic.NormalizeVersionIdentity(normalized.AsOf)
		if !ok {
			return semantic.TemporalQuery{}, &Error{Code: ErrVersionUnsupported, Message: fmt.Sprintf("invalid asOf version %q", normalized.AsOf)}
		}
		return semantic.TemporalQuery{Intent: semantic.TemporalExplicitVersion, Versions: []string{identity}, Confidence: 1}, nil
	case VersionRange:
		from := strings.TrimSpace(normalized.FromVersion)
		to := strings.TrimSpace(normalized.ToVersion)
		if from == "" || to == "" {
			return semantic.TemporalQuery{}, invalidArgument("range version policy requires fromVersion and toVersion")
		}
		if normalized.AsOf != "" {
			return semantic.TemporalQuery{}, invalidArgument("range version policy does not accept asOf")
		}
		fromIdentity, ok := semantic.NormalizeVersionIdentity(from)
		if !ok {
			return semantic.TemporalQuery{}, &Error{Code: ErrVersionUnsupported, Message: fmt.Sprintf("invalid fromVersion %q", from)}
		}
		toIdentity, toOK := semantic.NormalizeVersionIdentity(to)
		if !toOK {
			return semantic.TemporalQuery{}, &Error{Code: ErrVersionUnsupported, Message: fmt.Sprintf("invalid toVersion %q", to)}
		}
		if order, comparable := semantic.CompareVersionIdentity(fromIdentity, toIdentity); !comparable || order > 0 {
			return semantic.TemporalQuery{}, &Error{Code: ErrVersionUnsupported, Message: fmt.Sprintf("version range %q..%q is unordered or incomparable", from, to)}
		}
		inclusive := true
		return semantic.TemporalQuery{
			Intent: semantic.TemporalRangeHistory, Confidence: 1,
			Range: &semantic.TemporalRange{
				Kind: semantic.RangeVersion, Confidence: 1,
				Start: &semantic.RangeBoundary{Value: fromIdentity, Inclusive: inclusive},
				End:   &semantic.RangeBoundary{Value: toIdentity, Inclusive: inclusive},
			},
		}, nil
	default:
		return semantic.TemporalQuery{}, &Error{Code: ErrVersionUnsupported, Message: fmt.Sprintf("unsupported version policy %q", normalized.Type)}
	}
}

func evidenceKnowledgeMode(mode Mode, evidenceMode string) string {
	switch evidenceMode {
	case "auto", "hybrid", "vector", "lexical":
		return evidenceMode
	default:
		if mode == ModeEvidence {
			return ""
		}
		return "hybrid"
	}
}

func modes() []Mode {
	return []Mode{ModeEvidence, ModeSemantic, ModeHybrid, ModeAuto}
}

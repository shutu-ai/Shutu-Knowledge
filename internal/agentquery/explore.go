// Semantic graph exploration for the shared agent query contract. Explore
// activates seeds through lexical semantic search and follows typed relations;
// Expand starts from one exact unit ID. Traversal never fabricates support:
// every returned node and edge carries either exact evidence pointers or an
// explicit projection-only status, and endpoint visibility always honors the
// requested temporal scope against the same immutable compilation snapshot.
package agentquery

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/shutu-ai/shutu-knowledge/internal/semantic"
)

// TraversalDirection selects which typed relation endpoints are followed.
type TraversalDirection string

const (
	DirectionOut  TraversalDirection = "out"
	DirectionIn   TraversalDirection = "in"
	DirectionBoth TraversalDirection = "both"
)

// EvidenceStatus labels graph provenance. Exact carries auditable document IR
// or chunk pointers; projection-only elements are structural links (currently
// derived_from relations) whose support is the endpoint units' own resolved
// evidence, never an invented citation.
type EvidenceStatus string

const (
	EvidenceExact          EvidenceStatus = "exact"
	EvidenceProjectionOnly EvidenceStatus = "projection_only"
)

// Traversal bounds. Defaults keep one-hop answers small; explicit requests are
// clamped to hard ceilings so transport adapters cannot request unbounded
// walks. Clamping is deterministic and reported through Truncated.
const (
	defaultExploreHops   = 1
	maxExploreHops       = 4
	defaultExploreNodes  = 64
	maxExploreNodes      = 256
	defaultExploreFanOut = 16
	maxExploreFanOut     = 64
	defaultExploreTopK   = 8
	maxExploreTopK       = 32
)

// ExploreRequest activates semantic seeds with Query and then walks typed
// relations. The graph lane is semantic only; it never mixes evidence search
// hits into the result or silently downgrades to evidence.
type ExploreRequest struct {
	Query            string                  `json:"query"`
	BaseIDs          []string                `json:"baseIds,omitempty"`
	Generation       Generation              `json:"generation,omitempty"`
	VersionPolicy    VersionPolicy           `json:"versionPolicy,omitempty"`
	SemanticKinds    []semantic.UnitKind     `json:"semanticKinds,omitempty"`
	RelationTypes    []semantic.RelationType `json:"relationTypes,omitempty"`
	Direction        TraversalDirection      `json:"direction,omitempty"`
	MaxHops          int                     `json:"maxHops,omitempty"`
	MaxNodes         int                     `json:"maxNodes,omitempty"`
	FanOut           int                     `json:"fanOut,omitempty"`
	TopK             int                     `json:"topK,omitempty"`
	ExcludeConflicts bool                    `json:"excludeConflicts,omitempty"`
}

// ExpandRequest walks from one exact unit ID. The unit must resolve in the
// active or pinned generation of one requested base.
type ExpandRequest struct {
	UnitID           string                  `json:"unitId"`
	BaseIDs          []string                `json:"baseIds,omitempty"`
	Generation       Generation              `json:"generation,omitempty"`
	VersionPolicy    VersionPolicy           `json:"versionPolicy,omitempty"`
	RelationTypes    []semantic.RelationType `json:"relationTypes,omitempty"`
	Direction        TraversalDirection      `json:"direction,omitempty"`
	MaxHops          int                     `json:"maxHops,omitempty"`
	MaxNodes         int                     `json:"maxNodes,omitempty"`
	FanOut           int                     `json:"fanOut,omitempty"`
	ExcludeConflicts bool                    `json:"excludeConflicts,omitempty"`
}

// ExploreResult holds one independent graph per authorized base. Graphs are
// never merged into a fake cross-base generation.
type ExploreResult struct {
	Query       string             `json:"query"`
	Scope       []string           `json:"scope"`
	Graphs      []BaseExploreGraph `json:"graphs"`
	Diagnostics []Diagnostic       `json:"diagnostics,omitempty"`
	Degraded    bool               `json:"degraded,omitempty"`
}

// ExpandResult holds one graph per base that contains the requested unit.
type ExpandResult struct {
	UnitID      string             `json:"unitId"`
	Scope       []string           `json:"scope"`
	Graphs      []BaseExploreGraph `json:"graphs"`
	Diagnostics []Diagnostic       `json:"diagnostics,omitempty"`
	Degraded    bool               `json:"degraded,omitempty"`
}

// BaseExploreGraph is one base's traversal output with the routing plan that
// produced its seeds. It is a bounded, deterministic projection.
type BaseExploreGraph struct {
	BaseID      string              `json:"baseId"`
	Generation  int64               `json:"generation"`
	Plan        *semantic.QueryPlan `json:"plan,omitempty"`
	Seeds       []ExploreSeed       `json:"seeds"`
	Nodes       []ExploreNode       `json:"nodes"`
	Edges       []ExploreEdge       `json:"edges"`
	Paths       []ExplorePath       `json:"paths,omitempty"`
	Truncated   bool                `json:"truncated,omitempty"`
	Diagnostics []Diagnostic        `json:"diagnostics,omitempty"`
}

// ExploreSeed records one activated start unit and its activation score.
type ExploreSeed struct {
	UnitID string  `json:"unitId"`
	Score  float64 `json:"score"`
}

// ExploreNode is one visible unit with its exact evidence closure. Hops is the
// BFS distance from the nearest seed.
type ExploreNode struct {
	Unit           semantic.Unit             `json:"unit"`
	Hops           int                       `json:"hops"`
	IsSeed         bool                      `json:"isSeed,omitempty"`
	Evidence       []semantic.EvidenceSource `json:"evidence"`
	EvidenceStatus EvidenceStatus            `json:"evidenceStatus"`
}

// ExploreEdge is one traversed relation with its own provenance. Conflict is
// true only for contradicts relations: it reports an existing conflict
// relation, never which side of it is true.
type ExploreEdge struct {
	Relation       semantic.Relation         `json:"relation"`
	Evidence       []semantic.EvidenceSource `json:"evidence"`
	EvidenceStatus EvidenceStatus            `json:"evidenceStatus"`
	Conflict       bool                      `json:"conflict,omitempty"`
}

// ExplorePath is one canonical seed-to-node chain. Provenance is inherited
// from the referenced node and edge elements; paths add no new claims.
type ExplorePath struct {
	SeedUnitID string   `json:"seedUnitId"`
	UnitID     string   `json:"unitId"`
	Hops       int      `json:"hops"`
	EdgeIDs    []string `json:"edgeIds"`
}

// Explore activates seeds from semantic search and follows typed relations
// under the requested temporal scope. When no base has a usable semantic
// generation the error is ErrSemanticNotReady; activation that legitimately
// finds nothing is an empty result, never a fallback to evidence.
func (s *Service) Explore(ctx context.Context, request ExploreRequest) (ExploreResult, error) {
	if err := ctx.Err(); err != nil {
		return ExploreResult{}, err
	}
	request.Generation = normalizeGeneration(request.Generation)
	if strings.TrimSpace(request.Query) == "" {
		return ExploreResult{}, invalidArgument("query is required")
	}
	if err := validateGenerationSelector(request.Generation); err != nil {
		return ExploreResult{}, err
	}
	temporal, err := validateVersionPolicy(request.VersionPolicy)
	if err != nil {
		return ExploreResult{}, err
	}
	opts, err := normalizeTraversal(request.RelationTypes, request.Direction, request.MaxHops, request.MaxNodes, request.FanOut, request.ExcludeConflicts)
	if err != nil {
		return ExploreResult{}, err
	}
	opts.temporal = temporal
	scope, err := s.resolveScope(ctx, request.BaseIDs)
	if err != nil {
		return ExploreResult{}, err
	}
	result := ExploreResult{Query: request.Query, Scope: scope, Graphs: []BaseExploreGraph{}}
	usable := 0
	for _, baseID := range scope {
		compilation, _, snapshotErr := s.semanticSnapshot(ctx, baseID, request.Generation)
		if snapshotErr != nil {
			result.Diagnostics = append(result.Diagnostics, semanticSnapshotDiagnostic(baseID, snapshotErr))
			continue
		}
		response, searchErr := semantic.SearchCompilation(compilation, semantic.SearchOptions{
			Query: request.Query, TopK: exploreTopK(request.TopK),
			Kinds: request.SemanticKinds, Temporal: &temporal,
		})
		if searchErr != nil {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{
				Code: "SEMANTIC_SEARCH_FAILED", BaseID: baseID,
				Message: searchErr.Error(), Severity: "warning",
			})
			continue
		}
		usable++
		graph, buildErr := buildGraph(compilation, response.Hits, opts)
		if buildErr != nil {
			return ExploreResult{}, fmt.Errorf("explore base %s: %w", baseID, buildErr)
		}
		graph.BaseID = baseID
		graph.Generation = compilation.Generation
		graph.Plan = response.Routing
		result.Graphs = append(result.Graphs, graph)
	}
	if usable == 0 {
		return result, &Error{Code: ErrSemanticNotReady, Message: "no requested base has an active or pinned semantic generation"}
	}
	sort.Slice(result.Graphs, func(i, j int) bool { return result.Graphs[i].BaseID < result.Graphs[j].BaseID })
	result.Degraded = len(result.Diagnostics) > 0
	return result, nil
}

// Expand walks from one exact unit. The unit must resolve inside one
// requested base's compilation; a unit that exists but is hidden by the
// version policy is reported through the same stable error with a precise
// message instead of being silently dropped.
func (s *Service) Expand(ctx context.Context, request ExpandRequest) (ExpandResult, error) {
	if err := ctx.Err(); err != nil {
		return ExpandResult{}, err
	}
	request.Generation = normalizeGeneration(request.Generation)
	unitID := strings.TrimSpace(request.UnitID)
	if unitID == "" {
		return ExpandResult{}, invalidArgument("unitId is required")
	}
	if err := validateGenerationSelector(request.Generation); err != nil {
		return ExpandResult{}, err
	}
	temporal, err := validateVersionPolicy(request.VersionPolicy)
	if err != nil {
		return ExpandResult{}, err
	}
	opts, err := normalizeTraversal(request.RelationTypes, request.Direction, request.MaxHops, request.MaxNodes, request.FanOut, request.ExcludeConflicts)
	if err != nil {
		return ExpandResult{}, err
	}
	opts.temporal = temporal
	scope, err := s.resolveScope(ctx, request.BaseIDs)
	if err != nil {
		return ExpandResult{}, err
	}
	result := ExpandResult{UnitID: unitID, Scope: scope, Graphs: []BaseExploreGraph{}}
	found := false
	hiddenByPolicy := false
	semanticAvailable := false
	for _, baseID := range scope {
		compilation, _, snapshotErr := s.semanticSnapshot(ctx, baseID, request.Generation)
		if snapshotErr != nil {
			result.Diagnostics = append(result.Diagnostics, semanticSnapshotDiagnostic(baseID, snapshotErr))
			continue
		}
		semanticAvailable = true
		unit, exists := compilationUnit(compilation, unitID)
		if !exists {
			continue
		}
		resolved := resolvedCurrentVersion(compilation, opts.temporal)
		if !semantic.TemporalVersionVisible(unit, opts.temporal, resolved) {
			hiddenByPolicy = true
			continue
		}
		found = true
		graph, buildErr := buildGraph(compilation, []semantic.SearchHit{{Unit: unit, Score: 1}}, opts)
		if buildErr != nil {
			return ExpandResult{}, fmt.Errorf("expand base %s: %w", baseID, buildErr)
		}
		graph.BaseID = baseID
		graph.Generation = compilation.Generation
		result.Graphs = append(result.Graphs, graph)
	}
	if !found {
		if !semanticAvailable {
			return result, &Error{Code: ErrSemanticNotReady, Message: "no requested base has an active or pinned semantic generation"}
		}
		if hiddenByPolicy {
			return result, &Error{Code: ErrUnitNotFound, BaseID: "", Message: fmt.Sprintf("unit %q exists but is not visible under the requested version policy", unitID)}
		}
		return result, &Error{Code: ErrUnitNotFound, Message: fmt.Sprintf("unit %q was not found in the requested scope", unitID)}
	}
	sort.Slice(result.Graphs, func(i, j int) bool { return result.Graphs[i].BaseID < result.Graphs[j].BaseID })
	result.Degraded = len(result.Diagnostics) > 0
	return result, nil
}

// buildGraph runs one bounded BFS over one immutable compilation. Seeds keep
// semantic activation order; adjacency is sorted by relation ID so traversal
// and output are fully deterministic.
func buildGraph(compilation semantic.Compilation, hits []semantic.SearchHit, opts traversalOptions) (BaseExploreGraph, error) {
	graph := BaseExploreGraph{
		Seeds: make([]ExploreSeed, 0, len(hits)),
		Nodes: []ExploreNode{},
		Edges: []ExploreEdge{},
	}
	resolved := resolvedCurrentVersion(compilation, opts.temporal)
	units := make(map[string]semantic.Unit, len(compilation.Units))
	for _, unit := range compilation.Units {
		if semantic.TemporalVersionVisible(unit, opts.temporal, resolved) {
			units[unit.ID] = unit
		}
	}

	seeds := make([]string, 0, len(hits))
	for _, hit := range hits {
		if _, visible := units[hit.Unit.ID]; !visible {
			continue
		}
		if len(seeds) >= opts.maxNodes {
			graph.Truncated = true
			break
		}
		seeds = append(seeds, hit.Unit.ID)
		graph.Seeds = append(graph.Seeds, ExploreSeed{UnitID: hit.Unit.ID, Score: hit.Score})
	}
	if len(seeds) == 0 {
		return graph, nil
	}

	type step struct {
		relation semantic.Relation
		to       string
	}
	adjacency := map[string][]step{}
	for _, relation := range compilation.Relations {
		if relation.Status != semantic.UnitActive && relation.Status != semantic.UnitConflicted {
			continue
		}
		if len(opts.relationTypes) > 0 && !opts.relationTypes[relation.Type] {
			continue
		}
		if opts.excludeConflicts && relation.Type == semantic.RelationContradicts {
			continue
		}
		if opts.direction != DirectionIn {
			adjacency[relation.SubjectUnitID] = append(adjacency[relation.SubjectUnitID], step{relation: relation, to: relation.ObjectUnitID})
		}
		if opts.direction != DirectionOut {
			adjacency[relation.ObjectUnitID] = append(adjacency[relation.ObjectUnitID], step{relation: relation, to: relation.SubjectUnitID})
		}
	}
	for id := range adjacency {
		steps := adjacency[id]
		sort.Slice(steps, func(i, j int) bool { return steps[i].relation.ID < steps[j].relation.ID })
		adjacency[id] = steps
	}

	hopsByUnit := map[string]int{}
	parentUnit := map[string]string{}
	parentEdge := map[string]string{}
	edgesByID := map[string]semantic.Relation{}

	for _, seed := range seeds {
		hopsByUnit[seed] = 0
	}
	frontier := append([]string(nil), seeds...)
	for hop := 1; hop <= opts.maxHops && len(frontier) > 0; hop++ {
		next := make([]string, 0, len(frontier))
		for _, current := range frontier {
			discovered := 0
			for _, candidate := range adjacency[current] {
				if discovered >= opts.fanOut {
					graph.Truncated = true
					break
				}
				neighbor := candidate.to
				if _, visible := units[neighbor]; !visible {
					continue
				}
				edgesByID[candidate.relation.ID] = candidate.relation
				if _, visited := hopsByUnit[neighbor]; visited {
					continue
				}
				if len(hopsByUnit) >= opts.maxNodes {
					graph.Truncated = true
					break
				}
				hopsByUnit[neighbor] = hop
				parentUnit[neighbor] = current
				parentEdge[neighbor] = candidate.relation.ID
				next = append(next, neighbor)
				discovered++
			}
			if len(hopsByUnit) >= opts.maxNodes {
				break
			}
		}
		frontier = next
	}

	edgeIDs := make([]string, 0, len(edgesByID))
	for id := range edgesByID {
		edgeIDs = append(edgeIDs, id)
	}
	sort.Strings(edgeIDs)
	for _, id := range edgeIDs {
		relation := edgesByID[id]
		edge := ExploreEdge{
			Relation: relation,
			Conflict: relation.Type == semantic.RelationContradicts,
		}
		if len(relation.Sources) > 0 {
			edge.Evidence = append([]semantic.EvidenceSource(nil), relation.Sources...)
			edge.EvidenceStatus = EvidenceExact
		} else {
			edge.EvidenceStatus = EvidenceProjectionOnly
		}
		graph.Edges = append(graph.Edges, edge)
	}

	nodeIDs := make([]string, 0, len(hopsByUnit))
	for id := range hopsByUnit {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Slice(nodeIDs, func(i, j int) bool {
		if hopsByUnit[nodeIDs[i]] != hopsByUnit[nodeIDs[j]] {
			return hopsByUnit[nodeIDs[i]] < hopsByUnit[nodeIDs[j]]
		}
		return nodeIDs[i] < nodeIDs[j]
	})
	for _, id := range nodeIDs {
		evidence, err := semantic.ResolveEvidence(compilation.Units, id)
		if err != nil {
			return BaseExploreGraph{}, fmt.Errorf("resolve evidence for unit %s: %w", id, err)
		}
		graph.Nodes = append(graph.Nodes, ExploreNode{
			Unit: units[id], Hops: hopsByUnit[id], IsSeed: hopsByUnit[id] == 0,
			Evidence: evidence, EvidenceStatus: EvidenceExact,
		})
	}

	for _, id := range nodeIDs {
		if hopsByUnit[id] == 0 {
			continue
		}
		chain := make([]string, 0, hopsByUnit[id])
		cursor := id
		for hopsByUnit[cursor] > 0 {
			chain = append(chain, parentEdge[cursor])
			cursor = parentUnit[cursor]
		}
		for left, right := 0, len(chain)-1; left < right; left, right = left+1, right-1 {
			chain[left], chain[right] = chain[right], chain[left]
		}
		graph.Paths = append(graph.Paths, ExplorePath{
			SeedUnitID: cursor, UnitID: id, Hops: hopsByUnit[id], EdgeIDs: chain,
		})
	}
	return graph, nil
}

type traversalOptions struct {
	relationTypes    map[semantic.RelationType]bool
	direction        TraversalDirection
	maxHops          int
	maxNodes         int
	fanOut           int
	excludeConflicts bool
	temporal         semantic.TemporalQuery
}

func normalizeTraversal(relationTypes []semantic.RelationType, direction TraversalDirection, maxHops, maxNodes, fanOut int, excludeConflicts bool) (traversalOptions, error) {
	opts := traversalOptions{
		direction:        DirectionOut,
		maxHops:          defaultExploreHops,
		maxNodes:         defaultExploreNodes,
		fanOut:           defaultExploreFanOut,
		excludeConflicts: excludeConflicts,
		relationTypes:    map[semantic.RelationType]bool{},
	}
	if direction != "" {
		switch direction {
		case DirectionIn, DirectionOut, DirectionBoth:
			opts.direction = direction
		default:
			return opts, invalidArgument("direction must be %q, %q, or %q", DirectionIn, DirectionOut, DirectionBoth)
		}
	}
	seen := map[semantic.RelationType]bool{}
	for _, relationType := range relationTypes {
		switch relationType {
		case semantic.RelationDerivedFrom, semantic.RelationPartOf, semantic.RelationMentions,
			semantic.RelationRelatedTo, semantic.RelationSupports, semantic.RelationContradicts,
			semantic.RelationSupersedes, semantic.RelationSameTopic:
		default:
			return opts, invalidArgument("unsupported relation type %q", relationType)
		}
		if !seen[relationType] {
			seen[relationType] = true
			opts.relationTypes[relationType] = true
		}
	}
	opts.maxHops = clampPositive(maxHops, defaultExploreHops, maxExploreHops)
	opts.maxNodes = clampPositive(maxNodes, defaultExploreNodes, maxExploreNodes)
	opts.fanOut = clampPositive(fanOut, defaultExploreFanOut, maxExploreFanOut)
	return opts, nil
}

func clampPositive(value, fallback, ceiling int) int {
	if value <= 0 {
		return fallback
	}
	if value > ceiling {
		return ceiling
	}
	return value
}

func exploreTopK(topK int) int {
	return clampPositive(topK, defaultExploreTopK, maxExploreTopK)
}

func validateGenerationSelector(generation Generation) error {
	if generation == GenerationActive || generation == "" {
		return nil
	}
	parsed, err := strconv.ParseInt(string(generation), 10, 64)
	if err != nil || parsed <= 0 {
		return &Error{Code: ErrInvalidArgument, Message: fmt.Sprintf("generation must be %q or a positive integer", GenerationActive), Cause: err}
	}
	return nil
}

func compilationUnit(compilation semantic.Compilation, unitID string) (semantic.Unit, bool) {
	for _, unit := range compilation.Units {
		if unit.ID == unitID {
			return unit, true
		}
	}
	return semantic.Unit{}, false
}

func resolvedCurrentVersion(compilation semantic.Compilation, temporal semantic.TemporalQuery) string {
	if temporal.Intent == semantic.TemporalCurrent || temporal.Intent == semantic.TemporalValidity {
		resolved, _ := semantic.ResolveCurrentVersion(compilation)
		return resolved
	}
	return ""
}

func semanticSnapshotDiagnostic(baseID string, err error) Diagnostic {
	code := "SEMANTIC_NOT_READY"
	var appErr *Error
	if errors.As(err, &appErr) && appErr.Code == ErrGenerationNotFound {
		code = "GENERATION_NOT_FOUND"
	}
	return Diagnostic{Code: code, BaseID: baseID, Message: err.Error(), Severity: "warning"}
}
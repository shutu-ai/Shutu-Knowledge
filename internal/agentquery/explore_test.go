package agentquery

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/shutu-ai/shutu-knowledge/internal/config"
	"github.com/shutu-ai/shutu-knowledge/internal/knowledge"
	"github.com/shutu-ai/shutu-knowledge/internal/semantic"
	"github.com/shutu-ai/shutu-knowledge/internal/storage"
	"strconv"
)

const fixtureNow = int64(1_700_000_000_000)

func newExploreFixture(t *testing.T) (*Service, *semantic.Store, knowledge.Base) {
	t.Helper()
	home := t.TempDir()
	db, err := storage.Open(filepath.Join(home, "knowledge.db"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	raw, err := storage.NewRawFileStore(filepath.Join(home, "raw"))
	if err != nil {
		t.Fatalf("open raw store: %v", err)
	}
	service := knowledge.NewService(db, raw, config.Defaults())
	base, err := service.CreateBase("Explore", "graph fixture", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatalf("create base: %v", err)
	}
	return New(service), semantic.NewStore(db), base
}

func mustActivateCompilation(t *testing.T, store *semantic.Store, compilation semantic.Compilation) {
	t.Helper()
	compilation.State = semantic.CompilationBuilding
	if compilation.Compiler == "" {
		compilation.Compiler = semantic.BuiltinCompiler
		compilation.CompilerVersion = semantic.BuiltinCompilerVersion
		compilation.Model = semantic.BuiltinModel
		compilation.ModelVersion = semantic.BuiltinModelVersion
		compilation.PromptVersion = semantic.BuiltinPromptVersion
	}
	if compilation.CreatedAt == 0 {
		compilation.CreatedAt = fixtureNow
	}
	ctx := context.Background()
	if err := store.CreateCompilation(ctx, compilation); err != nil {
		t.Fatalf("create compilation: %v", err)
	}
	if err := store.ActivateCompilation(ctx, compilation.BaseID, compilation.Generation); err != nil {
		t.Fatalf("activate compilation: %v", err)
	}
}

func graphUnit(baseID string, generation int64, kind semantic.UnitKind, key, title, content, documentID string) semantic.Unit {
	return semantic.Unit{
		ID:     semantic.UnitID(baseID, generation, kind, key),
		BaseID: baseID, Generation: generation, Type: kind,
		Title: title, CanonicalKey: key, Content: content,
		Confidence: 0.9, Status: semantic.UnitActive,
		Sources: []semantic.EvidenceSource{{
			DocumentID: documentID, IndexGeneration: generation * 10,
			SourceVersion: generation, NodeID: documentID + "-node", ChunkID: documentID + "-chunk",
		}},
		CreatedAt: fixtureNow, UpdatedAt: fixtureNow,
	}
}

func graphVersionedFact(baseID string, generation int64, key, content, version string, status semantic.UnitStatus, supersededBy string) semantic.Unit {
	documentID := "doc-" + key
	unit := semantic.Unit{
		ID:     semantic.UnitID(baseID, generation, semantic.UnitFact, key),
		BaseID: baseID, Generation: generation, Type: semantic.UnitFact,
		Title: content, CanonicalKey: key, Content: content,
		Confidence: 0.9, Status: status, SupersededBy: supersededBy,
		Metadata: map[string]string{"temporal_version": version},
		Sources: []semantic.EvidenceSource{{
			DocumentID: documentID, IndexGeneration: generation * 10,
			SourceVersion: generation, NodeID: documentID + "-node", ChunkID: documentID + "-chunk",
		}},
		CreatedAt: fixtureNow, UpdatedAt: fixtureNow,
	}
	return unit
}

func graphRelation(baseID string, generation int64, relationType semantic.RelationType, subject, object semantic.Unit, sources []semantic.EvidenceSource) semantic.Relation {
	if sources == nil {
		sources = append(append([]semantic.EvidenceSource(nil), subject.Sources...), object.Sources...)
		semantic.SortEvidenceSources(sources)
		for i := range sources {
			sources[i].SourceOrder = i
		}
	}
	key := "fixture:" + string(relationType) + ":" + subject.CanonicalKey + ":" + object.CanonicalKey
	return semantic.Relation{
		ID:     semantic.RelationID(baseID, generation, relationType, subject.ID, object.ID, key),
		BaseID: baseID, Generation: generation, Type: relationType,
		SubjectUnitID: subject.ID, ObjectUnitID: object.ID,
		Predicate: string(relationType), Statement: subject.Title + " -> " + object.Title,
		CanonicalKey: key, Confidence: 0.8, Status: semantic.UnitActive,
		Sources: sources, CreatedAt: fixtureNow, UpdatedAt: fixtureNow,
	}
}

// TestExploreActivatesSeedsFromSemanticCompilation proves G2 semantic
// activation over the real compile pipeline, including snapshot identity,
// plan explanation, auditable per-node provenance, and deterministic repeats.
func TestExploreActivatesSeedsFromSemanticCompilation(t *testing.T) {
	ctx := context.Background()
	service, base := newQueryFixture(t)
	compilation, err := service.knowledge.CompileSemanticMemory(ctx, base.ID)
	if err != nil {
		t.Fatalf("compile semantic memory: %v", err)
	}

	request := ExploreRequest{Query: "KPI", BaseIDs: []string{base.ID}}
	result, err := service.Explore(ctx, request)
	if err != nil {
		t.Fatalf("explore: %v", err)
	}
	if len(result.Graphs) != 1 {
		t.Fatalf("graphs: %+v", result.Graphs)
	}
	graph := result.Graphs[0]
	if graph.BaseID != base.ID || graph.Generation != compilation.Generation {
		t.Fatalf("graph scope: base=%s generation=%d", graph.BaseID, graph.Generation)
	}
	if len(graph.Seeds) == 0 || graph.Seeds[0].UnitID == "" {
		t.Fatalf("seeds: %+v", graph.Seeds)
	}
	if graph.Plan == nil || graph.Plan.Query != "KPI" {
		t.Fatalf("plan: %+v", graph.Plan)
	}
	for _, node := range graph.Nodes {
		if node.EvidenceStatus != EvidenceExact || len(node.Evidence) == 0 {
			t.Fatalf("node %s provenance: %+v", node.Unit.ID, node)
		}
		for _, source := range node.Evidence {
			if source.IndexGeneration <= 0 || source.SourceVersion <= 0 ||
				(source.NodeID == "" && source.ChunkID == "") {
				t.Fatalf("node %s has a non-auditable evidence pointer: %+v", node.Unit.ID, source)
			}
		}
	}
	for _, edge := range graph.Edges {
		if edge.EvidenceStatus != EvidenceExact || len(edge.Evidence) == 0 {
			t.Fatalf("edge %s provenance: %+v", edge.Relation.ID, edge)
		}
	}

	again, err := service.Explore(ctx, request)
	if err != nil {
		t.Fatalf("repeat explore: %v", err)
	}
	if !reflect.DeepEqual(result.Graphs, again.Graphs) {
		t.Fatal("explore output is not deterministic")
	}
}

// TestExploreCrossDocumentExpansion proves typed relation traversal reaches
// units backed by different documents, with hop accounting, canonical paths,
// and edge provenance that spans the same evidence as its endpoints.
func TestExploreCrossDocumentExpansion(t *testing.T) {
	ctx := context.Background()
	service, store, base := newExploreFixture(t)
	const generation = int64(1)
	kpi := graphUnit(base.ID, generation, semantic.UnitConcept, "concept-kpi", "KPI", "KPI is calculated from SDR counters.", "doc-a")
	sdr := graphUnit(base.ID, generation, semantic.UnitConcept, "concept-sdr", "SDR", "SDR counters feed the KPI.", "doc-b")
	counter := graphUnit(base.ID, generation, semantic.UnitConcept, "concept-counter", "Counter", "Counter tables store SDR samples.", "doc-b")
	relationKPItoSDR := graphRelation(base.ID, generation, semantic.RelationRelatedTo, kpi, sdr, nil)
	relationSDRtoCounter := graphRelation(base.ID, generation, semantic.RelationRelatedTo, sdr, counter, nil)
	mustActivateCompilation(t, store, semantic.Compilation{
		BaseID: base.ID, Generation: generation,
		Units:     []semantic.Unit{kpi, sdr, counter},
		Relations: []semantic.Relation{relationKPItoSDR, relationSDRtoCounter},
	})

	result, err := service.Explore(ctx, ExploreRequest{
		Query: "KPI", BaseIDs: []string{base.ID}, MaxHops: 2,
	})
	if err != nil {
		t.Fatalf("explore: %v", err)
	}
	graph := result.Graphs[0]
	if len(graph.Seeds) != 1 || graph.Seeds[0].UnitID != kpi.ID {
		t.Fatalf("seeds: %+v", graph.Seeds)
	}
	if len(graph.Nodes) != 3 {
		t.Fatalf("nodes: %+v", graph.Nodes)
	}
	hops := map[string]int{}
	documents := map[string]bool{}
	for _, node := range graph.Nodes {
		hops[node.Unit.ID] = node.Hops
		if node.EvidenceStatus != EvidenceExact || len(node.Evidence) == 0 {
			t.Fatalf("node %s provenance: %+v", node.Unit.ID, node)
		}
		for _, source := range node.Evidence {
			documents[source.DocumentID] = true
		}
	}
	if hops[kpi.ID] != 0 || hops[sdr.ID] != 1 || hops[counter.ID] != 2 {
		t.Fatalf("hop accounting: %+v", hops)
	}
	if !documents["doc-a"] || !documents["doc-b"] {
		t.Fatalf("cross-document evidence missing: %v", documents)
	}
	if len(graph.Paths) != 2 {
		t.Fatalf("paths: %+v", graph.Paths)
	}
	for _, path := range graph.Paths {
		if path.SeedUnitID != kpi.ID {
			t.Fatalf("path seed: %+v", path)
		}
	}
	if path := findPath(graph.Paths, counter.ID); path == nil ||
		!reflect.DeepEqual(path.EdgeIDs, []string{relationKPItoSDR.ID, relationSDRtoCounter.ID}) {
		t.Fatalf("counter path: %+v", graph.Paths)
	}
	for _, edge := range graph.Edges {
		if edge.EvidenceStatus != EvidenceExact || len(edge.Evidence) < 2 {
			t.Fatalf("edge %s provenance: %+v", edge.Relation.ID, edge)
		}
	}

	// Direction out keeps the chain; direction in from the seed finds nothing.
	inResult, err := service.Explore(ctx, ExploreRequest{
		Query: "KPI", BaseIDs: []string{base.ID}, Direction: DirectionIn,
	})
	if err != nil {
		t.Fatalf("in explore: %v", err)
	}
	if len(inResult.Graphs[0].Nodes) != 1 || len(inResult.Graphs[0].Edges) != 0 {
		t.Fatalf("in explore result: %+v", inResult.Graphs[0])
	}
}

// TestExploreConflictRelationFiltering proves contradicts edges are reported
// as conflicts without deciding a side, and can be excluded explicitly.
func TestExploreConflictRelationFiltering(t *testing.T) {
	ctx := context.Background()
	service, store, base := newExploreFixture(t)
	const generation = int64(1)
	alpha := graphUnit(base.ID, generation, semantic.UnitConcept, "concept-alpha", "Alpha", "Alpha uses retention mode A.", "doc-a")
	beta := graphUnit(base.ID, generation, semantic.UnitConcept, "concept-beta", "Beta", "Beta uses retention mode B.", "doc-b")
	conflict := graphRelation(base.ID, generation, semantic.RelationContradicts, alpha, beta, nil)
	mustActivateCompilation(t, store, semantic.Compilation{
		BaseID: base.ID, Generation: generation,
		Units: []semantic.Unit{alpha, beta}, Relations: []semantic.Relation{conflict},
	})

	included, err := service.Explore(ctx, ExploreRequest{Query: "Alpha", BaseIDs: []string{base.ID}})
	if err != nil {
		t.Fatalf("explore: %v", err)
	}
	if len(included.Graphs[0].Edges) != 1 || !included.Graphs[0].Edges[0].Conflict {
		t.Fatalf("conflict edge not flagged: %+v", included.Graphs[0].Edges)
	}
	if len(included.Graphs[0].Nodes) != 2 {
		t.Fatalf("conflict traversal nodes: %+v", included.Graphs[0].Nodes)
	}

	excluded, err := service.Explore(ctx, ExploreRequest{
		Query: "Alpha", BaseIDs: []string{base.ID}, ExcludeConflicts: true,
	})
	if err != nil {
		t.Fatalf("exclude explore: %v", err)
	}
	if len(excluded.Graphs[0].Edges) != 0 || len(excluded.Graphs[0].Nodes) != 1 {
		t.Fatalf("exclude conflicts result: %+v", excluded.Graphs[0])
	}

	typed, err := service.Explore(ctx, ExploreRequest{
		Query: "Alpha", BaseIDs: []string{base.ID},
		RelationTypes: []semantic.RelationType{semantic.RelationSupports},
	})
	if err != nil {
		t.Fatalf("typed explore: %v", err)
	}
	if len(typed.Graphs[0].Edges) != 0 || len(typed.Graphs[0].Nodes) != 1 {
		t.Fatalf("relation type filter result: %+v", typed.Graphs[0])
	}
}

// TestExpandDirectionRelationTypeAndBounds proves exact-seed expansion,
// direction/relation filters, hard bounds, cycle safety, and stable errors.
func TestExpandDirectionRelationTypeAndBounds(t *testing.T) {
	ctx := context.Background()
	service, store, base := newExploreFixture(t)
	const generation = int64(1)
	kpi := graphUnit(base.ID, generation, semantic.UnitConcept, "concept-kpi", "KPI", "KPI overview.", "doc-a")
	sdr := graphUnit(base.ID, generation, semantic.UnitConcept, "concept-sdr", "SDR", "SDR counters.", "doc-b")
	counter := graphUnit(base.ID, generation, semantic.UnitConcept, "concept-counter", "Counter", "Counter tables.", "doc-b")
	gauge := graphUnit(base.ID, generation, semantic.UnitConcept, "concept-gauge", "Gauge", "Gauge metrics.", "doc-b")
	relationKPItoSDR := graphRelation(base.ID, generation, semantic.RelationRelatedTo, kpi, sdr, nil)
	relationSDRtoCounter := graphRelation(base.ID, generation, semantic.RelationRelatedTo, sdr, counter, nil)
	relationSDRtoGauge := graphRelation(base.ID, generation, semantic.RelationRelatedTo, sdr, gauge, nil)
	relationCounterToKPI := graphRelation(base.ID, generation, semantic.RelationRelatedTo, counter, kpi, nil)
	mustActivateCompilation(t, store, semantic.Compilation{
		BaseID: base.ID, Generation: generation,
		Units:     []semantic.Unit{kpi, sdr, counter, gauge},
		Relations: []semantic.Relation{relationKPItoSDR, relationSDRtoCounter, relationSDRtoGauge, relationCounterToKPI},
	})

	out, err := service.Expand(ctx, ExpandRequest{UnitID: kpi.ID, BaseIDs: []string{base.ID}})
	if err != nil {
		t.Fatalf("expand out: %v", err)
	}
	if len(out.Graphs[0].Nodes) != 2 || len(out.Graphs[0].Edges) != 1 {
		t.Fatalf("expand out: %+v", out.Graphs[0])
	}

	in, err := service.Expand(ctx, ExpandRequest{UnitID: kpi.ID, BaseIDs: []string{base.ID}, Direction: DirectionIn})
	if err != nil {
		t.Fatalf("expand in: %v", err)
	}
	if len(in.Graphs[0].Nodes) != 2 || len(in.Graphs[0].Edges) != 1 {
		t.Fatalf("expand in via incoming back-edge: %+v", in.Graphs[0])
	}

	both, err := service.Expand(ctx, ExpandRequest{
		UnitID: kpi.ID, BaseIDs: []string{base.ID}, Direction: DirectionBoth, MaxHops: 2,
	})
	if err != nil {
		t.Fatalf("expand both: %v", err)
	}
	if len(both.Graphs[0].Nodes) != 4 {
		t.Fatalf("expand both nodes: %+v", both.Graphs[0].Nodes)
	}
	if len(both.Graphs[0].Edges) != 4 {
		t.Fatalf("cycle back-edge not recorded exactly once per relation: %+v", both.Graphs[0].Edges)
	}

	typed, err := service.Expand(ctx, ExpandRequest{
		UnitID: kpi.ID, BaseIDs: []string{base.ID},
		RelationTypes: []semantic.RelationType{semantic.RelationSupersedes},
	})
	if err != nil {
		t.Fatalf("expand typed: %v", err)
	}
	if len(typed.Graphs[0].Nodes) != 1 || len(typed.Graphs[0].Edges) != 0 {
		t.Fatalf("expand typed: %+v", typed.Graphs[0])
	}

	bounded, err := service.Expand(ctx, ExpandRequest{UnitID: kpi.ID, BaseIDs: []string{base.ID}, MaxNodes: 1})
	if err != nil {
		t.Fatalf("expand bounded: %v", err)
	}
	if !bounded.Graphs[0].Truncated || len(bounded.Graphs[0].Nodes) != 1 {
		t.Fatalf("expand bounded: %+v", bounded.Graphs[0])
	}

	fanOut, err := service.Expand(ctx, ExpandRequest{
		UnitID: sdr.ID, BaseIDs: []string{base.ID}, FanOut: 1, MaxHops: 2,
	})
	if err != nil {
		t.Fatalf("expand fanOut: %v", err)
	}
	if !fanOut.Graphs[0].Truncated {
		t.Fatalf("fanOut truncation not reported: %+v", fanOut.Graphs[0])
	}

	_, err = service.Expand(ctx, ExpandRequest{UnitID: "ku_missing", BaseIDs: []string{base.ID}})
	var appErr *Error
	if !errors.As(err, &appErr) || appErr.Code != ErrUnitNotFound {
		t.Fatalf("missing unit error: %v", err)
	}
	if !strings.Contains(appErr.Message, "ku_missing") {
		t.Fatalf("missing unit message: %v", appErr)
	}
}

// TestExpandAndExploreVersionConsistency proves traversal honors the temporal
// scope on both seeds and endpoints, so history is queryable without mixing
// superseded and current claims.
func TestExpandAndExploreVersionConsistency(t *testing.T) {
	ctx := context.Background()
	service, store, base := newExploreFixture(t)
	const generation = int64(1)
	old := graphVersionedFact(base.ID, generation, "fact-128k", "context window is 128k", "0.2", semantic.UnitSuperseded, semantic.UnitID(base.ID, generation, semantic.UnitFact, "fact-1m"))
	latest := graphVersionedFact(base.ID, generation, "fact-1m", "context window is 1M", "0.3", semantic.UnitActive, "")
	relation := graphRelation(base.ID, generation, semantic.RelationSupersedes, old, latest, nil)
	mustActivateCompilation(t, store, semantic.Compilation{
		BaseID: base.ID, Generation: generation,
		Units: []semantic.Unit{old, latest}, Relations: []semantic.Relation{relation},
	})
	kinds := []semantic.UnitKind{semantic.UnitFact}

	current, err := service.Explore(ctx, ExploreRequest{
		Query: "context window", BaseIDs: []string{base.ID}, SemanticKinds: kinds,
	})
	if err != nil {
		t.Fatalf("current explore: %v", err)
	}
	currentGraph := current.Graphs[0]
	if len(currentGraph.Seeds) != 1 || currentGraph.Seeds[0].UnitID != latest.ID {
		t.Fatalf("current seeds: %+v", currentGraph.Seeds)
	}
	if len(currentGraph.Nodes) != 1 || len(currentGraph.Edges) != 0 {
		t.Fatalf("current graph leaked superseded history: %+v", currentGraph)
	}

	asOf, err := service.Explore(ctx, ExploreRequest{
		Query: "context window", BaseIDs: []string{base.ID}, SemanticKinds: kinds,
		VersionPolicy: VersionPolicy{Type: VersionAsOf, AsOf: "0.2"},
	})
	if err != nil {
		t.Fatalf("as-of explore: %v", err)
	}
	asOfGraph := asOf.Graphs[0]
	if len(asOfGraph.Seeds) != 1 || asOfGraph.Seeds[0].UnitID != old.ID {
		t.Fatalf("as-of seeds: %+v", asOfGraph.Seeds)
	}
	if len(asOfGraph.Nodes) != 1 || asOfGraph.Nodes[0].Unit.ID != old.ID || len(asOfGraph.Edges) != 0 {
		t.Fatalf("as-of graph leaked newer version: %+v", asOfGraph)
	}

	evolution, err := service.Explore(ctx, ExploreRequest{
		Query: "context window", BaseIDs: []string{base.ID}, SemanticKinds: kinds,
		VersionPolicy: VersionPolicy{Type: VersionAll},
	})
	if err != nil {
		t.Fatalf("evolution explore: %v", err)
	}
	if len(evolution.Graphs[0].Nodes) != 2 || len(evolution.Graphs[0].Edges) != 1 {
		t.Fatalf("evolution graph: %+v", evolution.Graphs[0])
	}
	if evolution.Graphs[0].Edges[0].Relation.Type != semantic.RelationSupersedes ||
		evolution.Graphs[0].Edges[0].Conflict {
		t.Fatalf("supersedes edge: %+v", evolution.Graphs[0].Edges[0])
	}

	_, err = service.Expand(ctx, ExpandRequest{UnitID: old.ID, BaseIDs: []string{base.ID}})
	var appErr *Error
	if !errors.As(err, &appErr) || appErr.Code != ErrUnitNotFound ||
		!strings.Contains(appErr.Message, "version policy") {
		t.Fatalf("hidden unit error: %v", err)
	}

	visible, err := service.Expand(ctx, ExpandRequest{
		UnitID: old.ID, BaseIDs: []string{base.ID},
		VersionPolicy: VersionPolicy{Type: VersionAsOf, AsOf: "0.2"},
	})
	if err != nil {
		t.Fatalf("as-of expand: %v", err)
	}
	if len(visible.Graphs[0].Nodes) != 1 || visible.Graphs[0].Nodes[0].Unit.ID != old.ID {
		t.Fatalf("as-of expand nodes: %+v", visible.Graphs[0].Nodes)
	}
}

// TestGraphProvenanceClosure proves no ghost citations: node evidence resolves
// through derived_from to exact pointers, projection-only edges are explicit,
// and every emitted pointer exists in the stored compilation.
func TestGraphProvenanceClosure(t *testing.T) {
	ctx := context.Background()
	service, store, base := newExploreFixture(t)
	const generation = int64(1)
	kpi := graphUnit(base.ID, generation, semantic.UnitConcept, "concept-kpi", "KPI", "KPI is calculated from SDR counters.", "doc-a")
	page := graphUnit(base.ID, generation, semantic.UnitKnowledgePage, "page-summary", "Research summary", "Research summary of KPI material.", "doc-a")
	page.Sources = nil
	page.DerivedFrom = []string{kpi.ID}
	projection := graphRelation(base.ID, generation, semantic.RelationDerivedFrom, page, kpi, []semantic.EvidenceSource{})
	mustActivateCompilation(t, store, semantic.Compilation{
		BaseID: base.ID, Generation: generation,
		Units: []semantic.Unit{kpi, page}, Relations: []semantic.Relation{projection},
	})

	stored, err := service.knowledge.GetSemanticCompilation(ctx, base.ID)
	if err != nil {
		t.Fatalf("read stored compilation: %v", err)
	}
	allowed := map[string]bool{}
	for _, unit := range stored.Units {
		for _, source := range unit.Sources {
			allowed[evidenceKey(source)] = true
		}
	}
	for _, relation := range stored.Relations {
		for _, source := range relation.Sources {
			allowed[evidenceKey(source)] = true
		}
	}

	result, err := service.Explore(ctx, ExploreRequest{
		Query: "Research", BaseIDs: []string{base.ID},
		SemanticKinds: []semantic.UnitKind{semantic.UnitKnowledgePage},
	})
	if err != nil {
		t.Fatalf("explore: %v", err)
	}
	graph := result.Graphs[0]
	if len(graph.Seeds) != 1 || graph.Seeds[0].UnitID != page.ID {
		t.Fatalf("seeds: %+v", graph.Seeds)
	}
	if len(graph.Nodes) != 2 {
		t.Fatalf("nodes: %+v", graph.Nodes)
	}
	pageNode := findNode(graph.Nodes, page.ID)
	if pageNode == nil || !pageNode.IsSeed {
		t.Fatalf("page seed node: %+v", graph.Nodes)
	}
	if pageNode.EvidenceStatus != EvidenceExact || len(pageNode.Evidence) == 0 {
		t.Fatalf("derived node provenance: %+v", pageNode)
	}
	for _, source := range pageNode.Evidence {
		if !allowed[evidenceKey(source)] {
			t.Fatalf("ghost node citation: %+v", source)
		}
	}
	if len(graph.Edges) != 1 {
		t.Fatalf("edges: %+v", graph.Edges)
	}
	edge := graph.Edges[0]
	if edge.EvidenceStatus != EvidenceProjectionOnly || len(edge.Evidence) != 0 {
		t.Fatalf("projection edge provenance: %+v", edge)
	}
}

// TestExploreScopeDiagnostics proves per-base failures are explicit, partial
// answers are degraded, and a fully unusable scope is a stable error.
func TestExploreScopeDiagnostics(t *testing.T) {
	ctx := context.Background()
	service, store, base1 := newExploreFixture(t)
	base2, err := service.knowledge.CreateBase("Second", "no compilation", "", knowledge.BaseConfig{})
	if err != nil {
		t.Fatalf("create second base: %v", err)
	}
	const generation = int64(1)
	kpi := graphUnit(base1.ID, generation, semantic.UnitConcept, "concept-kpi", "KPI", "KPI is calculated from SDR counters.", "doc-a")
	mustActivateCompilation(t, store, semantic.Compilation{
		BaseID: base1.ID, Generation: generation, Units: []semantic.Unit{kpi},
	})

	partial, err := service.Explore(ctx, ExploreRequest{Query: "KPI"})
	if err != nil {
		t.Fatalf("partial explore: %v", err)
	}
	if !partial.Degraded || len(partial.Graphs) != 1 || partial.Graphs[0].BaseID != base1.ID {
		t.Fatalf("partial result: %+v", partial)
	}
	if len(partial.Diagnostics) != 1 || partial.Diagnostics[0].Code != "SEMANTIC_NOT_READY" ||
		partial.Diagnostics[0].BaseID != base2.ID {
		t.Fatalf("partial diagnostics: %+v", partial.Diagnostics)
	}

	_, err = service.Explore(ctx, ExploreRequest{Query: "KPI", BaseIDs: []string{base2.ID}})
	var appErr *Error
	if !errors.As(err, &appErr) || appErr.Code != ErrSemanticNotReady {
		t.Fatalf("unavailable scope error: %v", err)
	}

	pinned, err := service.Explore(ctx, ExploreRequest{
		Query: "KPI", BaseIDs: []string{base1.ID}, Generation: Generation("999"),
	})
	if !errors.As(err, &appErr) || appErr.Code != ErrSemanticNotReady {
		t.Fatalf("pinned scope error: %v", err)
	}
	if len(pinned.Diagnostics) != 1 || pinned.Diagnostics[0].Code != "GENERATION_NOT_FOUND" {
		t.Fatalf("pinned diagnostics: %+v", pinned.Diagnostics)
	}
}

func findNode(nodes []ExploreNode, unitID string) *ExploreNode {
	for i := range nodes {
		if nodes[i].Unit.ID == unitID {
			return &nodes[i]
		}
	}
	return nil
}
func findPath(paths []ExplorePath, unitID string) *ExplorePath {
	for i := range paths {
		if paths[i].UnitID == unitID {
			return &paths[i]
		}
	}
	return nil
}

func evidenceKey(source semantic.EvidenceSource) string {
	return strings.Join([]string{source.UnitID, source.DocumentID,
		strconv.FormatInt(source.IndexGeneration, 10), strconv.FormatInt(source.SourceVersion, 10),
		source.NodeID, source.ChunkID, strconv.Itoa(source.SourceOrder)}, "|")
}

// Package semantic defines the 0.4 Knowledge Unit model. It is deliberately
// separate from the existing evidence index: compiled units never replace
// Document IR nodes or chunks, and every unit must resolve to evidence either
// directly or through an explicit derived-from chain.
package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// UnitKind is the small, Shutu-native set of compiled knowledge types.
type UnitKind string

const (
	UnitFact          UnitKind = "fact"
	UnitConcept       UnitKind = "concept"
	UnitTopic         UnitKind = "topic"
	UnitSummary       UnitKind = "summary"
	UnitKnowledgePage UnitKind = "knowledge_page"
)

// UnitStatus records lifecycle rather than truth. A conflicted unit remains
// queryable and provenance-backed until compilation resolves or supersedes it.
type UnitStatus string

const (
	UnitActive     UnitStatus = "active"
	UnitSuperseded UnitStatus = "superseded"
	UnitConflicted UnitStatus = "conflicted"
	UnitDeleted    UnitStatus = "deleted"
)

// RelationType is intentionally limited to relations that have a concrete
// retrieval, provenance, update, or conflict-management use.
type RelationType string

const (
	RelationDerivedFrom RelationType = "derived_from"
	RelationPartOf      RelationType = "part_of"
	RelationMentions    RelationType = "mentions"
	RelationRelatedTo   RelationType = "related_to"
	RelationSupports    RelationType = "supports"
	RelationContradicts RelationType = "contradicts"
	RelationSupersedes  RelationType = "supersedes"
	RelationSameTopic   RelationType = "same_topic"
)

// CompilationState is persisted for atomic visibility and restart recovery.
type CompilationState string

const (
	CompilationBuilding CompilationState = "building"
	CompilationActive   CompilationState = "active"
	CompilationRetired  CompilationState = "retired"
	CompilationFailed   CompilationState = "failed"
)

// EvidenceSource is one exact pointer into a document generation. NodeID and
// ChunkID are optional individually because an IR-only or compatibility-only
// projection can have one side absent, but at least one must be present.
type EvidenceSource struct {
	UnitID          string         `json:"unitId,omitempty"`
	DocumentID      string         `json:"documentId"`
	IndexGeneration int64          `json:"indexGeneration"`
	SourceVersion   int64          `json:"sourceVersion"`
	NodeID          string         `json:"nodeId,omitempty"`
	ChunkID         string         `json:"chunkId,omitempty"`
	SourceAnchor    map[string]any `json:"sourceAnchor,omitempty"`
	SourceOrder     int            `json:"sourceOrder,omitempty"`
}

// Unit is one addressable piece of compiled semantic memory.
type Unit struct {
	ID           string            `json:"id"`
	BaseID       string            `json:"baseId"`
	Generation   int64             `json:"generation"`
	Type         UnitKind          `json:"type"`
	Title        string            `json:"title"`
	CanonicalKey string            `json:"canonicalKey"`
	Content      string            `json:"content"`
	Aliases      []string          `json:"aliases,omitempty"`
	ParentUnitID string            `json:"parentUnitId,omitempty"`
	Confidence   float64           `json:"confidence"`
	Status       UnitStatus        `json:"status"`
	ValidFrom    int64             `json:"validFrom,omitempty"`
	ValidTo      int64             `json:"validTo,omitempty"`
	SupersededBy string            `json:"supersededBy,omitempty"`
	// Version-aware fields are optional and normalize to UNKNOWN/null. They
	// are persisted with the existing metadata envelope for 0.4 migration.
	Version         string            `json:"version,omitempty"`
	PublishedAt     int64             `json:"publishedAt,omitempty"`
	EffectiveAt     int64             `json:"effectiveAt,omitempty"`
	SourceAuthority int               `json:"sourceAuthority,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	Sources         []EvidenceSource  `json:"sources,omitempty"`
	DerivedFrom     []string          `json:"derivedFrom,omitempty"`
	CreatedAt       int64             `json:"createdAt"`
	UpdatedAt       int64             `json:"updatedAt"`
}

// Relation connects two units. It is not a general property-graph schema.
type Relation struct {
	ID            string            `json:"id"`
	BaseID        string            `json:"baseId"`
	Generation    int64             `json:"generation"`
	Type          RelationType      `json:"type"`
	SubjectUnitID string            `json:"subjectUnitId"`
	ObjectUnitID  string            `json:"objectUnitId"`
	Predicate     string            `json:"predicate,omitempty"`
	Statement     string            `json:"statement,omitempty"`
	CanonicalKey  string            `json:"canonicalKey"`
	Confidence    float64           `json:"confidence"`
	Status        UnitStatus        `json:"status"`
	ValidFrom     int64             `json:"validFrom,omitempty"`
	ValidTo       int64             `json:"validTo,omitempty"`
	SupersededBy  string            `json:"supersededBy,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Sources       []EvidenceSource  `json:"sources,omitempty"`
	CreatedAt     int64             `json:"createdAt"`
	UpdatedAt     int64             `json:"updatedAt"`
}

// Compilation is the immutable unit/relation set produced by one compiler run.
type Compilation struct {
	BaseID            string            `json:"baseId"`
	Generation        int64             `json:"generation"`
	State             CompilationState  `json:"state"`
	Compiler          string            `json:"compiler"`
	CompilerVersion   string            `json:"compilerVersion"`
	Model             string            `json:"model"`
	ModelVersion      string            `json:"modelVersion"`
	PromptVersion     string            `json:"promptVersion"`
	SourceDocumentIDs []string          `json:"sourceDocumentIds,omitempty"`
	Metadata          map[string]string `json:"metadata,omitempty"`
	Units             []Unit            `json:"units,omitempty"`
	Relations         []Relation        `json:"relations,omitempty"`
	CreatedAt         int64             `json:"createdAt"`
	CompletedAt       int64             `json:"completedAt,omitempty"`
}

// UnitID deterministically identifies a normalized unit within one immutable
// compilation generation.
func UnitID(baseID string, generation int64, kind UnitKind, canonicalKey string) string {
	seed := strings.Join([]string{baseID, fmt.Sprint(generation), string(kind), canonicalKey}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "ku_" + hex.EncodeToString(sum[:16])
}

// RelationID deterministically identifies a normalized relation.
func RelationID(baseID string, generation int64, relationType RelationType, subjectID, objectID, canonicalKey string) string {
	seed := strings.Join([]string{baseID, fmt.Sprint(generation), string(relationType), subjectID, objectID, canonicalKey}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "kr_" + hex.EncodeToString(sum[:16])
}

// Validate checks one source pointer without assuming SQL null semantics.
func (s EvidenceSource) Validate() error {
	if strings.TrimSpace(s.DocumentID) == "" {
		return fmt.Errorf("knowledge evidence source has no document ID")
	}
	if s.IndexGeneration <= 0 || s.SourceVersion <= 0 {
		return fmt.Errorf("knowledge evidence source %s has invalid generation/version", s.DocumentID)
	}
	if strings.TrimSpace(s.NodeID) == "" && strings.TrimSpace(s.ChunkID) == "" {
		return fmt.Errorf("knowledge evidence source %s has neither an IR node nor chunk", s.DocumentID)
	}
	return nil
}

// Validate checks local unit invariants. Graph references are checked by
// Compilation.Validate because derived and parent units may appear in any
// order in the compiler output.
func (u Unit) Validate() error {
	if strings.TrimSpace(u.ID) == "" {
		return fmt.Errorf("knowledge unit has no ID")
	}
	switch u.Type {
	case UnitFact, UnitConcept, UnitTopic, UnitSummary, UnitKnowledgePage:
	default:
		return fmt.Errorf("knowledge unit %s has unsupported type %q", u.ID, u.Type)
	}
	if strings.TrimSpace(u.BaseID) == "" || u.Generation <= 0 {
		return fmt.Errorf("knowledge unit %s has no base/generation scope", u.ID)
	}
	if strings.TrimSpace(u.Title) == "" || strings.TrimSpace(u.CanonicalKey) == "" || strings.TrimSpace(u.Content) == "" {
		return fmt.Errorf("knowledge unit %s must have title, canonical key, and content", u.ID)
	}
	if u.Confidence < 0 || u.Confidence > 1 {
		return fmt.Errorf("knowledge unit %s confidence %v is outside [0,1]", u.ID, u.Confidence)
	}
	if u.Status == "" {
		u.Status = UnitActive
	}
	switch u.Status {
	case UnitActive, UnitSuperseded, UnitConflicted, UnitDeleted:
	default:
		return fmt.Errorf("knowledge unit %s has unsupported status %q", u.ID, u.Status)
	}
	if u.ValidFrom != 0 && u.ValidTo != 0 && u.ValidTo < u.ValidFrom {
		return fmt.Errorf("knowledge unit %s has inverted validity interval", u.ID)
	}
	if u.ParentUnitID == u.ID {
		return fmt.Errorf("knowledge unit %s is its own parent", u.ID)
	}
	if u.Status == UnitSuperseded && strings.TrimSpace(u.SupersededBy) == "" {
		return fmt.Errorf("superseded knowledge unit %s has no successor", u.ID)
	}
	if u.SupersededBy == u.ID {
		return fmt.Errorf("knowledge unit %s supersedes itself", u.ID)
	}
	if len(u.Sources) == 0 && len(u.DerivedFrom) == 0 {
		return fmt.Errorf("knowledge unit %s has neither evidence nor derived_from provenance", u.ID)
	}
	seenSource := map[int]bool{}
	for _, source := range u.Sources {
		if source.SourceOrder < 0 {
			return fmt.Errorf("knowledge unit %s has a negative source order", u.ID)
		}
		if seenSource[source.SourceOrder] {
			return fmt.Errorf("knowledge unit %s repeats source order %d", u.ID, source.SourceOrder)
		}
		seenSource[source.SourceOrder] = true
		if err := source.Validate(); err != nil {
			return fmt.Errorf("unit %s: %w", u.ID, err)
		}
	}
	seenDerived := map[string]bool{}
	for _, id := range u.DerivedFrom {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("knowledge unit %s has an empty derived_from target", u.ID)
		}
		if id == u.ID {
			return fmt.Errorf("knowledge unit %s derives from itself", u.ID)
		}
		if seenDerived[id] {
			return fmt.Errorf("knowledge unit %s repeats derived_from target %s", u.ID, id)
		}
		seenDerived[id] = true
	}
	if want := UnitID(u.BaseID, u.Generation, u.Type, u.CanonicalKey); u.ID != want {
		return fmt.Errorf("knowledge unit ID %s is not deterministic (want %s)", u.ID, want)
	}
	return nil
}

// Validate checks local relation invariants.
func (r Relation) Validate() error {
	if strings.TrimSpace(r.ID) == "" {
		return fmt.Errorf("knowledge relation has no ID")
	}
	switch r.Type {
	case RelationDerivedFrom, RelationPartOf, RelationMentions, RelationRelatedTo,
		RelationSupports, RelationContradicts, RelationSupersedes, RelationSameTopic:
	default:
		return fmt.Errorf("knowledge relation %s has unsupported type %q", r.ID, r.Type)
	}
	if strings.TrimSpace(r.BaseID) == "" || r.Generation <= 0 {
		return fmt.Errorf("knowledge relation %s has no base/generation scope", r.ID)
	}
	if strings.TrimSpace(r.SubjectUnitID) == "" || strings.TrimSpace(r.ObjectUnitID) == "" {
		return fmt.Errorf("knowledge relation %s has an empty endpoint", r.ID)
	}
	if r.SubjectUnitID == r.ObjectUnitID {
		return fmt.Errorf("knowledge relation %s is self-referential", r.ID)
	}
	if r.Status == UnitSuperseded && strings.TrimSpace(r.SupersededBy) == "" {
		return fmt.Errorf("superseded knowledge relation %s has no successor", r.ID)
	}
	if r.SupersededBy == r.ID {
		return fmt.Errorf("knowledge relation %s supersedes itself", r.ID)
	}
	if strings.TrimSpace(r.CanonicalKey) == "" {
		return fmt.Errorf("knowledge relation %s has no canonical key", r.ID)
	}
	if len(r.Sources) == 0 && r.Type != RelationDerivedFrom {
		return fmt.Errorf("knowledge relation %s has neither evidence nor derived_from provenance", r.ID)
	}
	if r.Confidence < 0 || r.Confidence > 1 {
		return fmt.Errorf("knowledge relation %s confidence %v is outside [0,1]", r.ID, r.Confidence)
	}
	if r.Status == "" {
		r.Status = UnitActive
	}
	switch r.Status {
	case UnitActive, UnitSuperseded, UnitConflicted, UnitDeleted:
	default:
		return fmt.Errorf("knowledge relation %s has unsupported status %q", r.ID, r.Status)
	}
	if r.ValidFrom != 0 && r.ValidTo != 0 && r.ValidTo < r.ValidFrom {
		return fmt.Errorf("knowledge relation %s has inverted validity interval", r.ID)
	}
	for _, source := range r.Sources {
		if err := source.Validate(); err != nil {
			return fmt.Errorf("relation %s: %w", r.ID, err)
		}
	}
	if want := RelationID(r.BaseID, r.Generation, r.Type, r.SubjectUnitID, r.ObjectUnitID, r.CanonicalKey); r.ID != want {
		return fmt.Errorf("knowledge relation ID %s is not deterministic (want %s)", r.ID, want)
	}
	return nil
}

// Validate verifies the complete compilation graph before an atomic write.
func (c Compilation) Validate() error {
	if strings.TrimSpace(c.BaseID) == "" || c.Generation <= 0 {
		return fmt.Errorf("knowledge compilation has no base/generation scope")
	}
	if c.State == "" {
		c.State = CompilationBuilding
	}
	switch c.State {
	case CompilationBuilding, CompilationActive, CompilationRetired, CompilationFailed:
	default:
		return fmt.Errorf("knowledge compilation has unsupported state %q", c.State)
	}
	if strings.TrimSpace(c.Compiler) == "" || strings.TrimSpace(c.CompilerVersion) == "" ||
		strings.TrimSpace(c.Model) == "" || strings.TrimSpace(c.ModelVersion) == "" ||
		strings.TrimSpace(c.PromptVersion) == "" {
		return fmt.Errorf("knowledge compilation %s/%d lacks compiler/model/prompt provenance", c.BaseID, c.Generation)
	}
	if c.CreatedAt <= 0 {
		return fmt.Errorf("knowledge compilation %s/%d lacks creation time", c.BaseID, c.Generation)
	}

	units := map[string]Unit{}
	unitCanonical := map[string]string{}
	for _, unit := range c.Units {
		if err := unit.Validate(); err != nil {
			return err
		}
		if unit.BaseID != c.BaseID || unit.Generation != c.Generation {
			return fmt.Errorf("knowledge unit %s does not belong to compilation %s/%d", unit.ID, c.BaseID, c.Generation)
		}
		if _, exists := units[unit.ID]; exists {
			return fmt.Errorf("duplicate knowledge unit ID %s", unit.ID)
		}
		canonical := string(unit.Type) + "\x00" + unit.CanonicalKey
		if prior, exists := unitCanonical[canonical]; exists {
			return fmt.Errorf("duplicate canonical unit %q (%s and %s)", unit.CanonicalKey, prior, unit.ID)
		}
		unitCanonical[canonical] = unit.ID
		units[unit.ID] = unit
	}

	for _, unit := range c.Units {
		if unit.ParentUnitID != "" {
			if _, ok := units[unit.ParentUnitID]; !ok {
				return fmt.Errorf("knowledge unit %s references missing parent %s", unit.ID, unit.ParentUnitID)
			}
		}
		for _, derived := range unit.DerivedFrom {
			if _, ok := units[derived]; !ok {
				return fmt.Errorf("knowledge unit %s derives from missing unit %s", unit.ID, derived)
			}
		}
		if unit.SupersededBy != "" {
			if _, ok := units[unit.SupersededBy]; !ok {
				return fmt.Errorf("knowledge unit %s references missing successor %s", unit.ID, unit.SupersededBy)
			}
		}
	}
	if err := validateUnitAcyclic(c.Units); err != nil {
		return err
	}

	relations := map[string]Relation{}
	relationCanonical := map[string]string{}
	for _, relation := range c.Relations {
		if err := relation.Validate(); err != nil {
			return err
		}
		if relation.BaseID != c.BaseID || relation.Generation != c.Generation {
			return fmt.Errorf("knowledge relation %s does not belong to compilation %s/%d", relation.ID, c.BaseID, c.Generation)
		}
		if _, ok := units[relation.SubjectUnitID]; !ok {
			return fmt.Errorf("knowledge relation %s references missing subject %s", relation.ID, relation.SubjectUnitID)
		}
		if _, ok := units[relation.ObjectUnitID]; !ok {
			return fmt.Errorf("knowledge relation %s references missing object %s", relation.ID, relation.ObjectUnitID)
		}

		if _, exists := relations[relation.ID]; exists {
			return fmt.Errorf("duplicate knowledge relation ID %s", relation.ID)
		}
		canonical := string(relation.Type) + "\x00" + relation.CanonicalKey
		if prior, exists := relationCanonical[canonical]; exists {
			return fmt.Errorf("duplicate canonical relation %q (%s and %s)", relation.CanonicalKey, prior, relation.ID)
		}
		relationCanonical[canonical] = relation.ID
		relations[relation.ID] = relation
	}
	for _, relation := range c.Relations {
		if relation.SupersededBy != "" {
			if _, ok := relations[relation.SupersededBy]; !ok {
				return fmt.Errorf("knowledge relation %s references missing successor %s", relation.ID, relation.SupersededBy)
			}
		}
	}
	return nil
}

// ResolveEvidence follows source and derived_from links until every reachable
// exact evidence pointer is collected. It rejects missing targets and cycles,
// so a generated page can never claim support that cannot be audited.
func ResolveEvidence(units []Unit, unitID string) ([]EvidenceSource, error) {
	byID := make(map[string]Unit, len(units))
	for _, unit := range units {
		byID[unit.ID] = unit
	}
	seen := map[string]bool{}
	var out []EvidenceSource

	var visit func(unit Unit) error
	visit = func(unit Unit) error {
		if seen[unit.ID] {
			return nil
		}
		seen[unit.ID] = true
		out = append(out, unit.Sources...)
		for _, derived := range unit.DerivedFrom {
			next, ok := byID[derived]
			if !ok {
				return fmt.Errorf("knowledge unit %s derives from missing unit %s", unit.ID, derived)
			}
			if err := visit(next); err != nil {
				return err
			}
		}
		return nil
	}
	root, ok := byID[unitID]
	if !ok {
		return nil, fmt.Errorf("knowledge unit %s not found", unitID)
	}

	if err := visit(root); err != nil {
		return nil, err
	}
	SortEvidenceSources(out)
	return out, nil
}

// SortEvidenceSources makes provenance output reproducible.
func SortEvidenceSources(sources []EvidenceSource) {
	sort.SliceStable(sources, func(i, j int) bool {
		a, b := sources[i], sources[j]
		if a.DocumentID != b.DocumentID {
			return a.DocumentID < b.DocumentID
		}
		if a.IndexGeneration != b.IndexGeneration {
			return a.IndexGeneration < b.IndexGeneration
		}
		if a.NodeID != b.NodeID {
			return a.NodeID < b.NodeID
		}
		return a.ChunkID < b.ChunkID
	})
}

func validateUnitAcyclic(units []Unit) error {
	byID := make(map[string]Unit, len(units))
	for _, unit := range units {
		byID[unit.ID] = unit
	}
	const (
		white = 0
		gray  = 1
		black = 2
	)
	state := map[string]int{}
	var visit func(string) error
	visit = func(id string) error {
		switch state[id] {
		case black:
			return nil
		case gray:
			return fmt.Errorf("knowledge provenance cycle at unit %s", id)
		}
		state[id] = gray
		unit := byID[id]
		for _, nextID := range unit.DerivedFrom {
			if _, ok := byID[nextID]; !ok {
				return fmt.Errorf("knowledge unit %s derives from missing unit %s", id, nextID)
			}
			if err := visit(nextID); err != nil {
				return err
			}
		}
		if unit.ParentUnitID != "" {
			if _, ok := byID[unit.ParentUnitID]; !ok {
				return fmt.Errorf("knowledge unit %s references missing parent %s", id, unit.ParentUnitID)
			}
			if err := visit(unit.ParentUnitID); err != nil {
				return err
			}
		}
		state[id] = black
		return nil
	}
	ids := make([]string, 0, len(units))
	for _, unit := range units {
		ids = append(ids, unit.ID)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

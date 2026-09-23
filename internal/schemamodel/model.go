// Package schemamodel defines the generic, evidence-grounded schema semantic
// layer introduced in 0.7. It sits above Document IR and does not replace it.
package schemamodel

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Scope names the inclusive context that governs a schema entity. Scope is
// intentionally generic: workbook/sheet/table today, but directory/product/
// database/schema can use the same model later.
type Scope struct {
	Directory string `json:"directory,omitempty"`
	Product   string `json:"product,omitempty"`
	Workbook  string `json:"workbook,omitempty"`
	Sheet     string `json:"sheet,omitempty"`
	Table     string `json:"table,omitempty"`
}

// String returns a stable, human-readable scope key.
func (s Scope) String() string {
	parts := make([]string, 0, 5)
	for _, value := range []string{s.Directory, s.Product, s.Workbook, s.Sheet, s.Table} {
		if value = strings.TrimSpace(value); value != "" {
			parts = append(parts, value)
		}
	}
	if len(parts) == 0 {
		return "unscoped"
	}
	return strings.Join(parts, " / ")
}

// Key returns the canonical scope key used in entity identities.
func (s Scope) Key() string { return strings.ToLower(s.String()) }

// SourceRef is exact or explicitly degraded provenance for a schema entity.
type SourceRef struct {
	DocumentID    string `json:"document_id,omitempty"`
	DocumentTitle string `json:"document_title,omitempty"`
	NodeID        string `json:"node_id,omitempty"`
	SourcePath    string `json:"source_path,omitempty"`
	Sheet         string `json:"sheet,omitempty"`
	Range         string `json:"range,omitempty"`
	Generation    int64  `json:"generation,omitempty"`
	Granularity   string `json:"granularity,omitempty"`
	Context       string `json:"context,omitempty"`
}

// Granularity constants make degraded legacy provenance explicit.
const (
	RangeProvenance   = "workbook/sheet/range"
	SheetProvenance   = "workbook/sheet"
	SectionProvenance = "workbook/logical-section"
	UnknownProvenance = "unknown"
)

// Confidence bounds used by deterministic schema compilation.
const (
	MaxConfidence = 1.0
	MinConfidence = 0.0
)

// LogicalTable is one schema-like region. It may map to a workbook sheet or to
// a smaller region inside a sheet.
type LogicalTable struct {
	ID               string    `json:"id"`
	BaseID           string    `json:"base_id"`
	DocumentID       string    `json:"document_id"`
	Generation       int64     `json:"generation"`
	Name             string    `json:"name"`
	DisplayName      string    `json:"display_name,omitempty"`
	Aliases          []string  `json:"aliases,omitempty"`
	Description      string    `json:"description,omitempty"`
	BusinessPurpose  string    `json:"business_purpose,omitempty"`
	PurposeEvidence  string    `json:"purpose_evidence,omitempty"`
	PurposeInferred  bool      `json:"purpose_inferred,omitempty"`
	Scope            Scope     `json:"scope"`
	RegionAnchor     string    `json:"region_anchor,omitempty"`
	FieldIDs         []string  `json:"field_ids,omitempty"`
	Source           SourceRef `json:"source"`
	Confidence       float64   `json:"confidence"`
	DetectionSignals []string  `json:"detection_signals,omitempty"`
}

// EnumValue preserves the source order and value-to-label mapping.
type EnumValue struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// Enum is an ordered, evidence-backed value/domain mapping.
type Enum struct {
	ID          string      `json:"id"`
	FieldID     string      `json:"field_id"`
	Description string      `json:"description,omitempty"`
	Values      []EnumValue `json:"values"`
	Source      SourceRef   `json:"source"`
	Confidence  float64     `json:"confidence"`
}

// KeyCandidate types deliberately avoid database constraint semantics.
type KeyCandidateType string

const (
	KeyPrimary    KeyCandidateType = "PRIMARY_KEY_HINT"
	KeyIdentifier KeyCandidateType = "IDENTIFIER"
	KeyJoin       KeyCandidateType = "JOIN_CANDIDATE"
	KeyPartition  KeyCandidateType = "PARTITION_KEY"
	KeyTime       KeyCandidateType = "TIME_KEY"
	KeyUnknown    KeyCandidateType = "UNKNOWN"
)

// KeyCandidate is a probabilistic identifier/join hint, never a foreign key.
type KeyCandidate struct {
	ID         string           `json:"id"`
	FieldID    string           `json:"field_id"`
	Type       KeyCandidateType `json:"type"`
	Confidence float64          `json:"confidence"`
	Reason     string           `json:"reason"`
	Evidence   []SourceRef      `json:"evidence,omitempty"`
}

// Field identity is scope + table + field. Name alone is not an identity.
type Field struct {
	ID          string         `json:"id"`
	BaseID      string         `json:"base_id"`
	DocumentID  string         `json:"document_id"`
	Generation  int64          `json:"generation"`
	TableID     string         `json:"table_id"`
	TableName   string         `json:"table_name"`
	Scope       Scope          `json:"scope"`
	Name        string         `json:"name"`
	DisplayName string         `json:"display_name,omitempty"`
	Aliases     []string       `json:"aliases,omitempty"`
	DataType    string         `json:"data_type,omitempty"`
	Description string         `json:"description,omitempty"`
	Nullable    bool           `json:"nullable,omitempty"`
	Enum        *Enum          `json:"enum,omitempty"`
	KeyHints    []KeyCandidate `json:"key_hints,omitempty"`
	Concepts    []string       `json:"concepts,omitempty"`
	Source      SourceRef      `json:"source"`
	Confidence  float64        `json:"confidence"`
}

// BusinessConcept maps a generic concept to candidate fields. It is not an
// ontology and never asserts that similar fields are equivalent.
type BusinessConcept struct {
	ID          string      `json:"id"`
	BaseID      string      `json:"base_id"`
	Generation  int64       `json:"generation"`
	Name        string      `json:"name"`
	Aliases     []string    `json:"aliases,omitempty"`
	FieldIDs    []string    `json:"field_ids,omitempty"`
	DerivedFrom []SourceRef `json:"derived_from,omitempty"`
	Confidence  float64     `json:"confidence"`
}

// Model is an immutable schema compilation snapshot.
type Model struct {
	BaseID        string            `json:"base_id"`
	Generation    int64             `json:"generation"`
	Tables        []LogicalTable    `json:"tables,omitempty"`
	Fields        []Field           `json:"fields,omitempty"`
	Enums         []Enum            `json:"enums,omitempty"`
	Concepts      []BusinessConcept `json:"concepts,omitempty"`
	KeyCandidates []KeyCandidate    `json:"key_candidates,omitempty"`
	Diagnostics   []Diagnostic      `json:"diagnostics,omitempty"`
}

// Diagnostic is a bounded compile-time observation.
type Diagnostic struct {
	Code     string    `json:"code"`
	Message  string    `json:"message"`
	Severity string    `json:"severity"`
	Source   SourceRef `json:"source,omitempty"`
}

// Identity returns the required composite field identity.
func (f Field) Identity() string {
	return strings.Join([]string{f.Scope.Key(), normalizeIdentity(f.TableName), normalizeIdentity(f.Name)}, "\x00")
}

// TableIdentity returns the normalized logical-table identity.
func (t LogicalTable) Identity() string {
	return strings.Join([]string{t.Scope.Key(), normalizeIdentity(t.Name)}, "\x00")
}

// EntityID creates deterministic identifiers without exposing source values.
func EntityID(kind, baseID string, generation int64, identity string) string {
	seed := strings.Join([]string{kind, baseID, fmt.Sprint(generation), identity}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return kind + "_" + hex.EncodeToString(sum[:16])
}

// NormalizeText applies conservative whitespace normalization without changing
// domain spelling.
func NormalizeText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

// NormalizeIdentifier lowercases ASCII and preserves CJK text for IDs.
func NormalizeIdentifier(value string) string {
	return strings.ToLower(NormalizeText(value))
}

// SortCanonical makes compilation output deterministic for storage and tests.
func (m *Model) SortCanonical() {
	sort.SliceStable(m.Tables, func(i, j int) bool { return m.Tables[i].ID < m.Tables[j].ID })
	sort.SliceStable(m.Fields, func(i, j int) bool { return m.Fields[i].ID < m.Fields[j].ID })
	sort.SliceStable(m.Enums, func(i, j int) bool { return m.Enums[i].ID < m.Enums[j].ID })
	sort.SliceStable(m.Concepts, func(i, j int) bool { return m.Concepts[i].ID < m.Concepts[j].ID })
	sort.SliceStable(m.KeyCandidates, func(i, j int) bool { return m.KeyCandidates[i].ID < m.KeyCandidates[j].ID })
	for i := range m.Tables {
		sort.Strings(m.Tables[i].Aliases)
		sort.Strings(m.Tables[i].FieldIDs)
		sort.Strings(m.Tables[i].DetectionSignals)
	}
	for i := range m.Fields {
		sort.Strings(m.Fields[i].Aliases)
		sort.SliceStable(m.Fields[i].KeyHints, func(a, b int) bool { return m.Fields[i].KeyHints[a].ID < m.Fields[i].KeyHints[b].ID })
	}
	for i := range m.Concepts {
		sort.Strings(m.Concepts[i].Aliases)
		sort.Strings(m.Concepts[i].FieldIDs)
	}
}

// Validate rejects entities that violate identity/provenance invariants.
func (m Model) Validate() error {
	if m.BaseID == "" || m.Generation <= 0 {
		return fmt.Errorf("schema model requires base id and generation")
	}
	fields := make(map[string]Field, len(m.Fields))
	identities := make(map[string]string, len(m.Fields))
	for _, field := range m.Fields {
		if field.ID == "" || field.Name == "" || field.TableID == "" {
			return fmt.Errorf("schema field %q lacks id/name/table", field.ID)
		}
		if field.Scope.Table == "" || field.TableName == "" {
			return fmt.Errorf("schema field %q lost table scope", field.ID)
		}
		if field.DocumentID == "" || field.Generation == 0 || field.Source.DocumentID == "" {
			return fmt.Errorf("schema field %q lacks document provenance", field.ID)
		}
		if other, exists := identities[field.Identity()]; exists {
			return fmt.Errorf("duplicate field identity %q: %s/%s", field.Identity(), other, field.ID)
		}
		identities[field.Identity()] = field.ID
		fields[field.ID] = field
	}
	tables := make(map[string]LogicalTable, len(m.Tables))
	for _, table := range m.Tables {
		if table.ID == "" || table.Name == "" {
			return fmt.Errorf("schema table %q lacks id/name", table.ID)
		}
		if table.Source.DocumentID == "" {
			return fmt.Errorf("schema table %q lacks document provenance", table.ID)
		}
		tables[table.ID] = table
	}
	for _, table := range m.Tables {
		for _, fieldID := range table.FieldIDs {
			field, ok := fields[fieldID]
			if !ok {
				return fmt.Errorf("table %q references missing field %q", table.ID, fieldID)
			}
			if field.TableID != table.ID {
				return fmt.Errorf("table %q owns mismatched field %q", table.ID, fieldID)
			}
		}
	}
	for _, enum := range m.Enums {
		if enum.ID == "" || enum.FieldID == "" || len(enum.Values) == 0 {
			return fmt.Errorf("schema enum %q lacks field/values", enum.ID)
		}
		if _, ok := fields[enum.FieldID]; !ok {
			return fmt.Errorf("schema enum %q references missing field", enum.ID)
		}
	}
	for _, concept := range m.Concepts {
		if concept.ID == "" || concept.Name == "" || len(concept.FieldIDs) == 0 {
			return fmt.Errorf("schema concept %q lacks name/fields", concept.ID)
		}
		for _, fieldID := range concept.FieldIDs {
			if _, ok := fields[fieldID]; !ok {
				return fmt.Errorf("schema concept %q references missing field", concept.ID)
			}
		}
	}
	for _, candidate := range m.KeyCandidates {
		if candidate.ID == "" || candidate.FieldID == "" || candidate.Reason == "" {
			return fmt.Errorf("schema key candidate %q lacks field/reason", candidate.ID)
		}
		if _, ok := fields[candidate.FieldID]; !ok {
			return fmt.Errorf("schema key candidate %q references missing field", candidate.ID)
		}
	}
	return nil
}

func normalizeIdentity(value string) string {
	return strings.ToLower(NormalizeText(value))
}

func clampConfidence(value float64) float64 {
	if value < MinConfidence {
		return MinConfidence
	}
	if value > MaxConfidence {
		return MaxConfidence
	}
	return value
}

func compactSourceRefs(values []SourceRef) []SourceRef {
	if len(values) == 0 {
		return nil
	}
	sort.SliceStable(values, func(i, j int) bool { return values[i].Range < values[j].Range })
	out := values[:0]
	var previous SourceRef
	for i, value := range values {
		if i > 0 && value == previous {
			continue
		}
		out = append(out, value)
		previous = value
	}
	return out
}

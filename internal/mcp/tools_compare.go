package mcp

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shutu-ai/shutu-knowledge/internal/semantic"
)

type compareVersionsInput struct {
	BaseIDs         []string `json:"base_ids,omitempty" jsonschema:"knowledge bases to compare"`
	Query           string   `json:"query,omitempty" jsonschema:"optional query to narrow compared units"`
	From            string   `json:"from" jsonschema:"source version identity of the older side"`
	To              string   `json:"to" jsonschema:"source version identity of the newer side"`
	Generation      string   `json:"generation,omitempty" jsonschema:"semantic generation: active or a positive integer"`
	TimeDimension   string   `json:"time_dimension,omitempty" jsonschema:"only source_version is supported today"`
	IncludeEvidence bool     `json:"include_evidence,omitempty" jsonschema:"include exact evidence pointers per entry"`
}

func (s *server) registerCompareTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name: "knowledge_compare_versions", Title: "Compare versions",
		Description: "Compare compiled units between two source versions and report added, removed, changed, conflicting, and uncertain entries.",
	}, s.toolCompareVersions)
}

func (s *server) toolCompareVersions(ctx context.Context, req *mcp.CallToolRequest, input compareVersionsInput) (*mcp.CallToolResult, any, error) {
	return runTool(s, "knowledge_compare_versions", input, func(traceID string) (*Envelope, error) {
		from := strings.TrimSpace(input.From)
		to := strings.TrimSpace(input.To)
		if from == "" || to == "" {
			return errorEnvelope(traceID, CodeInvalidArgument, "from and to version identities are required"), nil
		}
		if input.TimeDimension != "" && input.TimeDimension != "source_version" {
			return errorEnvelope(traceID, CodeVersionUnsupported,
				fmt.Sprintf("time dimension %q is not supported; only source_version", input.TimeDimension)), nil
		}
		if _, ok := semantic.NormalizeVersionIdentity(from); !ok {
			return errorEnvelope(traceID, CodeVersionUnsupported, fmt.Sprintf("invalid from version %q", input.From)), nil
		}
		if _, ok := semantic.NormalizeVersionIdentity(to); !ok {
			return errorEnvelope(traceID, CodeVersionUnsupported, fmt.Sprintf("invalid to version %q", input.To)), nil
		}
		scope, err := s.agentQuery.ResolveScope(ctx, input.BaseIDs)
		if err != nil {
			code, message := mapAgentQueryError(err)
			return errorEnvelope(traceID, code, message), nil
		}
		reports := make([]map[string]any, 0, len(scope))
		anyReady := false
		warnings := []string{}
		for _, baseID := range scope {
			compilation, snapshotErr := s.semanticCompilation(ctx, baseID, generationFromInput(input.Generation))
			if snapshotErr != nil {
				warnings = append(warnings, "SEMANTIC_NOT_READY")
				reports = append(reports, map[string]any{"base_id": baseID, "error": "no usable semantic generation"})
				continue
			}
			anyReady = true
			reports = append(reports, compareBaseVersions(compilation, baseID, from, to, input.Query, input.IncludeEvidence))
		}
		if !anyReady {
			return errorEnvelope(traceID, CodeSemanticNotReady, "no requested base has an active or pinned semantic generation"), nil
		}
		return okEnvelope(traceID, map[string]any{
			"from": from, "to": to, "bases": reports,
		}, &EnvelopeContext{BaseIDs: scope}, dedupeStrings(warnings), false), nil
	})
}

// compareBaseVersions diffs one compilation across two explicit source
// versions. Absence of a record is only reported as removed when the surviving
// history proves supersession; unknown version identities stay uncertain
// instead of inventing deletions.
func compareBaseVersions(compilation semantic.Compilation, baseID, from, to, query string, includeEvidence bool) map[string]any {
	fromTemporal := semantic.TemporalQuery{Intent: semantic.TemporalExplicitVersion, Versions: []string{from}, Confidence: 1}
	toTemporal := semantic.TemporalQuery{Intent: semantic.TemporalExplicitVersion, Versions: []string{to}, Confidence: 1}
	fromSet := visibleUnitsByKey(compilation, fromTemporal, query)
	toSet := visibleUnitsByKey(compilation, toTemporal, query)

	unitView := func(unit semantic.Unit) map[string]any {
		view := map[string]any{
			"unit_id": unit.ID, "kind": string(unit.Type),
			"canonical_key": unit.CanonicalKey, "title": unit.Title,
			"content": unit.Content, "version": unit.Version,
			"status": string(unit.Status), "side": sideOf(unit.Version, from, to),
		}
		if includeEvidence {
			evidence, err := semantic.ResolveEvidence(compilation.Units, unit.ID)
			if err == nil {
				view["evidence"] = evidencePointers(evidence)
			}
		}
		return view
	}

	conflicted := false
	uncertain := false
	added := []map[string]any{}
	removed := []map[string]any{}
	changed := []map[string]any{}
	unchanged := 0
	for key, fromUnit := range fromSet {
		toUnit, ok := toSet[key]
		if !ok {
			if fromUnit.Version == "" {
				uncertain = true
			} else {
				removed = append(removed, unitView(fromUnit))
			}
			continue
		}
		if fromUnit.Content != toUnit.Content {
			changed = append(changed, map[string]any{
				"canonical_key": fromUnit.CanonicalKey, "kind": string(fromUnit.Type),
				"before": unitView(fromUnit), "after": unitView(toUnit),
			})
		} else {
			unchanged++
		}
		if fromUnit.Status == semantic.UnitConflicted || toUnit.Status == semantic.UnitConflicted {
			conflicted = true
		}
	}
	for key, toUnit := range toSet {
		if _, ok := fromSet[key]; ok {
			continue
		}
		if toUnit.Version == "" {
			uncertain = true
		}
		added = append(added, unitView(toUnit))
	}
	for _, fromUnit := range fromSet {
		if fromUnit.Status == semantic.UnitConflicted {
			conflicted = true
		}
	}
	sortEntries(added)
	sortEntries(removed)
	return map[string]any{
		"base_id": baseID, "generation": compilation.Generation,
		"added": added, "removed": removed, "changed": changed,
		"unchanged_count": unchanged, "conflicting": conflicted, "uncertain": uncertain,
	}
}

func sideOf(version, from, to string) string {
	switch {
	case strings.EqualFold(version, from):
		return "from"
	case strings.EqualFold(version, to):
		return "to"
	default:
		return version
	}
}

func visibleUnitsByKey(compilation semantic.Compilation, temporal semantic.TemporalQuery, query string) map[string]semantic.Unit {
	resolved, _ := semantic.ResolveCurrentVersion(compilation)
	out := map[string]semantic.Unit{}
	var terms []string
	if strings.TrimSpace(query) != "" {
		terms = semantic.SearchTerms(query)
	}
	for _, unit := range compilation.Units {
		if !semantic.TemporalVersionVisible(unit, temporal, resolved) {
			continue
		}
		if terms != nil && !unitMatchesAnyTerm(unit, terms) {
			continue
		}
		out[string(unit.Type)+"\x00"+unit.CanonicalKey] = unit
	}
	return out
}

func unitMatchesAnyTerm(unit semantic.Unit, terms []string) bool {
	haystack := strings.ToLower(unit.Title + "\x00" + unit.CanonicalKey + "\x00" + unit.Content)
	for _, term := range terms {
		if strings.Contains(haystack, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func sortEntries(entries []map[string]any) {
	sort.Slice(entries, func(i, j int) bool {
		left, _ := entries[i]["canonical_key"].(string)
		right, _ := entries[j]["canonical_key"].(string)
		return left < right
	})
}

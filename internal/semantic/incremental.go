package semantic

import (
	"fmt"
	"sort"
	"strings"
)

// MergeIncremental combines unaffected units from the previous immutable
// generation with a compilation built only from dirty documents. It removes
// provenance belonging to dirty/deleted documents, drops units that lose all
// support, remaps deterministic IDs to the new generation, and merges canonical
// units so cross-document support survives a partial recompile.
func MergeIncremental(previous, delta Compilation, dirtyDocuments, deletedDocuments map[string]bool, currentSourceDocumentIDs []string, now int64) (Compilation, error) {
	if previous.BaseID != delta.BaseID {
		return Compilation{}, fmt.Errorf("incremental merge base mismatch: %q and %q", previous.BaseID, delta.BaseID)
	}
	if now <= 0 {
		return Compilation{}, fmt.Errorf("incremental merge requires a timestamp")
	}
	allDirty := make(map[string]bool, len(dirtyDocuments)+len(deletedDocuments))
	for documentID := range dirtyDocuments {
		allDirty[documentID] = true
	}
	for documentID := range deletedDocuments {
		allDirty[documentID] = true
	}

	retained, dropped := sanitizePreviousUnits(previous.Units, allDirty)
	retained = fixRetainedProvenance(retained, dropped)
	retainedRelations := sanitizePreviousRelations(previous.Relations, retained, allDirty)
	oldToNew := remapIDs(retained, delta.Generation)

	units := make([]Unit, 0, len(retained)+len(delta.Units))
	merged := map[string]Unit{}
	for _, unit := range retained {
		next := remapUnit(unit, delta.BaseID, delta.Generation, oldToNew, now)
		units = append(units, next)
		merged[unitKey(next)] = next
	}
	for _, unit := range delta.Units {
		key := unitKey(unit)
		existing, ok := merged[key]
		if !ok {
			merged[key] = unit
			continue
		}
		merged[key] = mergeUnits(existing, unit, now)
	}
	unitOrder := make([]Unit, 0, len(merged))
	for _, unit := range merged {
		unitOrder = append(unitOrder, unit)
	}
	unitOrder = reconcileTemporalUnits(unitOrder, delta.BaseID, delta.Generation, now)
	sort.Slice(unitOrder, func(i, j int) bool {
		if unitOrder[i].Type != unitOrder[j].Type {
			return unitOrder[i].Type < unitOrder[j].Type
		}
		return unitOrder[i].CanonicalKey < unitOrder[j].CanonicalKey
	})

	relations := make([]Relation, 0, len(retainedRelations)+len(delta.Relations))
	mergedRelations := map[string]Relation{}
	for _, relation := range retainedRelations {
		next := remapRelation(relation, delta.BaseID, delta.Generation, oldToNew, now)
		if next.ID == "" {
			continue
		}
		relations = append(relations, next)
		mergedRelations[relationKey(next)] = next
	}
	for _, relation := range delta.Relations {
		key := relationKey(relation)
		if _, ok := mergedRelations[key]; !ok {
			mergedRelations[key] = relation
		}
	}
	relationOrder := make([]Relation, 0, len(mergedRelations))
	for _, relation := range mergedRelations {
		relationOrder = append(relationOrder, relation)
	}
	sort.Slice(relationOrder, func(i, j int) bool {
		if relationOrder[i].Type != relationOrder[j].Type {
			return relationOrder[i].Type < relationOrder[j].Type
		}
		return relationOrder[i].CanonicalKey < relationOrder[j].CanonicalKey
	})

	sourceIDs := normalizedIDs(currentSourceDocumentIDs)
	metadata := map[string]string{}
	for key, value := range previous.Metadata {
		metadata[key] = value
	}
	for key, value := range delta.Metadata {
		metadata[key] = value
	}
	mergedCompilation := Compilation{
		BaseID: delta.BaseID, Generation: delta.Generation, State: CompilationBuilding,
		Compiler: delta.Compiler, CompilerVersion: delta.CompilerVersion,
		Model: delta.Model, ModelVersion: delta.ModelVersion,
		PromptVersion: delta.PromptVersion, SourceDocumentIDs: sourceIDs,
		Metadata: metadata, Units: unitOrder, Relations: relationOrder,
		CreatedAt: now,
	}
	if err := mergedCompilation.Validate(); err != nil {
		return Compilation{}, fmt.Errorf("incremental semantic merge: %w", err)
	}
	return mergedCompilation, nil
}

func sanitizePreviousUnits(units []Unit, dirty map[string]bool) (map[string]Unit, map[string]bool) {
	retained := make(map[string]Unit, len(units))
	dropped := make(map[string]bool, len(units))
	for _, unit := range units {
		if unit.Type == UnitSummary && summaryDocumentID(unit.CanonicalKey) != "" && dirty[summaryDocumentID(unit.CanonicalKey)] {
			dropped[unit.ID] = true
			continue
		}
		next := unit
		next.Sources = nil
		for _, source := range unit.Sources {
			if !dirty[source.DocumentID] {
				next.Sources = append(next.Sources, source)
			}
		}
		if len(next.Sources) == 0 && len(next.DerivedFrom) == 0 {
			dropped[unit.ID] = true
			continue
		}
		retained[unit.ID] = next
	}
	return retained, dropped
}

func fixRetainedProvenance(units map[string]Unit, dropped map[string]bool) map[string]Unit {
	for {
		changed := false
		for id, unit := range units {
			next := unit
			derived := make([]string, 0, len(unit.DerivedFrom))
			for _, target := range unit.DerivedFrom {
				if !dropped[target] {
					derived = append(derived, target)
				}
			}
			next.DerivedFrom = derived
			if next.ParentUnitID != "" && dropped[next.ParentUnitID] {
				next.ParentUnitID = ""
			}
			if next.SupersededBy != "" && dropped[next.SupersededBy] {
				next.SupersededBy = ""
				if next.Status == UnitSuperseded {
					next.Status = UnitActive
				}
			}
			if len(next.Sources) == 0 && len(next.DerivedFrom) == 0 {
				delete(units, id)
				dropped[id] = true
				changed = true
				continue
			}
			if len(unit.DerivedFrom) != len(next.DerivedFrom) || unit.ParentUnitID != next.ParentUnitID || unit.SupersededBy != next.SupersededBy {
				units[id] = next
				changed = true
			}
		}
		if !changed {
			return units
		}
	}
}

func sanitizePreviousRelations(relations []Relation, units map[string]Unit, dirty map[string]bool) []Relation {
	out := make([]Relation, 0, len(relations))
	for _, relation := range relations {
		if _, ok := units[relation.SubjectUnitID]; !ok {
			continue
		}
		if _, ok := units[relation.ObjectUnitID]; !ok {
			continue
		}
		next := relation
		next.Sources = nil
		for _, source := range relation.Sources {
			if !dirty[source.DocumentID] {
				next.Sources = append(next.Sources, source)
			}
		}
		if len(next.Sources) == 0 && next.Type != RelationDerivedFrom {
			continue
		}
		if next.SupersededBy != "" {
			if _, ok := units[next.SupersededBy]; !ok {
				next.SupersededBy = ""
				if next.Status == UnitSuperseded {
					next.Status = UnitActive
				}
			}
		}
		out = append(out, next)
	}
	return out
}

func remapIDs(units map[string]Unit, generation int64) map[string]string {
	out := make(map[string]string, len(units))
	for _, unit := range units {
		out[unit.ID] = UnitID(unit.BaseID, generation, unit.Type, unit.CanonicalKey)
	}
	return out
}

func remapUnit(unit Unit, baseID string, generation int64, ids map[string]string, now int64) Unit {
	next := unit
	next.ID = UnitID(baseID, generation, unit.Type, unit.CanonicalKey)
	next.BaseID, next.Generation = baseID, generation
	next.CreatedAt, next.UpdatedAt = now, now
	next.DerivedFrom = remapIDsList(unit.DerivedFrom, ids)
	if unit.ParentUnitID != "" {
		next.ParentUnitID = ids[unit.ParentUnitID]
	}
	if unit.SupersededBy != "" {
		next.SupersededBy = ids[unit.SupersededBy]
	}
	return next
}

func remapRelation(relation Relation, baseID string, generation int64, ids map[string]string, now int64) Relation {
	subject, subjectOK := ids[relation.SubjectUnitID]
	object, objectOK := ids[relation.ObjectUnitID]
	if !subjectOK || !objectOK {
		return Relation{}
	}
	next := relation
	next.SubjectUnitID, next.ObjectUnitID = subject, object
	next.BaseID, next.Generation = baseID, generation
	next.ID = RelationID(baseID, generation, relation.Type, subject, object, relation.CanonicalKey)
	next.CreatedAt, next.UpdatedAt = now, now
	if relation.SupersededBy != "" {
		if successor, ok := ids[relation.SupersededBy]; ok {
			next.SupersededBy = successor
		}
	}
	return next
}

func mergeUnits(existing, update Unit, now int64) Unit {
	merged := existing
	merged.Title = firstNonEmpty(update.Title, existing.Title)
	merged.Content = firstNonEmpty(update.Content, existing.Content)
	merged.Aliases = mergeLists(existing.Aliases, update.Aliases)
	merged.DerivedFrom = mergeLists(existing.DerivedFrom, update.DerivedFrom)
	merged.Sources = mergeSources(existing.Sources, update.Sources)
	merged.Metadata = mergeStringMaps(existing.Metadata, update.Metadata)
	if update.Confidence > 0 {
		total := len(existing.Sources) + len(update.Sources)
		if total == 0 {
			merged.Confidence = update.Confidence
		} else {
			merged.Confidence = (existing.Confidence*float64(len(existing.Sources)) +
				update.Confidence*float64(len(update.Sources))) / float64(total)
		}
	}
	if update.Status != "" {
		merged.Status = update.Status
	}
	merged.UpdatedAt = now
	return merged
}

func mergeSources(existing, update []EvidenceSource) []EvidenceSource {
	seen := map[string]bool{}
	out := make([]EvidenceSource, 0, len(existing)+len(update))
	appendSource := func(source EvidenceSource) {
		key := fmt.Sprintf("%s\x00%d\x00%d\x00%s\x00%s", source.DocumentID, source.IndexGeneration,
			source.SourceVersion, source.NodeID, source.ChunkID)
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, source)
	}
	for _, source := range existing {
		appendSource(source)
	}
	for _, source := range update {
		appendSource(source)
	}
	SortEvidenceSources(out)
	for i := range out {
		out[i].SourceOrder = i
	}
	return out
}

func mergeLists(existing, update []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(existing)+len(update))
	for _, values := range [][]string{existing, update} {
		for _, value := range values {
			if value == "" || seen[value] {
				continue
			}
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func mergeStringMaps(existing, update map[string]string) map[string]string {
	out := make(map[string]string, len(existing)+len(update))
	for key, value := range existing {
		out[key] = value
	}
	for key, value := range update {
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func remapIDsList(ids []string, mapping map[string]string) []string {
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		next, ok := mapping[id]
		if !ok || seen[next] {
			continue
		}
		seen[next] = true
		out = append(out, next)
	}
	sort.Strings(out)
	if len(out) == 0 {
		return nil
	}
	return out
}

func unitKey(unit Unit) string {
	return string(unit.Type) + "\x00" + unit.CanonicalKey
}

func relationKey(relation Relation) string {
	return string(relation.Type) + "\x00" + relation.CanonicalKey
}

func summaryDocumentID(canonicalKey string) string {
	const prefix = "summary:doc:"
	if !strings.HasPrefix(canonicalKey, prefix) {
		return ""
	}
	remainder := strings.TrimPrefix(canonicalKey, prefix)
	if index := strings.Index(remainder, ":source:"); index >= 0 {
		return remainder[:index]
	}
	return ""
}

func normalizedIDs(ids []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

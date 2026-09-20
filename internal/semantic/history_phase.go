package semantic

import (
	"fmt"
	"sort"
	"strings"
)

// HistoricalPhase is a derived, regenerable projection over ordered Knowledge
// Units. It never creates a new durable graph and every key change points back
// to source units.
type HistoricalPhase struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	StartVersion  string            `json:"startVersion"`
	EndVersion    string            `json:"endVersion"`
	KeyChanges    []string          `json:"keyChanges,omitempty"`
	SourceUnitIDs []string          `json:"sourceUnitIds"`
	Evidence      []EvidenceSource  `json:"evidence,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Confidence    float64           `json:"confidence"`
}

// HistoricalTransition records one adjacent phase boundary with evidence.
type HistoricalTransition struct {
	FromPhaseID   string            `json:"fromPhaseId"`
	ToPhaseID     string            `json:"toPhaseId"`
	FromVersion   string            `json:"fromVersion"`
	ToVersion     string            `json:"toVersion"`
	Statement     string            `json:"statement"`
	SourceUnitIDs []string          `json:"sourceUnitIds"`
	Evidence      []EvidenceSource  `json:"evidence,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Confidence    float64           `json:"confidence"`
}

// HistoricalTimeline is the deterministic input to the history compiler.
type HistoricalTimeline struct {
	Phases      []HistoricalPhase      `json:"phases"`
	Transitions []HistoricalTransition `json:"transitions,omitempty"`
	Confidence  float64                `json:"confidence"`
	Diagnostics []string               `json:"diagnostics,omitempty"`
}

type versionUnitGroup struct {
	version string
	units   []Unit
}

func orderedVersionGroups(compilation Compilation) []versionUnitGroup {
	grouped := map[string][]Unit{}
	versions := make([]string, 0)
	for _, unit := range compilation.Units {
		if unit.Status == UnitDeleted || unit.Version == "" {
			continue
		}
		if _, ordered := parseVersionIdentity(unit.Version); !ordered {
			continue
		}
		if _, exists := grouped[unit.Version]; !exists {
			versions = append(versions, unit.Version)
		}
		grouped[unit.Version] = append(grouped[unit.Version], unit)
	}
	sort.SliceStable(versions, func(i, j int) bool {
		order, comparable := CompareVersionIdentity(versions[i], versions[j])
		if comparable && order != 0 {
			return order < 0
		}
		return versions[i] < versions[j]
	})
	out := make([]versionUnitGroup, 0, len(versions))
	for _, version := range versions {
		units := grouped[version]
		sort.SliceStable(units, func(i, j int) bool {
			left, right := unitRankForHistory(units[i]), unitRankForHistory(units[j])
			if left != right {
				return left < right
			}
			return units[i].CanonicalKey < units[j].canonicalKeySafe()
		})
		out = append(out, versionUnitGroup{version: version, units: units})
	}
	return out
}

// canonicalKeySafe keeps this helper local even if Unit grows methods later.
func (unit Unit) canonicalKeySafe() string { return unit.CanonicalKey }

func unitRankForHistory(unit Unit) int {
	switch unit.Type {
	case UnitFact:
		return 0
	case UnitSummary:
		return 1
	case UnitConcept:
		return 2
	case UnitTopic:
		return 3
	default:
		return 4
	}
}

func phaseSegments(groups []versionUnitGroup, maxPhases int) []string {
	if len(groups) == 0 {
		return nil
	}
	if maxPhases <= 0 {
		maxPhases = 4
	}
	if maxPhases > len(groups) {
		maxPhases = len(groups)
	}
	major := map[string]bool{}
	minor := map[string]bool{}
	for _, group := range groups {
		if identity, ok := parseVersionIdentity(group.version); ok && len(identity.parts) > 0 {
			major[fmt.Sprint(identity.parts[0])] = true
			if len(identity.parts) > 1 {
				minor[fmt.Sprintf("%d.%d", identity.parts[0], identity.parts[1])] = true
			}
		}
	}
	if len(major) >= 2 && len(major) <= maxPhases {
		return sortedGroupKeys(major, "major:")
	}
	if len(minor) >= 2 && len(minor) <= maxPhases {
		return sortedGroupKeys(minor, "minor:")
	}
	segments := make([]string, 0, maxPhases)
	for index := 0; index < maxPhases; index++ {
		segments = append(segments, fmt.Sprintf("bucket:%02d", index))
	}
	return segments
}

func sortedGroupKeys(values map[string]bool, prefix string) []string {
	keys := make([]string, 0, len(values))
	for value := range values {
		keys = append(keys, prefix+value)
	}
	sort.Strings(keys)
	return keys
}

func groupSegment(version string) string {
	if identity, ok := parseVersionIdentity(version); ok {
		if len(identity.parts) > 1 {
			return fmt.Sprintf("minor:%d.%d", identity.parts[0], identity.parts[1])
		}
		if len(identity.parts) > 0 {
			return fmt.Sprintf("major:%d", identity.parts[0])
		}
	}
	return "minor:unknown"
}

func clipHistoryText(value string, limit int) string {
	out := strings.Join(strings.Fields(value), " ")
	if len([]rune(out)) <= limit {
		return out
	}
	runes := []rune(out)
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

// DeriveHistoricalTimeline groups explicit, ordered source versions into
// evidence-backed phases. It is deterministic and does not call an LLM.
func DeriveHistoricalTimeline(compilation Compilation, maxPhases int) HistoricalTimeline {
	groups := orderedVersionGroups(compilation)
	out := HistoricalTimeline{Diagnostics: []string{}}
	if len(groups) == 0 {
		out.Diagnostics = append(out.Diagnostics, "no-ordered-version-evidence")
		return out
	}

	segments := phaseSegments(groups, maxPhases)
	if len(segments) == 0 {
		out.Diagnostics = append(out.Diagnostics, "no-phase-segments")
		return out
	}
	segmentOrder := map[string]int{}
	for index, segment := range segments {
		segmentOrder[segment] = index
	}
	buckets := make([][]versionUnitGroup, len(segments))
	if strings.HasPrefix(segments[0], "bucket:") {
		perBucket := (len(groups) + len(segments) - 1) / len(segments)
		for index, group := range groups {
			bucket := index / perBucket
			if bucket >= len(buckets) {
				bucket = len(buckets) - 1
			}
			buckets[bucket] = append(buckets[bucket], group)
		}
	} else {
		for _, group := range groups {
			segment := ""
			if identity, ok := parseVersionIdentity(group.version); ok {
				if strings.HasPrefix(segments[0], "major:") && len(identity.parts) > 0 {
					segment = fmt.Sprintf("major:%d", identity.parts[0])
				} else if strings.HasPrefix(segments[0], "minor:") && len(identity.parts) > 1 {
					segment = fmt.Sprintf("minor:%d.%d", identity.parts[0], identity.parts[1])
				}
			}
			// Only segments produced above are accepted. This prevents an
			// unsupported major/minor mix from silently crossing families.
			if _, ok := segmentOrder[segment]; !ok {
				continue
			}
			buckets[segmentOrder[segment]] = append(buckets[segmentOrder[segment]], group)
		}
	}

	for bucketIndex, bucket := range buckets {
		if len(bucket) == 0 {
			continue
		}
		start, end := bucket[0].version, bucket[len(bucket)-1].version
		phase := HistoricalPhase{
			ID:           fmt.Sprintf("phase-%02d", bucketIndex+1),
			Name:         fmt.Sprintf("Version %s to %s", start, end),
			StartVersion: start,
			EndVersion:   end,
			Confidence:   0.92,
			Metadata: map[string]string{
				"basis":         "ordered-source-version",
				"segment":       segments[bucketIndex],
				"version_count": fmt.Sprint(len(bucket)),
			},
		}
		keySeen := map[string]bool{}
		sourceSeen := map[string]bool{}
		evidenceSeen := map[string]bool{}
		for _, group := range bucket {
			for _, unit := range group.units {
				if sourceSeen[unit.ID] {
					continue
				}
				sourceSeen[unit.ID] = true
				phase.SourceUnitIDs = append(phase.SourceUnitIDs, unit.ID)
				for _, source := range unit.Sources {
					key := evidenceSourceKey(source)
					if !evidenceSeen[key] {
						evidenceSeen[key] = true
						phase.Evidence = append(phase.Evidence, source)
					}
				}
				if len(phase.KeyChanges) >= 4 {
					continue
				}
				change := clipHistoryText(unit.Content, 220)
				key := strings.ToLower(change)
				if change == "" || keySeen[key] {
					continue
				}
				keySeen[key] = true
				phase.KeyChanges = append(phase.KeyChanges, change)
			}
		}
		out.Phases = append(out.Phases, phase)
	}
	if len(out.Phases) < 2 {
		out.Confidence = 0.78
		out.Diagnostics = append(out.Diagnostics, "single-phase")
	} else {
		out.Confidence = 0.92
	}

	for index := 0; index+1 < len(out.Phases); index++ {
		from, to := out.Phases[index], out.Phases[index+1]
		transition := HistoricalTransition{
			FromPhaseID: from.ID, ToPhaseID: to.ID,
			FromVersion: from.EndVersion, ToVersion: to.StartVersion,
			Statement:  fmt.Sprintf("Ordered transition from version %s to version %s.", from.EndVersion, to.StartVersion),
			Confidence: 0.92,
			Metadata:   map[string]string{"basis": "ordered-source-version"},
		}
		sourceSeen := map[string]bool{}
		evidenceSeen := map[string]bool{}
		for _, phase := range []HistoricalPhase{from, to} {
			for _, unitID := range phase.SourceUnitIDs {
				if !sourceSeen[unitID] {
					sourceSeen[unitID] = true
					transition.SourceUnitIDs = append(transition.SourceUnitIDs, unitID)
				}
			}
			for _, source := range phase.Evidence {
				key := evidenceSourceKey(source)
				if !evidenceSeen[key] {
					evidenceSeen[key] = true
					transition.Evidence = append(transition.Evidence, source)
				}
			}
		}
		out.Transitions = append(out.Transitions, transition)
	}
	out.Diagnostics = append(out.Diagnostics, fmt.Sprintf("phases=%d", len(out.Phases)))
	return out
}

// SelectHistoricalRepresentatives chooses one best unit per selected version.
// Ambiguous “earlier” history is deliberately limited to the older half of the
// corpus and sampled across that range instead of selecting one oldest chunk.
func SelectHistoricalRepresentatives(compilation Compilation, requested TemporalRange, limit int, query string, queryTerms []string) []Unit {
	groups := orderedVersionGroups(compilation)
	if len(groups) == 0 || limit <= 0 {
		return nil
	}
	if requested.Kind != RangeAmbiguous && !requested.Ambiguous {
		resolved := ResolveTemporalRange(compilation, requested)
		if len(resolved.Units) == 0 {
			return nil
		}
		allowed := map[string]bool{}
		for _, version := range resolved.Versions {
			allowed[version] = true
		}
		filtered := make([]versionUnitGroup, 0, len(allowed))
		for _, group := range groups {
			if allowed[group.version] {
				filtered = append(filtered, group)
			}
		}
		groups = filtered
	} else if requested.Scope == "recent" {
		if limit < len(groups) {
			groups = groups[len(groups)-limit:]
		}
	} else if requested.Scope != "full" {
		earlyCount := (len(groups) / 2 / limit) * limit
		if earlyCount < limit {
			earlyCount = limit
		}
		if earlyCount > len(groups) {
			earlyCount = len(groups)
		}
		groups = groups[:earlyCount]
	}
	if len(groups) == 0 {
		return nil
	}
	if limit > len(groups) {
		limit = len(groups)
	}

	// Relative ranges prefer adjacency: “before X” needs the latest prior
	// evidence, while “after X” needs the earliest following evidence. Bounded
	// and full ranges continue sampling across the whole span.
	relativeBefore := requested.Start == nil && requested.End != nil
	relativeAfter := requested.Start != nil && requested.End == nil
	if (relativeBefore || relativeAfter) && len(groups) > limit {
		if relativeBefore {
			groups = groups[len(groups)-limit:]
			sort.Slice(groups, func(i, j int) bool { return groups[i].version > groups[j].version })
			groups = groups[:limit]
		}
	}

	selected := make([]Unit, 0, limit)
	for index := 0; index < limit && len(selected) < limit; index++ {
		start := index * len(groups) / limit
		end := (index + 1) * len(groups) / limit
		if start >= len(groups) {
			break
		}
		if end <= start {
			end = start + 1
		}
		if end > len(groups) {
			end = len(groups)
		}
		bestGroup := groups[start]
		bestUnit := bestHistoryUnit(bestGroup.units, queryTerms)
		bestScore := historyUnitScore(bestUnit, queryTerms)
		for _, group := range groups[start+1 : end] {
			unit := bestHistoryUnit(group.units, queryTerms)
			score := historyUnitScore(unit, queryTerms)
			if score > bestScore || (score == bestScore && unit.CanonicalKey < bestUnit.CanonicalKey) {
				bestGroup, bestUnit, bestScore = group, unit, score
			}
		}
		selected = append(selected, bestUnit)
	}
	return selected
}

func bestHistoryUnit(units []Unit, queryTerms []string) Unit {
	if len(units) == 0 {
		return Unit{}
	}
	best := units[0]
	bestScore := historyUnitScore(best, queryTerms)
	for _, unit := range units[1:] {
		score := historyUnitScore(unit, queryTerms)
		if score > bestScore || (score == bestScore && unitRankForHistory(unit) < unitRankForHistory(best)) {
			best, bestScore = unit, score
		}
	}
	return best
}

func historyUnitScore(unit Unit, queryTerms []string) float64 {
	if len(queryTerms) == 0 {
		return float64(10 - unitRankForHistory(unit))
	}
	haystack := normalizeSearchText(unit.Title + "\n" + unit.Content)
	score := float64(0)
	for _, term := range queryTerms {
		if strings.Contains(haystack, strings.ToLower(term)) {
			score++
		}
	}
	if score == 0 {
		return -1
	}
	return score*10 + float64(4-unitRankForHistory(unit))
}

// ClipHistoryEvidenceText bounds one representative history source so multiple
// phases fit inside the existing context budget.
func ClipHistoryEvidenceText(value string, limit int) string {
	return clipHistoryText(value, limit)
}

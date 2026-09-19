package semantic

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// RangeKind distinguishes evidence-backed version chronology from calendar
// time. Ambiguous ranges remain useful for broad history questions without
// inventing a precise boundary.
type RangeKind string

const (
	RangeVersion   RangeKind = "VERSION"
	RangeTime      RangeKind = "TIME"
	RangeAmbiguous RangeKind = "AMBIGUOUS"
	RangeEvent     RangeKind = "EVENT"
)

// RangeBoundary is one endpoint. A nil Value/Time with an inclusive flag is an
// open boundary, never an inferred epoch or "current" date.
type RangeBoundary struct {
	Value           string     `json:"value,omitempty"`
	ResolvedVersion string     `json:"resolvedVersion,omitempty"`
	Time            *time.Time `json:"time,omitempty"`
	Inclusive       bool       `json:"inclusive"`
}

// TemporalRange is the minimal 0.6 model for from/to, before/after, until/since,
// and open-ended history. Version and time ranges are deliberately separate.
type TemporalRange struct {
	Kind        RangeKind      `json:"kind"`
	Start       *RangeBoundary `json:"start,omitempty"`
	End         *RangeBoundary `json:"end,omitempty"`
	Ambiguous   bool           `json:"ambiguous,omitempty"`
	Confidence  float64        `json:"confidence"`
	Diagnostics []string       `json:"diagnostics,omitempty"`
}

var (
	rangeFromDatePattern    = regexp.MustCompile(`(?i)\b(20[0-9]{2})-([0-9]{2})-([0-9]{2})\b`)
	rangeFromVersionPattern = regexp.MustCompile(`(?i)(?:^|[^0-9a-z])v?([0-9]+(?:\.[0-9]+){1,3})(?:(?:[^0-9a-z.]|$))`)
	rangeMarkerPatterns     = []struct {
		kind      string
		pattern   *regexp.Regexp
		inclusive bool
	}{
		{"before", regexp.MustCompile(`(?i)\b(?:before|prior to|earlier than)\b\s+(.+)$`), false},
		{"until", regexp.MustCompile(`(?i)\b(?:until|through|through the end of)\b\s+(.+)$`), true},
		{"after", regexp.MustCompile(`(?i)\b(?:after|later than)\b\s+(.+)$`), false},
		{"since", regexp.MustCompile(`(?i)\b(?:since|from)\b\s+(.+)$`), true},
	}
	rangePairPattern = regexp.MustCompile(`(?i)\b(?:from\s+)?(.+?)\s+(?:to|through|until)\s+(.+)$`)
)

func versionBoundary(value string, inclusive bool) (*RangeBoundary, bool) {
	normalized, ok := NormalizeVersionIdentity(value)
	if !ok {
		return nil, false
	}
	if identity, parseOK := parseVersionIdentity(normalized); !parseOK || !identity.knownOrder {
		return nil, false
	}
	return &RangeBoundary{Value: normalized, Inclusive: inclusive}, true
}

func timeBoundary(value string, inclusive bool) (*RangeBoundary, bool) {
	match := rangeFromDatePattern.FindStringSubmatch(value)
	if match == nil {
		return nil, false
	}
	parsed, err := time.Parse("2006-01-02", match[1]+"-"+match[2]+"-"+match[3])
	if err != nil {
		return nil, false
	}
	return &RangeBoundary{Time: &parsed, Inclusive: inclusive}, true
}

func parseRangeEndpoint(value string, inclusive bool) (*RangeBoundary, RangeKind, bool) {
	if boundary, ok := versionBoundary(value, inclusive); ok {
		return boundary, RangeVersion, true
	}
	if boundary, ok := timeBoundary(value, inclusive); ok {
		return boundary, RangeTime, true
	}
	return nil, "", false
}

func compareRangeBoundaries(left, right RangeBoundary, kind RangeKind) (int, bool) {
	if kind == RangeVersion {
		return CompareVersionIdentity(left.Value, right.Value)
	}
	if kind == RangeTime && left.Time != nil && right.Time != nil {
		switch {
		case left.Time.Before(*right.Time):
			return -1, true
		case left.Time.After(*right.Time):
			return 1, true
		default:
			return 0, true
		}
	}
	return 0, false
}

// ParseTemporalRange extracts an explicit range. It never converts an
// ambiguous phrase into a fabricated version or date.
func ParseTemporalRange(query string) (TemporalRange, bool) {
	normalized := normalizeSearchText(query)
	if match := rangePairPattern.FindStringSubmatch(normalized); match != nil {
		left, leftKind, leftOK := parseRangeEndpoint(match[1], true)
		right, rightKind, rightOK := parseRangeEndpoint(match[2], true)
		if leftOK && rightOK && leftKind == rightKind {
			if order, comparable := compareRangeBoundaries(*left, *right, leftKind); comparable && order > 0 {
				left, right = right, left
			}
			return TemporalRange{Kind: leftKind, Start: left, End: right, Confidence: 0.96,
				Diagnostics: []string{"explicit-range"}}, true
		}
	}
	for _, marker := range rangeMarkerPatterns {
		match := marker.pattern.FindStringSubmatch(normalized)
		if match == nil {
			continue
		}
		boundary, kind, ok := parseRangeEndpoint(match[1], marker.inclusive)
		if !ok && (marker.kind == "before" || marker.kind == "after") {
			event := strings.TrimSpace(strings.Trim(match[1], " ?.,;:!"))
			if len(event) < 2 || len(event) > 80 || strings.EqualFold(event, "it") || strings.EqualFold(event, "that") {
				continue
			}
			boundary = &RangeBoundary{Value: event, Inclusive: marker.inclusive}
			kind = RangeEvent
			out := TemporalRange{Kind: kind, Confidence: 0.93, Diagnostics: []string{marker.kind + "-range"}}
			if marker.kind == "before" || marker.kind == "until" {
				out.End = boundary
			} else {
				out.Start = boundary
			}
			return out, true
		} else if ok {
			out := TemporalRange{Kind: kind, Confidence: 0.93, Diagnostics: []string{marker.kind + "-range"}}
			if marker.kind == "before" || marker.kind == "until" {
				out.End = boundary
			} else {
				out.Start = boundary
			}
			return out, true
		} else {
			continue
		}
	}
	if containsAnyHistoryAmbiguity(normalized) {
		return TemporalRange{Kind: RangeAmbiguous, Ambiguous: true, Confidence: 0.70,
			Diagnostics: []string{"ambiguous-history"}}, true
	}
	return TemporalRange{}, false
}

func containsAnyHistoryAmbiguity(normalized string) bool {
	for _, marker := range []string{"earlier", "early", "history", "historical", "previous",
		"prior", "past", "old version", "older version", "以前", "早期", "历史", "当时", "过去"} {
		if containsTemporalMarker(normalized, marker) {
			return true
		}
	}
	return false
}

// ContainsVersion reports whether an ordered unit version belongs to a version
// range. Ambiguous ranges return false so callers must use phase assembly.
func (r TemporalRange) ContainsVersion(version string) bool {
	if r.Kind != RangeVersion || strings.TrimSpace(version) == "" {
		return false
	}
	if r.Start != nil {
		order, ok := CompareVersionIdentity(version, r.Start.Value)
		if !ok || order < 0 || (order == 0 && !r.Start.Inclusive) {
			return false
		}
	}
	if r.End != nil {
		order, ok := CompareVersionIdentity(version, r.End.Value)
		if !ok || order > 0 || (order == 0 && !r.End.Inclusive) {
			return false
		}
	}
	return true
}

// Describe is deterministic diagnostics for tests and API observability.
func (r TemporalRange) Describe() string {
	if r.Kind == "" {
		return ""
	}
	kind := string(r.Kind)
	start, end := "OPEN", "OPEN"
	if r.Start != nil {
		if r.Start.Time != nil {
			start = r.Start.Time.Format("2006-01-02")
		} else {
			start = r.Start.Value
		}
		if r.Start.Inclusive {
			start += "+"
		}
	}
	if r.End != nil {
		if r.End.Time != nil {
			end = r.End.Time.Format("2006-01-02")
		} else {
			end = r.End.Value
		}
		if !r.End.Inclusive {
			end += "-"
		}
	}
	return fmt.Sprintf("%s[%s,%s] ambiguous=%v confidence=%.2f", kind, start, end, r.Ambiguous, r.Confidence)
}

// ResolvedTemporalRange separates the parsed query intent from the actual
// evidence selected from one immutable compilation. It never promotes an
// ambiguous range to a concrete version.
type ResolvedTemporalRange struct {
	Range       TemporalRange `json:"range"`
	Versions    []string      `json:"versions,omitempty"`
	Units       []Unit        `json:"units,omitempty"`
	UnitCount   int           `json:"unitCount"`
	Confidence  float64       `json:"confidence"`
	Diagnostics []string      `json:"diagnostics,omitempty"`
}

func boundaryVersion(boundary RangeBoundary) string {
	if boundary.ResolvedVersion != "" {
		return boundary.ResolvedVersion
	}
	return boundary.Value
}

func versionSatisfiesBoundary(version string, boundary *RangeBoundary, isStart bool) bool {
	if boundary == nil || boundary.ResolvedVersion == "" && boundary.Value == "" {
		return true
	}
	target := boundaryVersion(*boundary)
	order, comparable := CompareVersionIdentity(version, target)
	if !comparable {
		return false
	}
	if isStart {
		return order > 0 || (order == 0 && boundary.Inclusive)
	}
	return order < 0 || (order == 0 && boundary.Inclusive)
}

func unitTime(unit Unit) int64 {
	if unit.PublishedAt != 0 {
		return unit.PublishedAt
	}
	return unit.EffectiveAt
}

func timeSatisfiesBoundary(unit Unit, boundary *RangeBoundary, isStart bool) bool {
	if boundary == nil || boundary.Time == nil {
		return true
	}
	value := unitTime(unit)
	if value == 0 {
		return false
	}
	target := boundary.Time.Unix()
	if isStart {
		return value > target || (value == target && boundary.Inclusive)
	}
	return value < target || (value == target && boundary.Inclusive)
}

// ResolveTemporalRange selects only units whose existing VersionIdentity or
// temporal timestamp is proven to belong to the range. Relative event
// boundaries are resolved from evidence, never from a built-in feature table.
func ResolveTemporalRange(compilation Compilation, requested TemporalRange) ResolvedTemporalRange {
	out := ResolvedTemporalRange{Range: requested, Confidence: requested.Confidence}
	if requested.Kind == RangeAmbiguous || requested.Ambiguous {
		out.Diagnostics = append(out.Diagnostics, "ambiguous-range-not-materialized")
		return out
	}
	if requested.Kind == RangeEvent {
		needle := ""
		if requested.Start != nil {
			needle = requested.Start.Value
		} else if requested.End != nil {
			needle = requested.End.Value
		}
		needle = normalizeSearchText(needle)
		boundaryVersions := make([]string, 0)
		introductory := make([]string, 0)
		mentioned := make([]string, 0)
		for _, unit := range compilation.Units {
			if unit.Status == UnitDeleted || unit.Version == "" {
				continue
			}
			if _, ordered := parseVersionIdentity(unit.Version); !ordered {
				continue
			}
			haystack := normalizeSearchText(unit.Title + "\n" + unit.Content)
			if needle == "" || !strings.Contains(haystack, needle) {
				continue
			}
			mentioned = append(mentioned, unit.Version)
			if strings.Contains(haystack, "introduced") || strings.Contains(haystack, "added") ||
				strings.Contains(haystack, "shipped") || strings.Contains(haystack, "released") {
				introductory = append(introductory, unit.Version)
			}
		}
		boundaryVersions = introductory
		if len(boundaryVersions) == 0 {
			boundaryVersions = mentioned
		}
		orderedBoundary := orderedDistinctVersions(boundaryVersions)
		if len(orderedBoundary) == 0 {
			out.Diagnostics = append(out.Diagnostics, "event-boundary=UNKNOWN")
			out.Confidence = 0
			return out
		}
		eventVersion := orderedBoundary[0]
		if requested.Start != nil {
			requested.Start.ResolvedVersion = eventVersion
		}
		if requested.End != nil {
			requested.End.ResolvedVersion = eventVersion
		}
		out.Range = requested
		out.Diagnostics = append(out.Diagnostics, "event-boundary="+eventVersion)
	}

	type candidate struct {
		unit    Unit
		version string
	}
	candidates := make([]candidate, 0, len(compilation.Units))
	for _, unit := range compilation.Units {
		if unit.Status == UnitDeleted {
			continue
		}
		if requested.Kind == RangeVersion || requested.Kind == RangeEvent {
			version := unit.Version
			if version == "" {
				continue
			}
			if _, ordered := parseVersionIdentity(version); !ordered {
				continue
			}
			if !versionSatisfiesBoundary(version, requested.Start, true) ||
				!versionSatisfiesBoundary(version, requested.End, false) {
				continue
			}
			candidates = append(candidates, candidate{unit: unit, version: version})
		} else if requested.Kind == RangeTime {
			if unitTime(unit) == 0 {
				continue
			}
			if !timeSatisfiesBoundary(unit, requested.Start, true) ||
				!timeSatisfiesBoundary(unit, requested.End, false) {
				continue
			}
			candidates = append(candidates, candidate{unit: unit})
		}
	}
	if requested.Kind == RangeVersion || requested.Kind == RangeEvent {
		sort.SliceStable(candidates, func(i, j int) bool {
			left, right := candidates[i].version, candidates[j].version
			if order, comparable := CompareVersionIdentity(left, right); comparable && order != 0 {
				return order < 0
			}
			return candidates[i].unit.CanonicalKey < candidates[j].unit.CanonicalKey
		})
		seenVersion := map[string]bool{}
		for _, item := range candidates {
			if !seenVersion[item.version] {
				seenVersion[item.version] = true
				out.Versions = append(out.Versions, item.version)
			}
		}
	} else {
		sort.SliceStable(candidates, func(i, j int) bool {
			left, right := unitTime(candidates[i].unit), unitTime(candidates[j].unit)
			if left != right {
				return left < right
			}
			return candidates[i].unit.CanonicalKey < candidates[j].unit.CanonicalKey
		})
	}
	out.Units = make([]Unit, 0, len(candidates))
	for _, item := range candidates {
		out.Units = append(out.Units, item.unit)
	}
	out.UnitCount = len(out.Units)
	if out.UnitCount == 0 {
		out.Confidence = 0
		out.Diagnostics = append(out.Diagnostics, "range-evidence=EMPTY")
	} else {
		out.Confidence = requested.Confidence
		out.Diagnostics = append(out.Diagnostics, fmt.Sprintf("range-evidence=%d", out.UnitCount))
	}
	return out
}

func orderedDistinctVersions(values []string) []string {
	type item struct {
		value string
		order int
	}
	ordered := make([]item, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		order := 0
		for _, other := range values {
			if result, comparable := CompareVersionIdentity(value, other); comparable && result > 0 {
				order++
			}
		}
		ordered = append(ordered, item{value: value, order: order})
	}
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].order < ordered[j].order })
	out := make([]string, 0, len(ordered))
	for _, item := range ordered {
		out = append(out, item.value)
	}
	return out
}

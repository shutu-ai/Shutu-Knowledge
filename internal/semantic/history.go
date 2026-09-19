package semantic

import (
	"fmt"
	"regexp"
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
)

// RangeBoundary is one endpoint. A nil Value/Time with an inclusive flag is an
// open boundary, never an inferred epoch or "current" date.
type RangeBoundary struct {
	Value     string     `json:"value,omitempty"`
	Time      *time.Time `json:"time,omitempty"`
	Inclusive bool       `json:"inclusive"`
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
		if !ok {
			continue
		}
		out := TemporalRange{Kind: kind, Confidence: 0.93, Diagnostics: []string{marker.kind + "-range"}}
		if marker.kind == "before" || marker.kind == "until" {
			out.End = boundary
		} else {
			out.Start = boundary
		}
		return out, true
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

package semantic

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// TemporalIntent refines the broad 0.4 temporal route into the six query
// behaviors required by version-aware selection. NONE deliberately falls back
// to the existing evidence-first path.
type TemporalIntent string

const (
	TemporalNone            TemporalIntent = "NONE"
	TemporalCurrent         TemporalIntent = "CURRENT"
	TemporalExplicitVersion TemporalIntent = "AS_OF_VERSION"
	TemporalHistorical      TemporalIntent = "HISTORICAL"
	TemporalEvolution       TemporalIntent = "EVOLUTION"
	TemporalCompareVersions TemporalIntent = "COMPARE_VERSIONS"
	TemporalValidity        TemporalIntent = "VALIDITY"
	TemporalRangeHistory    TemporalIntent = "RANGE_HISTORY"
)

// TemporalQuery is the bounded result of lexical temporal parsing. Unknown
// chronology is represented by an empty version, never inferred from mtime.
type TemporalQuery struct {
	Intent      TemporalIntent `json:"intent"`
	Versions    []string       `json:"versions,omitempty"`
	FromVersion string         `json:"fromVersion,omitempty"`
	ToVersion   string         `json:"toVersion,omitempty"`
	Confidence  float64        `json:"confidence"`
	Signals     []string       `json:"signals,omitempty"`
	Range       *TemporalRange `json:"range,omitempty"`
}

type versionIdentity struct {
	normalized string
	release    bool
	parts      []int
	suffix     string
	knownOrder bool
}

var (
	semanticVersionPattern = regexp.MustCompile(`(?i)(?:^|[^0-9a-z])v?([0-9]+(?:\.[0-9]+){1,3})(?:([._-])([0-9a-z][0-9a-z._-]*))?(?:(?:[^0-9]|$))`)
	releaseVersionPattern  = regexp.MustCompile(`(?i)(?:^|[^0-9a-z])(?:rel[._-]?|release[ _-]?)(?:v[ _-]?)?([0-9]{1,3})(?:(?:[^0-9.]|$))`)
	threeGPPVersionPattern = regexp.MustCompile(`(?i)(?:^|[^0-9a-z])v([0-9]{2})([0-9]{2})([0-9]{2})(?:[a-z])?(?:(?:[^0-9]|$))`)
	compact3GPPPattern     = regexp.MustCompile(`(?i)v([0-9]{2})([0-9]{2})([0-9]{2})(?:[a-z])?(?:[._]|$)`)
	titleDatePattern       = regexp.MustCompile(`\b(20[0-9]{2})-([0-9]{2})-([0-9]{2})\b`)
	explicitVersionMarker  = regexp.MustCompile(`(?i)(?:^|[^0-9a-z])(?:v|version|ver|release|rel)[ ._=-]?[0-9]`)
)

// NormalizeVersionIdentity accepts the narrow classes needed by 0.5:
// semantic-ish product versions, 3GPP-style document versions, release
// numbers, and explicit ordering metadata. It intentionally does not turn an
// arbitrary string into a date or float.
func NormalizeVersionIdentity(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", false
	}
	lower := strings.ToLower(trimmed)
	if match := releaseVersionPattern.FindStringSubmatch(lower); match != nil {
		return "rel-" + match[1], true
	}
	if match := threeGPPVersionPattern.FindStringSubmatch(lower); match != nil {
		major := strings.TrimLeft(match[1], "0")
		minor := strings.TrimLeft(match[2], "0")
		patch := strings.TrimLeft(match[3], "0")
		if major == "" {
			major = "0"
		}
		if minor == "" {
			minor = "0"
		}
		if patch == "" {
			patch = "0"
		}
		return major + "." + minor + "." + patch, true
	}
	if match := semanticVersionPattern.FindStringSubmatch(lower); match != nil {
		out := strings.TrimPrefix(match[1], "v")
		if match[3] != "" {
			separator := match[2]
			if separator == "_" {
				separator = "-"
			}
			out += separator + strings.ToLower(match[3])
		}
		return out, true
	}
	// Explicit metadata may name a private ordering token. Keep it exact, but
	// callers must treat ordering as unknown unless Compare says otherwise.
	if len(lower) <= 64 && !strings.ContainsAny(lower, " \t\r\n") {
		cleaned := strings.TrimPrefix(lower, "version:")
		cleaned = strings.TrimPrefix(cleaned, "release:")
		return cleaned, cleaned != ""
	}
	return "", false
}

func parseVersionIdentity(value string) (versionIdentity, bool) {
	normalized, ok := NormalizeVersionIdentity(value)
	if !ok {
		return versionIdentity{}, false
	}
	out := versionIdentity{normalized: normalized}
	if strings.HasPrefix(normalized, "rel-") {
		if number, err := strconv.Atoi(strings.TrimPrefix(normalized, "rel-")); err == nil {
			out.release = true
			out.parts = []int{number}
			out.knownOrder = true
		}
		return out, true
	}
	main := normalized
	if boundary := strings.IndexAny(main, "-_+"); boundary >= 0 {
		out.suffix = main[boundary+1:]
		main = main[:boundary]
	}
	fields := strings.Split(main, ".")
	if len(fields) < 2 || len(fields) > 4 {
		return versionIdentity{}, false
	}
	parts := make([]int, 0, len(fields))
	for _, field := range fields {
		if field == "" {
			return versionIdentity{}, false
		}
		number, err := strconv.Atoi(field)
		if err != nil || number < 0 {
			return versionIdentity{}, false
		}
		parts = append(parts, number)
	}
	out.parts = parts
	out.knownOrder = out.suffix == ""
	return out, true
}

// CompareVersionIdentity returns -1/0/1 for comparable identities and
// false when chronology cannot be proven. Different families (semantic
// versions versus release numbers) are deliberately incomparable.
func CompareVersionIdentity(left, right string) (int, bool) {
	if strings.EqualFold(left, right) {
		return 0, true
	}
	a, aok := parseVersionIdentity(left)
	b, bok := parseVersionIdentity(right)
	if !aok || !bok || !a.knownOrder || !b.knownOrder || a.release != b.release {
		return 0, false
	}
	width := len(a.parts)
	if len(b.parts) > width {
		width = len(b.parts)
	}
	for i := 0; i < width; i++ {
		av, bv := 0, 0
		if i < len(a.parts) {
			av = a.parts[i]
		}
		if i < len(b.parts) {
			bv = b.parts[i]
		}
		if av < bv {
			return -1, true
		}
		if av > bv {
			return 1, true
		}
	}
	return 0, true
}

// LatestComparableVersion selects the greatest version only if every supplied
// identity can be ordered. The empty result is an explicit unknown.
func LatestComparableVersion(values []string) (string, bool) {
	candidates := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		identity, ok := parseVersionIdentity(value)
		if !ok || !identity.knownOrder {
			continue
		}
		if seen[identity.normalized] {
			continue
		}
		seen[identity.normalized] = true
		candidates = append(candidates, identity.normalized)
	}
	if len(candidates) == 0 {
		return "", false
	}
	sort.Strings(candidates)
	latest := candidates[0]
	for _, candidate := range candidates[1:] {
		order, ok := CompareVersionIdentity(latest, candidate)
		if !ok {
			return "", false
		}
		if order < 0 {
			latest = candidate
		}
	}
	return latest, true
}

// ParseTemporalQuery performs bounded, degradable lexical intent extraction.
// Explicit versions beat generic current/history language.
func ParseTemporalQuery(query string) TemporalQuery {
	normalized := normalizeSearchText(query)
	out := TemporalQuery{Intent: TemporalNone, Confidence: 0}
	if normalized == "" {
		return out
	}
	addSignal := func(signal string) { out.Signals = append(out.Signals, signal) }

	for _, match := range semanticVersionPattern.FindAllStringSubmatch(normalized, 4) {
		value := match[1]
		if match[3] != "" {
			separator := match[2]
			if separator == "_" {
				separator = "-"
			}
			value += separator + match[3]
		}
		if version, ok := NormalizeVersionIdentity(value); ok {
			out.Versions = append(out.Versions, version)
		}
	}
	for _, match := range releaseVersionPattern.FindAllStringSubmatch(normalized, 4) {
		if version, ok := NormalizeVersionIdentity("rel-" + match[1]); ok {
			out.Versions = append(out.Versions, version)
		}
	}
	seenVersions := map[string]bool{}
	deduplicated := make([]string, 0, len(out.Versions))
	for _, version := range out.Versions {
		if seenVersions[version] {
			continue
		}
		seenVersions[version] = true
		deduplicated = append(deduplicated, version)
	}
	out.Versions = deduplicated

	containsAny := func(markers ...string) bool {
		for _, marker := range markers {
			if !containsTemporalMarker(normalized, marker) {
				continue
			}
			return true
		}
		return false
	}
	evolutionLanguage := containsAny("what changed", "changed in", "how has", "how did", "evolution", "evolved",
		"when was", "when were", "when did", "introduced", "added in", "removed in", "deprecated",
		"什么时候", "何时引入", "何时删除", "如何演进", "有什么变化")
	if len(out.Versions) >= 2 && evolutionLanguage {
		out.Intent = TemporalEvolution
		out.FromVersion, out.ToVersion = orderedTemporalVersions(out.Versions)
		out.Confidence = 0.95
		addSignal("version-range-evolution")
	} else if len(out.Versions) >= 2 {
		out.Intent = TemporalCompareVersions
		out.FromVersion, out.ToVersion = orderedTemporalVersions(out.Versions)
		out.Confidence = 0.94
		addSignal("two-versions")
	} else if containsAny("from ", " from ") && containsAny(" to ") && len(out.Versions) >= 2 {
		out.Intent = TemporalEvolution
		out.FromVersion, out.ToVersion = orderedTemporalVersions(out.Versions)
		out.Confidence = 0.94
		addSignal("version-range")
	} else if containsAny("what changed", "changed in", "how has", "how did", "evolution", "evolved",
		"when was", "when were", "when did", "introduced", "added in", "removed in", "deprecated",
		"什么时候", "何时引入", "何时删除", "如何演进", "有什么变化") && len(out.Versions) > 0 {
		out.Intent = TemporalEvolution
		if len(out.Versions) == 2 {
			out.FromVersion, out.ToVersion = orderedTemporalVersions(out.Versions)
		}
		out.Confidence = 0.88
		addSignal("evolution")
	} else if len(out.Versions) > 0 {
		out.Intent = TemporalExplicitVersion
		out.FromVersion = out.Versions[0]
		out.ToVersion = out.FromVersion
		out.Confidence = 0.96
		addSignal("explicit-version")
	} else if containsAny("still", "currently valid", "still valid", "still supported", "还有效", "现在还") {
		out.Intent = TemporalValidity
		out.Confidence = 0.85
		addSignal("validity")
	} else if containsAny("history", "historical", "old version", "older version", "previous", "prior",
		"past", "before", "历史", "旧版本", "以前", "之前", "当时") {
		out.Intent = TemporalHistorical
		out.Confidence = 0.82
		addSignal("historical")
	} else if containsAny("current", "currently", "now", "latest", "newest", "today",
		"当前", "现在", "目前", "最新") {
		out.Intent = TemporalCurrent
		out.Confidence = 0.88
		addSignal("current")
	}
	temporalRange, rangeOK := ParseTemporalRange(query)
	if rangeOK && !(temporalRange.Ambiguous && len(out.Versions) > 0) {
		rangeCopy := temporalRange
		out.Range = &rangeCopy
		if temporalRange.Kind == RangeAmbiguous || temporalRange.Ambiguous || temporalRange.Kind == RangeEvent {
			out.Intent = TemporalRangeHistory
			out.Confidence = temporalRange.Confidence
			addSignal("range-history")
		} else if out.Intent == TemporalNone {
			out.Intent = TemporalRangeHistory
			out.Confidence = temporalRange.Confidence
			addSignal("explicit-range-history")
		}
	}
	if out.Intent != TemporalNone {
		return out
	}
	if containsAny("what changed", "changed in", "evolution", "introduced", "removed", "什么时候", "如何演进") {
		out.Intent = TemporalEvolution
		out.Confidence = 0.62
		addSignal("evolution-no-version")
	}
	return out
}

// containsTemporalMarker avoids accidental English substring hits such as
// “knowledge” matching “now”, while CJK markers remain substring-safe.
func containsTemporalMarker(normalized, marker string) bool {
	index := strings.Index(normalized, marker)
	if index < 0 {
		return false
	}
	if containsHan(marker) {
		return true
	}
	if index > 0 {
		before := []rune(normalized[index-1 : index])
		if !unicode.IsSpace(before[0]) && !isASCIIPunctuation(before[0]) {
			return false
		}
	}
	end := index + len(marker)
	if end >= len(normalized) {
		return true
	}
	after := []rune(normalized[end : end+1])
	return unicode.IsSpace(after[0]) || isASCIIPunctuation(after[0])
}

func containsHan(value string) bool {
	for _, r := range value {
		if unicode.Is(unicode.Han, r) {
			return true
		}
	}
	return false
}

func isASCIIPunctuation(value rune) bool {
	return (value >= '!' && value <= '/') || (value >= ':' && value <= '@') ||
		(value >= '[' && value <= '`') || (value >= '{' && value <= '~')
}

func orderedTemporalVersions(values []string) (string, string) {
	if len(values) == 0 {
		return "", ""
	}
	left, right := values[0], values[len(values)-1]
	if len(values) == 2 {
		left, right = values[0], values[1]
		if order, ok := CompareVersionIdentity(left, right); ok && order > 0 {
			left, right = right, left
		}
	}
	return left, right
}

// ExtractTemporalSource reuses only existing source identity inputs: explicit
// metadata, title/filename text, and the deterministic title date. It never
// uses file modification time and leaves every absent field empty.
func ExtractTemporalSource(title string, metadata map[string]string) (string, int64, int64, int, bool) {
	lookup := func(keys ...string) string {
		for _, key := range keys {
			if value := strings.TrimSpace(metadata[key]); value != "" {
				return value
			}
		}
		return ""
	}
	version := lookup("knowledge_version", "document_version", "release", "version", "temporal_version")
	published := parseTemporalTimestamp(lookup("published_at", "published", "date", "revision_date"))
	versionSource := title
	if match := titleDatePattern.FindStringSubmatch(title); match != nil {
		year, _ := strconv.Atoi(match[1])
		month, _ := strconv.Atoi(match[2])
		day, _ := strconv.Atoi(match[3])
		published = time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC).Unix()
		// An ISO publication date is temporal metadata, never a product
		// version. Remove it before title identity extraction.
		versionSource = titleDatePattern.ReplaceAllString(title, " ")
	}
	if version == "" {
		version = firstTemporalVersionInText(versionSource)
	}
	effective := parseTemporalTimestamp(lookup("effective_at", "effective", "valid_from"))
	authority := SourceAuthorityDefault
	if raw := lookup("source_authority", "source_priority"); raw != "" {
		authority = NormalizeSourceAuthority(raw)
	}
	return version, published, effective, authority, version != ""
}

func firstTemporalVersionInText(value string) string {
	if index := strings.LastIndex(strings.ToLower(value), "."); index >= 0 {
		switch strings.ToLower(value[index+1:]) {
		case "md", "txt", "json", "yaml", "yml", "html":
			value = value[:index]
		}
	}
	normalized := strings.Join(strings.Fields(strings.ToLower(value)), " ")
	if match := releaseVersionPattern.FindStringSubmatch(normalized); match != nil {
		if version, ok := NormalizeVersionIdentity("rel-" + match[1]); ok {
			return version
		}
	}
	if match := threeGPPVersionPattern.FindStringSubmatch(normalized); match != nil {
		if version, ok := NormalizeVersionIdentity("v" + match[1] + match[2] + match[3]); ok {
			return version
		}
	}
	if match := compact3GPPPattern.FindStringSubmatch(normalized); match != nil {
		if version, ok := NormalizeVersionIdentity("v" + match[1] + match[2] + match[3]); ok {
			return version
		}
	}
	if match := semanticVersionPattern.FindStringSubmatch(normalized); match != nil {
		version := match[1]
		if match[3] != "" {
			separator := match[2]
			if separator == "_" {
				separator = "-"
			}
			version += separator + match[3]
		}
		if result, ok := NormalizeVersionIdentity(version); ok {
			return result
		}
	}
	return ""
}

func parseTemporalTimestamp(value string) int64 {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	if number, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return number
	}
	layouts := []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02"}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, trimmed); err == nil {
			return parsed.Unix()
		}
	}
	return 0
}

const (
	SourceAuthorityReleaseNotes  = 1
	SourceAuthorityProduct       = 2
	SourceAuthoritySpecification = 2
	SourceAuthorityCode          = 3
	SourceAuthorityREADME        = 4
	SourceAuthorityDesign        = 5
	SourceAuthorityDefault       = 8
)

// NormalizeSourceAuthority maps a small explicit vocabulary to priority.
// Lower wins; unknown/default is deliberately weakest.
func NormalizeSourceAuthority(value string) int {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "release", "release_notes", "release-notes", "changelog":
		return SourceAuthorityReleaseNotes
	case "product", "documentation", "docs":
		return SourceAuthorityProduct
	case "specification", "spec":
		return SourceAuthoritySpecification
	case "code", "source":
		return SourceAuthorityCode
	case "readme":
		return SourceAuthorityREADME
	case "design", "old-design":
		return SourceAuthorityDesign
	default:
		if number, err := strconv.Atoi(value); err == nil && number >= 0 && number <= 100 {
			return number
		}
		return SourceAuthorityDefault
	}
}

func unitVersion(unit Unit) string { return strings.TrimSpace(unit.Metadata["temporal_version"]) }
func unitPublishedAt(unit Unit) int64 {
	return parseTemporalTimestamp(unit.Metadata["temporal_published_at"])
}
func unitSourceAuthority(unit Unit) int {
	if raw := strings.TrimSpace(unit.Metadata["temporal_source_authority"]); raw != "" {
		if number, err := strconv.Atoi(raw); err == nil {
			return number
		}
	}
	return SourceAuthorityDefault
}

// ResolveCurrentVersion derives the authoritative latest version from
// knowledge validity inputs. It returns false rather than guessing when
// versions are absent or cannot be ordered.
func ResolveCurrentVersion(compilation Compilation) (string, bool) {
	versions := make([]string, 0, len(compilation.Units))
	for _, unit := range compilation.Units {
		if unit.Status == UnitDeleted || unit.Version == "" {
			continue
		}
		versions = append(versions, unit.Version)
	}
	return LatestComparableVersion(versions)
}

func temporalMetadataFromSource(source SourceDocument) map[string]string {
	version, published, effective, authority, _ := ExtractTemporalSource(source.Title, source.TemporalMetadata)
	metadata := map[string]string{
		"temporal_source_authority": strconv.Itoa(authority),
	}
	if version != "" {
		metadata["temporal_version"] = version
	}
	if published > 0 {
		metadata["temporal_published_at"] = strconv.FormatInt(published, 10)
	}
	if effective > 0 {
		metadata["temporal_effective_at"] = strconv.FormatInt(effective, 10)
	}
	return metadata
}

func applyTemporalMetadata(unit *Unit, source SourceDocument) {
	metadata := temporalMetadataFromSource(source)
	for key, value := range metadata {
		if unit.Metadata == nil {
			unit.Metadata = map[string]string{}
		}
		unit.Metadata[key] = value
	}
}

// temporalFactScope is intentionally narrow: only “Version X <predicate> ...”
// facts are eligible for automatic supersession. Values are stripped from the
// scope, but arbitrary numbers elsewhere are not, reducing false grouping.
func temporalFactScope(content string) (scope string, version string, ok bool) {
	parsedVersion, predicate, attribute, parsed := parseVersionedFact(content)
	if !parsed {
		return "", "", false
	}
	return predicate + "\x00" + attribute, parsedVersion, true
}

// temporalVersionVisible implements scope filtering without deleting history.
func temporalVersionVisible(unit Unit, temporal TemporalQuery, resolved string) bool {
	statusOK := unit.Status == UnitActive || unit.Status == UnitConflicted
	version := unit.Version
	switch temporal.Intent {
	case TemporalCurrent, TemporalValidity:
		return statusOK
	case TemporalExplicitVersion, TemporalHistorical:
		if version != "" && len(temporal.Versions) > 0 {
			return unit.Status != UnitDeleted && containsIdentity(temporal.Versions, version)
		}
		return unit.Status == UnitActive
	case TemporalEvolution, TemporalCompareVersions:
		if unit.Status == UnitDeleted {
			return false
		}
		if len(temporal.Versions) == 0 {
			return statusOK || unit.Status == UnitSuperseded
		}
		if version != "" {
			return containsIdentity(temporal.Versions, version)
		}
		return statusOK
	default:
		return unit.Status == UnitActive
	}
}

func containsIdentity(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
		if order, ok := CompareVersionIdentity(value, target); ok && order == 0 {
			return true
		}
	}
	return false
}

func temporalVersionBoost(unit Unit, temporal TemporalQuery, resolved string) float64 {
	version := unit.Version
	switch temporal.Intent {
	case TemporalCurrent, TemporalValidity:
		if resolved != "" && version != "" {
			if order, ok := CompareVersionIdentity(version, resolved); ok && order == 0 {
				return 6
			}
			return -2
		}
		if version != "" {
			return 0.5
		}
		return -0.25
	case TemporalExplicitVersion, TemporalHistorical:
		if version != "" && containsIdentity(temporal.Versions, version) {
			return 8
		}
		if version == "" {
			return -0.5
		}
	case TemporalEvolution, TemporalCompareVersions:
		if version != "" && containsIdentity(temporal.Versions, version) {
			return 3.5
		}
	}
	return 0
}

func CompareEqual(left, right string) bool {
	order, ok := CompareVersionIdentity(left, right)
	return ok && order == 0
}

func temporalReason(unit Unit, intent TemporalIntent, resolved string) string {
	var reason []string
	if version := unitVersion(unit); version != "" {
		reason = append(reason, "source_version="+version)
	}
	if status := strings.TrimSpace(unit.Metadata["temporal_status"]); status != "" {
		reason = append(reason, "status="+status)
	}
	if resolved != "" {
		reason = append(reason, "resolved_for_intent="+string(intent)+"="+resolved)
	}
	if unit.SupersededBy != "" {
		reason = append(reason, "superseded_by="+unit.SupersededBy)
	}
	return strings.Join(reason, ", ")
}

func joinSortedMetadata(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+values[key])
	}
	return strings.Join(parts, "\x00")
}

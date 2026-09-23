package schemamodel

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// SourceRow is a bounded projection of one Document IR table row. The text is
// the existing tab-separated structured projection; Range preserves evidence.
type SourceRow struct {
	NodeID string
	Text   string
	Range  string
}

// DocumentInput scopes one spreadsheet document for schema compilation.
type DocumentInput struct {
	BaseID     string
	DocumentID string
	Generation int64
	Title      string
	SourcePath string
	SheetName  string
	Directory  string
	Product    string
	Rows       []SourceRow
}

// headerPatterns are generic schema signals. Generic business words such as
// bare "name" are deliberately excluded to avoid treating every table as a
// data dictionary.
var headerPatterns = []struct {
	key     string
	aliases []string
	weight  float64
	signal  string
}{
	{"field", []string{"字段名称", "字段名", "字段", "field name", "field", "column name"}, .38, "field_name"},
	{"type", []string{"数据类型", "类型", "data type", "type"}, .22, "data_type"},
	{"meaning", []string{"字段含义", "含义", "描述", "说明", "备注", "meaning", "description", "notes", "comment"}, .18, "description"},
	{"enum", []string{"枚举值", "枚举", "取值", "enum", "allowed values"}, .10, "enum"},
	{"length", []string{"长度", "最大长度", "length"}, .05, "length"},
	{"primary", []string{"主键", "primary key", "key"}, .07, "primary_key"},
}

// schemaRegion is one detected header/body area inside a sheet.
type schemaRegion struct {
	Name            string
	Description     string
	Purpose         string
	PurposeEvidence string
	PurposeInferred bool
	HeaderIndex     int
	HeaderRow       []string
	DataRows        []SourceRow
	Signals         []string
	Confidence      float64
	Range           string
}

// CompileDocument detects schema regions and compiles a generic schema model.
// Low-confidence non-dictionary tables produce an empty model and false
// Detected; they remain on the ordinary Excel path.
func CompileDocument(input DocumentInput) (Model, bool) {
	model := Model{BaseID: input.BaseID, Generation: input.Generation}
	regions := detectSchemaRegions(input.Rows)
	if len(regions) == 0 {
		return model, false
	}
	for _, region := range regions {
		compileRegion(&model, input, region)
	}
	buildConcepts(&model)
	model.SortCanonical()
	return model, len(model.Tables) > 0
}

func detectSchemaRegions(rows []SourceRow) []schemaRegion {
	if len(rows) == 0 {
		return nil
	}
	type pendingTitle struct {
		text        string
		description string
		rowIndex    int
	}
	regions := make([]schemaRegion, 0)
	var current *schemaRegion
	var title pendingTitle
	var currentTitle pendingTitle
	flush := func() {
		if current != nil && len(current.DataRows) > 0 {
			regions = append(regions, *current)
		}
		current = nil
	}
	previousNumber := 0
	for index, row := range rows {
		values := splitRowValues(row.Text)
		number, hasNumber := rowNumber(row.Range)
		gap := hasNumber && previousNumber > 0 && number > previousNumber+1
		previousNumber = number
		header, confidence, signals := detectHeader([][]string{values})
		if header != nil {
			flush()
			name := currentTitle.text
			description := currentTitle.description
			current = &schemaRegion{
				Name: name, Description: description, HeaderIndex: index, HeaderRow: values,
				DataRows: nil, Signals: signals, Confidence: confidence,
			}
			currentTitle = pendingTitle{}
			if hasNumber {
				current.Range = row.Range
			}
			continue
		}
		// A following schema header legitimizes a merged/section title; isolated
		// title-like cells are context, not schema fields.
		if current == nil {
			if isTitleLike(values) {
				currentTitle = pendingTitle{text: firstNonEmpty(values...), description: firstNonEmpty(values...), rowIndex: index}
			}
			continue
		}
		if gap {
			flush()
			if isTitleLike(values) {
				currentTitle = pendingTitle{text: firstNonEmpty(values...), description: firstNonEmpty(values...), rowIndex: index}
			}
			continue
		}
		if isSchemaHeaderValues(values) {
			flush()
			current = &schemaRegion{Name: currentTitle.text, Description: currentTitle.description, HeaderIndex: index, HeaderRow: values, Confidence: 0.40, Signals: []string{"repeated_header"}}
			if hasNumber {
				current.Range = row.Range
			}
			currentTitle = pendingTitle{}
			continue
		}
		current.DataRows = append(current.DataRows, row)
		_ = title
	}
	flush()
	// Multi-row headers are recognized only after logical boundaries are known
	// because the category row belongs to the region header, not the data set.
	return promoteMultiRowHeaders(rows, regions)
}

func promoteMultiRowHeaders(rows []SourceRow, regions []schemaRegion) []schemaRegion {
	// The current IR stores one flattened row list per parser table. A schema
	// header can be preceded by a category row. If the preceding nonempty row
	// is not itself a schema header and lies immediately above the header, use
	// its nonblank values as category context.
	byIndex := make(map[int]SourceRow, len(rows))
	for i, row := range rows {
		byIndex[i] = row
	}
	for r := range regions {
		headerIndex := regions[r].HeaderIndex
		if headerIndex == 0 {
			continue
		}
		previous := splitRowValues(rows[headerIndex-1].Text)
		if isSchemaHeaderValues(previous) || !isTitleLike(previous) {
			continue
		}
		combined := make([]string, len(regions[r].HeaderRow))
		for i, value := range regions[r].HeaderRow {
			category := ""
			if i < len(previous) {
				category = NormalizeText(previous[i])
			}
			field := NormalizeText(value)
			combined[i] = strings.TrimSpace(category + " " + field)
		}
		if _, confidence, signals := detectHeader([][]string{combined}); confidence > regions[r].Confidence {
			regions[r].HeaderRow = combined
			regions[r].Confidence = confidence
			regions[r].Signals = append(regions[r].Signals, signals...)
			regions[r].Range = joinRange(rows[headerIndex-1].Range, regions[r].Range)
		}
	}
	return regions
}

func compileRegion(model *Model, input DocumentInput, region schemaRegion) {
	if region.Confidence < 0.55 || len(region.DataRows) == 0 {
		return
	}
	columns := mapHeaderColumns(region.HeaderRow)
	if columns["field"] < 0 {
		return
	}
	tableName := NormalizeText(region.Name)
	if tableName == "" {
		tableName = NormalizeText(input.SheetName)
	}
	if tableName == "" {
		tableName = "logical_table"
	}
	tableIdentity := strings.Join([]string{input.DocumentID, input.SheetName, region.Range, tableName}, "\x00")
	tableID := EntityID("schtable", input.BaseID, input.Generation, tableIdentity)
	scope := Scope{Directory: input.Directory, Product: input.Product, Workbook: input.Title, Sheet: input.SheetName, Table: tableName}
	sourceRange := region.Range
	granularity := RangeProvenance
	if sourceRange == "" {
		granularity = SheetProvenance
	}
	table := LogicalTable{
		ID: tableID, BaseID: input.BaseID, DocumentID: input.DocumentID, Generation: input.Generation,
		Name: tableName, DisplayName: tableName, Scope: scope, RegionAnchor: sourceRange, Source: SourceRef{
			DocumentID: input.DocumentID, DocumentTitle: input.Title, SourcePath: input.SourcePath,
			Sheet: input.SheetName, Range: sourceRange, Generation: input.Generation, Granularity: granularity,
			Context: firstNonEmpty(region.Description),
		}, Confidence: clampConfidence(region.Confidence), DetectionSignals: dedupeStrings(region.Signals),
		Description:     region.Description,
		BusinessPurpose: region.Purpose, PurposeEvidence: region.PurposeEvidence, PurposeInferred: region.PurposeInferred,
	}
	for _, row := range region.DataRows {
		values := splitRowValues(row.Text)
		if isSchemaHeaderValues(values) {
			continue
		}
		fieldIndex := columns["field"]
		if fieldIndex >= len(values) || NormalizeText(values[fieldIndex]) == "" {
			continue
		}
		field := compileField(model, input, table, columns, values, row)
		if field.ID == "" {
			continue
		}
		model.Fields = append(model.Fields, field)
		table.FieldIDs = append(table.FieldIDs, field.ID)
		if field.Enum != nil {
			model.Enums = append(model.Enums, *field.Enum)
		}
		for _, hint := range field.KeyHints {
			model.KeyCandidates = append(model.KeyCandidates, hint)
		}
	}
	if len(table.FieldIDs) == 0 {
		return
	}
	model.Tables = append(model.Tables, table)
}

func compileField(model *Model, input DocumentInput, table LogicalTable, columns map[string]int, values []string, row SourceRow) Field {
	get := func(key string) string {
		index, ok := columns[key]
		if !ok || index >= len(values) {
			return ""
		}
		return NormalizeText(values[index])
	}
	name := get("field")
	display := name
	scope := table.Scope
	fieldIdentity := strings.Join([]string{input.DocumentID, table.RegionAnchor, table.Name, name}, "\x00")
	fieldID := EntityID("schfield", input.BaseID, input.Generation, fieldIdentity)
	granularity := RangeProvenance
	if row.Range == "" {
		granularity = SheetProvenance
	}
	source := SourceRef{
		DocumentID: input.DocumentID, DocumentTitle: input.Title, NodeID: row.NodeID,
		SourcePath: input.SourcePath, Sheet: input.SheetName, Range: row.Range,
		Generation: input.Generation, Granularity: granularity,
	}
	field := Field{
		ID: fieldID, BaseID: input.BaseID, DocumentID: input.DocumentID, Generation: input.Generation,
		TableID: table.ID, TableName: table.Name, Scope: scope, Name: name, DisplayName: display,
		Aliases: aliasesFor(name, display), DataType: get("type"), Description: firstNonEmpty(get("meaning"), get("notes")),
		Nullable: strings.Contains(strings.ToLower(get("notes")+get("meaning")), "nullable"),
		Concepts: conceptsFor(strings.Join([]string{name, display, get("meaning"), get("notes"), table.Name, table.Description}, " ")),
		Source:   source, Confidence: clampConfidence(table.Confidence * 0.92),
	}
	if mapping := get("enum"); mapping != "" {
		if enum := parseEnumMapping(fieldID, mapping, source); enum != nil {
			field.Enum = enum
		}
	}
	if enumValue, enumLabel := get("enum_value"), get("enum_label"); enumValue != "" || enumLabel != "" {
		if enum := parseEnumPair(fieldID, enumValue, enumLabel, source); enum != nil {
			field.Enum = enum
		}
	}
	if primary := get("primary"); primary != "" {
		hint := keyCandidate(model, fieldID, KeyPrimary, 0.95, "source marks this field as a primary-key hint", source)
		field.KeyHints = append(field.KeyHints, hint)
	}
	for _, hint := range deriveKeyHints(field, source) {
		field.KeyHints = append(field.KeyHints, hint)
	}
	return field
}

func parseEnumMapping(fieldID, mapping string, source SourceRef) *Enum {
	if strings.TrimSpace(mapping) == "" {
		return nil
	}
	parts := strings.FieldsFunc(mapping, func(r rune) bool {
		return r == ';' || r == '；' || r == '\n' || r == '|'
	})
	values := make([]EnumValue, 0, len(parts))
	for _, part := range parts {
		part = NormalizeText(part)
		if part == "" {
			continue
		}
		separator := strings.IndexAny(part, ":：=")
		if separator < 0 {
			values = append(values, EnumValue{Value: part})
			continue
		}
		value := NormalizeText(part[:separator])
		label := NormalizeText(part[separator+len(string(part[separator])):])
		if value == "" {
			value = label
			label = ""
		}
		if value != "" {
			values = append(values, EnumValue{Value: value, Label: label})
		}
	}
	if len(values) == 0 {
		return nil
	}
	return &Enum{
		ID: EntityID("schemum", "enum", 1, fieldID+"\x00"+mapping), FieldID: fieldID,
		Values: values, Source: source, Confidence: 0.82,
	}
}

func parseEnumPair(fieldID, value, label string, source SourceRef) *Enum {
	value, label = NormalizeText(value), NormalizeText(label)
	if value == "" && label == "" {
		return nil
	}
	if value == "" {
		value = label
		label = ""
	}
	return &Enum{
		ID: EntityID("schemum", "enum", 1, fieldID+"\x00pair"), FieldID: fieldID,
		Values: []EnumValue{{Value: value, Label: label}}, Source: source, Confidence: 0.75,
	}
}

func keyCandidate(model *Model, fieldID string, kind KeyCandidateType, confidence float64, reason string, source SourceRef) KeyCandidate {
	id := EntityID("schkey", "key", 1, fieldID+"\x00"+string(kind)+"\x00"+reason)
	return KeyCandidate{ID: id, FieldID: fieldID, Type: kind, Confidence: clampConfidence(confidence), Reason: reason, Evidence: compactSourceRefs([]SourceRef{source})}
}

func deriveKeyHints(field Field, source SourceRef) []KeyCandidate {
	name := NormalizeIdentifier(field.Name)
	hints := make([]KeyCandidate, 0, 2)
	if strings.HasSuffix(name, "id") || strings.HasSuffix(name, "key") || strings.HasSuffix(name, "no") || strings.HasSuffix(name, "number") {
		hints = append(hints, keyCandidate(nil, field.ID, KeyJoin, 0.60, "identifier-like name; relationship is only a candidate", source))
	}
	if containsAny(name, []string{"time", "date", "created", "updated", "时间", "日期"}) {
		hints = append(hints, keyCandidate(nil, field.ID, KeyTime, 0.50, "field name identifies a temporal key candidate", source))
	}
	return hints
}

func detectHeader(rows [][]string) (map[string]int, float64, []string) {
	for _, row := range rows {
		mapping, confidence, signals := mapHeaderColumnsWithScore(row)
		if confidence >= 0.55 {
			return mapping, confidence, signals
		}
	}
	return nil, 0, nil
}

func mapHeaderColumns(row []string) map[string]int {
	mapping, _, _ := mapHeaderColumnsWithScore(row)
	return mapping
}

func mapHeaderColumnsWithScore(row []string) (map[string]int, float64, []string) {
	mapping := make(map[string]int, len(headerPatterns))
	confidence := 0.10
	signals := make([]string, 0, len(headerPatterns))
	for _, pattern := range headerPatterns {
		index := -1
		for columnIndex, raw := range row {
			header := strings.ToLower(NormalizeText(raw))
			if header == "" {
				continue
			}
			for _, alias := range pattern.aliases {
				if header == alias || strings.Contains(header, alias) {
					index = columnIndex
					break
				}
			}
			if index >= 0 {
				break
			}
		}
		if index >= 0 {
			mapping[pattern.key] = index
			confidence += pattern.weight
			signals = append(signals, pattern.signal)
		}
	}
	if _, ok := mapping["field"]; !ok {
		return nil, 0, nil
	}
	_, hasType := mapping["type"]
	_, hasMeaning := mapping["meaning"]
	_, hasEnum := mapping["enum"]
	if !hasType && !hasMeaning && !hasEnum {
		return nil, 0, nil
	}
	return mapping, clampConfidence(confidence), signals
}

func aliasesFor(values ...string) []string {
	set := make(map[string]struct{})
	for _, value := range values {
		value = NormalizeText(value)
		if value == "" {
			continue
		}
		set[value] = struct{}{}
		lower := strings.ToLower(value)
		set[lower] = struct{}{}
		set[strings.ReplaceAll(lower, "_", " ")] = struct{}{}
		set[strings.ReplaceAll(lower, "-", " ")] = struct{}{}
	}
	out := make([]string, 0, len(set))
	for value := range set {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func splitRowValues(text string) []string { return strings.Split(text, "\t") }

func isTitleLike(values []string) bool {
	nonempty := make([]string, 0, len(values))
	for _, value := range values {
		if value = NormalizeText(value); value != "" {
			nonempty = append(nonempty, value)
		}
	}
	if len(nonempty) != 1 || len(nonempty[0]) < 2 {
		return false
	}
	return !isSchemaHeaderValues(values)
}

func isSchemaHeaderValues(values []string) bool {
	_, confidence, _ := mapHeaderColumnsWithScore(values)
	return confidence >= 0.55
}

func rowNumber(cellRange string) (int, bool) {
	if cellRange == "" {
		return 0, false
	}
	start := -1
	for index := 0; index < len(cellRange); index++ {
		if cellRange[index] >= '0' && cellRange[index] <= '9' {
			start = index
			break
		}
	}
	if start < 0 {
		return 0, false
	}
	end := start
	for end < len(cellRange) && cellRange[end] >= '0' && cellRange[end] <= '9' {
		end++
	}
	number, err := strconv.Atoi(cellRange[start:end])
	if err != nil {
		return 0, false
	}
	return number, true
}
func joinRange(first, second string) string {
	if first == "" {
		return second
	}
	if second == "" {
		return first
	}
	return first + ":" + second
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = NormalizeText(value); value != "" {
			return value
		}
	}
	return ""
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func containsAny(value string, needles []string) bool {
	value = strings.ToLower(value)
	for _, needle := range needles {
		if strings.Contains(value, strings.ToLower(needle)) {
			return true
		}
	}
	return false
}

var conceptPatterns = []struct {
	name    string
	aliases []string
}{
	{"User Identifier", []string{"user", "subscriber", "imsi", "msisdn", "用户", "订户"}},
	{"Site", []string{"site", "station", "cell", "站点", "小区", "基站"}},
	{"Traffic", []string{"traffic", "volume", "流量", "话务"}},
	{"Revenue", []string{"revenue", "fee", "charge", "收入", "费用", "计费"}},
	{"Status", []string{"status", "state", "状态"}},
	{"Time", []string{"time", "date", "时间", "日期"}},
	{"Terminal", []string{"terminal", "device", "终端", "设备"}},
	{"Network Generation", []string{"5g", "4g", "lte", "nr", "网络制式", "网络"}},
	{"Order", []string{"order", "订单"}},
	{"Fault", []string{"fault", "error", "故障", "错误"}},
	{"Product", []string{"product", "offer", "产品"}},
	{"Plan", []string{"plan", "package", "套餐"}},
	{"Roaming", []string{"roaming", "漫游"}},
	{"Session", []string{"session", "会话"}},
	{"Quality", []string{"quality", "kpi", "质量"}},
	{"Region", []string{"region", "province", "city", "区域", "省", "城市"}},
}

func conceptsFor(text string) []string {
	normalized := strings.ToLower(NormalizeText(text))
	out := make([]string, 0, 3)
	for _, concept := range conceptPatterns {
		if containsAny(normalized, concept.aliases) {
			out = append(out, concept.name)
		}
	}
	return out
}

func buildConcepts(model *Model) {
	byField := make(map[string]Field, len(model.Fields))
	for _, field := range model.Fields {
		byField[field.ID] = field
	}
	type conceptBuilder struct{ concept BusinessConcept }
	builders := make(map[string]*conceptBuilder)
	for _, field := range model.Fields {
		for _, name := range field.Concepts {
			id := EntityID("schcon", model.BaseID, model.Generation, "concept:"+name)
			builder, ok := builders[id]
			if !ok {
				builder = &conceptBuilder{concept: BusinessConcept{ID: id, BaseID: model.BaseID, Generation: model.Generation, Name: name, Confidence: 0.62}}
				builders[id] = builder
			}
			builder.concept.FieldIDs = append(builder.concept.FieldIDs, field.ID)
			builder.concept.DerivedFrom = append(builder.concept.DerivedFrom, field.Source)
		}
		_ = byField
	}
	model.Concepts = make([]BusinessConcept, 0, len(builders))
	for _, builder := range builders {
		builder.concept.FieldIDs = dedupeStrings(builder.concept.FieldIDs)
		builder.concept.DerivedFrom = compactSourceRefs(builder.concept.DerivedFrom)
		model.Concepts = append(model.Concepts, builder.concept)
	}
	_ = fmt.Sprint
}

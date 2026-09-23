package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/documentir"
	"github.com/shutu-ai/shutu-knowledge/internal/schemamodel"
)

// listSchemaSourceRows reconstructs raw table-cell values rather than reusing
// the human-facing `header=value` row projection. Cells are loaded in a second
// pass because IR ordering can interleave cell and row publication.
func (s *store) listSchemaSourceRows(ctx context.Context, docID string) ([]schemamodel.SourceRow, error) {
	doc, err := s.getDocumentContext(ctx, docID)
	if err != nil {
		return nil, err
	}
	type rawRow struct {
		nodeID      string
		sheet       string
		rangeAnchor string
		values      map[int]string
		maxCol      int
	}
	rowRows, err := s.db.QueryContext(ctx, `SELECT node_id, source_anchor
		FROM document_nodes
		WHERE doc_id = ? AND index_generation = ? AND node_type = ?
		ORDER BY node_order, node_id`, docID, doc.ActiveIndexGen, documentir.TypeTableRow)
	if err != nil {
		return nil, err
	}
	rowByNode := make(map[string]*rawRow)
	var order []string
	for rowRows.Next() {
		var nodeID, anchorRaw string
		if err := rowRows.Scan(&nodeID, &anchorRaw); err != nil {
			_ = rowRows.Close()
			return nil, err
		}
		var anchor documentir.SourceAnchor
		_ = json.Unmarshal([]byte(anchorRaw), &anchor)
		rowByNode[nodeID] = &rawRow{nodeID: nodeID, sheet: anchor.Sheet, rangeAnchor: anchor.CellRange, values: map[int]string{}}
		order = append(order, nodeID)
	}
	if err := rowRows.Err(); err != nil {
		_ = rowRows.Close()
		return nil, err
	}
	_ = rowRows.Close()

	cellRows, err := s.db.QueryContext(ctx, `SELECT parent_node_id, text, source_anchor
		FROM document_nodes
		WHERE doc_id = ? AND index_generation = ? AND node_type = ?
		ORDER BY node_order, node_id`, docID, doc.ActiveIndexGen, documentir.TypeTableCell)
	if err != nil {
		return nil, err
	}
	defer cellRows.Close()
	for cellRows.Next() {
		var parentID, text, anchorRaw string
		if err := cellRows.Scan(&parentID, &text, &anchorRaw); err != nil {
			return nil, err
		}
		row, ok := rowByNode[parentID]
		if !ok {
			continue
		}
		var anchor documentir.SourceAnchor
		_ = json.Unmarshal([]byte(anchorRaw), &anchor)
		column, _ := cellPosition(anchor.CellRange)
		if column <= 0 {
			continue
		}
		row.values[column] = text
		if column > row.maxCol {
			row.maxCol = column
		}
	}
	if err := cellRows.Err(); err != nil {
		return nil, err
	}
	out := make([]schemamodel.SourceRow, 0, len(order))
	for _, nodeID := range order {
		row := rowByNode[nodeID]
		values := make([]string, row.maxCol)
		for column := 1; column <= row.maxCol; column++ {
			values[column-1] = row.values[column]
		}
		out = append(out, schemamodel.SourceRow{NodeID: row.nodeID, Text: strings.Join(values, "\t"), Range: row.rangeAnchor, Sheet: row.sheet})
	}
	return out, nil
}

func cellPosition(ref string) (column, rowNumber int) {
	if ref == "" {
		return 0, 0
	}
	index := 0
	column = 0
	for index < len(ref) && ref[index] >= 'A' && ref[index] <= 'Z' {
		column = column*26 + int(ref[index]-'A') + 1
		index++
	}
	if index >= len(ref) {
		return column, 0
	}
	number, err := strconv.Atoi(ref[index:])
	if err != nil {
		return column, 0
	}
	return column, number
}

// CompileDocumentSchema compiles schema entities from the already published
// Document IR generation. It does not reparse or reimport source bytes.
func (s *Service) activeSchemaModels(ctx context.Context, baseID string) ([]schemamodel.Model, error) {
	s.schemaCacheMu.RLock()
	if s.schemaCacheValid && s.schemaCacheBaseID == baseID {
		models := s.schemaCacheModels
		s.schemaCacheMu.RUnlock()
		return models, nil
	}
	s.schemaCacheMu.RUnlock()
	models, err := schemamodel.NewStore(s.store.db).ActiveModels(ctx, baseID)
	if err != nil {
		return nil, err
	}
	s.schemaCacheMu.Lock()
	s.schemaCacheBaseID, s.schemaCacheModels, s.schemaCacheValid = baseID, models, true
	s.schemaCacheMu.Unlock()
	return models, nil
}

func (s *Service) invalidateSchemaCache() {
	s.schemaCacheMu.Lock()
	s.schemaCacheValid = false
	s.schemaCacheModels = nil
	s.schemaCacheMu.Unlock()
}

func (s *Service) CompileDocumentSchema(ctx context.Context, documentID string) (schemamodel.Model, error) {
	doc, err := s.store.getDocumentContext(ctx, documentID)
	if err != nil {
		return schemamodel.Model{}, err
	}
	rows, err := s.store.listSchemaSourceRows(ctx, documentID)
	if err != nil {
		return schemamodel.Model{}, err
	}
	input := schemamodel.DocumentInput{
		BaseID: doc.BaseID, DocumentID: doc.ID, Generation: doc.ActiveIndexGen,
		Title: doc.Title, SourcePath: doc.SourcePath, Rows: rows,
	}
	input.Generation = schemaGeneration(doc.ActiveIndexGen)
	started := time.Now()
	model, detected := schemamodel.CompileDocument(input)
	elapsed := time.Since(started).Milliseconds()
	if err := schemamodel.NewStore(s.store.db).ReplaceDocumentModel(ctx, input, model, elapsed, detected); err != nil {
		return schemamodel.Model{}, err
	}
	return model, nil
}

// CompileBaseSchema compiles every active document in a base. Existing IR is
// reused; a document without table rows is explicitly marked absent.
func (s *Service) CompileBaseSchema(ctx context.Context, baseID string) (SchemaCompileReport, error) {
	documents, err := s.store.listDocuments(baseID)
	if err != nil {
		return SchemaCompileReport{}, err
	}
	baseStarted := time.Now()
	report := SchemaCompileReport{BaseID: baseID, DocumentCount: len(documents)}
	store := schemamodel.NewStore(s.store.db)
	for _, doc := range documents {
		if doc.LifecycleState != LifecycleActive || strings.EqualFold(doc.SourceType, "directory") {
			continue
		}
		rows, err := s.store.listSchemaSourceRows(ctx, doc.ID)
		if err != nil {
			return report, fmt.Errorf("load schema rows %s: %w", doc.ID, err)
		}
		input := schemamodel.DocumentInput{
			BaseID: baseID, DocumentID: doc.ID, Generation: schemaGeneration(doc.ActiveIndexGen),
			Title: doc.Title, SourcePath: doc.SourcePath, Rows: rows,
		}
		started := time.Now()
		model, detected := schemamodel.CompileDocument(input)
		elapsed := time.Since(started).Milliseconds()
		if err := store.ReplaceDocumentModel(ctx, input, model, elapsed, detected); err != nil {
			return report, fmt.Errorf("compile schema %s: %w", doc.ID, err)
		}
		report.SourceRowCount += int64(len(rows))
		report.DetectedDocuments += boolInt64(detected)
		report.TableCount += int64(len(model.Tables))
		report.FieldCount += int64(len(model.Fields))
		report.EnumCount += int64(len(model.Enums))
		report.ConceptCount += int64(len(model.Concepts))
		report.KeyCandidateCount += int64(len(model.KeyCandidates))
		report.CompileMS += elapsed
	}
	report.ElapsedMS = time.Since(baseStarted).Milliseconds()
	s.invalidateSchemaCache()
	return report, nil
}

// SchemaCompileReport is a bounded, JSON-ready compile diagnostic.
type SchemaCompileReport struct {
	BaseID            string `json:"baseId"`
	DocumentCount     int    `json:"documentCount"`
	SourceRowCount    int64  `json:"sourceRowCount"`
	DetectedDocuments int64  `json:"detectedDocuments"`
	TableCount        int64  `json:"tableCount"`
	FieldCount        int64  `json:"fieldCount"`
	EnumCount         int64  `json:"enumCount"`
	ConceptCount      int64  `json:"conceptCount"`
	KeyCandidateCount int64  `json:"keyCandidateCount"`
	CompileMS         int64  `json:"compileMs"`
	ElapsedMS         int64  `json:"elapsedMs"`
}

func schemaGeneration(value int64) int64 {
	if value <= 0 {
		return 1
	}
	return value
}

func boolInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

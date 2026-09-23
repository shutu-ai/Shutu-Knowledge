package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/shutu-ai/shutu-knowledge/internal/documentir"
	"github.com/shutu-ai/shutu-knowledge/internal/schemamodel"
)

// listSchemaSourceRows loads only the active structured table-row projection
// needed by the generic schema compiler. Legacy documents produce zero rows.
func (s *store) listSchemaSourceRows(ctx context.Context, docID string) ([]schemamodel.SourceRow, error) {
	doc, err := s.getDocumentContext(ctx, docID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT node_id, text, source_anchor
		FROM document_nodes
		WHERE doc_id = ? AND index_generation = ? AND node_type = ?
		ORDER BY node_order, node_id`, docID, doc.ActiveIndexGen, documentir.TypeTableRow)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]schemamodel.SourceRow, 0)
	for rows.Next() {
		var row schemamodel.SourceRow
		var anchorRaw string
		if err := rows.Scan(&row.NodeID, &row.Text, &anchorRaw); err != nil {
			return nil, err
		}
		var anchor documentir.SourceAnchor
		if err := json.Unmarshal([]byte(anchorRaw), &anchor); err == nil {
			row.Range = anchor.CellRange
			row.Sheet = anchor.Sheet
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// CompileDocumentSchema compiles schema entities from the already published
// Document IR generation. It does not reparse or reimport source bytes.
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
			BaseID: baseID, DocumentID: doc.ID, Generation: doc.ActiveIndexGen,
			Title: doc.Title, SourcePath: doc.SourcePath, Rows: rows,
		}
		started := time.Now()
		model, detected := schemamodel.CompileDocument(input)
		elapsed := time.Since(started).Milliseconds()
		if err := store.ReplaceDocumentModel(ctx, input, model, elapsed, detected); err != nil {
			return report, fmt.Errorf("compile schema %s: %w", doc.ID, err)
		}
		report.DetectedDocuments += boolInt64(detected)
		report.TableCount += int64(len(model.Tables))
		report.FieldCount += int64(len(model.Fields))
		report.EnumCount += int64(len(model.Enums))
		report.ConceptCount += int64(len(model.Concepts))
		report.KeyCandidateCount += int64(len(model.KeyCandidates))
		report.CompileMS += elapsed
	}
	report.ElapsedMS = time.Since(baseStarted).Milliseconds()
	return report, nil
}

// SchemaCompileReport is a bounded, JSON-ready compile diagnostic.
type SchemaCompileReport struct {
	BaseID            string `json:"baseId"`
	DocumentCount     int    `json:"documentCount"`
	DetectedDocuments int64  `json:"detectedDocuments"`
	TableCount        int64  `json:"tableCount"`
	FieldCount        int64  `json:"fieldCount"`
	EnumCount         int64  `json:"enumCount"`
	ConceptCount      int64  `json:"conceptCount"`
	KeyCandidateCount int64  `json:"keyCandidateCount"`
	CompileMS         int64  `json:"compileMs"`
	ElapsedMS         int64  `json:"elapsedMs"`
}

func boolInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

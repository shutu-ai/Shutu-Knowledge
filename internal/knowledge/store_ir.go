package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/shutu-ai/shutu-knowledge/internal/documentir"
)

func (s *store) listDerivedKnowledge(ctx context.Context, docID string) ([]DerivedKnowledge, error) {
	doc, err := s.getDocumentContext(ctx, docID)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, doc_id, index_generation, kind, content, derived_from, model, model_version, created_at
		FROM derived_knowledge WHERE doc_id = ? AND index_generation = ? AND invalidated_at IS NULL ORDER BY kind, id`, docID, doc.ActiveIndexGen)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DerivedKnowledge
	for rows.Next() {
		var item DerivedKnowledge
		var raw string
		if err := rows.Scan(&item.ID, &item.DocID, &item.IndexGeneration, &item.Kind, &item.Content, &raw, &item.Model, &item.ModelVersion, &item.GeneratedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(raw), &item.DerivedFrom)
		out = append(out, item)
	}
	return out, rows.Err()
}

// enrichChunkProvenance loads only the small source-anchor projection needed
// by final search hits. It keeps the existing lexical/vector lane contracts
// unchanged and returns no rows for legacy generations without an IR.
func (s *store) enrichChunkProvenance(ctx context.Context, q queryRunner, chunks []Chunk) error {
	for i := range chunks {
		c := &chunks[i]
		// Search may enrich the candidate list before structure filtering and
		// enrich the final list again. Rebuild the projection idempotently so
		// node IDs are not duplicated and the most specific anchor wins.
		c.NodeIDs = nil
		c.NodeTypes = nil
		c.SourceAnchor = documentir.SourceAnchor{}
		bestAnchorScore := -1
		rows, err := q.QueryContext(ctx, `SELECT n.node_id, n.node_type, n.source_anchor, n.metadata
			FROM chunk_node_links l JOIN document_nodes n
			  ON n.doc_id = l.doc_id AND n.index_generation = l.index_generation AND n.node_id = l.node_id
			WHERE l.doc_id = ? AND l.index_generation = ? AND l.chunk_id = ?
			ORDER BY l.link_order`, c.DocID, c.IndexGeneration, c.ID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var nodeID, nodeType, rawAnchor, rawMetadata string
			if err := rows.Scan(&nodeID, &nodeType, &rawAnchor, &rawMetadata); err != nil {
				_ = rows.Close()
				return err
			}
			c.NodeIDs = append(c.NodeIDs, nodeID)
			c.NodeTypes = append(c.NodeTypes, nodeType)
			if rawAnchor != "" {
				var anchor documentir.SourceAnchor
				if err := json.Unmarshal([]byte(rawAnchor), &anchor); err == nil {
					metadata := map[string]string{}
					_ = json.Unmarshal([]byte(rawMetadata), &metadata)
					score := sourceAnchorScore(nodeType, metadata)
					if anchor.Kind != "" && score > bestAnchorScore {
						c.SourceAnchor = anchor
						bestAnchorScore = score
					}
				}
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		_ = rows.Close()
	}
	return nil
}

func sourceAnchorScore(nodeType string, metadata map[string]string) int {
	score := 20
	switch nodeType {
	case documentir.TypeTableCell:
		score = 100
		if metadata != nil && metadata["header"] == "true" {
			// A data cell is stronger evidence for a row query than the
			// header cell whose label happens to occur in a row representation.
			score = 90
		}
	case documentir.TypeParagraph, documentir.TypeBlock, documentir.TypeHeading:
		score = 80
	case documentir.TypePage, documentir.TypeSlide, documentir.TypeSheet:
		// Structure-aware page/slide/sheet chunks retain the whole container;
		// prefer that anchor over a coincident child block so page identity does
		// not depend on the first matching line of text.
		score = 90
	case documentir.TypeCaption, documentir.TypeFigure:
		score = 70
	}
	return score
}

// ListDocumentIR returns the active generation's structured nodes and
// relationships. The method is intentionally read-only and source-scoped.
func (s *store) listDocumentIR(ctx context.Context, docID string) (documentir.Document, error) {
	doc, err := s.getDocumentContext(ctx, docID)
	if err != nil {
		return documentir.Document{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT node_id, COALESCE(parent_node_id, ''), node_type,
		node_order, text, heading_path, source_anchor, COALESCE(page_number, 0),
		COALESCE(slide_number, 0), COALESCE(sheet_name, ''), COALESCE(bbox, '{}'),
		COALESCE(metadata, '{}'), parser, parser_version, confidence
		FROM document_nodes WHERE doc_id = ? AND index_generation = ? ORDER BY node_order, node_id`, docID, doc.ActiveIndexGen)
	if err != nil {
		return documentir.Document{}, err
	}
	defer rows.Close()
	ir := documentir.Document{IRVersion: documentir.Version, DocumentID: docID, Title: doc.Title}
	var parseConfig string
	if err := s.db.QueryRowContext(ctx, `SELECT ir_version, parser, parser_version, parse_config FROM document_parse_metadata WHERE doc_id = ? AND index_generation = ?`, docID, doc.ActiveIndexGen).Scan(&ir.IRVersion, &ir.Parser, &ir.ParserVersion, &parseConfig); err != nil && err != sql.ErrNoRows {
		return documentir.Document{}, err
	}
	if parseConfig != "" {
		_ = json.Unmarshal([]byte(parseConfig), &ir.ParseConfig)
	}
	for rows.Next() {
		var n documentir.Node
		var parent, headingRaw, anchorRaw, bboxRaw, metadataRaw string
		if err := rows.Scan(&n.ID, &parent, &n.Type, &n.Order, &n.Text, &headingRaw, &anchorRaw, &n.PageNumber, &n.SlideNumber, &n.SheetName, &bboxRaw, &metadataRaw, &n.Parser, &n.ParserVersion, &n.Confidence); err != nil {
			return documentir.Document{}, err
		}
		n.DocumentID, n.ParentID = docID, parent
		_ = json.Unmarshal([]byte(headingRaw), &n.HeadingPath)
		_ = json.Unmarshal([]byte(anchorRaw), &n.SourceAnchor)
		if strings.TrimSpace(bboxRaw) != "" && bboxRaw != "null" && bboxRaw != "{}" {
			var bbox documentir.BBox
			if err := json.Unmarshal([]byte(bboxRaw), &bbox); err == nil {
				n.BBox = &bbox
			}
		}
		if strings.TrimSpace(metadataRaw) != "" && metadataRaw != "null" && metadataRaw != "{}" {
			_ = json.Unmarshal([]byte(metadataRaw), &n.Metadata)
		}
		ir.Nodes = append(ir.Nodes, n)
	}
	if err := rows.Err(); err != nil {
		return documentir.Document{}, err
	}
	relRows, err := s.db.QueryContext(ctx, `SELECT relationship_id, from_node_id, to_node_id, relationship_type, relationship_order FROM document_relationships WHERE doc_id = ? AND index_generation = ? ORDER BY relationship_order, relationship_id`, docID, doc.ActiveIndexGen)
	if err != nil && err != sql.ErrNoRows {
		return documentir.Document{}, err
	}
	if relRows != nil {
		defer relRows.Close()
		for relRows.Next() {
			var r documentir.Relationship
			if err := relRows.Scan(&r.ID, &r.FromNodeID, &r.ToNodeID, &r.Type, &r.Order); err != nil {
				return documentir.Document{}, err
			}
			ir.Relationships = append(ir.Relationships, r)
		}
	}
	for i := range ir.Nodes {
		if ir.Nodes[i].ParentID != "" {
			for j := range ir.Nodes {
				if ir.Nodes[j].ID == ir.Nodes[i].ParentID {
					ir.Nodes[j].ChildrenIDs = append(ir.Nodes[j].ChildrenIDs, ir.Nodes[i].ID)
					break
				}
			}
		}
	}
	return ir, nil
}

// listDocumentIRsContext loads active IR for a bounded metadata page in three
// bulk queries instead of making three queries per document. Semantic first
// compilation is document-heavy; this keeps the database round trips bounded
// by batch count rather than corpus size.
func (s *store) listDocumentIRsContext(ctx context.Context, documents []Document) (map[string]documentir.Document, error) {
	out := make(map[string]documentir.Document, len(documents))
	byID := make(map[string]Document, len(documents))
	for _, document := range documents {
		byID[document.ID] = document
		out[document.ID] = documentir.Document{
			IRVersion: documentir.Version, DocumentID: document.ID, Title: document.Title,
		}
	}
	const batchSize = 100
	for start := 0; start < len(documents); start += batchSize {
		end := minInt(start+batchSize, len(documents))
		ids := make([]string, 0, end-start)
		for _, document := range documents[start:end] {
			ids = append(ids, document.ID)
		}
		placeholders := strings.Repeat("?,", len(ids))
		placeholders = strings.TrimSuffix(placeholders, ",")
		args := make([]any, 0, len(ids))
		for _, id := range ids {
			args = append(args, id)
		}

		rows, err := s.db.QueryContext(ctx, `SELECT n.doc_id, n.node_id, COALESCE(n.parent_node_id, ''), n.node_type,
			n.node_order, n.text, n.heading_path, n.source_anchor, COALESCE(n.page_number, 0),
			COALESCE(n.slide_number, 0), COALESCE(n.sheet_name, ''), COALESCE(n.bbox, '{}'),
			COALESCE(n.metadata, '{}'), n.parser, n.parser_version, n.confidence
			FROM document_nodes n
			JOIN documents d ON d.id = n.doc_id AND n.index_generation = d.active_index_generation
			WHERE n.doc_id IN (`+placeholders+`)
			ORDER BY n.doc_id, n.node_order, n.node_id`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var documentID string
			var n documentir.Node
			var parent, headingRaw, anchorRaw, bboxRaw, metadataRaw string
			if err := rows.Scan(&documentID, &n.ID, &parent, &n.Type, &n.Order, &n.Text,
				&headingRaw, &anchorRaw, &n.PageNumber, &n.SlideNumber, &n.SheetName,
				&bboxRaw, &metadataRaw, &n.Parser, &n.ParserVersion, &n.Confidence); err != nil {
				_ = rows.Close()
				return nil, err
			}
			n.DocumentID, n.ParentID = documentID, parent
			_ = json.Unmarshal([]byte(headingRaw), &n.HeadingPath)
			_ = json.Unmarshal([]byte(anchorRaw), &n.SourceAnchor)
			if strings.TrimSpace(bboxRaw) != "" && bboxRaw != "null" && bboxRaw != "{}" {
				var bbox documentir.BBox
				if err := json.Unmarshal([]byte(bboxRaw), &bbox); err == nil {
					n.BBox = &bbox
				}
			}
			if strings.TrimSpace(metadataRaw) != "" && metadataRaw != "null" && metadataRaw != "{}" {
				_ = json.Unmarshal([]byte(metadataRaw), &n.Metadata)
			}
			ir := out[documentID]
			ir.Nodes = append(ir.Nodes, n)
			out[documentID] = ir
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()

		metaRows, err := s.db.QueryContext(ctx, `SELECT m.doc_id, m.ir_version, m.parser, m.parser_version, m.parse_config
			FROM document_parse_metadata m
			JOIN documents d ON d.id = m.doc_id AND m.index_generation = d.active_index_generation
			WHERE m.doc_id IN (`+placeholders+`) ORDER BY m.doc_id`, args...)
		if err != nil {
			return nil, err
		}
		for metaRows.Next() {
			var documentID, parseConfig string
			ir := documentir.Document{}
			if err := metaRows.Scan(&documentID, &ir.IRVersion, &ir.Parser, &ir.ParserVersion, &parseConfig); err != nil {
				_ = metaRows.Close()
				return nil, err
			}
			if _, exists := byID[documentID]; !exists {
				continue
			}
			if parseConfig != "" {
				_ = json.Unmarshal([]byte(parseConfig), &ir.ParseConfig)
			}
			base := out[documentID]
			base.IRVersion, base.Parser, base.ParserVersion, base.ParseConfig =
				ir.IRVersion, ir.Parser, ir.ParserVersion, ir.ParseConfig
			out[documentID] = base
		}
		if err := metaRows.Err(); err != nil {
			_ = metaRows.Close()
			return nil, err
		}
		_ = metaRows.Close()

		relRows, err := s.db.QueryContext(ctx, `SELECT r.doc_id, r.relationship_id, r.from_node_id, r.to_node_id,
			r.relationship_type, r.relationship_order
			FROM document_relationships r
			JOIN documents d ON d.id = r.doc_id AND r.index_generation = d.active_index_generation
			WHERE r.doc_id IN (`+placeholders+`)
			ORDER BY r.doc_id, r.relationship_order, r.relationship_id`, args...)
		if err != nil {
			return nil, err
		}
		for relRows.Next() {
			var documentID string
			var relation documentir.Relationship
			if err := relRows.Scan(&documentID, &relation.ID, &relation.FromNodeID,
				&relation.ToNodeID, &relation.Type, &relation.Order); err != nil {
				_ = relRows.Close()
				return nil, err
			}
			if _, exists := byID[documentID]; !exists {
				continue
			}
			ir := out[documentID]
			ir.Relationships = append(ir.Relationships, relation)
			out[documentID] = ir
		}
		if err := relRows.Err(); err != nil {
			_ = relRows.Close()
			return nil, err
		}
		_ = relRows.Close()
	}

	for documentID, ir := range out {
		index := make(map[string]int, len(ir.Nodes))
		for i := range ir.Nodes {
			index[ir.Nodes[i].ID] = i
		}
		for i := range ir.Nodes {
			if parent := ir.Nodes[i].ParentID; parent != "" {
				if j, ok := index[parent]; ok {
					ir.Nodes[j].ChildrenIDs = append(ir.Nodes[j].ChildrenIDs, ir.Nodes[i].ID)
				}
			}
		}
		out[documentID] = ir
	}
	return out, nil
}

// chunkIDsByNodes bulk-loads exact chunk-node mappings for a metadata page.
func (s *store) chunkIDsByNodes(ctx context.Context, documents []Document) (map[string]map[string][]string, error) {
	out := make(map[string]map[string][]string, len(documents))
	const batchSize = 100
	for start := 0; start < len(documents); start += batchSize {
		end := minInt(start+batchSize, len(documents))
		ids := make([]string, 0, end-start)
		for _, document := range documents[start:end] {
			ids = append(ids, document.ID)
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
		args := make([]any, 0, len(ids))
		for _, id := range ids {
			args = append(args, id)
		}
		rows, err := s.db.QueryContext(ctx, `SELECT l.doc_id, l.node_id, l.chunk_id
			FROM chunk_node_links l
			JOIN documents d ON d.id = l.doc_id AND l.index_generation = d.active_index_generation
			WHERE l.doc_id IN (`+placeholders+`)
			ORDER BY l.doc_id, l.node_id, l.link_order, l.chunk_id`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var documentID, nodeID, chunkID string
			if err := rows.Scan(&documentID, &nodeID, &chunkID); err != nil {
				_ = rows.Close()
				return nil, err
			}
			if out[documentID] == nil {
				out[documentID] = map[string][]string{}
			}
			out[documentID][nodeID] = append(out[documentID][nodeID], chunkID)
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return out, nil
}

// countSemanticCompileDocumentsContext returns the bounded admission unit
// count used by the durable semantic-compilation operation.
func (s *store) countSemanticCompileDocumentsContext(ctx context.Context, baseID string) (int, error) {
	var total int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(DISTINCT d.id)
		FROM documents d
		WHERE d.base_id = ? AND d.lifecycle_state = ? AND d.status = ?
		  AND EXISTS (
			SELECT 1 FROM document_nodes n
			WHERE n.doc_id = d.id AND n.index_generation = d.active_index_generation
		  )`, baseID, LifecycleActive, StatusReady).Scan(&total)
	return total, err
}

// chunkIDsByNode returns the active generation's exact chunk-node mapping for
// semantic compilation. A missing mapping is not fabricated; the compiler can
// still cite the IR node directly.
func (s *store) chunkIDsByNode(ctx context.Context, docID string, generation int64) (map[string][]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT node_id, chunk_id
		FROM chunk_node_links WHERE doc_id = ? AND index_generation = ?
		ORDER BY node_id, link_order, chunk_id`, docID, generation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]string{}
	for rows.Next() {
		var nodeID, chunkID string
		if err := rows.Scan(&nodeID, &chunkID); err != nil {
			return nil, err
		}
		out[nodeID] = append(out[nodeID], chunkID)
	}
	return out, rows.Err()
}

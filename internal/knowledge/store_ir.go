package knowledge

import (
	"context"
	"database/sql"
	"encoding/json"

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
		rows, err := q.QueryContext(ctx, `SELECT n.node_id, n.node_type, n.source_anchor
			FROM chunk_node_links l JOIN document_nodes n
			  ON n.doc_id = l.doc_id AND n.index_generation = l.index_generation AND n.node_id = l.node_id
			WHERE l.doc_id = ? AND l.index_generation = ? AND l.chunk_id = ?
			ORDER BY l.link_order`, c.DocID, c.IndexGeneration, c.ID)
		if err != nil {
			return err
		}
		for rows.Next() {
			var nodeID, nodeType, rawAnchor string
			if err := rows.Scan(&nodeID, &nodeType, &rawAnchor); err != nil {
				_ = rows.Close()
				return err
			}
			c.NodeIDs = append(c.NodeIDs, nodeID)
			c.NodeTypes = append(c.NodeTypes, nodeType)
			if c.SourceAnchor.Kind == "" && rawAnchor != "" {
				var anchor documentir.SourceAnchor
				if err := json.Unmarshal([]byte(rawAnchor), &anchor); err == nil {
					c.SourceAnchor = anchor
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

// ListDocumentIR returns the active generation's structured nodes and
// relationships. The method is intentionally read-only and source-scoped.
func (s *store) listDocumentIR(ctx context.Context, docID string) (documentir.Document, error) {
	doc, err := s.getDocumentContext(ctx, docID)
	if err != nil {
		return documentir.Document{}, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT node_id, COALESCE(parent_node_id, ''), node_type,
		node_order, text, heading_path, source_anchor, COALESCE(page_number, 0),
		COALESCE(slide_number, 0), COALESCE(sheet_name, ''), parser, parser_version, confidence
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
		var parent, headingRaw, anchorRaw string
		if err := rows.Scan(&n.ID, &parent, &n.Type, &n.Order, &n.Text, &headingRaw, &anchorRaw, &n.PageNumber, &n.SlideNumber, &n.SheetName, &n.Parser, &n.ParserVersion, &n.Confidence); err != nil {
			return documentir.Document{}, err
		}
		n.DocumentID, n.ParentID = docID, parent
		_ = json.Unmarshal([]byte(headingRaw), &n.HeadingPath)
		_ = json.Unmarshal([]byte(anchorRaw), &n.SourceAnchor)
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

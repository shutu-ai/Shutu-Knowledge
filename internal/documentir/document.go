// Package documentir defines the parser-independent structured document
// representation used by the 0.3 document-intelligence foundation.
package documentir

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const Version = "document-ir/v1"

const (
	TypeDocument  = "document"
	TypeSection   = "section"
	TypePage      = "page"
	TypeSlide     = "slide"
	TypeSheet     = "sheet"
	TypeBlock     = "block"
	TypeParagraph = "paragraph"
	TypeHeading   = "heading"
	TypeList      = "list"
	TypeListItem  = "list_item"
	TypeTable     = "table"
	TypeTableRow  = "table_row"
	TypeTableCell = "table_cell"
	TypeFigure    = "figure"
	TypeImage     = "image"
	TypeCaption   = "caption"
	TypeCode      = "code_block"
	TypeFootnote  = "footnote"
	TypeHeader    = "header"
	TypeFooter    = "footer"
)

type Document struct {
	IRVersion     string            `json:"ir_version"`
	DocumentID    string            `json:"document_id"`
	Title         string            `json:"title,omitempty"`
	Parser        string            `json:"parser,omitempty"`
	ParserVersion string            `json:"parser_version,omitempty"`
	ParseConfig   map[string]string `json:"parse_config,omitempty"`
	Nodes         []Node            `json:"nodes"`
	Relationships []Relationship    `json:"relationships,omitempty"`

	nodeTextIndex map[string][]int
	nodeChildIDs  map[string][]int
	nodeIDs       map[string]int
}

type Node struct {
	ID            string            `json:"id"`
	Type          string            `json:"type"`
	DocumentID    string            `json:"document_id"`
	ParentID      string            `json:"parent_id,omitempty"`
	ChildrenIDs   []string          `json:"children_ids,omitempty"`
	Order         int               `json:"order"`
	Text          string            `json:"text,omitempty"`
	HeadingPath   []string          `json:"heading_path,omitempty"`
	SourceAnchor  SourceAnchor      `json:"source_anchor,omitempty"`
	PageNumber    int               `json:"page_number,omitempty"`
	SlideNumber   int               `json:"slide_number,omitempty"`
	SheetName     string            `json:"sheet_name,omitempty"`
	BBox          *BBox             `json:"bbox,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
	Parser        string            `json:"parser,omitempty"`
	ParserVersion string            `json:"parser_version,omitempty"`
	Confidence    float64           `json:"confidence,omitempty"`
}

type Relationship struct {
	ID         string `json:"id"`
	FromNodeID string `json:"from_node_id"`
	ToNodeID   string `json:"to_node_id"`
	Type       string `json:"type"`
	Order      int    `json:"order,omitempty"`
}

type SourceAnchor struct {
	Kind        string `json:"kind,omitempty"`
	Page        int    `json:"page,omitempty"`
	Slide       int    `json:"slide,omitempty"`
	Shape       string `json:"shape,omitempty"`
	Sheet       string `json:"sheet,omitempty"`
	CellRange   string `json:"cell_range,omitempty"`
	Section     string `json:"section,omitempty"`
	Paragraph   int    `json:"paragraph,omitempty"`
	Block       int    `json:"block,omitempty"`
	LogicalPath string `json:"logical_path,omitempty"`
	BBox        *BBox  `json:"bbox,omitempty"`
}

type BBox struct {
	X1 float64 `json:"x1"`
	Y1 float64 `json:"y1"`
	X2 float64 `json:"x2"`
	Y2 float64 `json:"y2"`
}

func (a SourceAnchor) String() string {
	b, _ := json.Marshal(a)
	return string(b)
}

func NodeID(documentID, nodeType, logicalPosition string, anchor SourceAnchor) string {
	seed := strings.Join([]string{documentID, nodeType, logicalPosition, anchor.String()}, "\x00")
	sum := sha256.Sum256([]byte(seed))
	return "node_" + hex.EncodeToString(sum[:16])
}

func RelationshipID(from, to, relation string) string {
	sum := sha256.Sum256([]byte(from + "\x00" + to + "\x00" + relation))
	return "rel_" + hex.EncodeToString(sum[:16])
}

// BindDocument assigns document identity and deterministic IDs to an IR
// created by a parser. It also repairs all internal references atomically.
func (d *Document) BindDocument(documentID string) error {
	if d == nil {
		return fmt.Errorf("document IR is nil")
	}
	if strings.TrimSpace(documentID) == "" {
		return fmt.Errorf("document ID is empty")
	}
	d.nodeTextIndex = nil
	d.nodeChildIDs = nil
	d.nodeIDs = nil
	if d.IRVersion == "" {
		d.IRVersion = Version
	}
	if d.IRVersion != Version {
		return fmt.Errorf("unsupported document IR version %q", d.IRVersion)
	}
	oldToNew := make(map[string]string, len(d.Nodes))
	for i := range d.Nodes {
		n := &d.Nodes[i]
		old := n.ID
		logical := fmt.Sprintf("%06d", i)
		if n.SourceAnchor.LogicalPath != "" {
			logical = n.SourceAnchor.LogicalPath
		}
		n.DocumentID = documentID
		n.ID = NodeID(documentID, n.Type, logical, n.SourceAnchor)
		oldToNew[old] = n.ID
	}
	for i := range d.Nodes {
		n := &d.Nodes[i]
		n.ParentID = oldToNew[n.ParentID]
		n.ChildrenIDs = nil
	}
	indexesByID := make(map[string]int, len(d.Nodes))
	for i := range d.Nodes {
		indexesByID[d.Nodes[i].ID] = i
	}
	for _, node := range d.Nodes {
		if node.ParentID == "" {
			continue
		}
		if parentIndex, ok := indexesByID[node.ParentID]; ok {
			d.Nodes[parentIndex].ChildrenIDs = append(d.Nodes[parentIndex].ChildrenIDs, node.ID)
		}
	}
	for i := range d.Relationships {
		r := &d.Relationships[i]
		r.FromNodeID, r.ToNodeID = oldToNew[r.FromNodeID], oldToNew[r.ToNodeID]
		r.ID = RelationshipID(r.FromNodeID, r.ToNodeID, r.Type)
	}
	d.DocumentID = documentID
	return d.Validate()
}

func (d Document) Validate() error {
	if d.IRVersion != Version {
		return fmt.Errorf("unsupported document IR version %q", d.IRVersion)
	}
	seen := make(map[string]bool, len(d.Nodes))
	for _, n := range d.Nodes {
		if n.ID == "" || n.Type == "" {
			return fmt.Errorf("IR node has no ID or type")
		}
		if seen[n.ID] {
			return fmt.Errorf("duplicate IR node ID %s", n.ID)
		}
		seen[n.ID] = true
		if n.ParentID != "" && !seen[n.ParentID] { /* parent may appear later */
		}
	}
	for _, n := range d.Nodes {
		if n.ParentID != "" && !seen[n.ParentID] {
			return fmt.Errorf("IR node %s has missing parent %s", n.ID, n.ParentID)
		}
	}
	return nil
}

// NodeTextIndex returns a process-local lookup from normalized node text to
// node positions. It is intentionally unexported and non-serialized; callers
// use it only after parser output is bound and immutable.
func (d *Document) NodeByID(id string) (Node, bool) {
	d.NodeTextIndex()
	i, ok := d.nodeIDs[id]
	if !ok {
		return Node{}, false
	}
	return d.Nodes[i], true
}

func (d *Document) NodeChildrenIndex() map[string][]int {
	d.NodeTextIndex()
	return d.nodeChildIDs
}

func (d *Document) NodeTextIndex() map[string][]int {
	if d.nodeTextIndex != nil {
		return d.nodeTextIndex
	}
	index := make(map[string][]int, len(d.Nodes))
	children := make(map[string][]int)
	ids := make(map[string]int, len(d.Nodes))
	for i := range d.Nodes {
		normalized := strings.TrimSpace(d.Nodes[i].Text)
		if normalized == "" {
			continue
		}
		index[normalized] = append(index[normalized], i)
		ids[d.Nodes[i].ID] = i
		if parent := d.Nodes[i].ParentID; parent != "" {
			children[parent] = append(children[parent], i)
		}
	}
	d.nodeTextIndex = index
	d.nodeChildIDs = children
	d.nodeIDs = ids
	return index
}

func (d Document) MarshalJSONStable() ([]byte, error) {
	return json.Marshal(d)
}

// FromText provides the deterministic degraded representation for parsers
// which only expose text. It preserves headings and paragraph boundaries.
func FromText(title, text, parserName, parserVersion string) *Document {
	d := &Document{IRVersion: Version, Title: strings.TrimSpace(title), Parser: parserName, ParserVersion: parserVersion}
	d.Nodes = append(d.Nodes, Node{ID: "tmp:document", Type: TypeDocument, Order: 0, Text: strings.TrimSpace(text), SourceAnchor: SourceAnchor{Kind: "logical", LogicalPath: "document"}, Parser: parserName, ParserVersion: parserVersion})
	parent := 0
	headingPath := []string{}
	paragraphOrder := 0
	for _, part := range strings.Split(strings.TrimSpace(text), "\n\n") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, "#") {
			level := 0
			for level < len(part) && part[level] == '#' {
				level++
			}
			label := strings.TrimSpace(strings.TrimLeft(part, "#"))
			if label == "" {
				continue
			}
			if level > len(headingPath)+1 {
				level = len(headingPath) + 1
			}
			headingPath = append(headingPath[:min(level-1, len(headingPath))], label)
			d.Nodes = append(d.Nodes, Node{ID: fmt.Sprintf("tmp:heading/%d", paragraphOrder), Type: TypeHeading, ParentID: d.Nodes[parent].ID, Order: paragraphOrder, Text: label, HeadingPath: append([]string(nil), headingPath...), SourceAnchor: SourceAnchor{Kind: "logical", Section: strings.Join(headingPath, " / "), LogicalPath: fmt.Sprintf("heading/%d", paragraphOrder)}, Parser: parserName, ParserVersion: parserVersion})
			parent = len(d.Nodes) - 1
			paragraphOrder++
			continue
		}
		d.Nodes = append(d.Nodes, Node{ID: fmt.Sprintf("tmp:paragraph/%d", paragraphOrder), Type: TypeParagraph, ParentID: d.Nodes[parent].ID, Order: paragraphOrder, Text: part, HeadingPath: append([]string(nil), headingPath...), SourceAnchor: SourceAnchor{Kind: "logical", Section: strings.Join(headingPath, " / "), LogicalPath: fmt.Sprintf("paragraph/%d", paragraphOrder)}, Parser: parserName, ParserVersion: parserVersion})
		paragraphOrder++
	}
	return d
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Text returns the stable searchable projection of the IR.
func (d Document) Text() string {
	var parts []string
	for _, n := range d.Nodes {
		if n.Type != TypeDocument && strings.TrimSpace(n.Text) != "" {
			parts = append(parts, strings.TrimSpace(n.Text))
		}
	}
	if len(parts) == 0 && len(d.Nodes) > 0 {
		return strings.TrimSpace(d.Nodes[0].Text)
	}
	return strings.Join(parts, "\n\n")
}

func (d Document) SortNodes() {
	sort.SliceStable(d.Nodes, func(i, j int) bool { return d.Nodes[i].Order < d.Nodes[j].Order })
}

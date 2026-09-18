package semantic

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

const WikiViewVersion = "wiki-view-v1"

// WikiSection is a rendered concept section inside one topic page.
type WikiSection struct {
	Title       string   `json:"title"`
	UnitID      string   `json:"unitId"`
	Body        string   `json:"body"`
	FactUnitIDs []string `json:"factUnitIds,omitempty"`
}

// WikiPage is a derived, regenerable view. It is not the source of truth and
// carries no user-authored content in 0.4.
type WikiPage struct {
	ID             string           `json:"id"`
	Title          string           `json:"title"`
	TopicUnitID    string           `json:"topicUnitId,omitempty"`
	Summary        string           `json:"summary"`
	Sections       []WikiSection    `json:"sections,omitempty"`
	ConceptUnitIDs []string         `json:"conceptUnitIds,omitempty"`
	FactUnitIDs    []string         `json:"factUnitIds,omitempty"`
	RelatedPageIDs []string         `json:"relatedPageIds,omitempty"`
	Evidence       []EvidenceSource `json:"evidence,omitempty"`
	Generation     int64            `json:"generation"`
}

// WikiView is the complete deterministic projection of one compilation.
type WikiView struct {
	Generation  int64      `json:"generation"`
	Version     string     `json:"version"`
	Regenerable bool       `json:"regenerable"`
	Pages       []WikiPage `json:"pages,omitempty"`
}

// RenderWiki builds a Living Wiki view from Topic, Concept, and Fact units.
// Nothing is persisted and original evidence is never mutated.
func RenderWiki(compilation Compilation) (WikiView, error) {
	if strings.TrimSpace(compilation.BaseID) == "" || compilation.Generation <= 0 {
		return WikiView{}, fmt.Errorf("wiki rendering requires a semantic generation")
	}
	units := map[string]Unit{}
	topics := make([]Unit, 0)
	for _, unit := range compilation.Units {
		units[unit.ID] = unit
		if unit.Type == UnitTopic && unit.Status == UnitActive {
			topics = append(topics, unit)
		}
	}
	sort.Slice(topics, func(i, j int) bool {
		if topics[i].Title != topics[j].Title {
			return topics[i].Title < topics[j].Title
		}
		return topics[i].CanonicalKey < topics[j].CanonicalKey
	})

	view := WikiView{Generation: compilation.Generation, Version: WikiViewVersion, Regenerable: true}
	if len(topics) == 0 {
		// A tiny corpus may only have concepts. Render those as independent
		// pages rather than inventing a topic ontology.
		for _, unit := range compilation.Units {
			if unit.Type != UnitConcept || unit.Status != UnitActive {
				continue
			}
			page, err := conceptWikiPage(compilation, unit)
			if err != nil {
				return WikiView{}, err
			}
			view.Pages = append(view.Pages, page)
		}
	} else {
		for _, topic := range topics {
			page, err := topicWikiPage(compilation, topic, units)
			if err != nil {
				return WikiView{}, err
			}
			view.Pages = append(view.Pages, page)
		}
	}
	sort.Slice(view.Pages, func(i, j int) bool {
		if view.Pages[i].Title != view.Pages[j].Title {
			return view.Pages[i].Title < view.Pages[j].Title
		}
		return view.Pages[i].ID < view.Pages[j].ID
	})
	attachRelatedPages(&view)
	return view, nil
}

// RenderMarkdown renders one page with explicit generated-view and evidence
// provenance headers.
func (p WikiPage) RenderMarkdown() string {
	var builder strings.Builder
	builder.WriteString("# ")
	builder.WriteString(p.Title)
	builder.WriteString("\n\n> Generated Wiki view. Edit the source documents and recompile; this page is not authoritative evidence.\n\n")
	builder.WriteString(p.Summary)
	builder.WriteString("\n\n")
	for _, section := range p.Sections {
		builder.WriteString("## ")
		builder.WriteString(section.Title)
		builder.WriteString("\n")
		builder.WriteString(section.Body)
		builder.WriteString("\n\n")
	}
	if len(p.Evidence) > 0 {
		builder.WriteString("## Source evidence\n")
		for i, source := range p.Evidence {
			builder.WriteString(fmt.Sprintf("- [%d] document=%s generation=%d source_version=%d node=%s chunk=%s\n",
				i+1, source.DocumentID, source.IndexGeneration, source.SourceVersion, source.NodeID, source.ChunkID))
		}
	}
	return builder.String()
}

func topicWikiPage(compilation Compilation, topic Unit, units map[string]Unit) (WikiPage, error) {
	page := WikiPage{
		ID:    wikiPageID(compilation.BaseID, compilation.Generation, topic.CanonicalKey),
		Title: firstNonEmpty(topic.Title, topic.CanonicalKey), TopicUnitID: topic.ID,
		Summary: topic.Content, ConceptUnitIDs: append([]string(nil), topic.DerivedFrom...),
		Generation: compilation.Generation,
	}
	sort.Strings(page.ConceptUnitIDs)
	for _, conceptID := range page.ConceptUnitIDs {
		concept, ok := units[conceptID]
		if !ok || concept.Type != UnitConcept {
			continue
		}
		section := WikiSection{
			Title: firstNonEmpty(concept.Title, concept.CanonicalKey), UnitID: concept.ID,
			Body: concept.Content, FactUnitIDs: append([]string(nil), concept.DerivedFrom...),
		}
		sort.Strings(section.FactUnitIDs)
		page.Sections = append(page.Sections, section)
		page.FactUnitIDs = append(page.FactUnitIDs, concept.DerivedFrom...)
	}
	sort.Strings(page.FactUnitIDs)
	page.FactUnitIDs = dedupeStrings(page.FactUnitIDs)
	evidence, err := ResolveEvidence(compilation.Units, topic.ID)
	if err != nil {
		return WikiPage{}, fmt.Errorf("wiki topic %s: %w", topic.ID, err)
	}
	page.Evidence = evidence
	return page, nil
}

func conceptWikiPage(compilation Compilation, concept Unit) (WikiPage, error) {
	page := WikiPage{
		ID:    wikiPageID(compilation.BaseID, compilation.Generation, "concept:"+concept.CanonicalKey),
		Title: firstNonEmpty(concept.Title, concept.CanonicalKey), Summary: concept.Content,
		ConceptUnitIDs: []string{concept.ID}, FactUnitIDs: append([]string(nil), concept.DerivedFrom...),
		Generation: compilation.Generation,
	}
	sort.Strings(page.FactUnitIDs)
	for _, factID := range page.FactUnitIDs {
		fact, ok := unitByID(compilation.Units, factID)
		if !ok || fact.Type != UnitFact {
			continue
		}
		page.Sections = append(page.Sections, WikiSection{
			Title: firstNonEmpty(fact.Title, fact.CanonicalKey), UnitID: fact.ID,
			Body: fact.Content, FactUnitIDs: []string{fact.ID},
		})
	}
	evidence, err := ResolveEvidence(compilation.Units, concept.ID)
	if err != nil {
		return WikiPage{}, fmt.Errorf("wiki concept %s: %w", concept.ID, err)
	}
	page.Evidence = evidence
	return page, nil
}

func attachRelatedPages(view *WikiView) {
	factPages := map[string][]int{}
	for i, page := range view.Pages {
		for _, factID := range page.FactUnitIDs {
			factPages[factID] = append(factPages[factID], i)
		}
	}
	related := make([]map[string]bool, len(view.Pages))
	for i := range related {
		related[i] = map[string]bool{}
	}
	for _, indexes := range factPages {
		if len(indexes) < 2 {
			continue
		}
		for _, from := range indexes {
			for _, to := range indexes {
				if from == to {
					continue
				}
				related[from][view.Pages[to].ID] = true
			}
		}
	}
	for i := range view.Pages {
		ids := make([]string, 0, len(related[i]))
		for id := range related[i] {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		view.Pages[i].RelatedPageIDs = ids
	}
}

func unitByID(units []Unit, id string) (Unit, bool) {
	for _, unit := range units {
		if unit.ID == id {
			return unit, true
		}
	}
	return Unit{}, false
}

func wikiPageID(baseID string, generation int64, canonicalKey string) string {
	sum := sha256.Sum256([]byte(baseID + "\x00" + fmt.Sprint(generation) + "\x00" + canonicalKey))
	return "wiki_" + hex.EncodeToString(sum[:16])
}

func dedupeStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

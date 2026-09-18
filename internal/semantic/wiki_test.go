package semantic

import (
	"strings"
	"testing"
)

func TestRenderWikiBuildsRegenerableViewFromUnits(t *testing.T) {
	compilation := searchCompilationFixture(t)
	first, err := RenderWiki(compilation)
	if err != nil {
		t.Fatal(err)
	}
	second, err := RenderWiki(compilation)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Pages) == 0 || first.Version != WikiViewVersion || !first.Regenerable {
		t.Fatalf("wiki view = %+v", first)
	}
	page := first.Pages[0]
	if page.TopicUnitID == "" || len(page.Sections) == 0 || len(page.ConceptUnitIDs) == 0 ||
		len(page.FactUnitIDs) == 0 || len(page.Evidence) == 0 {
		t.Fatalf("wiki page lacks unit/evidence chain: %+v", page)
	}
	markdown := page.RenderMarkdown()
	for _, expected := range []string{"Generated Wiki view", "## Source evidence", "node=", "chunk="} {
		if !strings.Contains(markdown, expected) {
			t.Fatalf("markdown missing %q:\n%s", expected, markdown)
		}
	}
	if len(first.Pages) != len(second.Pages) || page.ID != second.Pages[0].ID || markdown != second.Pages[0].RenderMarkdown() {
		t.Fatal("wiki rendering is not deterministic")
	}
}

func TestRenderWikiFallsBackToConcepts(t *testing.T) {
	compilation := searchCompilationFixture(t)
	compilation.Units = filterWikiTopics(compilation.Units)
	view, err := RenderWiki(compilation)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Pages) == 0 {
		t.Fatal("concept fallback produced no pages")
	}
	for _, page := range view.Pages {
		if page.TopicUnitID != "" || len(page.Evidence) == 0 {
			t.Fatalf("invalid fallback page: %+v", page)
		}
	}
}

func filterWikiTopics(units []Unit) []Unit {
	out := make([]Unit, 0, len(units))
	for _, unit := range units {
		if unit.Type != UnitTopic {
			out = append(out, unit)
		}
	}
	// Remove now-dangling concept derived references so validation/provenance
	// remains internally representable for the renderer.
	for i := range out {
		if out[i].Type != UnitConcept {
			continue
		}
		out[i].DerivedFrom = keepFactReferences(units, out[i].DerivedFrom)
	}
	return out
}

func keepFactReferences(all []Unit, ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		for _, unit := range all {
			if unit.ID == id && unit.Type == UnitFact {
				out = append(out, id)
				break
			}
		}
	}
	return out
}

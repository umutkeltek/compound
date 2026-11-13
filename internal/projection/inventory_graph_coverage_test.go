package projection

import (
	"fmt"
	"strings"
	"testing"
)

func addDatastore(obs *InventoryObservation, id, label string, facts map[string]InventoryObservedFact) {
	if facts == nil {
		facts = map[string]InventoryObservedFact{}
	}
	obs.Resources = append(obs.Resources, InventoryObservedResource{ID: id, Kind: "datastore", Provider: "pg", Label: label,
		Source: "lab", Places: []string{}, Facts: facts})
}

func questionsOfKind(p InventoryProjection, kind string) []InventoryQuestion {
	var out []InventoryQuestion
	for _, q := range p.Questions {
		if q.Kind == kind {
			out = append(out, q)
		}
	}
	return out
}

// A fact no reader reports for any thing of a kind is a gap in what is
// collected: one line with the count, not one question per thing.
func TestInventoryCoverageGapIsOneLineNotAQuestionPerThing(t *testing.T) {
	threeMore := func(obs *InventoryObservation) {
		for i := 1; i <= 3; i++ {
			addDatastore(obs, fmt.Sprintf("pg:datastore:extra%d", i), fmt.Sprintf("extra-db-%d", i), nil)
		}
	}
	p := inventoryWithGraph(t, threeMore)
	if qs := questionsOfKind(p, "datastore.backed_up"); len(qs) != 0 {
		t.Fatalf("no datastore reports backed_up: it is a coverage gap, not %d questions", len(qs))
	}
	var gap *InventoryCoverageGap
	for i := range p.CoverageGaps {
		if p.CoverageGaps[i].Kind == "datastore.backed_up" {
			gap = &p.CoverageGaps[i]
		}
	}
	if gap == nil || gap.Resources != 4 || len(gap.About) != 4 || gap.Gap != "No reader reports whether datastores are backed up: 4 datastores" {
		t.Fatalf("coverage gaps = %+v", p.CoverageGaps)
	}
	if !strings.Contains(gap.AnswerWith, "reader") || !strings.HasSuffix(gap.AnswerWith, "give by, on and evidence") {
		t.Fatalf("a gap says how to close it: %q", gap.AnswerWith)
	}
	text := FormatInventoryQuestionsText(p)
	if !strings.Contains(text, "  - No reader reports whether datastores are backed up: 4 datastores (datastore.backed_up).") {
		t.Fatalf("questions text has no gap line:\n%s", text)
	}
	if strings.Contains(text, "extra-db-1") {
		t.Fatalf("a gap is not listed thing by thing:\n%s", text)
	}
	if line := FormatInventoryText(p); !strings.Contains(line, "facts no reader reports (datastore.backed_up 4") {
		t.Fatalf("inventory text does not count the gap:\n%s", line)
	}

	// Once one datastore reports it, the fact is collected: the others become
	// questions and the gap is gone.
	reported := inventoryWithGraph(t, func(obs *InventoryObservation) {
		threeMore(obs)
		findGraphResource(*obs, gfDB).Facts["backed_up"] = gfOK(true)
	})
	if qs := questionsOfKind(reported, "datastore.backed_up"); len(qs) != 3 {
		t.Fatalf("reported for one datastore: the other 3 are questions, got %+v", qs)
	}
	for _, g := range reported.CoverageGaps {
		if g.Kind == "datastore.backed_up" {
			t.Fatalf("gap must close once a reader reports the fact: %+v", g)
		}
	}
}

// Questions are ranked by how many products depend on what they are about,
// then by dependents, then by label; the default text shows the top 12 with
// the total and how to see them all.
func TestInventoryQuestionsRankedByWhatDependsOnThemTopTwelve(t *testing.T) {
	p := inventoryWithGraph(t, func(obs *InventoryObservation) {
		reportedElsewhere(obs)                                        // no coverage gap lines in the text
		findGraphResource(*obs, gfDB).Facts["backed_up"] = gfOK(true) // collected, and not asked
		for i := 1; i <= 14; i++ {
			addDatastore(obs, fmt.Sprintf("pg:datastore:ds%02d", i), fmt.Sprintf("ds-%02d", i),
				map[string]InventoryObservedFact{"backed_up": gfOK("not_known")})
		}
		shop := findGraphResource(*obs, gfShop)
		shop.Links = append(shop.Links, gfLink("uses", "pg:datastore:ds11", ""), gfLink("uses", "pg:datastore:ds07", ""))
		notes := findGraphResource(*obs, gfNotes)
		notes.Links = append(notes.Links, gfLink("uses", "pg:datastore:ds11", ""))
		obs.Resources = append(obs.Resources, InventoryObservedResource{ID: "external:service:solo", Kind: "external_service",
			Provider: "soloco", Label: "Solo service", Source: "repos", Places: []string{}, Facts: map[string]InventoryObservedFact{},
			Links: []InventoryLink{gfLink("uses", "pg:datastore:ds03", "")}})
	})
	if len(p.Questions) < 14 {
		t.Fatalf("questions = %d", len(p.Questions))
	}
	first := []string{"pg:datastore:ds11", "pg:datastore:ds07", "pg:datastore:ds03"}
	for i, id := range first {
		if p.Questions[i].About != id {
			t.Fatalf("question %d is about %s, want %s (2 products, 1 product, 1 dependent): %+v", i, p.Questions[i].About, id, p.Questions[:4])
		}
	}
	if q := p.Questions[0]; q.Products != 2 || q.Dependents != 2 {
		t.Fatalf("top question counts = %+v", q)
	}
	for i := 4; i < len(p.Questions); i++ {
		a, b := p.Questions[i-1], p.Questions[i]
		if a.Products < b.Products || (a.Products == b.Products && a.Dependents < b.Dependents) ||
			(a.Products == b.Products && a.Dependents == b.Dependents && a.label > b.label) {
			t.Fatalf("questions not ranked by products, dependents, label at %d: %+v then %+v", i, a, b)
		}
	}
	text := FormatInventoryQuestionsText(p)
	want := fmt.Sprintf("Questions: the top 12 of %d, the ones most products and things depend on first (`--questions --all` lists every one; --json too).", len(p.Questions))
	if !strings.Contains(text, want) || strings.Count(text, "\n  - ") != 12 {
		t.Fatalf("default text must list 12 of %d and say how to see all:\n%s", len(p.Questions), text)
	}
	if !strings.Contains(text, "  - Is ds-11 backed up? (…ds11, datastore.backed_up; dependents: 2 products, 2 things in all)") {
		t.Fatalf("top question line:\n%s", text)
	}
	all := FormatInventoryQuestionsTextWith(p, InventoryTextOptions{All: true})
	if strings.Count(all, "\n  - ") != len(p.Questions) {
		t.Fatalf("--all lists every question:\n%s", all)
	}
}

// An identity's second-factor and recovery questions are asked only when an
// account signs in with it.
func TestInventoryIdentityQuestionsOnlyWhenAnAccountSignsInWithIt(t *testing.T) {
	p := inventoryWithGraph(t, func(obs *InventoryObservation) {
		reportedElsewhere(obs)
		loose := InventoryObservedResource{ID: "identity:loose-key", Kind: "identity", Provider: "logins", Label: "loose key",
			Source: "logins", Places: []string{}, Facts: map[string]InventoryObservedFact{"second_factor_on_file": gfOK(false)}}
		obs.Resources = append(obs.Resources, loose)
		mailbox := findGraphResource(*obs, gfMailbox)
		mailbox.Links = append(mailbox.Links, gfLink("signs_in_with", "identity:loose-key", ""))
	})
	asked := map[string]bool{}
	for _, q := range p.Questions {
		if strings.HasPrefix(q.Kind, "identity.") {
			asked[q.About] = true
		}
	}
	if asked["identity:loose-key"] {
		t.Fatalf("no account signs in with the loose key: no question, got %+v", p.Questions)
	}
	if !asked[gfIdentity] {
		t.Fatalf("an account signs in with the ops login: its questions stay, got %+v", p.Questions)
	}
}

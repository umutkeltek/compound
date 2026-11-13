package projection

import (
	"strings"
	"testing"
	"time"
)

func inventoryWithGraph(t *testing.T, edit func(*InventoryObservation)) InventoryProjection {
	t.Helper()
	home := t.TempDir()
	if _, err := RefreshInventory(home, graphFixture(t, inventoryT0, edit), InventoryRefreshOptions{}, inventoryT0); err != nil {
		t.Fatal(err)
	}
	p, err := BuildInventory(home, InventoryQuery{Products: gfResolver}, inventoryT0)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func questionKinds(p InventoryProjection) map[string]string {
	out := map[string]string{}
	for _, q := range p.Questions {
		out[q.Kind] = q.About
	}
	return out
}

// reportedElsewhere adds a second identity (an account signs in with it) and
// a second datastore that report recovery_on_file and backed_up, so those
// facts are collected for their kinds and stay questions, not coverage gaps.
func reportedElsewhere(obs *InventoryObservation) {
	other := InventoryObservedResource{ID: "identity:other-login", Kind: "identity", Provider: "logins", Label: "other login",
		Source: "logins", Places: []string{}, Facts: map[string]InventoryObservedFact{
			"second_factor_on_file": gfOK(true), "recovery_on_file": gfOK(true)}}
	store := InventoryObservedResource{ID: "pg:datastore:other", Kind: "datastore", Provider: "pg", Label: "other-db",
		Source: "lab", Places: []string{}, Facts: map[string]InventoryObservedFact{"backed_up": gfOK(true)}}
	obs.Resources = append(obs.Resources, other, store)
	account := findGraphResource(*obs, gfAccountID)
	account.Links = append(account.Links, gfLink("signs_in_with", "identity:other-login", ""))
}

func TestInventoryQuestionsEachKindAppearsAndDisappearsWhenOnFile(t *testing.T) {
	// A second deployment for the shop product, so whether each is live matters.
	twoDeployments := func(obs *InventoryObservation) {
		second := InventoryObservedResource{ID: "deploy:site:api", Kind: "deployment", Provider: "sitehost", Label: "api-deploy",
			Source: "repos", Places: []string{}, Facts: map[string]InventoryObservedFact{}}
		obs.Resources = append(obs.Resources, second)
		shop := findGraphResource(*obs, gfShop)
		shop.Links = append(shop.Links, gfLink("deployed_on", "deploy:site:api", ""))
		reportedElsewhere(obs)
	}
	p := inventoryWithGraph(t, twoDeployments)
	want := map[string]string{
		"hostname.traced":                gfHostname,
		"deployment.live":                gfDeploy,
		"identity.second_factor_on_file": gfIdentity,
		"identity.recovery_on_file":      gfIdentity,
		"datastore.backed_up":            gfDB,
		"backup_target.off_host":         gfBackup,
		"repo.has_remote":                gfRepo,
	}
	got := questionKinds(p)
	for kind, about := range want {
		if got[kind] == "" {
			t.Fatalf("question %s missing: %+v", kind, p.Questions)
		}
		if kind != "deployment.live" && got[kind] != about {
			t.Fatalf("question %s is about %s, want %s", kind, got[kind], about)
		}
	}
	for _, q := range p.Questions {
		if q.Question == "" || q.Why == "" || !strings.Contains(q.AnswerWith, "declared") && q.Kind != "repo.has_remote" || !strings.HasSuffix(q.AnswerWith, "give by, on and evidence") {
			t.Fatalf("question must say what, why and the declared line: %+v", q)
		}
	}
	// One deployment only: live not known is not a question.
	if questionKinds(inventoryWithGraph(t, nil))["deployment.live"] != "" {
		t.Fatal("a product with one deployment raises no live question")
	}

	// Each fact on file makes its question go away.
	onFile := inventoryWithGraph(t, func(obs *InventoryObservation) {
		twoDeployments(obs)
		findGraphResource(*obs, gfHostname).Facts["traced"] = gfOK("end_to_end")
		findGraphResource(*obs, gfDeploy).Facts["live"] = gfOK("yes")
		findGraphResource(*obs, "deploy:site:api").Facts["live"] = gfOK("no")
		identity := findGraphResource(*obs, gfIdentity)
		identity.Facts["second_factor_on_file"] = gfOK(true)
		identity.Facts["recovery_on_file"] = gfOK(true)
		findGraphResource(*obs, gfDB).Facts["backed_up"] = gfOK(true)
		findGraphResource(*obs, gfBackup).Facts["off_host"] = gfOK(true)
		findGraphResource(*obs, gfRepo).Facts["has_remote"] = gfOK(true)
	})
	if len(onFile.Questions) != 0 {
		t.Fatalf("every fact is on file, no question may remain: %+v", onFile.Questions)
	}
	if text := FormatInventoryQuestionsText(onFile); !strings.Contains(text, "No questions") {
		t.Fatalf("no-questions text:\n%s", text)
	}
	// A fact on file as no keeps the identity and backup questions, and a
	// datastore that says no is a warning, not a question.
	no := inventoryWithGraph(t, func(obs *InventoryObservation) {
		reportedElsewhere(obs)
		findGraphResource(*obs, gfIdentity).Facts["recovery_on_file"] = gfOK(false)
		findGraphResource(*obs, gfDB).Facts["backed_up"] = gfOK(false)
	})
	kinds := questionKinds(no)
	if kinds["identity.recovery_on_file"] == "" || kinds["datastore.backed_up"] != "" || !inventoryWarned(no, gfDB, "backed_up") {
		t.Fatalf("no answers = %+v", no.Questions)
	}
}

func TestInventorySharedPiecesAndUnknownProductRow(t *testing.T) {
	p := inventoryWithGraph(t, nil)
	// The account owns the host and the domain: what it owns stops when it goes,
	// so it is the most shared piece, then the host.
	if len(p.Shared) < 2 || p.Shared[0].Resource != gfAccountID || p.Shared[0].Dependents != 10 || p.Shared[0].Products != 2 ||
		p.Shared[1].Resource != gfHost || p.Shared[1].Dependents != 8 || p.Shared[1].Products != 2 {
		t.Fatalf("shared = %+v", p.Shared)
	}
	for i := 1; i < len(p.Shared); i++ {
		a, b := p.Shared[i-1], p.Shared[i]
		if a.Products < b.Products || (a.Products == b.Products && a.Dependents < b.Dependents) {
			t.Fatalf("shared not ordered by products then dependents: %+v", p.Shared)
		}
		if b.Dependents < 2 || b.Kind == "product" {
			t.Fatalf("a shared piece has two or more dependents and is not a product: %+v", b)
		}
	}
	var rows []InventoryAttention
	for _, a := range p.Attention {
		if a.Kind == "product" {
			rows = append(rows, a)
		}
	}
	if len(rows) != 1 || rows[0].Why != "1 product reference is not a Compound product and is shown as an unknown product: ghost-app" {
		t.Fatalf("unknown product attention = %+v", rows)
	}
	// Without the identity owner, every product reference is unknown: still one row.
	home := graphImported(t, inventoryT0)
	bare, _ := BuildInventory(home, InventoryQuery{}, inventoryT0)
	n := 0
	for _, a := range bare.Attention {
		if a.Kind == "product" {
			n++
			if !strings.HasPrefix(a.Why, "4 product references") {
				t.Fatalf("row = %+v", a)
			}
		}
	}
	if n != 1 {
		t.Fatalf("unknown products must be one attention row, got %d", n)
	}

	text := FormatInventoryText(p)
	start := strings.Index(text, "Shared pieces")
	end := strings.Index(text, "Resources:")
	if start < 0 || end < start {
		t.Fatalf("text has no shared section:\n%s", text)
	}
	if lines := strings.Count(text[start:end], "\n"); lines > 8 {
		t.Fatalf("shared and questions take %d lines, at most 8:\n%s", lines, text[start:end])
	}
	for _, want := range []string{"  lab-node …e:n1 (host): 8 dependents, 2 products", "Questions: 4 (", "; 2 facts no reader reports (datastore.backed_up 1, identity.recovery_on_file 1)", "`compound system inventory --questions` lists them.", "  - unknown products: 1 product reference is not"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text missing %q:\n%s", want, text)
		}
	}
	questions := FormatInventoryQuestionsText(p)
	if !strings.Contains(questions, "Questions (4), the ones most products and things depend on first") || !strings.Contains(questions, "  - No reader reports whether datastores are backed up: 1 datastore (datastore.backed_up).") {
		t.Fatalf("questions text:\n%s", questions)
	}
	// Filtered by kind, only that kind's questions and shared pieces remain.
	byKind, _ := BuildInventory(home, InventoryQuery{Kind: "identity", Products: gfResolver}, inventoryT0.Add(time.Minute))
	for _, q := range byKind.Questions {
		if !strings.HasPrefix(q.Kind, "identity.") {
			t.Fatalf("--kind identity question = %+v", q)
		}
	}
	if len(byKind.Questions) != 1 || len(byKind.CoverageGaps) != 1 || len(byKind.Shared) != 0 {
		t.Fatalf("--kind identity: %d questions, shared %+v", len(byKind.Questions), byKind.Shared)
	}
}

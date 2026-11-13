package projection

// Falsifiers for the system graph and its walks. Each fails without the
// feature it names: confidence never rises along a path, a cycle ends, the
// depth limit holds, impact counts each dependent once, product references
// resolve through the identity owner (both spellings) or show as unknown,
// carried things keep their links, ambiguity is refused, and text output
// masks every id. All data is synthetic.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func dependsOn(t *testing.T, home, target string, depth int) InventoryWalkProjection {
	t.Helper()
	p, err := BuildInventoryDepends(home, InventoryDependsQuery{Target: target, Depth: depth, Products: gfResolver}, inventoryT0)
	if err != nil {
		t.Fatalf("depends %s: %v", target, err)
	}
	return p
}

func impactOf(t *testing.T, home, target string) InventoryWalkProjection {
	t.Helper()
	p, err := BuildInventoryImpact(home, target, gfResolver, inventoryT0)
	if err != nil {
		t.Fatalf("impact %s: %v", target, err)
	}
	return p
}

func walkStep(p InventoryWalkProjection, id string) (InventoryGraphStep, bool) {
	for _, s := range append(append([]InventoryGraphStep{}, p.Steps...), p.RestsOn...) {
		if s.Resource == id {
			return s, true
		}
	}
	return InventoryGraphStep{}, false
}

func TestInventoryGraphCountsAndUnresolved(t *testing.T) {
	home := graphImported(t, inventoryT0)
	p, err := BuildInventoryGraph(home, InventoryGraphQuery{Products: gfResolver}, inventoryT0)
	if err != nil {
		t.Fatal(err)
	}
	// 19 resources, the two spellings of one product merged into one thing.
	if p.SchemaVersion != InventoryGraphSchemaVersion || p.Nodes != 18 || p.NodesByKind["product"] != 2 || p.NodesByKind["container"] != 2 {
		t.Fatalf("nodes = %d %v", p.Nodes, p.NodesByKind)
	}
	if p.Edges != 23 || p.EdgesByRelation["runs_on"] != 4 || p.EdgesByConfidence["inferred"] != 3 || p.EdgesByConfidence["declared"] != 4 {
		t.Fatalf("edges = %d %v %v", p.Edges, p.EdgesByRelation, p.EdgesByConfidence)
	}
	if len(p.Unresolved) != 1 || p.Unresolved[0].Ref != gfGhost || p.Unresolved[0].Kind != "unknown_product" || p.Unresolved[0].Links != 1 {
		t.Fatalf("unresolved = %+v", p.Unresolved)
	}
	if len(p.EdgeList) != 0 {
		t.Fatalf("no filter, no link list: %+v", p.EdgeList)
	}
	byRel, _ := BuildInventoryGraph(home, InventoryGraphQuery{Rel: "runs_on", Products: gfResolver}, inventoryT0)
	if byRel.EdgeListTotal != 4 || len(byRel.EdgeList) != 4 {
		t.Fatalf("--rel runs_on = %+v", byRel.EdgeList)
	}
	byKind, _ := BuildInventoryGraph(home, InventoryGraphQuery{Kind: "identity", Products: gfResolver}, inventoryT0)
	if byKind.EdgeListTotal != 3 { // account signs in with it; it keeps a secret and recovers through a mailbox
		t.Fatalf("--kind identity = %+v", byKind.EdgeList)
	}
	if _, err := BuildInventoryGraph(home, InventoryGraphQuery{Rel: "run_on"}, inventoryT0); err == nil || !strings.Contains(err.Error(), "nearest: runs_on") {
		t.Fatalf("unknown --rel err = %v", err)
	}
	if _, err := BuildInventoryGraph(home, InventoryGraphQuery{Kind: "bucket"}, inventoryT0); err == nil {
		t.Fatal("unknown --kind must be refused")
	}
	text := FormatInventoryGraphText(byKind)
	for _, want := range []string{"Things: 18 (", "Not known: 1 unknown products, 0 things", "ghost-app …-app (unknown product), named by 1 links", "Links with kind identity: 3.", "signs in with ops login …ogin (identity)"} {
		if !strings.Contains(text, want) {
			t.Fatalf("graph text missing %q:\n%s", want, text)
		}
	}
}

func TestInventoryDependsWalksDependsLinksAndEndsWithWhatItRestsOn(t *testing.T) {
	home := graphImported(t, inventoryT0)
	p := dependsOn(t, home, gfShop, 0)
	if p.Status != InventoryWalkOK || p.DepthLimit != DefaultInventoryDependsDepth || p.RoutesFollowed {
		t.Fatalf("depends status = %+v", p)
	}
	var got []string
	for _, s := range p.Steps {
		got = append(got, s.Resource)
	}
	// By depth, then kind in kinds-file order, then label.
	want := []string{gfDeploy, gfPay, gfStoreApp, gfDB, gfDBCt, gfGuest, gfWebCt, gfHost}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("steps = %v\nwant %v", got, want)
	}
	// code_in is not a dependency: the repo is not in the walk.
	if _, found := walkStep(p, gfRepo); found {
		t.Fatal("a repo the code is in is not something the product runs on")
	}
	if pay, _ := walkStep(p, gfPay); pay.LinkConfidence != "declared" || pay.PathConfidence != "declared" {
		t.Fatalf("declared link step = %+v", pay)
	}
	var rests []string
	for _, s := range p.RestsOn {
		rests = append(rests, s.Kind)
	}
	if strings.Join(rests, ",") != "account,mailbox,identity,secret_store" {
		t.Fatalf("rests on = %v (%+v)", rests, p.RestsOn)
	}
	if acct, _ := walkStep(p, gfAccountID); acct.PathConfidence != "declared" || acct.Via != gfHost || acct.RelTitle != "owned by" {
		t.Fatalf("account step = %+v", acct)
	}
	if len(p.NotKnown) != 1 || p.NotKnown[0].Resource != gfDeploy || p.NotKnown[0].Why != "Live not known" {
		t.Fatalf("not known = %+v", p.NotKnown)
	}
	text := FormatInventoryWalkText(p)
	for _, want := range []string{
		"What Shop app …-app (product) cannot work without: 8 things, up to 6 links deep",
		"\n  deployed on web-deploy …:web (deployment)\n",
		"\n    uses main-db …main (datastore)\n",
		"uses Pay service …:pay (external_service) · link declared",
		"Rests on (accounts, identities, secret stores and their routes):",
		"Example account …k8m4 (account) · owned by, from lab-node …e:n1 · link declared",
		"Not known (1):\n  web-deploy …:web: Live not known",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("depends text missing %q:\n%s", want, text)
		}
	}
}

func TestInventoryDependsInferredLinkIsNeverShownObserved(t *testing.T) {
	// Without the second container's links, the guest and the host are
	// reached only through the inferred deployed_on link.
	data := graphFixture(t, inventoryT0, func(obs *InventoryObservation) {
		findGraphResource(*obs, gfDBCt).Links = nil
		findGraphResource(*obs, gfDB).Links = findGraphResource(*obs, gfDB).Links[1:]
	})
	home := t.TempDir()
	if _, err := RefreshInventory(home, data, InventoryRefreshOptions{}, inventoryT0); err != nil {
		t.Fatal(err)
	}
	p := dependsOn(t, home, gfDeploy, 0)
	web, _ := walkStep(p, gfWebCt)
	if web.LinkConfidence != "inferred" || web.PathConfidence != "inferred" {
		t.Fatalf("inferred link step = %+v", web)
	}
	for _, id := range []string{gfGuest, gfHost} {
		s, _ := walkStep(p, id)
		if s.LinkConfidence != "observed" || s.PathConfidence != "inferred" {
			t.Fatalf("an observed link below an inferred one must keep the path inferred: %+v", s)
		}
	}
	if acct, _ := walkStep(p, gfAccountID); acct.PathConfidence != "inferred" {
		t.Fatalf("what it rests on takes the weakest link too: %+v", acct)
	}
	text := FormatInventoryWalkText(p)
	if !strings.Contains(text, "deployed on web-ct …:web (container) · link inferred\n") || !strings.Contains(text, "runs on lab-node …e:n1 (host) · path inferred") {
		t.Fatalf("text must say inferred:\n%s", text)
	}
	// The surest path wins when there are several: the full fixture reaches
	// the web container also through observed links.
	full := dependsOn(t, graphImported(t, inventoryT0), gfDeploy, 0)
	if web, _ := walkStep(full, gfWebCt); web.PathConfidence != "observed" {
		t.Fatalf("an observed path must win over an inferred one: %+v", web)
	}
}

func TestInventoryDependsCycleEndsAndDepthLimitHolds(t *testing.T) {
	home := graphImported(t, inventoryT0)
	// The two containers call each other: the walk ends, each counted once.
	p := dependsOn(t, home, gfWebCt, 0)
	seen := map[string]int{}
	for _, s := range p.Steps {
		seen[s.Resource]++
	}
	if seen[gfDBCt] != 1 || seen[gfWebCt] != 0 || len(p.Steps) != 3 {
		t.Fatalf("cycle walk = %+v", p.Steps)
	}
	limited := dependsOn(t, home, gfShop, 2)
	for _, s := range limited.Steps {
		if s.Depth > 2 {
			t.Fatalf("step past the depth limit: %+v", s)
		}
	}
	if _, found := walkStep(limited, gfHost); found {
		t.Fatal("the host is five links away and must not be reached at depth 2")
	}
	stopped := false
	for _, n := range limited.NotKnown {
		if n.Resource == gfDB && strings.HasPrefix(n.Why, "depth limit 2 reached") {
			stopped = true
		}
	}
	if !stopped {
		t.Fatalf("the walk must say where the depth limit stopped it: %+v", limited.NotKnown)
	}
}

func TestInventoryDependsFollowsRoutesFromAHostname(t *testing.T) {
	home := graphImported(t, inventoryT0)
	p := dependsOn(t, home, "app.shop.example", 0)
	if !p.RoutesFollowed || p.Start.Resource != gfHostname {
		t.Fatalf("hostname start = %+v", p.Start)
	}
	if web, ok := walkStep(p, gfWebCt); !ok || web.RelTitle != "routes to" {
		t.Fatalf("a hostname depends on where it routes: %+v", p.Steps)
	}
	if len(p.NotKnown) != 1 || p.NotKnown[0].Why != "Traced not known" {
		t.Fatalf("not known = %+v", p.NotKnown)
	}
	// From anything else a route link is not a dependency.
	if _, found := walkStep(dependsOn(t, home, gfDomain, 0), gfHostname); found {
		t.Fatal("route links are walked only from a hostname")
	}
}

func TestInventoryImpactCountsEachDependentOnceProductsFirst(t *testing.T) {
	home := graphImported(t, inventoryT0)
	p := impactOf(t, home, gfHost)
	if p.SchemaVersion != InventoryImpactSchemaVersion || len(p.Steps) != 8 || p.Products != 2 {
		t.Fatalf("impact = %d steps, %d products: %+v", len(p.Steps), p.Products, p.Steps)
	}
	seen := map[string]bool{}
	for i, s := range p.Steps {
		if seen[s.Resource] {
			t.Fatalf("%s counted twice", s.Resource)
		}
		seen[s.Resource] = true
		if (i < 2) != (s.Kind == "product") {
			t.Fatalf("products must come first: %+v", p.Steps)
		}
	}
	// The cycle between the containers does not count anything twice, and a
	// hostname that routes to a container is hit when the host goes.
	if !seen[gfHostname] || !seen[gfDBCt] || !seen[gfWebCt] {
		t.Fatalf("impact misses a dependent: %v", seen)
	}
	text := FormatInventoryWalkText(p)
	if !strings.Contains(text, "What depends on lab-node …e:n1 (host): 8 things, 2 of them products.") {
		t.Fatalf("impact text:\n%s", text)
	}
	// Both spellings of a product are one dependent.
	pay := impactOf(t, home, gfPay)
	if len(pay.Steps) != 2 || pay.Products != 2 {
		t.Fatalf("impact of the pay service = %+v", pay.Steps)
	}
}

func TestInventoryProductReferencesResolveThroughTheIdentityOwner(t *testing.T) {
	home := graphImported(t, inventoryT0)
	// Either spelling, or the label, reaches the one merged product.
	for _, q := range []string{gfNotes, gfNotesAlt, "Notes app", "notes-app"} {
		p := dependsOn(t, home, q, 0)
		if p.Status != InventoryWalkOK || p.Start.Resource != gfNotes {
			t.Fatalf("query %q = %s %+v %s", q, p.Status, p.Start, p.Notice)
		}
		if _, ok := walkStep(p, gfPay); !ok {
			t.Fatalf("the old spelling's link must belong to the merged product: %+v", p.Steps)
		}
	}
	// Without the identity owner nothing resolves: every product is unknown,
	// kept, and shown as such.
	p, err := BuildInventoryGraph(home, InventoryGraphQuery{}, inventoryT0)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Unresolved) != 4 || p.NodesByKind["product"] != 0 {
		t.Fatalf("no resolver: unresolved %+v kinds %v", p.Unresolved, p.NodesByKind)
	}
	d, _ := BuildInventoryDepends(home, InventoryDependsQuery{Target: gfShop}, inventoryT0)
	if d.Status != InventoryWalkOK || d.Start.Unknown != "unknown_product" || len(d.NotKnown) == 0 || !strings.HasPrefix(d.NotKnown[0].Why, "unknown product") {
		t.Fatalf("an unresolved product is kept and shown unknown: %+v %+v", d.Start, d.NotKnown)
	}
}

func TestInventoryWalkAmbiguousAndNotFound(t *testing.T) {
	data := graphFixture(t, inventoryT0, func(obs *InventoryObservation) {
		findGraphResource(*obs, gfDBCt).Label = "web-ct"
	})
	home := t.TempDir()
	if _, err := RefreshInventory(home, data, InventoryRefreshOptions{}, inventoryT0); err != nil {
		t.Fatal(err)
	}
	p := dependsOn(t, home, "WEB-CT", 0)
	if p.Status != InventoryWalkAmbiguous || len(p.Candidates) != 2 || p.Start != nil {
		t.Fatalf("ambiguous = %+v", p)
	}
	text := FormatInventoryWalkText(p)
	if !strings.Contains(text, "2 things match") || !strings.Contains(text, "web-ct …r:db (container)") {
		t.Fatalf("ambiguous text:\n%s", text)
	}
	// The masked id the text shows is enough to ask again.
	if again := dependsOn(t, home, "…r:db", 0); again.Status != InventoryWalkOK || again.Start.Resource != gfDBCt {
		t.Fatalf("masked id query = %+v", again)
	}
	if none := impactOf(t, home, "no such thing"); none.Status != InventoryWalkNotFound {
		t.Fatalf("not found = %+v", none)
	}
	missing, err := BuildInventoryDepends(t.TempDir(), InventoryDependsQuery{Target: "x"}, inventoryT0)
	if err != nil || missing.Status != InventoryWalkMissing || !strings.Contains(FormatInventoryWalkText(missing), "no snapshot") {
		t.Fatalf("missing snapshot = %+v %v", missing, err)
	}
}

func TestInventoryGraphCarriedThingsKeepTheirLinksMarked(t *testing.T) {
	home := graphImported(t, inventoryT0)
	t1 := inventoryT0.Add(time.Hour)
	failed := graphFixture(t, t1, func(obs *InventoryObservation) {
		for i := range obs.Sources {
			if obs.Sources[i].ID == "lab" {
				obs.Sources[i].State = "failed"
			}
		}
		var kept []InventoryObservedResource
		for _, r := range obs.Resources {
			if r.Source != "lab" {
				kept = append(kept, r)
			}
		}
		obs.Resources = kept
	})
	if _, err := RefreshInventory(home, failed, InventoryRefreshOptions{}, t1); err != nil {
		t.Fatal(err)
	}
	g, _ := BuildInventoryGraph(home, InventoryGraphQuery{Products: gfResolver}, t1)
	if g.Edges != 23 || g.CarriedEdges == 0 || len(g.Unresolved) != 1 {
		t.Fatalf("carried links must stay in the graph: edges %d carried %d unresolved %+v", g.Edges, g.CarriedEdges, g.Unresolved)
	}
	p := dependsOn(t, home, gfShop, 0)
	host, ok := walkStep(p, gfHost)
	if !ok || !host.Carried || host.LastObservedAt != inventoryT0.Format(time.RFC3339) {
		t.Fatalf("carried host step = %+v", host)
	}
	if !strings.Contains(FormatInventoryWalkText(p), "lab-node …e:n1 (host) · from the last good read") {
		t.Fatalf("text must mark carried things:\n%s", FormatInventoryWalkText(p))
	}
}

func TestInventoryGraphTextMasksIDsAndIsDeterministic(t *testing.T) {
	home := graphImported(t, inventoryT0)
	opaque := strings.TrimPrefix(gfAccountID, "aws:account:")
	check := func(name, text string) {
		t.Helper()
		for i := 0; i+5 <= len(opaque); i++ {
			if strings.Contains(text, opaque[i:i+5]) {
				t.Fatalf("%s shows more than the last four characters of an id:\n%s", name, text)
			}
		}
	}
	render := func() []string {
		g, _ := BuildInventoryGraph(home, InventoryGraphQuery{Kind: "account", Products: gfResolver}, inventoryT0)
		d := dependsOn(t, home, gfShop, 0)
		i := impactOf(t, home, gfAccountID)
		var out []string
		for _, v := range []any{g, d, i} {
			b, _ := json.Marshal(v)
			out = append(out, string(b))
		}
		out = append(out, FormatInventoryGraphText(g), FormatInventoryWalkText(d), FormatInventoryWalkText(i))
		return out
	}
	first := render()
	for _, text := range first[3:] {
		check("text", text)
	}
	for n := 0; n < 3; n++ {
		again := render()
		for i := range first {
			if again[i] != first[i] {
				t.Fatalf("output %d differs between runs:\n%s\n---\n%s", i, first[i], again[i])
			}
		}
	}
}

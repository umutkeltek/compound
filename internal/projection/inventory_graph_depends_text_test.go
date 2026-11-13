package projection

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func dependsWith(t *testing.T, target string, edit func(*InventoryObservation)) InventoryWalkProjection {
	t.Helper()
	home := t.TempDir()
	if _, err := RefreshInventory(home, graphFixture(t, inventoryT0, edit), InventoryRefreshOptions{}, inventoryT0); err != nil {
		t.Fatal(err)
	}
	p, err := BuildInventoryDepends(home, InventoryDependsQuery{Target: target, Products: gfResolver}, inventoryT0)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func stepIn(steps []InventoryGraphStep, id string) (InventoryGraphStep, bool) {
	for _, s := range steps {
		if s.Resource == id {
			return s, true
		}
	}
	return InventoryGraphStep{}, false
}

// A depends answer says where the code is and which addresses serve the
// thing, and what it rests on includes the accounts that own those
// addresses and what they lead to, with each account's sign-in and store.
func TestInventoryDependsShowsCodeAddressesAndTheAccountsBehindThem(t *testing.T) {
	const (
		siteHost = "hostname:www.shop.example"
		webSvc   = "sitehost:service:www"
		hostAcct = "sitehost:account:team"
	)
	p := dependsWith(t, gfShop, func(obs *InventoryObservation) {
		shop := findGraphResource(*obs, gfShop)
		shop.Links = append(shop.Links, gfLink("serves", siteHost, ""))
		obs.Resources = append(obs.Resources,
			InventoryObservedResource{ID: siteHost, Kind: "hostname", Provider: "dnsco", Label: "www.shop.example", Source: "dns",
				Places: []string{}, Facts: map[string]InventoryObservedFact{},
				Links: []InventoryLink{gfLink("points_to", webSvc, "inferred"), gfLink("owned_by", gfDomain, "")}},
			InventoryObservedResource{ID: webSvc, Kind: "service", Provider: "sitehost", Label: "www site", Source: "repos",
				Places: []string{}, Facts: map[string]InventoryObservedFact{}, Links: []InventoryLink{gfLink("owned_by", hostAcct, "")}},
			InventoryObservedResource{ID: hostAcct, Kind: "account", Provider: "sitehost", Label: "Site host team", Source: "logins",
				Places: []string{}, Facts: map[string]InventoryObservedFact{}})
	})
	if c, ok := stepIn(p.Code, gfRepo); !ok || c.Via != gfShop || c.Rel != "code_in" {
		t.Fatalf("code = %+v", p.Code)
	}
	if _, inWalk := stepIn(p.Steps, gfRepo); inWalk {
		t.Fatal("code is where the code is, not something the product runs on")
	}
	if a, ok := stepIn(p.Addresses, siteHost); !ok || a.Via != gfShop || a.Rel != "serves" {
		t.Fatalf("served address = %+v", p.Addresses)
	}
	if a, ok := stepIn(p.Addresses, gfHostname); !ok || a.Via != gfWebCt || !a.viaIsTarget {
		t.Fatalf("an address routing into what the walk reached = %+v", p.Addresses)
	}
	lead, ok := stepIn(p.Addresses, webSvc)
	if !ok || lead.Via != siteHost || lead.LinkConfidence != "inferred" || lead.PathConfidence != "inferred" {
		t.Fatalf("where the address leads = %+v", lead)
	}
	if acct, ok := stepIn(p.RestsOn, hostAcct); !ok || acct.Via != webSvc || acct.PathConfidence != "inferred" {
		t.Fatalf("the account owning what an address leads to rests on its path = %+v", p.RestsOn)
	}
	text := FormatInventoryWalkText(p)
	for _, want := range []string{
		"Code (1):\n  shop …shop (repo)",
		"Addresses that serve it (2):\n",
		"  serves www.shop.example …mple (hostname) → points to www site …:www (service) · link inferred\n",
		"  app.shop.example …mple (hostname) · routes to web-ct …:web\n",
		"  Site host team …team (account) · www site …:www owned by it · path inferred\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("depends text missing %q:\n%s", want, text)
		}
	}
}

// A parent that is an account is ownership, not a place a thing runs on; and
// what an account owns depends on it: impact walks ownership backwards.
func TestInventoryParentAccountIsOwnershipAndImpactFollowsIt(t *testing.T) {
	const siteHost = "hostname:www.shop.example"
	edit := func(obs *InventoryObservation) {
		domain := findGraphResource(*obs, gfDomain)
		domain.Links, domain.Parent = nil, gfAccountID // the DNS reader gives the account as the domain's parent
		shop := findGraphResource(*obs, gfShop)
		shop.Links = append(shop.Links, gfLink("serves", siteHost, ""))
		obs.Resources = append(obs.Resources, InventoryObservedResource{ID: siteHost, Kind: "hostname", Provider: "dnsco",
			Label: "www.shop.example", Source: "dns", Places: []string{}, Facts: map[string]InventoryObservedFact{},
			Links: []InventoryLink{gfLink("owned_by", gfDomain, "")}})
	}
	home := t.TempDir()
	if _, err := RefreshInventory(home, graphFixture(t, inventoryT0, edit), InventoryRefreshOptions{}, inventoryT0); err != nil {
		t.Fatal(err)
	}
	impact, err := BuildInventoryImpact(home, gfAccountID, gfResolver, inventoryT0)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := stepIn(impact.Steps, gfDomain)
	if !ok || d.Rel != "owned_by" || d.RelTitle != "owned by" {
		t.Fatalf("a domain whose parent is the account is owned by it: %+v", d)
	}
	for _, id := range []string{siteHost, gfShop} {
		if _, ok := stepIn(impact.Steps, id); !ok {
			t.Fatalf("what the account owns, and what serves through it, depends on it: %s missing from %+v", id, impact.Steps)
		}
	}
	if impact.Products < 1 {
		t.Fatalf("impact products = %d", impact.Products)
	}
	if text := FormatInventoryWalkText(impact); strings.Contains(text, "runs on Example account") {
		t.Fatalf("a domain never runs on an account:\n%s", text)
	}
	dep, err := BuildInventoryDepends(home, InventoryDependsQuery{Target: gfShop, Products: gfResolver}, inventoryT0)
	if err != nil {
		t.Fatal(err)
	}
	if _, inSteps := stepIn(dep.Steps, gfAccountID); inSteps {
		t.Fatal("an account is what a product rests on, not a step it runs on")
	}
	if acct, ok := stepIn(dep.RestsOn, gfAccountID); !ok || acct.Via != gfDomain || acct.Rel != "owned_by" {
		t.Fatalf("rests on = %+v", dep.RestsOn)
	}
}

// Long groups fold to a few rows with a count and the option that lists
// them; what lies beneath a machine folds into the machine's line; --all
// unfolds everything. The answer says when its information was last seen.
func TestInventoryDependsTextFoldsLongGroupsAndSaysWhenSeen(t *testing.T) {
	older := inventoryT0.Add(-30 * time.Hour).UTC().Format(time.RFC3339)
	p := dependsWith(t, gfShop, func(obs *InventoryObservation) {
		for i := 1; i <= 8; i++ {
			addDatastore(obs, fmt.Sprintf("pg:datastore:extra%d", i), fmt.Sprintf("extra-db-%d", i), nil)
		}
		addDatastore(obs, "nas:datastore:share", "nas-share", nil)
		shop := findGraphResource(*obs, gfShop) // after the appends: a pointer into the grown slice
		for i := 1; i <= 8; i++ {
			shop.Links = append(shop.Links, gfLink("uses", fmt.Sprintf("pg:datastore:extra%d", i), ""))
		}
		guest := findGraphResource(*obs, gfGuest)
		guest.Links = append(guest.Links, gfLink("uses", "nas:datastore:share", ""))
		findGraphResource(*obs, gfPay).ObservedAt = older
	})
	text := FormatInventoryWalkText(p)
	for _, want := range []string{
		"Needs: data stores (9):\n",
		"  ... and 3 more (--all lists them)\n",
		"app-vm …:101 (guest) · db-ct …r:db runs on it → runs on lab-node …e:n1 (host); beneath it 1 more thing (datastore 1; --all lists them)\n",
		"Seen: the snapshot was observed 2031-03-04 10:00 UTC; 1 thing comes from second-hand evidence, the oldest seen 2031-03-03 04:00 UTC.\n",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("folded depends text missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "nas-share") || strings.Contains(text, "extra-db-8") {
		t.Fatalf("folded rows must not be listed:\n%s", text)
	}
	all := FormatInventoryWalkTextWith(p, InventoryTextOptions{All: true})
	for _, want := range []string{"extra-db-8", "nas-share …hare (datastore) · app-vm …:101 uses it", "lab-node …e:n1 (host) · app-vm …:101 runs on it"} {
		if !strings.Contains(all, want) {
			t.Fatalf("--all text missing %q:\n%s", want, all)
		}
	}
	if strings.Contains(all, "more (--all lists them)") || strings.Contains(all, "beneath it") {
		t.Fatalf("--all folds nothing:\n%s", all)
	}
}

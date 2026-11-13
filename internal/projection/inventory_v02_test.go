package projection

// Falsifiers for the v0.2 observation contract: typed links with confidence,
// evidence and observed_at, resource observed_at, strict refusal of a link the
// kinds do not know, the boolean warning rule, and links that survive refresh,
// carry-forward and the diff. A v0.1 file still imports.

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestInventoryV02StoresTypedLinksAndResourceObservedAt(t *testing.T) {
	older := inventoryT0.Add(-72 * time.Hour).Format(time.RFC3339)
	data := graphFixture(t, inventoryT0, func(obs *InventoryObservation) {
		r := findGraphResource(*obs, gfRepo)
		r.ObservedAt = older
		r.Facts["last_commit"] = InventoryObservedFact{Value: "2031-02-01", State: "ok"}
	})
	home := t.TempDir()
	if _, err := RefreshInventory(home, data, InventoryRefreshOptions{}, inventoryT0); err != nil {
		t.Fatalf("v0.2 refresh: %v", err)
	}
	snap, _ := LoadInventorySnapshot(home)
	deploy := findGraphResource(snap.Observation, gfDeploy)
	if deploy == nil || len(deploy.Links) != 2 || deploy.Links[0].Confidence != "inferred" || deploy.Links[0].Evidence != "fixture" {
		t.Fatalf("stored deploy links = %+v", deploy)
	}
	p, err := BuildInventory(home, InventoryQuery{}, inventoryT0)
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := findInventoryResource(p, gfRepo)
	if repo.ObservedAt != older {
		t.Fatalf("resource observed_at = %q", repo.ObservedAt)
	}
	// A fact without its own time takes the resource's, not the source's.
	if f, _ := findInventoryFact(repo, "last_commit"); f.ObservedAt != older {
		t.Fatalf("fact fallback time = %q, want the resource's %q", f.ObservedAt, older)
	}
	if f, _ := findInventoryFact(repo, "has_remote"); f.ObservedAt != older {
		t.Fatalf("fact fallback time = %q", f.ObservedAt)
	}
}

func TestInventoryV02RefusesLinksTheContractDoesNotAllow(t *testing.T) {
	cases := []struct {
		name string
		edit func(obs *InventoryObservation)
		want string
	}{
		{"unknown relation names the nearest", func(obs *InventoryObservation) {
			findGraphResource(*obs, gfWebCt).Links[0].Rel = "run_on"
		}, `unknown relation "run_on" (nearest: runs_on)`},
		{"self link", func(obs *InventoryObservation) {
			findGraphResource(*obs, gfWebCt).Links[1].To = gfWebCt
		}, "points at its own resource"},
		{"bad confidence", func(obs *InventoryObservation) {
			findGraphResource(*obs, gfDB).Links[0].Confidence = "probably"
		}, `unknown confidence "probably"`},
		{"long evidence", func(obs *InventoryObservation) {
			findGraphResource(*obs, gfDB).Links[0].Evidence = strings.Repeat("e", 201)
		}, "evidence is longer than 200"},
		{"bad link time", func(obs *InventoryObservation) {
			findGraphResource(*obs, gfDB).Links[0].ObservedAt = "yesterday"
		}, `observed_at "yesterday"`},
		{"bad resource time", func(obs *InventoryObservation) {
			findGraphResource(*obs, gfDB).ObservedAt = "last week"
		}, `observed_at "last week"`},
		{"empty product reference", func(obs *InventoryObservation) {
			findGraphResource(*obs, gfPay).Links[0].To = "product:"
		}, "product reference has no id"},
		{"product id on another kind", func(obs *InventoryObservation) {
			findGraphResource(*obs, gfShop).Kind = "service"
		}, "a product resource has kind product"},
		{"v0.2 field in a v0.1 file", func(obs *InventoryObservation) {
			obs.SchemaVersion = "compound.system_inventory_observation.v0.1"
		}, "need schema_version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			_, err := RefreshInventory(home, graphFixture(t, inventoryT0, tc.edit), InventoryRefreshOptions{}, inventoryT0)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if strings.Contains(err.Error(), gfAccountID) {
				t.Fatalf("error leaks an unmasked id: %v", err)
			}
		})
	}
	// A target outside the file is kept (a failed source's resources come back
	// only by carry-forward) and counted in a note, never refused.
	res, err := RefreshInventory(t.TempDir(), graphFixture(t, inventoryT0, func(obs *InventoryObservation) {
		findGraphResource(*obs, gfDB).Links[0].To = "docker:container:gone-zz91"
	}), InventoryRefreshOptions{}, inventoryT0)
	if err != nil || len(res.Notes) != 1 || !strings.HasPrefix(res.Notes[0], "1 links point at a resource that is not in the inventory") {
		t.Fatalf("dangling target: notes %v err %v", res.Notes, err)
	}
	// Strict decoding still holds inside a link.
	var doc map[string]any
	if err := json.Unmarshal(graphFixture(t, inventoryT0, nil), &doc); err != nil {
		t.Fatal(err)
	}
	link := inventoryFixtureResource(doc, 0)["links"].([]any)[0].(map[string]any)
	link["weight"] = 3
	raw, _ := json.Marshal(doc)
	if _, err := RefreshInventory(t.TempDir(), raw, InventoryRefreshOptions{}, inventoryT0); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown link field err = %v", err)
	}
}

func TestInventoryV01FileStillImportsWithLooseLinks(t *testing.T) {
	data := mutateInventoryFixture(t, inventoryT0, func(doc map[string]any) {
		r := inventoryFixtureResource(doc, 2)
		r["links"] = append(r["links"].([]any), map[string]any{"rel": "near", "to": "rack 4"})
	})
	home := t.TempDir()
	res, err := RefreshInventory(home, data, InventoryRefreshOptions{}, inventoryT0)
	if err != nil || res.Resources != 6 {
		t.Fatalf("a v0.1 file must still import: %+v %v", res, err)
	}
	snap, _ := LoadInventorySnapshot(home)
	if snap.Observation.SchemaVersion != "compound.system_inventory_observation.v0.1" {
		t.Fatalf("stored schema = %q", snap.Observation.SchemaVersion)
	}
	// Its links stored without v0.2 fields: the snapshot bytes keep the v0.1 shape.
	raw, _ := json.Marshal(snap.Observation.Resources[2].Links)
	if strings.Contains(string(raw), "confidence") || !strings.Contains(string(raw), `"rel":"near"`) {
		t.Fatalf("v0.1 links stored as %s", raw)
	}
}

func TestInventoryBooleanWarnRule(t *testing.T) {
	home := graphImported(t, inventoryT0)
	p, err := BuildInventory(home, InventoryQuery{}, inventoryT0)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{gfRepo, gfBackup} {
		var row *InventoryAttention
		for i := range p.Attention {
			if p.Attention[i].ResourceID == id {
				row = &p.Attention[i]
			}
		}
		if row == nil || !strings.HasSuffix(row.Why, " is no") {
			t.Fatalf("a no under is_false must warn for %s: %+v", id, row)
		}
	}
	// yes, and not reported, do not warn.
	if inventoryWarned(p, gfIdentity, "sign_in_works") || inventoryWarned(p, gfDB, "backed_up") {
		t.Fatalf("yes or unreported must not warn: %+v", p.Attention)
	}
	if text := FormatInventoryText(p); !strings.Contains(text, "shop …shop (repo): Has a remote is no") {
		t.Fatalf("text missing the boolean warning:\n%s", text)
	}
	flipped := t.TempDir()
	data := graphFixture(t, inventoryT0, func(obs *InventoryObservation) {
		findGraphResource(*obs, gfRepo).Facts["has_remote"] = gfOK(true)
	})
	if _, err := RefreshInventory(flipped, data, InventoryRefreshOptions{}, inventoryT0); err != nil {
		t.Fatal(err)
	}
	p, _ = BuildInventory(flipped, InventoryQuery{}, inventoryT0)
	if inventoryWarned(p, gfRepo, "has_remote") {
		t.Fatal("has_remote yes must not warn")
	}
}

func TestInventoryLinksSurviveCarryForwardAndDiff(t *testing.T) {
	home := graphImported(t, inventoryT0)
	// The lab source fails: its resources are carried with their links.
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
		t.Fatalf("refresh with a failed source: %v", err)
	}
	snap, _ := LoadInventorySnapshot(home)
	db := findGraphResource(snap.Observation, gfDB)
	if db == nil || !db.Carried || len(db.Links) != 2 || db.Links[1].Confidence != "declared" {
		t.Fatalf("carried datastore must keep its links: %+v", db)
	}
	p, _ := BuildInventory(home, InventoryQuery{}, t1)
	if len(p.Changes.LinksAdded) != 0 || len(p.Changes.LinksRemoved) != 0 {
		t.Fatalf("carrying must change no link: %+v", p.Changes)
	}

	// A link removed and one added show in the diff, with their confidence.
	t2 := inventoryT0.Add(2 * time.Hour)
	next := graphFixture(t, t2, func(obs *InventoryObservation) {
		web := findGraphResource(*obs, gfWebCt)
		web.Links = web.Links[:1] // drop calls → db container
		dep := findGraphResource(*obs, gfDeploy)
		dep.Links = append(dep.Links, gfLink("calls", gfPay, "inferred"))
	})
	if _, err := RefreshInventory(home, next, InventoryRefreshOptions{}, t2); err != nil {
		t.Fatal(err)
	}
	p, _ = BuildInventory(home, InventoryQuery{}, t2)
	c := p.Changes
	if len(c.LinksAdded) != 1 || c.LinksAdded[0].ResourceID != gfDeploy || c.LinksAdded[0].Rel != "calls" || c.LinksAdded[0].Confidence != "inferred" {
		t.Fatalf("links added = %+v", c.LinksAdded)
	}
	if len(c.LinksRemoved) != 1 || c.LinksRemoved[0].ResourceID != gfWebCt || c.LinksRemoved[0].To != gfDBCt {
		t.Fatalf("links removed = %+v", c.LinksRemoved)
	}
	if text := FormatInventoryText(p); !strings.Contains(text, "1 links added, 1 links removed") {
		t.Fatalf("changes line must count links:\n%s", text)
	}
}

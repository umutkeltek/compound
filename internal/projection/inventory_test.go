package projection

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// inventoryLongID is a deliberately long fake id: output a person reads may
// show only its last four characters.
const inventoryLongID = "aws:account:0d5c3e9a7b41f2c8e6a19d73b5f0c4e2a8d6b1f3e5c7a9d0b2f4e6a8c1d3q7z9"

// inventoryFixture is a synthetic observation. {{T}} is replaced with the
// observation time so tests never compare the wall clock with fixed dates.
const inventoryFixture = `{
  "schema_version": "compound.system_inventory_observation.v0.1",
  "observed_at": "{{T}}",
  "collector": {"name": "example-collector", "version": "0.0.1"},
  "sources": [
    {"id": "cloud-main", "provider": "aws", "state": "ok", "detail": "", "observed_at": "{{T}}", "duration_ms": 900},
    {"id": "lab", "provider": "examplehv", "state": "ok", "detail": "", "observed_at": "{{T}}", "duration_ms": 400},
    {"id": "edge-box", "provider": "ssh_host", "state": "failed", "detail": "ssh: connection timed out", "observed_at": "{{T}}", "duration_ms": 20000},
    {"id": "dns", "provider": "dnsco", "state": "needs_sign_in", "detail": "token expired", "observed_at": "{{T}}", "duration_ms": 120},
    {"id": "seats", "provider": "json_file", "state": "ok", "detail": "", "observed_at": "{{T}}", "duration_ms": 2}
  ],
  "resources": [
    {"id": "` + inventoryLongID + `", "kind": "account", "provider": "aws", "label": "Example main account",
     "parent": "", "source": "cloud-main", "places": ["cloud"], "links": [],
     "facts": {
       "identity_ok": {"value": true, "unit": "bool", "state": "ok", "detail": "", "observed_at": "{{T}}"},
       "resources": {"value": 42, "unit": "count", "state": "ok", "detail": "", "observed_at": "{{T}}"},
       "cost_month_to_date": {"value": null, "unit": "usd", "state": "costs_money", "detail": "the cost API charges per request", "observed_at": "{{T}}"},
       "credit_left": {"value": null, "unit": "usd", "state": "needs_owner", "detail": "only the owner can read credits", "observed_at": "{{T}}"},
       "account_tail": {"value": "q7z9", "unit": "", "state": "ok", "detail": "", "observed_at": "{{T}}"}
     }},
    {"id": "examplehv:node:example-node-1", "kind": "host", "provider": "examplehv", "label": "lab-node",
     "parent": "", "source": "lab", "places": [], "links": [],
     "facts": {
       "cpu_pct": {"value": 12, "unit": "pct", "state": "ok", "detail": "", "observed_at": "{{T}}"},
       "memory_pct": {"value": 63.4, "unit": "pct", "state": "ok", "detail": "", "observed_at": "{{T}}"},
       "disk_pct": {"value": 85, "unit": "pct", "state": "ok", "detail": "", "observed_at": "{{T}}"},
       "guests_running": {"value": 1, "unit": "count", "state": "ok", "detail": "", "observed_at": "{{T}}"}
     }},
    {"id": "examplehv:vm:101", "kind": "guest", "provider": "examplehv", "label": "app-vm",
     "parent": "examplehv:node:example-node-1", "source": "lab", "places": ["apps.example"], "links": [{"rel": "runs_on", "to": "examplehv:node:example-node-1"}],
     "facts": {
       "status": {"value": "running", "unit": "", "state": "ok", "detail": "", "observed_at": "{{T}}"},
       "cpu_pct": {"value": 3, "unit": "pct", "state": "ok", "detail": "", "observed_at": "{{T}}"},
       "memory_pct": {"value": 97, "unit": "pct", "state": "ok", "detail": "", "observed_at": "{{T}}"},
       "disk_pct": {"value": null, "unit": "pct", "state": "not_supported", "detail": "guest agent not installed", "observed_at": "{{T}}"}
     }},
    {"id": "examplehv:ct:102", "kind": "guest", "provider": "examplehv", "label": "collector-ct",
     "parent": "examplehv:node:example-node-1", "source": "lab", "places": [], "links": [],
     "facts": {
       "status": {"value": "stopped", "unit": "", "state": "ok", "detail": "", "observed_at": "{{T}}"}
     }},
    {"id": "dnsco:zone:5f1e", "kind": "domain", "provider": "dnsco", "label": "shop.example",
     "parent": "", "source": "dns", "places": [], "links": [],
     "facts": {
       "plan": {"value": null, "unit": "", "state": "needs_sign_in", "detail": "token expired", "observed_at": "{{T}}"}
     }},
    {"id": "seatco:plan:team", "kind": "plan", "provider": "seatco", "label": "Example seat",
     "parent": "", "source": "seats", "places": [], "links": [],
     "facts": {
       "balance": {"value": 12.5, "unit": "usd", "state": "ok", "detail": "", "observed_at": "{{T}}"},
       "window_used_pct": {"value": 93, "unit": "pct", "state": "ok", "detail": "", "observed_at": "{{T}}"},
       "resets_at": {"value": "{{T}}", "unit": "time", "state": "ok", "detail": "", "observed_at": "{{T}}"}
     }}
  ]
}`

var inventoryT0 = time.Date(2031, 3, 4, 10, 0, 0, 0, time.UTC)

func inventoryFixtureAt(at time.Time) []byte {
	return []byte(strings.ReplaceAll(inventoryFixture, "{{T}}", at.UTC().Format(time.RFC3339)))
}

// mutateInventoryFixture decodes the fixture, applies fn, and re-encodes it.
func mutateInventoryFixture(t *testing.T, at time.Time, fn func(doc map[string]any)) []byte {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(inventoryFixtureAt(at), &doc); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	fn(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func inventoryFixtureResource(doc map[string]any, i int) map[string]any {
	return doc["resources"].([]any)[i].(map[string]any)
}

func TestInventoryRefreshStoresSnapshotAndKeepsPrevious(t *testing.T) {
	home := t.TempDir()
	imported := inventoryT0.Add(5 * time.Minute)
	res, err := RefreshInventory(home, inventoryFixtureAt(inventoryT0), InventoryRefreshOptions{}, imported)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if res.Resources != 6 || res.ByKind["guest"] != 2 || res.Sources != 5 || res.SourcesByState["failed"] != 1 {
		t.Fatalf("result counts = %+v", res)
	}
	if res.PreviousKept || res.ImportedAt != imported.Format(time.RFC3339) {
		t.Fatalf("first import result = %+v", res)
	}
	if res.SnapshotPath != filepath.Join(home, "observed", "inventory", "snapshot.json") {
		t.Fatalf("snapshot path = %s", res.SnapshotPath)
	}
	snap, err := LoadInventorySnapshot(home)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if snap.ImportedAt != imported.Format(time.RFC3339) || snap.Observation.ObservedAt != inventoryT0.Format(time.RFC3339) {
		t.Fatalf("stored snapshot times = %s / %s", snap.ImportedAt, snap.Observation.ObservedAt)
	}
	if _, err := LoadPreviousInventorySnapshot(home); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("no previous expected after first import, got %v", err)
	}

	later := inventoryT0.Add(24 * time.Hour)
	res2, err := RefreshInventory(home, inventoryFixtureAt(later), InventoryRefreshOptions{}, later.Add(time.Minute))
	if err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if !res2.PreviousKept || res2.PreviousObservedAt != inventoryT0.Format(time.RFC3339) {
		t.Fatalf("second result = %+v", res2)
	}
	prev, err := LoadPreviousInventorySnapshot(home)
	if err != nil || prev.Observation.ObservedAt != inventoryT0.Format(time.RFC3339) {
		t.Fatalf("previous snapshot = %+v, %v", prev.Observation.ObservedAt, err)
	}
	// A third import overwrites previous.json with the second snapshot.
	third := later.Add(time.Hour)
	if _, err := RefreshInventory(home, inventoryFixtureAt(third), InventoryRefreshOptions{}, third); err != nil {
		t.Fatalf("third refresh: %v", err)
	}
	prev, _ = LoadPreviousInventorySnapshot(home)
	if prev.Observation.ObservedAt != later.Format(time.RFC3339) {
		t.Fatalf("previous after third import = %s, want %s", prev.Observation.ObservedAt, later.Format(time.RFC3339))
	}
}

func TestInventoryRefreshRejectsContractViolations(t *testing.T) {
	cases := []struct {
		name string
		edit func(doc map[string]any)
		want string
	}{
		{"unknown kind", func(doc map[string]any) {
			inventoryFixtureResource(doc, 4)["kind"] = "bucket"
		}, `unknown kind "bucket"`},
		{"duplicate id", func(doc map[string]any) {
			inventoryFixtureResource(doc, 3)["id"] = "examplehv:vm:101"
		}, "id used twice"},
		{"ok with null value", func(doc map[string]any) {
			facts := inventoryFixtureResource(doc, 1)["facts"].(map[string]any)
			facts["disk_pct"].(map[string]any)["value"] = nil
		}, "state ok needs a value"},
		{"non-ok with a value", func(doc map[string]any) {
			facts := inventoryFixtureResource(doc, 0)["facts"].(map[string]any)
			facts["cost_month_to_date"].(map[string]any)["value"] = 4242
		}, "state costs_money must have value null"},
		{"unknown fact state", func(doc map[string]any) {
			facts := inventoryFixtureResource(doc, 1)["facts"].(map[string]any)
			facts["cpu_pct"].(map[string]any)["state"] = "probably"
		}, `unknown state "probably"`},
		{"undeclared source", func(doc map[string]any) {
			inventoryFixtureResource(doc, 1)["source"] = "nowhere"
		}, `source "nowhere" is not listed`},
		{"dangling parent", func(doc map[string]any) {
			inventoryFixtureResource(doc, 2)["parent"] = "examplehv:node:gone"
		}, "parent"},
		{"object value", func(doc map[string]any) {
			facts := inventoryFixtureResource(doc, 1)["facts"].(map[string]any)
			facts["cpu_pct"].(map[string]any)["value"] = map[string]any{"a": 1}
		}, "value must be a number, string, bool or null"},
		{"unknown field", func(doc map[string]any) {
			inventoryFixtureResource(doc, 1)["colour"] = "red"
		}, "unknown field"},
		{"wrong schema", func(doc map[string]any) {
			doc["schema_version"] = "compound.other.v9"
		}, "schema_version"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			_, err := RefreshInventory(home, mutateInventoryFixture(t, inventoryT0, tc.edit), InventoryRefreshOptions{}, inventoryT0)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if strings.Contains(err.Error(), inventoryLongID) {
				t.Fatalf("error leaks an unmasked id: %v", err)
			}
			if _, statErr := os.Stat(InventorySnapshotPath(home)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("a rejected observation must not write a snapshot (stat err %v)", statErr)
			}
		})
	}
}

func TestInventoryRefreshRefusesOlderObservationUnlessAllowed(t *testing.T) {
	home := t.TempDir()
	if _, err := RefreshInventory(home, inventoryFixtureAt(inventoryT0), InventoryRefreshOptions{}, inventoryT0); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	older := inventoryT0.Add(-time.Hour)
	_, err := RefreshInventory(home, inventoryFixtureAt(older), InventoryRefreshOptions{}, inventoryT0)
	if err == nil || !strings.Contains(err.Error(), "older than the stored snapshot") || !strings.Contains(err.Error(), "--allow-older") {
		t.Fatalf("older observation err = %v", err)
	}
	snap, _ := LoadInventorySnapshot(home)
	if snap.Observation.ObservedAt != inventoryT0.Format(time.RFC3339) {
		t.Fatalf("refused import changed the snapshot: %s", snap.Observation.ObservedAt)
	}
	if _, err := RefreshInventory(home, inventoryFixtureAt(older), InventoryRefreshOptions{AllowOlder: true}, inventoryT0); err != nil {
		t.Fatalf("--allow-older import: %v", err)
	}
	snap, _ = LoadInventorySnapshot(home)
	if snap.Observation.ObservedAt != older.Format(time.RFC3339) {
		t.Fatalf("--allow-older did not import: %s", snap.Observation.ObservedAt)
	}
}

func TestInventoryRefreshUsesOverlayKindsAndSurvivesBrokenOverlay(t *testing.T) {
	withBucket := func(doc map[string]any) {
		inventoryFixtureResource(doc, 4)["kind"] = "bucket"
	}
	home := t.TempDir()
	writeProjectionFile(t, filepath.Join(home, "config", "inventory-kinds.yaml"),
		"kinds:\n- id: bucket\n  title: Bucket\n  facts:\n  - {name: stored_gb, title: Stored, unit: gb, group: use}\n")
	if _, err := RefreshInventory(home, mutateInventoryFixture(t, inventoryT0, withBucket), InventoryRefreshOptions{}, inventoryT0); err != nil {
		t.Fatalf("overlay kind must be accepted: %v", err)
	}

	broken := t.TempDir()
	writeProjectionFile(t, filepath.Join(broken, "config", "inventory-kinds.yaml"), "kinds: [ {id: \n")
	res, err := RefreshInventory(broken, inventoryFixtureAt(inventoryT0), InventoryRefreshOptions{}, inventoryT0)
	if err != nil {
		t.Fatalf("a broken overlay must not stop an import of embedded kinds: %v", err)
	}
	if res.OverlayError == "" {
		t.Fatal("refresh must report the overlay error")
	}
}

func inventoryImported(t *testing.T, at time.Time) string {
	t.Helper()
	home := t.TempDir()
	if _, err := RefreshInventory(home, inventoryFixtureAt(at), InventoryRefreshOptions{}, at); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	return home
}

func findInventoryResource(p InventoryProjection, id string) (InventoryResourceView, bool) {
	for _, r := range p.Resources {
		if r.ID == id {
			return r, true
		}
	}
	return InventoryResourceView{}, false
}

func findInventoryFact(r InventoryResourceView, name string) (InventoryFactView, bool) {
	for _, f := range r.Facts {
		if f.Name == name {
			return f, true
		}
	}
	return InventoryFactView{}, false
}

func TestInventoryProjectionShape(t *testing.T) {
	home := inventoryImported(t, inventoryT0)
	now := inventoryT0.Add(2 * time.Hour)
	p, err := BuildInventory(home, InventoryQuery{}, now)
	if err != nil {
		t.Fatalf("BuildInventory: %v", err)
	}
	if p.SchemaVersion != InventorySchemaVersion || p.Snapshot.State != "fresh" || p.Snapshot.AgeHours == nil || *p.Snapshot.AgeHours != 2 {
		t.Fatalf("snapshot = %+v", p.Snapshot)
	}
	if p.Filter.StaleHours != DefaultInventoryStaleHours || p.Boundary == "" || len(p.Kinds) != 8 {
		t.Fatalf("filter/boundary/kinds = %+v %q %d", p.Filter, p.Boundary, len(p.Kinds))
	}
	if p.Summary.Resources != 6 || p.Summary.ByKind["guest"] != 2 || p.Summary.ByProvider["examplehv"] != 3 {
		t.Fatalf("summary = %+v", p.Summary)
	}
	host, ok := findInventoryResource(p, "examplehv:node:example-node-1")
	if !ok || host.Children != 2 {
		t.Fatalf("host = %+v", host)
	}
	// Every declared fact is listed in kind order, then the undeclared ones.
	if len(host.Facts) != 5 || host.Facts[0].Name != "cpu_pct" || host.Facts[4].Name != "cost_month" {
		t.Fatalf("host facts = %+v", host.Facts)
	}
	account, _ := findInventoryResource(p, inventoryLongID)
	tail, ok := findInventoryFact(account, "account_tail")
	if !ok || tail.Group != InventoryGroupOther || tail.Value != "q7z9" {
		t.Fatalf("undeclared fact must be kept as other: %+v", tail)
	}
	// Order: failed source, sign-in source, then warnings by how far past.
	var order []string
	for _, a := range p.Attention {
		order = append(order, a.Kind+":"+a.Label+":"+a.Fact)
	}
	want := []string{"source:edge-box:", "source:dns:", "host:lab-node:disk_pct", "plan:Example seat:window_used_pct", "guest:app-vm:memory_pct"}
	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Fatalf("attention order = %v\nwant %v", order, want)
	}
	if p.Changes.Note == "" || len(p.Changes.Added) != 0 {
		t.Fatalf("first import has nothing to compare: %+v", p.Changes)
	}

	byKind, err := BuildInventory(home, InventoryQuery{Kind: "guest"}, now)
	if err != nil || byKind.Summary.Resources != 2 {
		t.Fatalf("--kind guest = %+v, %v", byKind.Summary, err)
	}
	byProvider, _ := BuildInventory(home, InventoryQuery{Provider: "dnsco"}, now)
	if byProvider.Summary.Resources != 1 || len(byProvider.Sources) != 1 || byProvider.Sources[0].ID != "dns" {
		t.Fatalf("--provider dnsco = %+v sources %+v", byProvider.Summary, byProvider.Sources)
	}
	if _, err := BuildInventory(home, InventoryQuery{Kind: "bucket"}, now); err == nil || !strings.Contains(err.Error(), `unknown kind "bucket"`) {
		t.Fatalf("unknown --kind err = %v", err)
	}
}

func TestInventoryMissingSnapshotIsValidJSONWithNotice(t *testing.T) {
	p, err := BuildInventory(t.TempDir(), InventoryQuery{}, inventoryT0)
	if err != nil {
		t.Fatalf("BuildInventory: %v", err)
	}
	if p.Snapshot.State != "missing" || !strings.Contains(p.Snapshot.Notice, "compound system inventory refresh --collector-json") {
		t.Fatalf("snapshot = %+v", p.Snapshot)
	}
	if len(p.Attention) != 1 || p.Attention[0].Kind != "snapshot" {
		t.Fatalf("attention = %+v", p.Attention)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("missing snapshot JSON invalid: %v", err)
	}
	if back["resources"] == nil || back["attention"] == nil || back["sources"] == nil {
		t.Fatalf("arrays must be [] not null: %s", data)
	}
	if text := FormatInventoryText(p); !strings.Contains(text, "no snapshot") || !strings.Contains(text, "refresh --collector-json") {
		t.Fatalf("missing text = %q", text)
	}
}

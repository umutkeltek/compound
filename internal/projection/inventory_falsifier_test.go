package projection

// Falsifier tests for the system inventory reader. Each one fails if the
// feature it names is removed: unknown never renders as a number, unreported
// money facts say "not known", staleness is visible, thresholds come from the
// kinds data, refresh enforces the contract, changes are reported, the
// overlay extends the kinds, a broken overlay still serves, and text shows
// only masked ids. All data is synthetic.

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// storeInventoryUnchecked writes a snapshot without refresh validation, the
// way a hand-edited or older-version file could reach the reader.
func storeInventoryUnchecked(t *testing.T, home string, data []byte, importedAt time.Time) {
	t.Helper()
	var obs InventoryObservation
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&obs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	snap := InventorySnapshot{SchemaVersion: InventorySnapshotSchemaVersion, ImportedAt: importedAt.UTC().Format(time.RFC3339), Observation: obs}
	if err := writeInventorySnapshot(home, snap); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func inventoryJSON(t *testing.T, p InventoryProjection) string {
	t.Helper()
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestInventoryFalsifierNonOKFactNeverRendersANumber(t *testing.T) {
	// The contract says a non-ok fact has value null, and refresh enforces
	// it; the reader must still never show a number that slipped through.
	withValues := func(doc map[string]any) {
		account := inventoryFixtureResource(doc, 0)["facts"].(map[string]any)
		account["cost_month_to_date"].(map[string]any)["value"] = 4242.17
		host := inventoryFixtureResource(doc, 1)["facts"].(map[string]any)
		host["disk_pct"] = map[string]any{"value": 9876, "unit": "pct", "state": "failed", "detail": "df timed out", "observed_at": ""}
	}
	if _, err := RefreshInventory(t.TempDir(), mutateInventoryFixture(t, inventoryT0, withValues), InventoryRefreshOptions{}, inventoryT0); err == nil {
		t.Fatal("refresh must reject a non-ok fact that carries a value")
	}

	home := t.TempDir()
	// previous.json carries the same smuggled values, so changes are covered too.
	storeInventoryUnchecked(t, home, mutateInventoryFixture(t, inventoryT0, withValues), inventoryT0)
	storeInventoryUnchecked(t, home, mutateInventoryFixture(t, inventoryT0.Add(time.Hour), func(doc map[string]any) {
		withValues(doc)
		account := inventoryFixtureResource(doc, 0)["facts"].(map[string]any)
		account["cost_month_to_date"].(map[string]any)["value"] = 5353.29
	}), inventoryT0.Add(time.Hour))
	p, err := BuildInventory(home, InventoryQuery{}, inventoryT0.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("BuildInventory: %v", err)
	}
	account, _ := findInventoryResource(p, inventoryLongID)
	cost, _ := findInventoryFact(account, "cost_month_to_date")
	if cost.Value != nil || cost.State != "costs_money" {
		t.Fatalf("non-ok cost fact = %+v, want value null", cost)
	}
	host, _ := findInventoryResource(p, "examplehv:node:example-node-1")
	disk, _ := findInventoryFact(host, "disk_pct")
	if disk.Value != nil || disk.Warn {
		t.Fatalf("failed disk fact = %+v, want value null and no warning", disk)
	}
	for _, a := range p.Attention {
		if a.Fact == "disk_pct" && a.Kind == "host" {
			t.Fatalf("a failed fact must not raise a threshold warning: %+v", a)
		}
	}
	js := inventoryJSON(t, p)
	text := FormatInventoryText(p)
	for _, smuggled := range []string{"4242", "5353", "9876"} {
		if strings.Contains(js, smuggled) {
			t.Fatalf("JSON renders a non-ok value %s:\n%s", smuggled, js)
		}
		if strings.Contains(text, smuggled) {
			t.Fatalf("text renders a non-ok value %s:\n%s", smuggled, text)
		}
	}
}

func TestInventoryFalsifierUnreportedCostFactSaysNotKnown(t *testing.T) {
	home := inventoryImported(t, inventoryT0)
	p, err := BuildInventory(home, InventoryQuery{}, inventoryT0)
	if err != nil {
		t.Fatalf("BuildInventory: %v", err)
	}
	host, _ := findInventoryResource(p, "examplehv:node:example-node-1")
	cost, ok := findInventoryFact(host, "cost_month")
	if !ok {
		t.Fatalf("declared but unreported cost fact must be listed: %+v", host.Facts)
	}
	if cost.State != "not_reported" || cost.Value != nil || cost.Detail != "not known: the collector did not report it" || cost.Group != InventoryGroupCost {
		t.Fatalf("unreported cost fact = %+v", cost)
	}
	account, _ := findInventoryResource(p, inventoryLongID)
	if ends, _ := findInventoryFact(account, "credit_ends"); ends.State != "not_reported" || ends.Value != nil {
		t.Fatalf("unreported credit fact = %+v", ends)
	}
	text := FormatInventoryText(p)
	hostLine := ""
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, "lab-node") && strings.Contains(line, "CPU used") {
			hostLine = line
		}
	}
	if !strings.Contains(hostLine, "cost not known") {
		t.Fatalf("host line must say cost not known: %q\n%s", hostLine, text)
	}
	if strings.Contains(hostLine, "Cost per month 0") {
		t.Fatalf("unreported cost rendered as zero: %q", hostLine)
	}
}

func TestInventoryFalsifierStaleSnapshotSetsNoticeAndAttention(t *testing.T) {
	home := inventoryImported(t, inventoryT0)
	p, err := BuildInventory(home, InventoryQuery{}, inventoryT0.Add(40*time.Hour))
	if err != nil {
		t.Fatalf("BuildInventory: %v", err)
	}
	if p.Snapshot.State != "stale" || !strings.Contains(p.Snapshot.Notice, "40 hours") || !strings.Contains(p.Snapshot.Notice, "refresh --collector-json") {
		t.Fatalf("stale snapshot = %+v", p.Snapshot)
	}
	if len(p.Attention) == 0 || p.Attention[0].Kind != "snapshot" || p.Attention[0].Why != p.Snapshot.Notice {
		t.Fatalf("stale snapshot must be the first attention row: %+v", p.Attention)
	}
	if text := FormatInventoryText(p); !strings.Contains(text, "Stale.") || !strings.Contains(text, "Notice: The inventory was observed 40 hours ago") {
		t.Fatalf("stale text:\n%s", text)
	}
	wider, _ := BuildInventory(home, InventoryQuery{StaleHours: 48}, inventoryT0.Add(40*time.Hour))
	if wider.Snapshot.State != "fresh" || wider.Snapshot.Notice != "" {
		t.Fatalf("--stale-hours 48 must keep a 40-hour snapshot fresh: %+v", wider.Snapshot)
	}
	for _, a := range wider.Attention {
		if a.Kind == "snapshot" {
			t.Fatalf("fresh snapshot must not raise a snapshot row: %+v", a)
		}
	}
}

func inventoryWarned(p InventoryProjection, id, fact string) bool {
	for _, a := range p.Attention {
		if a.ResourceID == id && a.Fact == fact {
			return true
		}
	}
	return false
}

func TestInventoryFalsifierThresholdsComeFromKindsData(t *testing.T) {
	const hostID = "examplehv:node:example-node-1"
	home := inventoryImported(t, inventoryT0)
	p, _ := BuildInventory(home, InventoryQuery{}, inventoryT0)
	host, _ := findInventoryResource(p, hostID)
	disk, _ := findInventoryFact(host, "disk_pct")
	if !disk.Warn || !inventoryWarned(p, hostID, "disk_pct") {
		t.Fatalf("disk 85 must warn under the built-in 80 threshold: %+v", disk)
	}
	cpu, _ := findInventoryFact(host, "cpu_pct")
	if cpu.Warn || inventoryWarned(p, hostID, "cpu_pct") {
		t.Fatalf("cpu 12 must not warn: %+v", cpu)
	}

	// Raising the threshold in the overlay removes the warning; adding one
	// to a fact that had none creates a warning.
	writeProjectionFile(t, filepath.Join(home, "config", "inventory-kinds.yaml"),
		"kinds:\n- id: host\n  facts:\n  - {name: disk_pct, warn_at: 90}\n  - {name: cpu_pct, warn_at: 10, warn_when: above}\n")
	p, _ = BuildInventory(home, InventoryQuery{}, inventoryT0)
	if p.OverlayError != "" {
		t.Fatalf("overlay error: %s", p.OverlayError)
	}
	host, _ = findInventoryResource(p, hostID)
	disk, _ = findInventoryFact(host, "disk_pct")
	if disk.Warn || inventoryWarned(p, hostID, "disk_pct") {
		t.Fatalf("overlay threshold 90 must clear the disk warning: %+v", disk)
	}
	cpu, _ = findInventoryFact(host, "cpu_pct")
	if !cpu.Warn || !inventoryWarned(p, hostID, "cpu_pct") {
		t.Fatalf("overlay threshold 10 must make cpu 12 warn: %+v", cpu)
	}

	// A below threshold works the other way round.
	writeProjectionFile(t, filepath.Join(home, "config", "inventory-kinds.yaml"),
		"kinds:\n- id: plan\n  facts:\n  - {name: balance, warn_at: 20, warn_when: below}\n")
	p, _ = BuildInventory(home, InventoryQuery{}, inventoryT0)
	if !inventoryWarned(p, "seatco:plan:team", "balance") {
		t.Fatalf("balance 12.5 must warn below 20: %+v", p.Attention)
	}
}

func TestInventoryFalsifierDiffReportsAddedRemovedAndChangedFacts(t *testing.T) {
	home := inventoryImported(t, inventoryT0)
	next := mutateInventoryFixture(t, inventoryT0.Add(24*time.Hour), func(doc map[string]any) {
		resources := doc["resources"].([]any)
		// remove the stopped container (index 3)
		resources = append(resources[:3], resources[4:]...)
		host := resources[1].(map[string]any)["facts"].(map[string]any)
		// one value change and one state change
		host["disk_pct"].(map[string]any)["value"] = 88
		host["memory_pct"] = map[string]any{"value": nil, "unit": "pct", "state": "failed", "detail": "timeout", "observed_at": ""}
		// keep the plan's reset time equal, so only observed_at moves there
		plan := resources[4].(map[string]any)["facts"].(map[string]any)
		plan["resets_at"].(map[string]any)["value"] = inventoryT0.Format(time.RFC3339)
		resources = append(resources, map[string]any{
			"id": "dnsco:zone:9a0b", "kind": "domain", "provider": "dnsco", "label": "blog.example",
			"parent": "", "source": "dns", "places": []any{}, "links": []any{}, "facts": map[string]any{},
		})
		doc["resources"] = resources
	})
	if _, err := RefreshInventory(home, next, InventoryRefreshOptions{}, inventoryT0.Add(24*time.Hour)); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	p, err := BuildInventory(home, InventoryQuery{}, inventoryT0.Add(25*time.Hour))
	if err != nil {
		t.Fatalf("BuildInventory: %v", err)
	}
	c := p.Changes
	if c.PreviousObservedAt != inventoryT0.Format(time.RFC3339) {
		t.Fatalf("previous observed_at = %q", c.PreviousObservedAt)
	}
	if len(c.Added) != 1 || c.Added[0].ResourceID != "dnsco:zone:9a0b" || c.Added[0].Label != "blog.example" {
		t.Fatalf("added = %+v", c.Added)
	}
	if len(c.Removed) != 1 || c.Removed[0].ResourceID != "examplehv:ct:102" {
		t.Fatalf("removed = %+v", c.Removed)
	}
	got := map[string]InventoryFactChange{}
	for _, fc := range c.FactChanges {
		got[fc.ResourceID+"/"+fc.Fact] = fc
	}
	if len(got) != 2 {
		t.Fatalf("fact changes = %+v (observed_at alone must not count)", c.FactChanges)
	}
	disk := got["examplehv:node:example-node-1/disk_pct"]
	if from, _ := inventoryNumber(disk.From); from != 85 {
		t.Fatalf("disk change = %+v", disk)
	}
	if to, _ := inventoryNumber(disk.To); to != 88 {
		t.Fatalf("disk change = %+v", disk)
	}
	mem := got["examplehv:node:example-node-1/memory_pct"]
	if mem.FromState != "ok" || mem.ToState != "failed" || mem.To != nil {
		t.Fatalf("memory change = %+v", mem)
	}
	if text := FormatInventoryText(p); !strings.Contains(text, "1 added, 1 removed, 2 facts changed") {
		t.Fatalf("changes line missing:\n%s", text)
	}
}

func TestInventoryFalsifierOverlayAddsKindAndFactToTheRead(t *testing.T) {
	home := t.TempDir()
	writeProjectionFile(t, filepath.Join(home, "config", "inventory-kinds.yaml"), `kinds:
  - id: bucket
    title: Storage bucket
    question: How much is stored?
    facts:
      - {name: stored_gb, title: Stored, unit: gb, group: use, warn_at: 100, warn_when: above}
      - {name: cost_month, title: Cost per month, unit: "", group: cost}
  - id: host
    facts:
      - {name: load_avg, title: Load average, unit: "", group: use}
`)
	data := mutateInventoryFixture(t, inventoryT0, func(doc map[string]any) {
		doc["resources"] = append(doc["resources"].([]any), map[string]any{
			"id": "objstore:bucket:b1", "kind": "bucket", "provider": "objstore", "label": "media",
			"parent": "", "source": "cloud-main", "places": []any{}, "links": []any{},
			"facts": map[string]any{"stored_gb": map[string]any{"value": 140, "unit": "gb", "state": "ok", "detail": "", "observed_at": ""}},
		})
	})
	if _, err := RefreshInventory(home, data, InventoryRefreshOptions{}, inventoryT0); err != nil {
		t.Fatalf("refresh with overlay kind: %v", err)
	}
	p, err := BuildInventory(home, InventoryQuery{Kind: "bucket"}, inventoryT0)
	if err != nil {
		t.Fatalf("--kind bucket must be known through the overlay: %v", err)
	}
	if p.Summary.Resources != 1 || !inventoryWarned(p, "objstore:bucket:b1", "stored_gb") {
		t.Fatalf("bucket projection = %+v attention %+v", p.Summary, p.Attention)
	}
	bucket := p.Resources[0]
	if cost, _ := findInventoryFact(bucket, "cost_month"); cost.State != "not_reported" {
		t.Fatalf("overlay cost fact must be listed as not reported: %+v", bucket.Facts)
	}
	all, _ := BuildInventory(home, InventoryQuery{}, inventoryT0)
	host, _ := findInventoryResource(all, "examplehv:node:example-node-1")
	load, ok := findInventoryFact(host, "load_avg")
	if !ok || load.Group != InventoryGroupUse || load.State != "not_reported" {
		t.Fatalf("overlay fact must be declared on host: %+v", host.Facts)
	}
}

func TestInventoryFalsifierBrokenOverlayStillServesTheRead(t *testing.T) {
	home := inventoryImported(t, inventoryT0)
	writeProjectionFile(t, filepath.Join(home, "config", "inventory-kinds.yaml"), "kinds:\n- {id: host, facts: [{name: disk_pct, warn_at: oops}]}\n")
	p, err := BuildInventory(home, InventoryQuery{}, inventoryT0)
	if err != nil {
		t.Fatalf("a broken overlay must not fail the read: %v", err)
	}
	if p.OverlayError == "" || p.Summary.Resources != 6 || len(p.Kinds) != 8 {
		t.Fatalf("broken overlay read = overlay_error %q resources %d kinds %d", p.OverlayError, p.Summary.Resources, len(p.Kinds))
	}
	if !inventoryWarned(p, "examplehv:node:example-node-1", "disk_pct") {
		t.Fatal("built-in thresholds must still apply")
	}
	if text := FormatInventoryText(p); !strings.Contains(text, "Kinds overlay ignored") {
		t.Fatalf("text must say the overlay was ignored:\n%s", text)
	}
}

func TestInventoryFalsifierTextShowsOnlyMaskedIDs(t *testing.T) {
	home := inventoryImported(t, inventoryT0)
	// Two imports, so the changes line and diff paths are exercised too.
	if _, err := RefreshInventory(home, inventoryFixtureAt(inventoryT0.Add(time.Hour)), InventoryRefreshOptions{}, inventoryT0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, q := range []InventoryQuery{{}, {Kind: "account"}, {Provider: "aws"}} {
		p, err := BuildInventory(home, q, inventoryT0.Add(50*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		text := FormatInventoryText(p)
		if !strings.Contains(text, "…q7z9") {
			t.Fatalf("masked id missing for %+v:\n%s", q, text)
		}
		opaque := strings.TrimPrefix(inventoryLongID, "aws:account:")
		for i := 0; i+5 <= len(opaque); i++ {
			if window := opaque[i : i+5]; strings.Contains(text, window) {
				t.Fatalf("text for %+v shows %q, more than the last four characters of the id:\n%s", q, window, text)
			}
		}
	}
}

func TestInventoryFalsifierTextStaysNearFortyLines(t *testing.T) {
	data := mutateInventoryFixture(t, inventoryT0, func(doc map[string]any) {
		resources := doc["resources"].([]any)
		for i := 0; i < 30; i++ {
			resources = append(resources, map[string]any{
				"id": "dnsco:zone:z" + string(rune('a'+i%26)) + string(rune('a'+i/26)), "kind": "domain", "provider": "dnsco",
				"label": "site" + string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".example", "parent": "", "source": "dns",
				"places": []any{}, "links": []any{}, "facts": map[string]any{},
			})
		}
		doc["resources"] = resources
	})
	home := t.TempDir()
	if _, err := RefreshInventory(home, data, InventoryRefreshOptions{}, inventoryT0); err != nil {
		t.Fatal(err)
	}
	p, _ := BuildInventory(home, InventoryQuery{}, inventoryT0)
	text := FormatInventoryText(p)
	if n := strings.Count(text, "\n"); n > 42 {
		t.Fatalf("text is %d lines:\n%s", n, text)
	}
	if !strings.Contains(text, "more: compound system inventory --kind domain --provider dnsco") {
		t.Fatalf("a capped group must say how to see the rest:\n%s", text)
	}
	filtered, _ := BuildInventory(home, InventoryQuery{Kind: "domain"}, inventoryT0)
	if got := strings.Count(FormatInventoryText(filtered), ".example …"); got != 31 {
		t.Fatalf("--kind domain must list every domain, got %d", got)
	}
}

package projection

// Falsifiers for carrying a source's resources from its last good read when
// the source does not answer: one listing tool timing out must not make its
// resources look removed.

import (
	"strings"
	"testing"
	"time"
)

// inventorySourceDown sets a source's state and drops its resources, the way a
// collector reports a source whose listing tool did not answer.
func inventorySourceDown(source, state string) func(doc map[string]any) {
	return func(doc map[string]any) {
		for _, s := range doc["sources"].([]any) {
			if row := s.(map[string]any); row["id"] == source {
				row["state"] = state
				row["detail"] = "listing timed out"
			}
		}
		var kept []any
		for _, r := range doc["resources"].([]any) {
			if r.(map[string]any)["source"] != source {
				kept = append(kept, r)
			}
		}
		doc["resources"] = kept
	}
}

func refreshInventoryAt(t *testing.T, home string, data []byte, opts InventoryRefreshOptions, now time.Time) InventoryRefreshResult {
	t.Helper()
	res, err := RefreshInventory(home, data, opts, now)
	if err != nil {
		t.Fatalf("refresh at %s: %v", now.Format(time.RFC3339), err)
	}
	return res
}

func TestInventoryCarryFailedSourceKeepsItsResources(t *testing.T) {
	home := inventoryImported(t, inventoryT0)
	t1 := inventoryT0.Add(2 * time.Hour)
	res := refreshInventoryAt(t, home, mutateInventoryFixture(t, t1, inventorySourceDown("lab", "failed")), InventoryRefreshOptions{}, t1)
	if res.Carried != 3 || res.Resources != 6 || len(res.Dropped) != 0 {
		t.Fatalf("refresh result = carried %d resources %d dropped %+v", res.Carried, res.Resources, res.Dropped)
	}
	snap, _ := LoadInventorySnapshot(home)
	for _, r := range snap.Observation.Resources {
		if r.Source != "lab" {
			continue
		}
		if !r.Carried || r.LastObservedAt != inventoryT0.Format(time.RFC3339) {
			t.Fatalf("stored lab resource %s = carried %v last %q", r.ID, r.Carried, r.LastObservedAt)
		}
		for name, f := range r.Facts {
			if f.ObservedAt != inventoryT0.Format(time.RFC3339) {
				t.Fatalf("carried fact %s/%s observed_at changed to %q", r.ID, name, f.ObservedAt)
			}
		}
	}

	now := inventoryT0.Add(3 * time.Hour)
	p, err := BuildInventory(home, InventoryQuery{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Changes.Added) != 0 || len(p.Changes.Removed) != 0 {
		t.Fatalf("carried resources must not show as added or removed: %+v", p.Changes)
	}
	for _, fc := range p.Changes.FactChanges {
		if strings.HasPrefix(fc.ResourceID, "examplehv:") {
			t.Fatalf("a carried resource must not show fact changes: %+v", fc)
		}
	}
	if p.Summary.Carried != 3 || p.Summary.Resources != 6 {
		t.Fatalf("summary = %+v", p.Summary)
	}
	host, _ := findInventoryResource(p, "examplehv:node:example-node-1")
	if !host.Carried || host.LastObservedAt != inventoryT0.Format(time.RFC3339) {
		t.Fatalf("host view = carried %v last %q", host.Carried, host.LastObservedAt)
	}
	if disk, _ := findInventoryFact(host, "disk_pct"); disk.State != "ok" || disk.Value == nil {
		t.Fatalf("carried facts must be kept unchanged: %+v", disk)
	}
	var labRow *InventoryAttention
	for i := range p.Attention {
		if p.Attention[i].Kind == "source" && p.Attention[i].Label == "lab" {
			labRow = &p.Attention[i]
		}
	}
	if labRow == nil || !strings.Contains(labRow.Why, "source failed: listing timed out; 3 resources shown from the last good read, 3 hours ago") {
		t.Fatalf("failed source row = %+v", labRow)
	}
	text := FormatInventoryText(p)
	for _, want := range []string{"lab-node …de-1: ", "from the last good read, 3 hours ago", "0 added, 0 removed, ", "last good read of a source that did not answer: 3 resources"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text missing %q:\n%s", want, text)
		}
	}
}

func TestInventoryCarrySkippedSourceDropsItsResources(t *testing.T) {
	home := inventoryImported(t, inventoryT0)
	t1 := inventoryT0.Add(time.Hour)
	res := refreshInventoryAt(t, home, mutateInventoryFixture(t, t1, inventorySourceDown("lab", "skipped")), InventoryRefreshOptions{}, t1)
	if res.Carried != 0 || res.Resources != 3 {
		t.Fatalf("a skipped source must carry nothing: %+v", res)
	}
	p, _ := BuildInventory(home, InventoryQuery{}, t1)
	if len(p.Changes.Removed) != 3 || p.Summary.Carried != 0 {
		t.Fatalf("skipped source resources must be removed: %+v summary %+v", p.Changes.Removed, p.Summary)
	}
	for _, r := range p.Changes.Removed {
		if r.Reason != "" {
			t.Fatalf("a plain removal has no drop reason: %+v", r)
		}
	}
}

func TestInventoryCarryDropsAfterMaxAge(t *testing.T) {
	down := inventorySourceDown("lab", "failed")
	run := func(opts InventoryRefreshOptions) (string, InventoryRefreshResult) {
		home := inventoryImported(t, inventoryT0)
		t1 := inventoryT0.Add(24 * time.Hour)
		refreshInventoryAt(t, home, mutateInventoryFixture(t, t1, down), opts, t1)
		snap, _ := LoadInventorySnapshot(home)
		for _, r := range snap.Observation.Resources {
			if r.Carried && r.LastObservedAt != inventoryT0.Format(time.RFC3339) {
				t.Fatalf("a second failure must keep the first last_observed_at, got %q", r.LastObservedAt)
			}
		}
		t8 := inventoryT0.Add(8 * 24 * time.Hour)
		return home, refreshInventoryAt(t, home, mutateInventoryFixture(t, t8, down), opts, t8)
	}

	home, res := run(InventoryRefreshOptions{})
	if res.Carried != 0 || len(res.Dropped) != 3 || res.Resources != 3 {
		t.Fatalf("8-day-old carried resources must be dropped: %+v", res)
	}
	p, _ := BuildInventory(home, InventoryQuery{}, inventoryT0.Add(8*24*time.Hour))
	if len(p.Changes.Removed) != 3 {
		t.Fatalf("dropped resources must be reported as removed: %+v", p.Changes)
	}
	for _, r := range p.Changes.Removed {
		if r.Reason != "not seen for 7 days" {
			t.Fatalf("removed reason = %q", r.Reason)
		}
	}

	_, res = run(InventoryRefreshOptions{CarryMaxAge: 10 * 24 * time.Hour})
	if res.Carried != 3 || len(res.Dropped) != 0 {
		t.Fatalf("a 10-day carry window must keep 8-day-old resources: %+v", res)
	}
}

func TestInventoryCarryRecoveredSourceReplacesCarriedRows(t *testing.T) {
	home := inventoryImported(t, inventoryT0)
	t1 := inventoryT0.Add(time.Hour)
	refreshInventoryAt(t, home, mutateInventoryFixture(t, t1, inventorySourceDown("lab", "failed")), InventoryRefreshOptions{}, t1)
	t2 := inventoryT0.Add(2 * time.Hour)
	res := refreshInventoryAt(t, home, inventoryFixtureAt(t2), InventoryRefreshOptions{}, t2)
	if res.Carried != 0 || res.Resources != 6 {
		t.Fatalf("recovered source result = %+v", res)
	}
	snap, _ := LoadInventorySnapshot(home)
	seen := map[string]bool{}
	for _, r := range snap.Observation.Resources {
		if seen[r.ID] {
			t.Fatalf("duplicate id after recovery: %s", r.ID)
		}
		seen[r.ID] = true
		if r.Carried || r.LastObservedAt != "" {
			t.Fatalf("recovered resource still carried: %+v", r)
		}
	}
	p, _ := BuildInventory(home, InventoryQuery{}, t2)
	if p.Summary.Carried != 0 || len(p.Changes.Added) != 0 || len(p.Changes.Removed) != 0 {
		t.Fatalf("recovery projection = summary %+v changes %+v", p.Summary, p.Changes)
	}

	// A partly recovered source: the resource it lists again is fresh, the
	// others stay carried, and no id appears twice.
	t3 := inventoryT0.Add(3 * time.Hour)
	refreshInventoryAt(t, home, mutateInventoryFixture(t, t3, func(doc map[string]any) {
		resources := doc["resources"].([]any)
		host := resources[1]
		inventorySourceDown("lab", "failed")(doc)
		doc["resources"] = append(doc["resources"].([]any), host)
	}), InventoryRefreshOptions{}, t3)
	p, _ = BuildInventory(home, InventoryQuery{}, t3)
	if p.Summary.Resources != 6 || p.Summary.Carried != 2 {
		t.Fatalf("partial recovery summary = %+v", p.Summary)
	}
	if host, _ := findInventoryResource(p, "examplehv:node:example-node-1"); host.Carried {
		t.Fatal("a resource the source listed again must not be carried")
	}
}

func TestInventoryCarryFieldsAreNotAcceptedFromACollector(t *testing.T) {
	_, err := RefreshInventory(t.TempDir(), mutateInventoryFixture(t, inventoryT0, func(doc map[string]any) {
		inventoryFixtureResource(doc, 1)["carried"] = true
	}), InventoryRefreshOptions{}, inventoryT0)
	if err == nil || !strings.Contains(err.Error(), "set by refresh, never by a collector") {
		t.Fatalf("collector-set carried err = %v", err)
	}
}

package contract

import (
	"fmt"
	"strings"
	"testing"

	"compound/internal/capgraph"
)

// The summary is what a session-start harness reads. It must name the top five offers
// (name and ref) and the sealed pack id, or the model never sees an offer and hit-rate
// matching cannot tell which pack a session was served.
func TestServedSummaryNamesTopOffersAndSealedPackID(t *testing.T) {
	t.Setenv("COMPOUND_MEM_ENGINE_BIN", "")
	t.Setenv("COMPOUND_MEM_ENGINE_TIMEOUT", "")
	home := t.TempDir()
	graph, err := capgraph.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	// Seven matching offers with distinct weights: the top five must be named, the last
	// two must stay in the drilldown only.
	type offer struct{ id, name string }
	var offers []offer
	for i := 0; i < 7; i++ {
		o := offer{id: fmt.Sprintf("skill:fixture-release-%d", i), name: fmt.Sprintf("Fixture Release Helper %d", i)}
		offers = append(offers, o)
		if err := graph.Add(capgraph.Node{
			ID: o.id, Kind: "skill", Name: o.name,
			Triggers: []string{"release", "ledger"}, Weight: float64(10 - i),
		}); err != nil {
			t.Fatal(err)
		}
	}
	runtime, err := MaterializeRuntime(Input{
		Harness: "codex", Cwd: ambientScopeRepo(t, "alpha-repo"), Task: "prepare the ledger release",
		RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute",
	}, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if servedPackIDMarker.MatchString(runtime.ContextPack.SummaryMD) {
		t.Fatalf("an unsealed pack must not claim a sealed id: %q", runtime.ContextPack.SummaryMD)
	}
	sealed, err := SealRuntimeMaterialization(runtime)
	if err != nil {
		t.Fatal(err)
	}
	summary := sealed.ContextPack.SummaryMD
	for i, o := range offers {
		line := "- " + o.name + " — capability://" + o.id
		if got := strings.Contains(summary, line); got != (i < servedSummaryOfferLimit) {
			t.Errorf("offer #%d %q rendered=%v, want %v; summary:\n%s", i+1, line, got, i < servedSummaryOfferLimit, summary)
		}
	}
	if !strings.Contains(summary, "7 capability offer(s) surfaced") {
		t.Errorf("offer count line lost: %q", summary)
	}
	want := "Context pack: " + sealed.ContextPack.ID + "."
	if !strings.HasPrefix(sealed.ContextPack.ID, "ctx_") || strings.Count(summary, want) != 1 {
		t.Fatalf("summary must name the sealed pack %s exactly once:\n%s", sealed.ContextPack.ID, summary)
	}

	// The load path re-seals a persisted pack and compares ids: the marker must not feed
	// the digest, or every served pack would fail its own identity check.
	resealed, err := SealRuntimeMaterialization(sealed)
	if err != nil {
		t.Fatal(err)
	}
	if resealed.ContextPack.ID != sealed.ContextPack.ID || resealed.ContextPack.SummaryMD != summary {
		t.Fatalf("reseal changed identity: id %s -> %s\nsummary before:\n%s\nafter:\n%s",
			sealed.ContextPack.ID, resealed.ContextPack.ID, summary, resealed.ContextPack.SummaryMD)
	}
}

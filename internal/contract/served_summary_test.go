package contract

import (
	"fmt"
	"strings"
	"testing"

	"compound/internal/capgraph"
	"compound/internal/envelope"
	"compound/internal/mem"
)

// A live session start has no prompt, so the hook passes its event name as the task. The
// event name must not rank facts or offers: before this rule, every session start served
// facts and offers whose text merely says "SessionStart" or "session".
func TestHookEventNameIsNotTaskText(t *testing.T) {
	t.Setenv("COMPOUND_MEM_ENGINE_BIN", "")
	t.Setenv("COMPOUND_MEM_ENGINE_TIMEOUT", "")
	home := t.TempDir()
	promoteAmbientScopeClaims(t, home, []mem.Candidate{
		{Entity: "HookPipeline", Predicate: "authority_status", Object: "active hooks are SessionStart and UserPromptSubmit", ClaimType: "architecture", Visibility: "contextual"},
	})
	graph, err := capgraph.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	if err := graph.Add(capgraph.Node{ID: "tool:fixture.session-notebook", Kind: "tool", Name: "fixture session notebook", Triggers: []string{"sessionstart", "session"}, Weight: 1}); err != nil {
		t.Fatal(err)
	}
	in := Input{Harness: "codex", Cwd: ambientScopeRepo(t, "alpha-repo"), RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute"}
	for _, event := range envelope.HookNames {
		in.Task = event
		pack, err := PackWithCapabilities(in, DefaultPolicy(), home, fixedNow)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(pack.SummaryMD, "HookPipeline") || len(pack.ActiveMemory) != 0 {
			t.Errorf("task %q ranked a fact on the event name: memory=%v", event, pack.ActiveMemory)
		}
		if strings.Contains(pack.SummaryMD, "fixture session notebook") {
			t.Errorf("task %q ranked an offer on the event name:\n%s", event, pack.SummaryMD)
		}
	}
	in.Task = "which hooks are active at SessionStart"
	pack, err := PackWithCapabilities(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pack.SummaryMD, "HookPipeline.authority_status") {
		t.Fatalf("a real task that names the event must still recall the fact:\n%s", pack.SummaryMD)
	}
}

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

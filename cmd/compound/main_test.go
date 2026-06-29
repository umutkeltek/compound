package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/capgraph"
	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/hook"
	"compound/internal/work"
)

var cliTestNow = time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

func TestHookSessionEndAutoConsumesCapabilityFeedback(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}

	graph, err := capgraph.Open(homePath)
	if err != nil {
		t.Fatalf("open capability graph: %v", err)
	}
	if err := graph.Add(capgraph.Node{
		ID:       "skill:session-end-proof",
		Kind:     "skill",
		Name:     "Session End Proof",
		Triggers: []string{"session end proof"},
	}); err != nil {
		t.Fatalf("add capability node: %v", err)
	}

	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work store: %v", err)
	}
	w, err := store.Create(work.CreateInput{
		Owner:            "codex",
		SuccessPredicate: "session-end validates used capability",
	}, cliTestNow)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	if _, err := store.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatalf("claim work: %v", err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "running"}, cliTestNow); err != nil {
		t.Fatalf("advance running: %v", err)
	}
	if _, err := store.RecordAction(
		w.ID,
		testActionIntent(w.ID, "act_session_end_used", "capability://skill:session-end-proof"),
		testPolicyDecision(w.ID, "pol_session_end_used", "act_session_end_used"),
		cliTestNow,
	); err != nil {
		t.Fatalf("record action: %v", err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "verifying"}, cliTestNow); err != nil {
		t.Fatalf("advance verifying: %v", err)
	}

	fixturePath := filepath.Join(t.TempDir(), "session-end.json")
	writeJSONFixture(t, fixturePath, hook.Fixture{
		Harness:  "codex",
		WorkID:   w.ID,
		MoveType: "outward_delta",
		Outcome:  "completed",
		Summary:  "session-end closure used capability",
	})

	cmdHook([]string{"session-end", fixturePath})

	edges := readCapabilityEdges(t, homePath)
	if !hasCapabilityEdge(edges, capgraph.EdValidatedBy, "skill:session-end-proof") {
		t.Fatalf("session-end did not auto-validate used capability; edges=%+v", edges)
	}
}

func TestImportedSkillOfferCanBecomeValidatedSessionEndFeedback(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}

	cmdCapability([]string{"import-skills", filepath.Join("..", "..", "fixtures", "skills", "skill-manifest.yaml")})
	cmdHook([]string{"user-prompt-submit", writeHookFixture(t, hook.Fixture{
		Harness:  "codex",
		Cwd:      "/repo/product",
		RepoKind: "product",
		Task:     "pricing paywall",
		Prompt:   "prepare pricing paywall review",
	})})

	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work store: %v", err)
	}
	w := onlyWork(t, store)
	if w.IDs.ContextPackID == "" {
		t.Fatal("user-prompt work did not preserve context_pack_id")
	}
	pack := readContextPack(t, homePath, w.IDs.ContextPackID)
	if !contextPackIncludes(pack, "capability://skill:pricing") {
		t.Fatalf("context pack did not include imported pricing capability; included=%+v", pack.Included)
	}

	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "ready"}, cliTestNow); err != nil {
		t.Fatalf("advance ready: %v", err)
	}
	if _, err := store.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatalf("claim work: %v", err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "running"}, cliTestNow); err != nil {
		t.Fatalf("advance running: %v", err)
	}
	if _, err := store.RecordAction(
		w.ID,
		testActionIntent(w.ID, "act_imported_skill_used", "capability://skill:pricing"),
		testPolicyDecision(w.ID, "pol_imported_skill_used", "act_imported_skill_used"),
		cliTestNow,
	); err != nil {
		t.Fatalf("record action: %v", err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "verifying"}, cliTestNow); err != nil {
		t.Fatalf("advance verifying: %v", err)
	}

	cmdHook([]string{"session-end", writeHookFixture(t, hook.Fixture{
		Harness:  "codex",
		WorkID:   w.ID,
		MoveType: "outward_delta",
		Outcome:  "completed",
		Summary:  "pricing paywall review used imported skill",
	})})

	edges := readCapabilityEdges(t, homePath)
	if !hasCapabilityEdge(edges, capgraph.EdValidatedBy, "skill:pricing") {
		t.Fatalf("session-end did not validate imported used skill; edges=%+v", edges)
	}
}

func TestWorkCloseExplicitFeedbackAutoConsumes(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	graph, err := capgraph.Open(homePath)
	if err != nil {
		t.Fatalf("open capability graph: %v", err)
	}
	for _, node := range []capgraph.Node{
		{ID: "skill:missed-cli", Kind: "skill", Name: "Missed CLI", Triggers: []string{"pricing"}},
		{ID: "skill:false-cli", Kind: "skill", Name: "False CLI", Triggers: []string{"pricing"}},
	} {
		if err := graph.Add(node); err != nil {
			t.Fatalf("add node %s: %v", node.ID, err)
		}
	}

	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work store: %v", err)
	}
	w, err := store.Create(work.CreateInput{Owner: "codex", SuccessPredicate: "record explicit feedback"}, cliTestNow)
	if err != nil {
		t.Fatalf("create work: %v", err)
	}
	if _, err := store.Claim(w.ID, "codex", cliTestNow); err != nil {
		t.Fatalf("claim work: %v", err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "running"}, cliTestNow); err != nil {
		t.Fatalf("advance running: %v", err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "verifying"}, cliTestNow); err != nil {
		t.Fatalf("advance verifying: %v", err)
	}

	cmdWork([]string{
		"close", w.ID,
		"--outcome", "failed",
		"--move-type", "outward_delta",
		"--summary", "pricing review",
		"--changed",
		"--missed", "capability://skill:missed-cli",
		"--false-triggered", "capability://skill:false-cli",
	})

	edges := readCapabilityEdges(t, homePath)
	if !hasCapabilityEdgeTo(edges, capgraph.EdMissedWhen, "skill:missed-cli") {
		t.Fatalf("work close did not auto-consume missed feedback; edges=%+v", edges)
	}
	if !hasCapabilityEdgeTo(edges, capgraph.EdFalseTriggeredBy, "skill:false-cli") {
		t.Fatalf("work close did not auto-consume false-trigger feedback; edges=%+v", edges)
	}
}

func testActionIntent(workID, id, capability string) envelope.ActionIntent {
	return envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            id,
			CreatedAt:     cliTestNow.Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "hook", Name: "compound-test"},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Actor:         "codex",
		Capability:    capability,
		Action:        "write_receipt",
		Target:        envelope.ActionTarget{Kind: "path", Path: "events"},
		MutationClass: "local_reversible",
		Reversibility: "easy",
	}
}

func testPolicyDecision(workID, id, actionID string) envelope.PolicyDecision {
	return envelope.PolicyDecision{
		Header: envelope.Header{
			SchemaVersion: envelope.SVPolicyDecision,
			ID:            id,
			CreatedAt:     cliTestNow.Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "policy", Name: "compound-test"},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		ActionIntentID: actionID,
		Outcome:        "allow_with_obligations",
		Reasons:        []string{"test action is local and reversible"},
		Obligations:    []string{"emit receipt"},
	}
}

func writeHookFixture(t *testing.T, fixture hook.Fixture) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hook.json")
	writeJSONFixture(t, path, fixture)
	return path
}

func onlyWork(t *testing.T, store *work.Store) envelope.WorkEnvelope {
	t.Helper()
	works, err := store.List()
	if err != nil {
		t.Fatalf("list work: %v", err)
	}
	if len(works) != 1 {
		t.Fatalf("work count = %d, want 1: %+v", len(works), works)
	}
	return works[0]
}

func readContextPack(t *testing.T, homePath, id string) envelope.ContextPack {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(homePath, "context", "packs", id+".json"))
	if err != nil {
		t.Fatalf("read context pack %s: %v", id, err)
	}
	var pack envelope.ContextPack
	if err := json.Unmarshal(b, &pack); err != nil {
		t.Fatalf("parse context pack %s: %v", id, err)
	}
	if err := pack.Validate(); err != nil {
		t.Fatalf("context pack %s invalid: %v", id, err)
	}
	return pack
}

func contextPackIncludes(pack envelope.ContextPack, ref string) bool {
	for _, included := range pack.Included {
		if included.Ref == ref {
			return true
		}
	}
	return false
}

func writeJSONFixture(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readCapabilityEdges(t *testing.T, homePath string) []capgraph.Edge {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(homePath, "registries", "capability-edges.jsonl"))
	if err != nil {
		t.Fatalf("read capability edges: %v", err)
	}
	var edges []capgraph.Edge
	for _, line := range splitJSONLines(string(b)) {
		var edge capgraph.Edge
		if err := json.Unmarshal([]byte(line), &edge); err != nil {
			t.Fatalf("parse edge %q: %v", line, err)
		}
		edges = append(edges, edge)
	}
	return edges
}

func splitJSONLines(s string) []string {
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

func hasCapabilityEdge(edges []capgraph.Edge, kind, from string) bool {
	for _, edge := range edges {
		if edge.Kind == kind && edge.From == from {
			return true
		}
	}
	return false
}

func hasCapabilityEdgeTo(edges []capgraph.Edge, kind, to string) bool {
	for _, edge := range edges {
		if edge.Kind == kind && edge.To == to {
			return true
		}
	}
	return false
}

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
	if _, err := store.RecordAction(w.ID, testActionIntent(w.ID), testPolicyDecision(w.ID), cliTestNow); err != nil {
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

func testActionIntent(workID string) envelope.ActionIntent {
	return envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            "act_session_end_used",
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
		Capability:    "capability://skill:session-end-proof",
		Action:        "write_receipt",
		Target:        envelope.ActionTarget{Kind: "path", Path: "events"},
		MutationClass: "local_reversible",
		Reversibility: "easy",
	}
}

func testPolicyDecision(workID string) envelope.PolicyDecision {
	return envelope.PolicyDecision{
		Header: envelope.Header{
			SchemaVersion: envelope.SVPolicyDecision,
			ID:            "pol_session_end_used",
			CreatedAt:     cliTestNow.Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "policy", Name: "compound-test"},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		ActionIntentID: "act_session_end_used",
		Outcome:        "allow_with_obligations",
		Reasons:        []string{"test action is local and reversible"},
		Obligations:    []string{"emit receipt"},
	}
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

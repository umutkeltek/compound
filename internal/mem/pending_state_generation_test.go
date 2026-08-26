package mem

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"compound/internal/candidates"
)

func TestPendingStateGenerationDetectsSameCountCorpusReplacement(t *testing.T) {
	home := t.TempDir()
	store, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	write := func(id string) {
		t.Helper()
		candidate := Candidate{ID: id, Entity: id, Predicate: "value", Object: id, State: "proposed", Visibility: "operational", Derivation: "extracted", CreatedAt: "2026-08-26T00:00:00Z"}
		if err := candidates.WriteCandidate(home, candidates.Memory, id, &candidate); err != nil {
			t.Fatal(err)
		}
	}
	write("cand_a")
	write("cand_b")
	state, err := store.ReconcilePendingState("test", t0)
	if err != nil {
		t.Fatal(err)
	}
	if state.Files != 2 || state.Generation == "" {
		t.Fatalf("state = %+v, want two files and generation", state)
	}

	if err := os.Remove(filepath.Join(store.candDir(), "cand_a.json")); err != nil {
		t.Fatal(err)
	}
	write("cand_c")
	names, generation, _, err := store.candidateInboxMetadata()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || generation == state.Generation {
		t.Fatalf("replacement generation did not move: names=%v before=%s after=%s", names, state.Generation, generation)
	}
	if pendingStateFresh(state, len(names), generation, t0.Add(time.Minute)) {
		t.Fatal("same file count with a different candidate set must be stale")
	}
}

func TestPendingMetadataIgnoresNonCandidateEntries(t *testing.T) {
	home := t.TempDir()
	store, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	candidate := Candidate{ID: "cand_a", Entity: "a", Predicate: "value", Object: "a", State: "proposed", Visibility: "operational", Derivation: "extracted", CreatedAt: "2026-08-26T00:00:00Z"}
	if err := candidates.WriteCandidate(home, candidates.Memory, candidate.ID, &candidate); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.candDir(), "scratch.tmp"), []byte("not a candidate"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(store.candDir(), "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	names, _, _, err := store.candidateInboxMetadata()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "cand_a.json" {
		t.Fatalf("candidate names = %v, want only cand_a.json", names)
	}
}

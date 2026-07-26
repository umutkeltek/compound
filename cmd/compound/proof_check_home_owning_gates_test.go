package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestHomeOwningGatesAreAllRealGates keeps the declared set and the gate list from drifting
// apart. A renamed or retired gate would otherwise leave a dangling runner that never fires —
// the gate would quietly fall through to the generic path and read the operator's home again.
func TestHomeOwningGatesAreAllRealGates(t *testing.T) {
	steps, err := proofCheckSteps("")
	if err != nil {
		t.Fatal(err)
	}
	names := make(map[string]bool, len(steps))
	for _, s := range steps {
		names[s.Name] = true
	}
	for name := range proofCheckHomeOwningGates {
		if !names[name] {
			t.Fatalf("proofCheckHomeOwningGates names %q, which is not a gate in proofCheckSteps — the runner is dead code and that gate now inherits the live home", name)
		}
	}
}

// TestHomeOwningGatesIgnoreTheAmbientHome is the WIRING proof, and it is separate from the
// per-gate tests on purpose: those call their runner directly, so deleting a gate's entry in
// the dispatch map leaves every one of them green while the gate silently returns to reading
// COMPOUND_HOME. This drives the real runProofCheckStep and points COMPOUND_HOME at a path
// that does not exist — a gate that reads it fails, a gate that creates its own does not.
//
// Covers the two gates whose command carries an explicit binary path. The console gates
// resolve the compound binary from os.Executable(), which under `go test` is the test binary,
// so they cannot be driven end-to-end from here; their entry in the map is covered by the
// membership test above and their behavior by their own files.
func TestHomeOwningGatesIgnoreTheAmbientHome(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the compound binary")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Clean(filepath.Join(wd, "..", ".."))
	exe := compoundTestBinary(t)
	t.Setenv("COMPOUND_HOME", filepath.Join(t.TempDir(), "no-such-home"))

	for _, tc := range []struct {
		name    string
		command []string
	}{
		{"memory-doctor", []string{exe, "memory", "doctor", "--json"}},
		{"workbench-projection", []string{exe, "workbench", "--json"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, owns := proofCheckHomeOwningGates[tc.name]; !owns {
				t.Fatalf("%s is no longer declared home-owning", tc.name)
			}
			got := runProofCheckStep(repo, proofCheckStep{Name: tc.name, Command: tc.command, ProofRef: tc.name}, "")
			if got.Status != "ok" {
				t.Fatalf("%s = %s (exit %d) with an absent ambient COMPOUND_HOME — it read the environment instead of seeding its own home:\n%s",
					tc.name, got.Status, got.ExitCode, got.OutputTail)
			}
		})
	}
}

//go:build ignore

// S2 RunAttempt tests — these test fields and methods (ContextPackRef,
// Workspace, Command, Heartbeat, TranscriptRef, DiffRef, TestRef, ProofRefs)
// that S1 deliberately omits. Remove the //go:build ignore tag above when S2
// lands the corresponding setters on Store and fields on RunAttempt.
// See id.go package doc: "S1 deliberately omits context_pack_ref / workspace /
// command / heartbeat / transcript_ref / diff_ref / test_ref / proof_refs".

package runattempt

import (
	"testing"
	"time"
)

// Mint -> setter -> Get without loss. Empty defaults on a fresh Mint; set
// the field; re-Get; assert.
func TestRunAttemptRoundTripsNewFields(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	ra, err := s.Mint("work_abc", "compound", HarnessPlaceholder, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		do   func() error
		want func(RunAttempt) bool
	}{
		{"context_pack", func() error { _, e := s.SetContextPack(ra.ID, "ctx_pack_42"); return e },
			func(got RunAttempt) bool { return got.ContextPackRef == "ctx_pack_42" }},
		{"workspace", func() error { _, e := s.SetWorkspace(ra.ID, "/repos/compound"); return e },
			func(got RunAttempt) bool { return got.Workspace == "/repos/compound" }},
		{"command", func() error { _, e := s.SetCommand(ra.ID, "compound panel apply"); return e },
			func(got RunAttempt) bool { return got.Command == "compound panel apply" }},
		{"heartbeat", func() error { _, e := s.Heartbeat(ra.ID, now.Add(30*time.Second)); return e },
			func(got RunAttempt) bool { return got.LastHeartbeatAt.Equal(now.Add(30 * time.Second)) }},
		{"transcript", func() error { _, e := s.SetTranscript(ra.ID, "panel-runs/prun_abc/run_record.json"); return e },
			func(got RunAttempt) bool { return got.TranscriptRef == "panel-runs/prun_abc/run_record.json" }},
		{"diff", func() error { _, e := s.SetDiff(ra.ID, "diff/run_abc.patch"); return e },
			func(got RunAttempt) bool { return got.DiffRef == "diff/run_abc.patch" }},
		{"test", func() error { _, e := s.SetTest(ra.ID, "tests/run_abc.junit.xml"); return e },
			func(got RunAttempt) bool { return got.TestRef == "tests/run_abc.junit.xml" }},
		{"proof_refs", func() error { _, e := s.SetProofRefs(ra.ID, []string{"receipt_abc", "transition_abc"}); return e },
			func(got RunAttempt) bool {
				return len(got.ProofRefs) == 2 && got.ProofRefs[0] == "receipt_abc" && got.ProofRefs[1] == "transition_abc"
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.do(); err != nil {
				t.Fatalf("%s setter: %v", tc.name, err)
			}
			got, err := Open(home).Get(ra.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !tc.want(got) {
				t.Fatalf("%s did not round-trip, got: %+v", tc.name, got)
			}
		})
	}
}

// TestRunAttemptSettersRefuseOnTerminal pins the audit invariant: a
// terminalized RunAttempt (succeeded|failed) cannot be retroactively
// rewritten. Every S2 setter must reject a terminal RunAttempt with an
// error so a failed close cannot later sprout fresh evidence.
func TestRunAttemptSettersRefuseOnTerminal(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	ra, err := s.Mint("work_abc", "compound", HarnessPlaceholder, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Complete(ra.ID, StatusSucceeded, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Heartbeat on a terminal RunAttempt is also refused (it would imply the
	// attempt is still running, contradicting the terminal status).
	setters := []struct {
		name string
		fn   func() error
	}{
		{"context_pack", func() error { _, e := s.SetContextPack(ra.ID, "ctx_x"); return e }},
		{"workspace", func() error { _, e := s.SetWorkspace(ra.ID, "/x"); return e }},
		{"command", func() error { _, e := s.SetCommand(ra.ID, "x"); return e }},
		{"heartbeat", func() error { _, e := s.Heartbeat(ra.ID, now.Add(2*time.Minute)); return e }},
		{"transcript", func() error { _, e := s.SetTranscript(ra.ID, "x"); return e }},
		{"diff", func() error { _, e := s.SetDiff(ra.ID, "x"); return e }},
		{"test", func() error { _, e := s.SetTest(ra.ID, "x"); return e }},
		{"proof_refs", func() error { _, e := s.SetProofRefs(ra.ID, []string{"x"}); return e }},
	}
	for _, tc := range setters {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.fn(); err == nil {
				t.Fatalf("setter on terminal RunAttempt must fail")
			}
		})
	}
}

// TestRunAttemptSetProofRefsClearsOnEmpty proves SetProofRefs("") clears
// existing refs rather than no-op-ing. Useful when a harness attaches the
// wrong refs by mistake and wants to retract (before terminal only).
func TestRunAttemptSetProofRefsClearsOnEmpty(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	ra, err := s.Mint("work_abc", "compound", HarnessPlaceholder, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetProofRefs(ra.ID, []string{"a", "b", "c"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetProofRefs(ra.ID, nil); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ra.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ProofRefs) != 0 {
		t.Fatalf("SetProofRefs(nil) must clear refs, got %v", got.ProofRefs)
	}
}

// TestRunAttemptHeartbeatAdvancesOnlyForward pins heartbeat semantics:
// each Heartbeat must advance LastHeartbeatAt; a stale heartbeat (now <=
// current) is a no-op. The audit invariant is that heartbeats reflect the
// latest activity, not the earliest call.
func TestRunAttemptHeartbeatAdvancesOnlyForward(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	t0 := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	ra, err := s.Mint("work_abc", "compound", HarnessPlaceholder, t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Heartbeat(ra.ID, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	// Stale heartbeat: must NOT move LastHeartbeatAt backwards.
	if _, err := s.Heartbeat(ra.ID, t0.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(ra.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.LastHeartbeatAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("stale heartbeat regressed timestamp: got %v, want %v", got.LastHeartbeatAt, t0.Add(time.Minute))
	}
	// Forward heartbeat: must advance.
	if _, err := s.Heartbeat(ra.ID, t0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ra.ID)
	if !got.LastHeartbeatAt.Equal(t0.Add(2 * time.Minute)) {
		t.Fatalf("forward heartbeat did not advance: got %v, want %v", got.LastHeartbeatAt, t0.Add(2*time.Minute))
	}
}

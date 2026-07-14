package main

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"compound/internal/finding"
)

// compound doctor composes existing organ states into one read-only surface. This proves the
// composition end-to-end: an empty home reports hooks-absent + an install hint, lease-free, zero
// findings, and the fail-open placement default; recording a finding plus a superseding revision
// moves the live/superseded counts. It writes nothing.
func TestDoctorComposesOrganState(t *testing.T) {
	home := t.TempDir()
	base := t.TempDir() // empty base => no harness hooks installed

	var out, errOut bytes.Buffer
	args := []string{"--home", home, "--base", base, "--binary", "/usr/local/bin/compound", "--json"}
	if code := runDoctor(args, &out, &errOut); code != 0 {
		t.Fatalf("doctor should succeed, code=%d stderr=%q", code, errOut.String())
	}
	var rep doctorReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("parse doctor json: %v (%q)", err, out.String())
	}
	if len(rep.Hooks) == 0 {
		t.Fatal("doctor should report per-harness hook status")
	}
	for _, h := range rep.Hooks {
		if h.Status != "absent" {
			t.Fatalf("empty base => hook %s should be absent, got %q", h.Harness, h.Status)
		}
	}
	if rep.HookHint == "" {
		t.Fatal("absent hooks should yield an install hint")
	}
	if rep.LeaseHeld {
		t.Fatal("no lease should be held")
	}
	// policy.DefaultConfig and an empty-home policy.LoadConfig use observe. The block assertion in
	// TestLoadConfigOverlaysDefaultPolicy follows an explicit policy.yaml fixture; it is not the
	// built-in default. Doctor must report the loaded mode and make fail-open staging explicit.
	if rep.PlacementMode != "observe" {
		t.Fatalf("empty-home placement mode should be observe, got %q", rep.PlacementMode)
	}
	if rep.PlacementNote == "" {
		t.Fatal("observe mode should carry an explicit fail-open warning")
	}
	if rep.FindingsTotal != 0 {
		t.Fatalf("no findings expected, got %d", rep.FindingsTotal)
	}

	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	f1, err := finding.Record(home, finding.Finding{Topic: "deploy-runbook", Body: "v1 steps"}, now)
	if err != nil {
		t.Fatalf("record f1: %v", err)
	}
	if _, err := finding.Record(home, finding.Finding{Topic: "deploy-runbook", Body: "v2 steps", Supersedes: f1.ID}, now.Add(time.Minute)); err != nil {
		t.Fatalf("record f2: %v", err)
	}

	out.Reset()
	errOut.Reset()
	if code := runDoctor(args, &out, &errOut); code != 0 {
		t.Fatalf("doctor rerun failed: %s", errOut.String())
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("parse doctor json (rerun): %v", err)
	}
	if rep.FindingsTotal != 2 || rep.FindingsLive != 1 || rep.FindingsSuperseded != 1 {
		t.Fatalf("findings counts wrong: total=%d live=%d superseded=%d", rep.FindingsTotal, rep.FindingsLive, rep.FindingsSuperseded)
	}
}

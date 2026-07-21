package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/work"
)

func captureCoordination(t *testing.T, args []string) string {
	t.Helper()
	var buf strings.Builder
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	cmdCoordination(args)
	w.Close()
	chunk := make([]byte, 4096)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			buf.Write(chunk[:n])
		}
		if err != nil {
			break
		}
	}
	os.Stdout = old
	return buf.String()
}

func coordinationCLIHome(t *testing.T) string {
	t.Helper()
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("home init: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	w, err := store.Create(work.CreateInput{Owner: "lane-x", DesiredOutcome: "ship the parser", SuccessPredicate: "parser lands"}, cliTestNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := store.Claim(w.ID, "lane-x", cliTestNow.Add(time.Minute)); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: "running", Actor: "lane-x", Note: "start"}, cliTestNow.Add(2*time.Minute)); err != nil {
		t.Fatalf("advance: %v", err)
	}
	return w.ID
}

// The console contract: `coordination --json` emits compound.coordination.v0.1
// with me / others / waits, generic actors, no hardcoded identities.
func TestCoordinationCLIJSONContract(t *testing.T) {
	workID := coordinationCLIHome(t)

	out := captureCoordination(t, []string{"--json"})
	var report work.CoordinationReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, out)
	}
	if report.SchemaVersion != work.CoordinationSchemaVersion {
		t.Fatalf("schema_version = %q", report.SchemaVersion)
	}
	if report.Me.Actor != "lane-x" { // most-present actor, never a hardcoded name
		t.Fatalf("me.actor = %q, want lane-x (doing the running work)", report.Me.Actor)
	}
	if report.Me.Doing == nil || report.Me.Doing.WorkID != workID {
		t.Fatalf("me.doing = %+v, want %s", report.Me.Doing, workID)
	}
	if report.Me.CanContinue {
		t.Fatalf("me.can_continue = true; running work needs run evidence")
	}
	if report.Me.WaitReason != "run_evidence" {
		t.Fatalf("me.wait_reason = %q", report.Me.WaitReason)
	}
	if len(report.Waits) == 0 {
		t.Fatalf("waits empty; lane-x is blocked on %s", workID)
	}
}

// `--actor` repoints "me" without changing the projection's honesty: an
// explicit actor with no presence gets an empty face, never a re-target.
func TestCoordinationCLIExplicitActor(t *testing.T) {
	coordinationCLIHome(t)
	out := captureCoordination(t, []string{"--json", "--actor", "lane-y"})
	var report work.CoordinationReport
	if err := json.Unmarshal([]byte(out), &report); err != nil {
		t.Fatalf("unmarshal: %v\nraw: %s", err, out)
	}
	if report.Actor != "lane-y" || !report.ActorExplicit {
		t.Fatalf("actor = %q explicit=%v", report.Actor, report.ActorExplicit)
	}
	if report.Me.Doing != nil {
		t.Fatalf("lane-y has no live work; me.doing = %+v", report.Me.Doing)
	}
	var sawLaneX bool
	for _, other := range report.Others {
		if other.Actor == "lane-x" {
			sawLaneX = true
		}
	}
	if !sawLaneX {
		t.Fatalf("lane-x dropped from others: %+v", report.Others)
	}
}

// Human output keeps the coordination vocabulary: Me / Others / Waiting-on.
func TestCoordinationCLIHumanSections(t *testing.T) {
	coordinationCLIHome(t)
	out := captureCoordination(t, []string{})
	for _, want := range []string{"ME", "OTHERS", "WAITING-ON", "lane-x", "run_evidence"} {
		if !strings.Contains(out, want) {
			t.Fatalf("human output missing %q:\n%s", want, out)
		}
	}
}

func TestCoordinationCLIUsage(t *testing.T) {
	out := captureCoordination(t, []string{"--help"})
	if !strings.Contains(out, "usage: compound coordination") {
		t.Fatalf("usage output = %q", out)
	}
}

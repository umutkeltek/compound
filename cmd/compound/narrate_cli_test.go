package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/home"
	"compound/internal/narrate"
	"compound/internal/work"
)

// seedNarrateHome builds a home with one held (needs_you) and one ready work
// item so attention produces a real board for narrate to compose over.
func seedNarrateHome(t *testing.T) string {
	t.Helper()
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work store: %v", err)
	}
	if _, err := store.Create(work.CreateInput{
		WorkID: "work_hold_narrate", Owner: "compound-core", State: "held",
		SuccessPredicate: "held work waits for an operator decision",
	}, cliTestNow); err != nil {
		t.Fatalf("create held work: %v", err)
	}
	if _, err := store.Create(work.CreateInput{
		WorkID: "work_ready_narrate", Owner: "compound-core", State: "ready",
		SuccessPredicate: "safe local work can be claimed",
	}, cliTestNow); err != nil {
		t.Fatalf("create ready work: %v", err)
	}
	return homePath
}

// The CLI emits a valid script, leaves the orient cursor untouched (read-only),
// and persists only its own emphasis sidecar.
func TestNarrateCLIEmitsScriptAndLeavesOrientCursorUntouched(t *testing.T) {
	homePath := seedNarrateHome(t)
	cursorPath := filepath.Join(homePath, "projections", "orient-operator-last-seq.json")
	sidecarPath := filepath.Join(homePath, "projections", "narrate-thread-seen.json")

	if _, err := os.Stat(cursorPath); !os.IsNotExist(err) {
		t.Fatalf("orient cursor should not exist on a fresh home before narrate: %v", err)
	}

	output := captureStdout(t, func() { cmdNarrate([]string{"--json", "--home", homePath}) })
	var script narrate.Script
	if err := json.Unmarshal([]byte(output), &script); err != nil {
		t.Fatalf("parse narrate JSON: %v\n%s", err, output)
	}
	if script.SchemaVersion != narrate.SchemaVersion {
		t.Fatalf("schema version = %q, want %q", script.SchemaVersion, narrate.SchemaVersion)
	}
	if script.Counts.Narrated+script.Counts.Folded != script.Counts.TotalMembers {
		t.Fatalf("conservation broke through the CLI: %+v", script.Counts)
	}
	if script.ScriptHash == "" {
		t.Fatalf("script_hash missing from CLI output")
	}

	// narrate must NOT advance (or even create) the orient operator cursor.
	if _, err := os.Stat(cursorPath); !os.IsNotExist(err) {
		t.Fatalf("narrate advanced/created the orient cursor (spec §7 forbids it): %v", err)
	}
	// narrate persists its own emphasis sidecar.
	if _, err := os.Stat(sidecarPath); err != nil {
		t.Fatalf("narrate did not persist its thread-seen sidecar: %v", err)
	}
}

// --text renders a felt read: voiced beat lines and a trailing conservation line.
func TestNarrateCLITextRendersConservationLine(t *testing.T) {
	homePath := seedNarrateHome(t)
	out := captureStdout(t, func() { cmdNarrate([]string{"--text", "--home", homePath}) })
	if !strings.Contains(out, "narrated ") || !strings.Contains(out, "· folded ") {
		t.Fatalf("text render missing the conservation line:\n%s", out)
	}
	if !strings.Contains(out, "[") {
		t.Fatalf("text render produced no voiced beat line:\n%s", out)
	}
	t.Logf("narrate --text:\n%s", out)
}

// The same served script is embedded in orient --operator (one builder).
func TestOrientOperatorEmbedsNarrateScript(t *testing.T) {
	homePath := seedNarrateHome(t)
	output := captureStdout(t, func() { cmdOrientOperator(homePath, []string{"--operator", "--json"}) })
	var report struct {
		SchemaVersion string          `json:"schema_version"`
		Narrate       *narrate.Script `json:"narrate"`
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("parse orient --operator JSON: %v\n%s", err, output)
	}
	if report.Narrate == nil {
		t.Fatalf("orient --operator did not embed a narrate script:\n%s", output)
	}
	if report.Narrate.SchemaVersion != narrate.SchemaVersion {
		t.Fatalf("embedded narrate schema = %q, want %q", report.Narrate.SchemaVersion, narrate.SchemaVersion)
	}
	if report.Narrate.ScriptHash == "" {
		t.Fatalf("embedded narrate script missing script_hash")
	}
}

// --mood re-weights but never changes membership or warn presence, end to end.
func TestNarrateCLIMoodReweightsFixedMembership(t *testing.T) {
	homePath := seedNarrateHome(t)
	run := func(mood string) narrate.Script {
		out := captureStdout(t, func() { cmdNarrate([]string{"--json", "--mood", mood, "--home", homePath}) })
		var s narrate.Script
		if err := json.Unmarshal([]byte(out), &s); err != nil {
			t.Fatalf("parse narrate JSON (%s): %v\n%s", mood, err, out)
		}
		return s
	}
	members := func(s narrate.Script) map[string]bool {
		m := map[string]bool{}
		for _, b := range s.Beats {
			m[b.ThreadID] = true
		}
		for _, g := range s.Standing.Groups {
			m[g.ThreadID] = true
		}
		return m
	}
	d := run("decide")
	y := run("worry")
	md, my := members(d), members(y)
	if len(md) != len(my) {
		t.Fatalf("mood changed membership size: decide=%d worry=%d", len(md), len(my))
	}
	for k := range md {
		if !my[k] {
			t.Fatalf("mood dropped member %q", k)
		}
	}
}

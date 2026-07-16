package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/activity"
	"compound/internal/home"
)

// F2 at the CLI door: a fresh home (sensor disabled, no grant) must refuse `activity drain` with a
// non-zero exit, an honest reason, and ZERO rows under observed/activity/.
func TestActivityCLIDrainFailsClosedOnFreshHome(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	spool := t.TempDir()
	if err := os.WriteFile(filepath.Join(spool, "2026-07-15.ndjson"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("seed spool: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runActivity([]string{"drain", "--spool", spool, "--home", homePath}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("drain on a fresh home must exit non-zero, got 0 (stdout=%q)", stdout.String())
	}
	if !strings.Contains(stderr.String(), "refused") {
		t.Fatalf("refusal must carry an honest reason, got %q", stderr.String())
	}
	if entries, err := os.ReadDir(activity.SegmentsRoot(homePath)); err == nil && len(entries) > 0 {
		t.Fatalf("fail-closed drain must write zero segment rows, found %d entries", len(entries))
	}
}

// The status verb is ungated read-only truth: it must report enabled=false on a fresh home and
// exit zero.
func TestActivityCLIStatusReportsDisabled(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if code := runActivity([]string{"status", "--home", homePath}, &stdout, &stderr); code != 0 {
		t.Fatalf("status must exit zero, got %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "enabled=false") {
		t.Fatalf("fresh home must report enabled=false, got %q", stdout.String())
	}
}

// An unknown verb or bare call prints usage and exits 2 — the operator door never guesses.
func TestActivityCLIUnknownVerbPrintsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := runActivity([]string{"bogus"}, &stdout, &stderr); code != 2 {
		t.Fatalf("unknown verb must exit 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("unknown verb must print usage, got %q", stderr.String())
	}
}

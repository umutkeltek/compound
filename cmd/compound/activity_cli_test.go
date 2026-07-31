package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

// aw materialize accepts --start/--end, so a malformed value must NOT be
// silently swallowed (the bug being fixed): it must exit 2 with a clear reason.
func TestActivityCLIAWMaterializeInvalidStartExits2(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := runActivity([]string{"aw", "materialize", "--start", "not-a-time", "--home", homePath}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("invalid --start must exit 2, got %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "invalid --start") {
		t.Fatalf("error must name --start, got %q", stderr.String())
	}
}

// A window where --start is not before --end is a usage error, exit 2.
func TestActivityCLIAWMaterializeInvertedWindowExits2(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := runActivity([]string{"aw", "materialize",
		"--start", "2026-07-31T00:00:00Z",
		"--end", "2026-07-30T00:00:00Z",
		"--home", homePath}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("inverted window must exit 2, got %d (stderr=%q)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "must be before") {
		t.Fatalf("error must explain the inverted window, got %q", stderr.String())
	}
}

// Direct unit coverage of the window resolver: defaults, explicit bounds, and
// the "--flag=value" form (which valueFlag alone would silently drop).
func TestParseAWMaterializeWindow(t *testing.T) {
	now := time.Date(2026, 7, 31, 12, 0, 0, 0, time.UTC)
	var stderr bytes.Buffer

	// Default: trailing 7 days ending now.
	start, end, ok := parseAWMaterializeWindow(nil, now, &stderr)
	if !ok {
		t.Fatalf("default window must parse, stderr=%q", stderr.String())
	}
	if !end.Equal(now) || !start.Equal(now.Add(-7*24*time.Hour)) {
		t.Fatalf("default window = [%s, %s], want trailing 7d", start, end)
	}

	// Explicit space-form bounds.
	start, end, ok = parseAWMaterializeWindow([]string{
		"--start", "2026-07-30T00:00:00Z",
		"--end", "2026-07-31T00:00:00Z",
	}, now, &stderr)
	if !ok {
		t.Fatalf("explicit window must parse, stderr=%q", stderr.String())
	}
	if start.Format(time.RFC3339) != "2026-07-30T00:00:00Z" || end.Format(time.RFC3339) != "2026-07-31T00:00:00Z" {
		t.Fatalf("explicit window = [%s, %s]", start, end)
	}

	// Equals-form bound must be honored, not dropped.
	start, _, ok = parseAWMaterializeWindow([]string{"--start=2026-07-29T00:00:00Z"}, now, &stderr)
	if !ok {
		t.Fatalf("equals-form window must parse, stderr=%q", stderr.String())
	}
	if start.Format(time.RFC3339) != "2026-07-29T00:00:00Z" {
		t.Fatalf("equals-form start = %s, want 2026-07-29T00:00:00Z", start)
	}

	// Malformed end is rejected.
	if _, _, ok = parseAWMaterializeWindow([]string{"--end", "nope"}, now, &stderr); ok {
		t.Fatalf("malformed --end must not parse")
	}
}

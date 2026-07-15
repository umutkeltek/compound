package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runEventStoreCLI(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := runEventStore(args, &out, &errb)
	return out.String(), errb.String(), code
}

func seedLooseCLIRecord(t *testing.T, homePath, id, body string) {
	t.Helper()
	eventsDir := filepath.Join(homePath, "events")
	if err := os.MkdirAll(eventsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := `{"schema_version":"compound.receipt.v0.1","id":"` + id + `","created_at":"2026-07-15T00:00:00Z","value":"` + body + `"}` + "\n"
	if err := os.WriteFile(filepath.Join(eventsDir, id+".json"), []byte(rec), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestEventStoreMigrateVerbEndToEnd drives the wired CLI verb across all three
// modes on one seeded home: dry-run must not mutate, apply must close the gate
// and archive originals, verify must confirm and exit 0.
func TestEventStoreMigrateVerbEndToEnd(t *testing.T) {
	homePath := t.TempDir()
	for _, id := range []string{"rec_cli_a", "rec_cli_b"} {
		seedLooseCLIRecord(t, homePath, id, "body-"+id)
	}

	out, errStr, code := runEventStoreCLI(t, "migrate", "--dry-run", "--home", homePath)
	if code != 0 {
		t.Fatalf("dry-run exit=%d stderr=%s", code, errStr)
	}
	if !strings.Contains(out, "would_migrate: 2") {
		t.Fatalf("dry-run did not project 2 migrations:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(homePath, "events", "migration", "plan.json")); !os.IsNotExist(err) {
		t.Fatalf("dry-run wrote a plan (stat err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(homePath, "events", "rec_cli_a.json")); err != nil {
		t.Fatalf("dry-run archived a record: %v", err)
	}

	out, errStr, code = runEventStoreCLI(t, "migrate", "--apply", "--home", homePath)
	if code != 0 {
		t.Fatalf("apply exit=%d stderr=%s\n%s", code, errStr, out)
	}
	if !strings.Contains(out, "complete: true") || !strings.Contains(out, "migrated: 2") {
		t.Fatalf("apply did not close the gate:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(homePath, "events", "migration", "completion.json")); err != nil {
		t.Fatalf("apply did not write completion.json: %v", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(homePath, "events", "*.json")); len(matches) != 0 {
		t.Fatalf("apply left loose records: %v", matches)
	}

	_, errStr, code = runEventStoreCLI(t, "migrate", "--verify", "--home", homePath)
	if code != 0 {
		t.Fatalf("verify exit=%d stderr=%s", code, errStr)
	}
}

// TestEventStoreMigrateVerifyFlagsLegacyWriter proves the post-completion status
// check exits non-zero and names the degraded state when a loose file reappears.
func TestEventStoreMigrateVerifyFlagsLegacyWriter(t *testing.T) {
	homePath := t.TempDir()
	seedLooseCLIRecord(t, homePath, "rec_cli_x", "x")
	if _, _, code := runEventStoreCLI(t, "migrate", "--apply", "--home", homePath); code != 0 {
		t.Fatalf("apply exit=%d", code)
	}
	seedLooseCLIRecord(t, homePath, "rec_cli_late", "late")

	out, _, code := runEventStoreCLI(t, "migrate", "--verify", "--home", homePath)
	if code == 0 {
		t.Fatalf("verify exited 0 despite a reappearing legacy writer:\n%s", out)
	}
	if !strings.Contains(out, "degraded:legacy_writer_active") {
		t.Fatalf("verify did not surface degraded:legacy_writer_active:\n%s", out)
	}
}

func TestEventStoreMigrateRequiresExactlyOneMode(t *testing.T) {
	homePath := t.TempDir()
	if _, _, code := runEventStoreCLI(t, "migrate", "--home", homePath); code != 2 {
		t.Fatalf("missing mode exit=%d, want 2", code)
	}
	if _, _, code := runEventStoreCLI(t, "migrate", "--apply", "--verify", "--home", homePath); code != 2 {
		t.Fatalf("two modes exit=%d, want 2", code)
	}
	if _, _, code := runEventStoreCLI(t, "unknown-sub"); code != 2 {
		t.Fatalf("unknown subcommand exit=%d, want 2", code)
	}
}

// TestEventStoreMigrateUsageLineIsRegisteredAtTopLevel mirrors the capture/act
// verbs: a new top-level verb must be discoverable in the top-level help text.
func TestEventStoreMigrateUsageLineIsRegisteredAtTopLevel(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repoRoot, "cmd", "compound", "main.go"))
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if !strings.Contains(string(data), "compound event-store migrate ") {
		t.Fatalf("top-level usage() must document `compound event-store migrate`")
	}
}

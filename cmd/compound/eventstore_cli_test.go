package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"compound/internal/policy"
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
	if !strings.Contains(string(data), "compound event-store rebaseline ") {
		t.Fatalf("top-level usage() must document `compound event-store rebaseline`")
	}
}

// FALSIFIER (refusal floor): rebaseline refuses outright — in BOTH preview and
// --confirm — when the home never completed migration; there is nothing to
// re-baseline yet, and no sidecar is written.
func TestEventStoreRebaselineRefusesWhenMigrationIncomplete(t *testing.T) {
	homePath := t.TempDir()

	out, errStr, code := runEventStoreCLI(t, "rebaseline", "--home", homePath)
	if code == 0 {
		t.Fatalf("expected non-zero exit with no completed migration; stdout=%q", out)
	}
	if !strings.Contains(errStr, "migration is not complete") {
		t.Fatalf("expected a migration-incomplete message, got stderr=%q", errStr)
	}
	if _, err := os.Stat(filepath.Join(homePath, "events", "migration", "stream-baseline.json")); !os.IsNotExist(err) {
		t.Fatalf("no sidecar should exist: stat err=%v", err)
	}

	_, errStr, code = runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_incomplete", "--home", homePath)
	if code == 0 {
		t.Fatalf("expected non-zero exit on --confirm with no completed migration")
	}
	if !strings.Contains(errStr, "migration is not complete") {
		t.Fatalf("expected a migration-incomplete message on confirm, got stderr=%q", errStr)
	}
}

// FALSIFIER F6 (preview render): the dry-run default writes nothing and
// projects current -> proposed sizes for each frozen stream.
func TestEventStoreRebaselineDryRunWritesNothing(t *testing.T) {
	homePath := t.TempDir()
	seedLooseCLIRecord(t, homePath, "rec_rb_dry", "x")
	streamPath := filepath.Join(homePath, "events", "hook-events.ndjson")
	if err := os.MkdirAll(filepath.Dir(streamPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(streamPath, []byte(`{"id":"pre"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, code := runEventStoreCLI(t, "migrate", "--apply", "--home", homePath); code != 0 {
		t.Fatalf("apply exit=%d", code)
	}
	sidecarPath := filepath.Join(homePath, "events", "migration", "stream-baseline.json")
	before, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("expected the auto-written sidecar after apply: %v", err)
	}

	out, errStr, code := runEventStoreCLI(t, "rebaseline", "--home", homePath)
	if code != 0 {
		t.Fatalf("dry-run should succeed, got code=%d stderr=%q", code, errStr)
	}
	if !strings.Contains(out, "dry-run") {
		t.Fatalf("dry-run output should say so, got %q", out)
	}
	if !strings.Contains(out, "proposed=") {
		t.Fatalf("dry-run should project proposed sizes, got %q", out)
	}
	after, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("dry-run must not write the sidecar")
	}
}

// FALSIFIER F4 (governance floor): --confirm WITHOUT an allowing PolicyDecision
// refuses and leaves the sidecar byte-identical.
func TestEventStoreRebaselineRefusesWithoutGrant(t *testing.T) {
	homePath := t.TempDir()
	seedLooseCLIRecord(t, homePath, "rec_rb_nogrant", "x")
	if _, _, code := runEventStoreCLI(t, "migrate", "--apply", "--home", homePath); code != 0 {
		t.Fatalf("apply exit=%d", code)
	}
	sidecarPath := filepath.Join(homePath, "events", "migration", "stream-baseline.json")
	before, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(homePath, "events", "migration", ".rebaseline.lock")
	if err := os.Remove(lockPath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	homeBefore := snapshotHomeFiles(t, homePath)

	out, errStr, code := runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_nogrant", "--home", homePath)
	if code == 0 {
		t.Fatalf("expected non-zero exit with no allowing PolicyDecision; stdout=%q", out)
	}
	if !strings.Contains(errStr, "refused") {
		t.Fatalf("expected a refusal message, got stderr=%q", errStr)
	}
	after, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("sidecar mutated despite refusal")
	}
	if entries, err := os.ReadDir(filepath.Join(homePath, "artifacts", "sha256")); err == nil && len(entries) != 0 {
		t.Fatalf("refused confirm wrote artifact bodies: %v", entries)
	} else if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(homePath, "events", "migration", "rebaseline-pending.json")); !os.IsNotExist(err) {
		t.Fatalf("refused confirm wrote pending journal: %v", err)
	}
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("refused confirm created rebaseline lock: %v", err)
	}
	if homeAfter := snapshotHomeFiles(t, homePath); !reflect.DeepEqual(homeAfter, homeBefore) {
		t.Fatalf("refused confirm changed home:\nbefore=%v\nafter=%v", homeBefore, homeAfter)
	}
}

func snapshotHomeFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		out[rel] = string(body)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

// --confirm requires --work (a governed act belongs to a work), mirroring `compound act`.
func TestEventStoreRebaselineConfirmRequiresWork(t *testing.T) {
	homePath := t.TempDir()
	seedLooseCLIRecord(t, homePath, "rec_rb_nowork", "x")
	if _, _, code := runEventStoreCLI(t, "migrate", "--apply", "--home", homePath); code != 0 {
		t.Fatalf("apply exit=%d", code)
	}
	if _, _, code := runEventStoreCLI(t, "rebaseline", "--confirm", "--home", homePath); code != 2 {
		t.Fatalf("expected exit=2 without --work, got %d", code)
	}
}

// FALSIFIERS F2 (re-baseline path) + F6 (confirm render): a completed home
// whose hook-events.ndjson legitimately grew reports degraded; a GRANTED
// --confirm performs the governed write, records a resolvable receipt +
// transition, and `migrate --verify` reports ok again afterward.
func TestEventStoreRebaselineWritesWithGrantAndClosesTheGateAgain(t *testing.T) {
	homePath := t.TempDir()
	seedLooseCLIRecord(t, homePath, "rec_rb_grant", "x")
	streamPath := filepath.Join(homePath, "events", "hook-events.ndjson")
	if err := os.MkdirAll(filepath.Dir(streamPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(streamPath, []byte(`{"id":"pre"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, code := runEventStoreCLI(t, "migrate", "--apply", "--home", homePath); code != 0 {
		t.Fatalf("apply exit=%d", code)
	}

	// Legitimate growth AFTER completion (the gap this whole packet closes).
	f, err := os.OpenFile(streamPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"id":"grew"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if out, _, code := runEventStoreCLI(t, "migrate", "--verify", "--home", homePath); code == 0 {
		t.Fatalf("expected verify to report degraded before rebaseline:\n%s", out)
	}

	sidecarPath := filepath.Join(homePath, "events", "migration", "stream-baseline.json")
	grantSrc := filepath.Join(homePath, "rebaseline-grant.yaml")
	raw := "generated_at: \"2026-07-16T12:00:00Z\"\n" +
		"grants:\n" +
		"  - grant_id: grant_rebaseline\n" +
		"    capabilities: [\"" + eventStoreRebaselineCapability + "\"]\n" +
		"    target_scope: \"" + sidecarPath + "\"\n" +
		"    mutation_class: external_side_effect\n" +
		"    reversibility_ceiling: reversible\n" +
		"    blast_radius: local\n"
	if err := os.WriteFile(grantSrc, []byte(raw), 0o644); err != nil {
		t.Fatalf("write grant source: %v", err)
	}
	if _, err := policy.ImportStandingGrants(homePath, grantSrc, time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("import standing grants: %v", err)
	}

	out, errStr, code := runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_rebaseline", "--home", homePath, "--json")
	if code != 0 {
		t.Fatalf("granted confirm should succeed, got code=%d stderr=%q\n%s", code, errStr, out)
	}
	var result struct {
		ReceiptID      string               `json:"receipt_id"`
		TransitionID   string               `json:"transition_id"`
		PolicySource   string               `json:"policy_source"`
		PolicyAuthority policy.AuthorityFace `json:"policy_authority"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("parse rebaseline output json: %v (%q)", err, out)
	}
	if result.ReceiptID == "" || result.TransitionID == "" {
		t.Fatalf("expected receipt_id and transition_id in output: %q", out)
	}
	if result.PolicySource == "" || !result.PolicyAuthority.Current || !result.PolicyAuthority.Allowed {
		t.Fatalf("confirmed JSON omitted current allowing authority: %+v", result)
	}
	if !testEventExists(t, homePath, result.ReceiptID) {
		t.Fatalf("receipt %s must resolve in the canonical event store", result.ReceiptID)
	}
	if !testEventExists(t, homePath, result.TransitionID) {
		t.Fatalf("transition %s must resolve in the canonical event store", result.TransitionID)
	}

	if out2, errStr2, code2 := runEventStoreCLI(t, "migrate", "--verify", "--home", homePath); code2 != 0 {
		t.Fatalf("verify should report ok after rebaseline: code=%d stderr=%s\n%s", code2, errStr2, out2)
	}
}

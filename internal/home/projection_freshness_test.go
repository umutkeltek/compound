package home

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeProjection(t *testing.T, home, name, body string) {
	t.Helper()
	dir := filepath.Join(home, "projections")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestProjectionFreshnessCatchesTheFixThatWasNotServed reconstructs
// find_b27a3de99682 as a fixture: the negatives projection was built at 13:08 by
// the pre-fix binary, the fixed binary was installed at 14:00, and the read path
// kept serving the 13:08 body. Every other signal said done.
func TestProjectionFreshnessCatchesTheFixThatWasNotServed(t *testing.T) {
	home := t.TempDir()
	writeProjection(t, home, "negatives.json", `{"computed_at":"2026-07-25T13:08:57Z","records":[]}`)

	installedAt := time.Date(2026, 7, 25, 14, 0, 0, 0, time.UTC)
	now := time.Date(2026, 7, 25, 14, 5, 0, 0, time.UTC)

	rows, err := ProjectionFreshnessReport(home, installedAt, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one projection row, got %+v", rows)
	}
	row := rows[0]
	if !row.OlderThanCode {
		t.Fatalf("a projection built before the installed binary must be flagged: %+v", row)
	}
	if row.TimestampField != "computed_at" {
		t.Errorf("timestamp field = %q, want computed_at", row.TimestampField)
	}
	// The row has to be actionable, or the operator learns the gap exists and not
	// what to do about it — which is where the original failure already left them.
	if row.RebuildCommand != "compound negatives build" {
		t.Errorf("row must name the command that rebuilds it, got %q", row.RebuildCommand)
	}

	stale, undated, oldest := StaleProjectionSummary(rows)
	if stale != 1 || undated != 0 {
		t.Errorf("summary = stale %d undated %d, want 1 and 0", stale, undated)
	}
	if oldest == "" {
		t.Error("summary must name the oldest offender")
	}
}

// TestProjectionFreshnessClearsAProjectionBuiltByTheRunningCode is the other half:
// after the rebuild, the same check must go quiet. A guard that cannot go quiet is
// noise, and noise gets skipped.
func TestProjectionFreshnessClearsAProjectionBuiltByTheRunningCode(t *testing.T) {
	home := t.TempDir()
	writeProjection(t, home, "negatives.json", `{"computed_at":"2026-07-25T14:30:00Z","records":[]}`)

	installedAt := time.Date(2026, 7, 25, 14, 0, 0, 0, time.UTC)
	now := time.Date(2026, 7, 25, 15, 0, 0, 0, time.UTC)

	rows, err := ProjectionFreshnessReport(home, installedAt, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].OlderThanCode {
		t.Fatalf("a projection rebuilt after install must not be flagged: %+v", rows)
	}
	if stale, _, _ := StaleProjectionSummary(rows); stale != 0 {
		t.Errorf("stale count = %d, want 0 after the rebuild", stale)
	}
}

// TestProjectionFreshnessTreatsUndatedAsUnprovable pins the third state. A body
// that records no build time cannot be cleared, but it cannot be accused either —
// reporting it as stale would invent a defect out of a missing field, and the
// operator would start ignoring the section.
func TestProjectionFreshnessTreatsUndatedAsUnprovable(t *testing.T) {
	home := t.TempDir()
	writeProjection(t, home, "lane-aliases.json", `{"aliases":{"a":"b"}}`)

	rows, err := ProjectionFreshnessReport(home, time.Now().UTC(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected one row, got %+v", rows)
	}
	if !rows[0].Undated {
		t.Errorf("a projection with no timestamp field must be marked undated: %+v", rows[0])
	}
	if rows[0].OlderThanCode {
		t.Errorf("undated must never be reported as older-than-code: %+v", rows[0])
	}
	stale, undated, _ := StaleProjectionSummary(rows)
	if stale != 0 || undated != 1 {
		t.Errorf("summary = stale %d undated %d, want 0 and 1", stale, undated)
	}
}

// TestProjectionFreshnessReadsEachProducersTimestampVocabulary pins that the check
// works across the field names producers actually use. Forcing one name would mean
// a migration whose only benefit is this check, and every projection it missed
// meanwhile would read as undated — i.e. silently unchecked.
func TestProjectionFreshnessReadsEachProducersTimestampVocabulary(t *testing.T) {
	home := t.TempDir()
	writeProjection(t, home, "a-computed.json", `{"computed_at":"2026-07-10T00:00:00Z"}`)
	writeProjection(t, home, "b-generated.json", `{"generated_at":"2026-07-10T00:00:00Z"}`)
	writeProjection(t, home, "c-imported.json", `{"imported_at":"2026-07-10T00:00:00Z"}`)
	writeProjection(t, home, "d-updated.json", `{"updated_at":"2026-07-10T00:00:00Z"}`)
	// Not a projection body: an append log's mtime is not a build time.
	writeProjection(t, home, "e-log.jsonl", `{"computed_at":"2026-07-10T00:00:00Z"}`)
	// Torn or foreign json is not this check's business to report on.
	writeProjection(t, home, "f-broken.json", `{not json`)

	installedAt := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	rows, err := ProjectionFreshnessReport(home, installedAt, installedAt)
	if err != nil {
		t.Fatalf("a torn body must not fail the whole report: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("expected the 4 dated .json bodies, got %d: %+v", len(rows), rows)
	}
	for _, row := range rows {
		if row.Undated || !row.OlderThanCode {
			t.Errorf("%s: expected dated and older-than-code, got %+v", row.Name, row)
		}
	}
	// Deterministic order, so two runs of doctor do not reorder their own output.
	for i := 1; i < len(rows); i++ {
		if rows[i-1].Name > rows[i].Name {
			t.Errorf("rows not sorted by name: %q before %q", rows[i-1].Name, rows[i].Name)
		}
	}
}

// TestProjectionFreshnessReportsNothingWithoutAProjectionsDir keeps the check
// fail-open: a home that has never built a projection is not a health problem, and
// doctor must not error on it.
func TestProjectionFreshnessReportsNothingWithoutAProjectionsDir(t *testing.T) {
	rows, err := ProjectionFreshnessReport(t.TempDir(), time.Now().UTC(), time.Now().UTC())
	if err != nil {
		t.Fatalf("absent projections dir must not error: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("expected no rows, got %+v", rows)
	}
}

// TestProjectionFreshnessNeverReportsAnAbsolutePath pins that a row is quotable.
// doctor output gets pasted into findings and commit messages, and this repo ships
// public — a row carrying /Users/<name>/... would leak the operator's filesystem
// into whatever quotes it.
func TestProjectionFreshnessNeverReportsAnAbsolutePath(t *testing.T) {
	home := t.TempDir()
	writeProjection(t, home, "negatives.json", `{"computed_at":"2026-07-10T00:00:00Z"}`)

	rows, err := ProjectionFreshnessReport(home, time.Now().UTC(), time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if filepath.IsAbs(row.RelPath) {
			t.Errorf("RelPath must stay home-relative, got %q", row.RelPath)
		}
		if got := row.RelPath; got != "projections/negatives.json" {
			t.Errorf("RelPath = %q, want projections/negatives.json", got)
		}
	}
}

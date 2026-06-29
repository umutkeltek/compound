package projection

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDoctorFreshFixture(t *testing.T) {
	report, err := DoctorFile(filepath.Join("..", "..", "fixtures", "projections", "freshness.yaml"), time.Date(2026, 6, 29, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != StatusFresh {
		t.Fatalf("status = %q, report = %+v", report.Status, report)
	}
	if report.OutputHash == "" || len(report.Sources) != 1 {
		t.Fatalf("hashes not populated: %+v", report)
	}
}

func TestDoctorDriftedSourceHash(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "view.md"), "view\n")
	writeFile(t, filepath.Join(dir, "source.jsonl"), "changed\n")
	manifest := Manifest{
		ProjectionID:         "projection:test",
		ViewRef:              "view.md",
		SourceRefs:           []string{"source.jsonl"},
		SourceLedgerSeq:      2,
		BuiltAt:              "2026-06-29T00:00:00Z",
		MaxAgeHours:          24,
		Generator:            "test",
		CoverageVerdict:      "complete",
		ExpectedSourceHashes: map[string]string{"source.jsonl": "sha256:not-current"},
	}
	report, err := Doctor(manifest, dir, time.Date(2026, 6, 29, 1, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != StatusDrifted {
		t.Fatalf("status = %q, want drifted (%+v)", report.Status, report)
	}
}

func TestDoctorStaleCoverage(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "view.md"), "view\n")
	writeFile(t, filepath.Join(dir, "source.jsonl"), "source\n")
	manifest := Manifest{
		ProjectionID:    "projection:test",
		ViewRef:         "view.md",
		SourceRefs:      []string{"source.jsonl"},
		SourceLedgerSeq: 2,
		BuiltAt:         "2026-06-29T00:00:00Z",
		MaxAgeHours:     1,
		Generator:       "test",
		CoverageVerdict: "partial",
	}
	report, err := Doctor(manifest, dir, time.Date(2026, 6, 29, 3, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if report.Status != StatusStale {
		t.Fatalf("status = %q, want stale (%+v)", report.Status, report)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

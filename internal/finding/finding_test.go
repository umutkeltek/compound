package finding

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var fixedNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func TestRecordShowRoundTrip(t *testing.T) {
	home := t.TempDir()
	rec, err := Record(home, Finding{Topic: "deploy", Body: "the deploy verb is `bin/ship`", WorkID: "work_abc"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rec.ID, "find_") {
		t.Fatalf("id = %q, want find_ prefix", rec.ID)
	}
	if rec.CID != CID(rec.Body) || !strings.HasPrefix(rec.CID, "sha256:") {
		t.Fatalf("cid = %q", rec.CID)
	}
	if rec.SourceClass != "evidence" {
		t.Fatalf("source_class = %q, want evidence (findings are never promoted truth)", rec.SourceClass)
	}
	got, err := Show(home, rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Body != rec.Body || got.WorkID != "work_abc" || got.Topic != "deploy" {
		t.Fatalf("round-trip mismatch: %+v", got)
	}
}

// TestRecordWritesOnlyUnderObservedFindings is the never-ledger invariant: recording a finding must
// leave the ledger byte-identical, so every file it writes lands under observed/findings.
func TestRecordWritesOnlyUnderObservedFindings(t *testing.T) {
	home := t.TempDir()
	if _, err := Record(home, Finding{Topic: "ci-retry", Body: "flaky tests retry via CI_RETRIES=2"}, fixedNow); err != nil {
		t.Fatal(err)
	}
	err := filepath.WalkDir(home, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(home, p)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(filepath.ToSlash(rel), "observed/findings/") {
			t.Fatalf("Record wrote outside observed/findings (never-ledger invariant broken): %s", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRecordIdempotentOnContent(t *testing.T) {
	home := t.TempDir()
	a, err := Record(home, Finding{Topic: "t", Body: "same body"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Record(home, Finding{Topic: "t", Body: "same body"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID != b.ID {
		t.Fatalf("same (topic,body) must yield same id: %q vs %q", a.ID, b.ID)
	}
	all, err := List(home, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Fatalf("idempotent record must not duplicate: got %d files", len(all))
	}
}

func TestListFilterAndSupersession(t *testing.T) {
	home := t.TempDir()
	v1, err := Record(home, Finding{Topic: "runbook", Body: "v1 procedure"}, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := Record(home, Finding{Topic: "runbook", Body: "v2 procedure", Supersedes: v1.ID}, fixedNow.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Record(home, Finding{Topic: "unrelated", Body: "noise"}, fixedNow); err != nil {
		t.Fatal(err)
	}

	runbook, err := List(home, "runbook")
	if err != nil {
		t.Fatal(err)
	}
	if len(runbook) != 2 {
		t.Fatalf("topic filter should return 2 runbook findings, got %d", len(runbook))
	}
	if runbook[0].ID != v2.ID || runbook[0].Supersedes != v1.ID {
		t.Fatalf("newest-first + supersession wrong: %+v", runbook[0])
	}
}

func TestShowRejectsPathEscape(t *testing.T) {
	home := t.TempDir()
	if _, err := Show(home, "../../etc/passwd"); err == nil {
		t.Fatal("Show must reject a path-shaped id")
	}
}

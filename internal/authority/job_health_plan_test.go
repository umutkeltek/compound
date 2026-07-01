package authority

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJobHealthPlanRecordsReviewWithoutMutatingScheduler(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(t.TempDir(), "cron-jobs.json")
	writeJobRosterFixture(t, source)
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	if _, err := ImportJobRoster(home, source, now); err != nil {
		t.Fatal(err)
	}
	roster, err := LoadJobRosterAuthority(home)
	if err != nil {
		t.Fatal(err)
	}

	report, err := RecordJobHealthPlans(home, roster, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != JobHealthPlanReportSchemaVersion ||
		report.PlanCount != 1 ||
		len(report.Plans) != 1 ||
		report.Plans[0].Status != "review_required" ||
		report.Plans[0].JobName != "ibkr-exit-check-daily" ||
		report.Plans[0].RepairCount != 1 {
		t.Fatalf("bad job health report: %+v", report)
	}
	if len(report.Paths) != 1 {
		t.Fatalf("paths = %+v", report.Paths)
	}
	review, err := os.ReadFile(report.Paths[0].ReviewPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(review)
	for _, want := range []string{"does not run", "Required Evidence", "ibkr-exit-check-daily", "missing python venv"} {
		if !strings.Contains(text, want) {
			t.Fatalf("review missing %q:\n%s", want, text)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("job health plan must not promote claims, stat err=%v", err)
	}
}

func TestJobHealthPlanNoopsWhenRosterHealthy(t *testing.T) {
	home := t.TempDir()
	roster := JobRosterAuthority{
		SchemaVersion:  JobRosterAuthoritySchema,
		AuthorityRef:   JobRosterAuthorityRef,
		SourcePath:     "jobs.json",
		SourceRevision: "sha256:healthy",
		Jobs: []JobSummary{
			{JobID: "ok", Name: "healthy job", LastStatus: "ok"},
		},
	}
	report, err := RecordJobHealthPlans(home, roster, time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if report.PlanCount != 0 || len(report.Plans) != 0 || len(report.Paths) != 0 {
		t.Fatalf("healthy roster should not write plans: %+v", report)
	}
	if _, err := os.Stat(filepath.Join(home, "reviews", "job-health")); !os.IsNotExist(err) {
		t.Fatalf("healthy roster should not create review dir, stat err=%v", err)
	}
}

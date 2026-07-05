package sourceobs

import (
	"testing"
	"time"
)

func TestListSourceObservationsProjectsAppliedCandidates(t *testing.T) {
	home := t.TempDir()
	obs := validObservation()
	applied, err := Apply(home, obs, tSource)
	if err != nil {
		t.Fatal(err)
	}
	report, err := ListSourceObservations(home, tSource.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if report.SchemaVersion != SourceObservationListSchemaVersion || report.AuthorityRef != "compound://source/observations" {
		t.Fatalf("report identity = %+v", report)
	}
	if report.Counts.TotalCandidates != 1 || report.Counts.SourceObservations != 1 || report.Counts.NoPromoteDecisions != 1 || report.Counts.PromoteDisallowed != 1 {
		t.Fatalf("counts = %+v", report.Counts)
	}
	if len(report.Items) != 1 {
		t.Fatalf("items = %+v", report.Items)
	}
	item := report.Items[0]
	if item.SourceObservationID != obs.ID || item.CandidateID != applied.CandidateIDs[0] || item.NoPromoteID != applied.NoPromoteIDs[0] {
		t.Fatalf("item = %+v applied=%+v", item, applied)
	}
}

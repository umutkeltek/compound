package routeevidence

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/envelope"
)

func TestRouteOutcomeReviewRecordsObservationForFailedReceipt(t *testing.T) {
	home := t.TempDir()
	ev := fixtureEvidence()
	ev.WorkID = "work_route_outcome"
	rec := fixtureReceipt("failed", false)
	targetPath := filepath.Join(t.TempDir(), "router.json")
	original := []byte(`{"routes":[]}` + "\n")
	if err := os.WriteFile(targetPath, original, 0o644); err != nil {
		t.Fatal(err)
	}

	review, err := BuildRouteOutcomeReview(ev, rec, targetPath, t0)
	if err != nil {
		t.Fatal(err)
	}
	if review.SchemaVersion != RouteOutcomeReviewSchemaVersion ||
		review.Assessment.Class != "route_remorse" ||
		!review.Assessment.ShouldReviewPolicy ||
		review.ReceiptID != rec.ID ||
		review.RouteEvidenceID != ev.ID ||
		review.TargetHash == "" {
		t.Fatalf("bad review: %+v", review)
	}

	review, err = RecordRouteOutcomeObservation(home, review, t0)
	if err != nil {
		t.Fatal(err)
	}
	if review.CandidateID == "" || review.NoPromoteID == "" || !review.RecordedNoPromote {
		t.Fatalf("review did not record observation/no-promote: %+v", review)
	}
	paths, err := RecordRouteOutcomeReview(home, review)
	if err != nil {
		t.Fatal(err)
	}
	var recorded RouteOutcomeReview
	data, err := os.ReadFile(paths.PlanPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.CandidateID != review.CandidateID || recorded.NoPromoteID != review.NoPromoteID {
		t.Fatalf("recorded review missing observation ids: %+v", recorded)
	}
	reviewText := string(readFile(t, paths.ReviewPath))
	for _, want := range []string{
		"does not mutate gateway/provider config",
		"Required Evidence Before Policy Change",
		"not task success by itself",
		"route_remorse",
	} {
		if !strings.Contains(reviewText, want) {
			t.Fatalf("review missing %q:\n%s", want, reviewText)
		}
	}
	candidateData := string(readFile(t, review.RecordedCandidatePath))
	if !strings.Contains(candidateData, `"kind": "route_observation"`) ||
		!strings.Contains(candidateData, `"verdict": "no_promote"`) ||
		!strings.Contains(candidateData, review.ID) {
		t.Fatalf("bad route outcome candidate:\n%s", candidateData)
	}
	noPromoteData := string(readFile(t, filepath.Join(home, "ledger", "no-promote-decisions.jsonl")))
	if !strings.Contains(noPromoteData, review.CandidateID) ||
		!strings.Contains(noPromoteData, "route outcome requires review") {
		t.Fatalf("bad no-promote ledger:\n%s", noPromoteData)
	}
	if got := string(readFile(t, targetPath)); got != string(original) {
		t.Fatalf("target mutated:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(home, "ledger", "claims.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("route outcome review must not promote claims, stat err=%v", err)
	}
}

func TestRouteOutcomeReviewDoesNotRecordObservationForSupportedRoute(t *testing.T) {
	home := t.TempDir()
	ev := fixtureEvidence()
	ev.WorkID = "work_route_outcome"
	rec := fixtureReceipt("completed", true)
	review, err := BuildRouteOutcomeReview(ev, rec, "", t0)
	if err != nil {
		t.Fatal(err)
	}
	if review.Assessment.Class != "route_supported" || review.Assessment.ShouldReviewPolicy {
		t.Fatalf("assessment = %+v", review.Assessment)
	}
	if _, err := RecordRouteOutcomeObservation(home, review, t0); err == nil ||
		!strings.Contains(err.Error(), "does not require policy observation") {
		t.Fatalf("err = %v, want no policy observation", err)
	}
	paths, err := RecordRouteOutcomeReview(home, review)
	if err != nil {
		t.Fatal(err)
	}
	text := string(readFile(t, paths.ReviewPath))
	if !strings.Contains(text, "route_supported") ||
		!strings.Contains(text, "do not change routing policy from one successful receipt") {
		t.Fatalf("review missing supported-route guard:\n%s", text)
	}
}

func TestLoadEvidenceByIDReadsRouteEvidenceLedger(t *testing.T) {
	home := t.TempDir()
	ev := fixtureEvidence()
	if err := RecordEvidence(home, ev); err != nil {
		t.Fatal(err)
	}
	got, err := LoadEvidenceByID(home, ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != ev.ID || got.RequestID != ev.RequestID {
		t.Fatalf("got %+v, want %+v", got, ev)
	}
}

func fixtureReceipt(outcome string, changed bool) envelope.Receipt {
	return envelope.Receipt{
		Header: envelope.Header{
			SchemaVersion: envelope.SVReceipt,
			ID:            "rec_route_outcome",
			CreatedAt:     rfc3339(t0),
			Producer:      envelope.Producer{Kind: "fixture", Name: "compound"},
			IDs:           envelope.IDs{WorkID: "work_route_outcome"},
			Scope:         envelope.Scope{EntityID: "system:compound", Surface: "route"},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  []string{"receipt:evidence"},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Outcome:  outcome,
		MoveType: "outward_delta",
		StateDelta: envelope.StateDelta{
			Changed:      changed,
			Summary:      "route outcome fixture",
			Verification: []string{"go test ./internal/routeevidence"},
		},
		Cost: envelope.ReceiptCost{ModelCalls: []string{"route_fixture"}},
	}
}

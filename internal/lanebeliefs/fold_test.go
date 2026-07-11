package lanebeliefs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
	"compound/internal/mem"
)

var foldTestTime = time.Date(2026, 7, 9, 13, 0, 0, 0, time.UTC)

func TestFoldIdempotentWithCursorAndOneObservationPerWork(t *testing.T) {
	homePath := t.TempDir()
	for i := 0; i < 20; i++ {
		writeLaneReceiptEvent(t, homePath, laneReceiptFixture{
			ReceiptID: fmt.Sprintf("rec_spam_%02d", i),
			WorkID:    "work_one",
			LaneID:    "fixture/judge",
			MoveType:  "control_plane_repair",
			Outcome:   "completed",
			Changed:   true,
		})
	}

	report, err := Fold(homePath, FoldOptions{Now: foldTestTime})
	if err != nil {
		t.Fatal(err)
	}
	if report.RawReceiptCount != 20 || report.NewObservationCount != 1 || report.CandidateCount != 1 {
		t.Fatalf("report counts = raw %d observations %d candidates %d, want 20/1/1: %+v", report.RawReceiptCount, report.NewObservationCount, report.CandidateCount, report)
	}
	if report.ReinforcementCount != 0 {
		t.Fatalf("fold must not reinforce while proposing, got %d", report.ReinforcementCount)
	}
	if len(report.Aggregates) != 1 {
		t.Fatalf("aggregates = %+v, want one", report.Aggregates)
	}
	agg := report.Aggregates[0]
	if agg.WorkCount != 1 || agg.ReceiptCount != 20 || agg.OpportunityCount != 1 || agg.HitCount != 1 {
		t.Fatalf("aggregate = %+v, want one work observation with twenty receipts", agg)
	}
	if agg.AbovePromotionFloor {
		t.Fatalf("one-work spam must not cross promotion floor: %+v", agg)
	}
	if agg.PosteriorBand != "park" {
		t.Fatalf("posterior band = %q, want park", agg.PosteriorBand)
	}

	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := store.AllCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || candidates[0].State != "parked" {
		t.Fatalf("candidates = %+v, want single parked proposal", candidates)
	}

	again, err := Fold(homePath, FoldOptions{Now: foldTestTime.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if again.RawReceiptCount != 0 || again.NewObservationCount != 0 || again.CandidateCount != 0 || again.ReinforcementCount != 0 {
		t.Fatalf("second fold over unchanged events must be empty, got %+v", again)
	}
}

func TestFoldNeverCopiesReceiptSummaryIntoBeliefFields(t *testing.T) {
	homePath := t.TempDir()
	secret := "operator-personal-do-not-copy"
	for i := 0; i < MIN_LANE_RECEIPTS; i++ {
		writeLaneReceiptEvent(t, homePath, laneReceiptFixture{
			ReceiptID: fmt.Sprintf("rec_private_%02d", i),
			WorkID:    "work_private",
			LaneID:    "fixture/judge",
			MoveType:  "control_plane_repair",
			Outcome:   "completed",
			Changed:   true,
			Summary:   secret,
		})
	}
	if _, err := Fold(homePath, FoldOptions{Now: foldTestTime}); err != nil {
		t.Fatal(err)
	}
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	candidates, err := store.AllCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("candidates = %+v, want one", candidates)
	}
	encoded, err := json.Marshal(candidates[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) {
		t.Fatalf("belief candidate leaked summary text: %s", encoded)
	}
}

func TestFoldUnresolvedLaneYieldsZeroEvidence(t *testing.T) {
	homePath := t.TempDir()
	for i := 0; i < MIN_LANE_RECEIPTS; i++ {
		writeLaneReceiptEvent(t, homePath, laneReceiptFixture{
			ReceiptID: fmt.Sprintf("rec_unresolved_%02d", i),
			WorkID:    "work_unresolved",
			LaneID:    "*",
			MoveType:  "control_plane_repair",
			Outcome:   "completed",
			Changed:   true,
		})
	}
	report, err := Fold(homePath, FoldOptions{Now: foldTestTime})
	if err != nil {
		t.Fatal(err)
	}
	if report.UnresolvedReceiptCount != MIN_LANE_RECEIPTS || report.NewObservationCount != 0 || report.CandidateCount != 0 {
		t.Fatalf("unresolved report = %+v, want unresolved receipts and zero evidence", report)
	}
}

func TestFoldIgnoresNonterminalActionReceipt(t *testing.T) {
	homePath := t.TempDir()
	writeLaneReceiptEvent(t, homePath, laneReceiptFixture{
		ReceiptID: "rec_intermediate_action",
		WorkID:    "work_parent_still_running",
		LaneID:    "fixture/judge",
		MoveType:  "control_plane_repair",
		Outcome:   "completed",
		Changed:   true,
		To:        "running",
	})

	report, err := Fold(homePath, FoldOptions{Now: foldTestTime})
	if err != nil {
		t.Fatal(err)
	}
	if report.RawReceiptCount != 0 || report.NewObservationCount != 0 || report.CandidateCount != 0 || len(report.Aggregates) != 0 {
		t.Fatalf("nonterminal action receipt became lane outcome evidence: %+v", report)
	}
}

func TestFoldIncludesCancelledTerminalReceipt(t *testing.T) {
	homePath := t.TempDir()
	writeLaneReceiptEvent(t, homePath, laneReceiptFixture{
		ReceiptID: "rec_cancelled_terminal",
		WorkID:    "work_cancelled_terminal",
		LaneID:    "fixture/judge",
		MoveType:  "outward_delta",
		Outcome:   "no_op",
		Changed:   false,
		To:        "cancelled",
	})

	report, err := Fold(homePath, FoldOptions{Now: foldTestTime})
	if err != nil {
		t.Fatal(err)
	}
	if report.RawReceiptCount != 1 || report.NewObservationCount != 1 || len(report.Aggregates) != 1 {
		t.Fatalf("cancelled terminal receipt disappeared from lane outcomes: %+v", report)
	}
}

func TestFoldConstantsMatchBinding(t *testing.T) {
	if MIN_LANE_RECEIPTS != 5 || MIN_CORROBORATION_WORKS != 2 || BETA_PRIOR_ALPHA != 2 || BETA_PRIOR_BETA != 2 || PROMOTE_POSTERIOR_FLOOR != 0.66 || COOL_AFTER_FOLDS != 3 || PER_CYCLE_CANDIDATE_CAP <= 0 {
		t.Fatalf("fold constants drifted")
	}
}

type laneReceiptFixture struct {
	ReceiptID string
	WorkID    string
	LaneID    string
	MoveType  string
	Outcome   string
	Changed   bool
	Summary   string
	To        string
}

func writeLaneReceiptEvent(t *testing.T, homePath string, fx laneReceiptFixture) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(homePath, "events"), 0o755); err != nil {
		t.Fatal(err)
	}
	created := foldTestTime.Format(time.RFC3339)
	receipt := envelope.Receipt{
		Header: envelope.Header{
			SchemaVersion: envelope.SVReceipt,
			ID:            fx.ReceiptID,
			CreatedAt:     created,
			Producer:      envelope.Producer{Kind: "test", Name: "fixture", LaneID: fx.LaneID},
			IDs:           envelope.IDs{WorkID: fx.WorkID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier0",
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Outcome:    fx.Outcome,
		MoveType:   fx.MoveType,
		StateDelta: envelope.StateDelta{Changed: fx.Changed, Summary: fx.Summary, FilesChanged: []string{"a.go", "b.go"}},
		Cost:       envelope.ReceiptCost{ModelCalls: []string{}},
		Skills:     envelope.ReceiptSkills{Candidate: []string{}},
		Memory:     envelope.ReceiptMemory{Candidates: []string{}},
		Next:       envelope.ReceiptNext{SuggestedWork: []string{}},
		ProofRefs:  []string{"proof:test"},
	}
	writeJSONFile(t, filepath.Join(homePath, "events", fx.ReceiptID+".json"), receipt)
	to := fx.To
	if to == "" {
		to = "completed"
	}
	line := map[string]any{
		"ts":       created,
		"work_id":  fx.WorkID,
		"from":     "running",
		"to":       to,
		"attached": []string{fx.ReceiptID},
	}
	b, err := json.Marshal(line)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(homePath, "events", "work.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

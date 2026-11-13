package work

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"compound/internal/home"
)

// workViewFixture names the rows the same-output fixture plants, so tests can
// assert on them without re-deriving IDs.
type workViewFixture struct {
	Open           []string
	Terminal       []string
	ContinueRuns   int
	DispatchRuns   int
	ResolvableRuns int
	ArchivedPacket string
}

// newEquivalenceStore opens a store over an initialized home, which archiving
// requires.
func newEquivalenceStore(t *testing.T) *Store {
	t.Helper()
	homePath := filepath.Join(t.TempDir(), ".compound")
	if err := home.Init(homePath, "test", fixedNow); err != nil {
		t.Fatal(err)
	}
	s, err := Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	s.advanceClock = func(logical time.Time) time.Time { return logical }
	return s
}

// buildWorkViewEquivalenceFixture plants a home with every work state the work
// view projects (open, claimed, running, verifying, blocked, held, and
// closed-but-not-archived) plus many continue-run and dispatch-step-run files,
// of which only a few survive the recent limit. Runs share generated_at
// seconds so the ID tie-break is exercised, some resolve to terminal work, some
// stay waiting on open or missing work or on a non-work blocker, and two files
// are unreadable so their integrity warnings must survive too. Every
// timestamp derives from at; nothing reads the wall clock.
func buildWorkViewEquivalenceFixture(t *testing.T, s *Store, at time.Time) workViewFixture {
	t.Helper()
	var fx workViewFixture
	create := func(predicate, state string, created time.Time) string {
		t.Helper()
		w, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: predicate, State: state}, created)
		if err != nil {
			t.Fatalf("create %q: %v", predicate, err)
		}
		return w.ID
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	base := at.Add(-72 * time.Hour)

	// Active-packet rows: a live one, and a newer one that is closed and
	// archived, so the active-packet proof must find its newest candidate
	// among archived works.
	packetLive, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "equivalence live packet work", CanonicalSurface: DefaultActivePacketSurface}, base.Add(-10*time.Hour))
	must(err)
	packetArchived, err := s.Create(CreateInput{Owner: "compound-core", SuccessPredicate: "equivalence archived packet work", CanonicalSurface: DefaultActivePacketSurface}, base.Add(-9*time.Hour))
	must(err)
	closeWorkbenchTestWork(t, s, packetArchived.ID, base.Add(-8*time.Hour), "equivalence archived packet close")
	_, err = home.ArchiveOperationalHistory(s.home, home.OperationalArchiveOptions{Kinds: []string{"work"}}, base.Add(-7*time.Hour))
	must(err)
	fx.ArchivedPacket = packetArchived.ID

	// Open rows, one per state the view distinguishes.
	ready := create("equivalence ready work", "", base)
	readyTwo := create("equivalence second ready work", "", base.Add(time.Minute))
	proposed := create("equivalence proposed work", "proposed", base.Add(2*time.Minute))
	held := create("equivalence held work", "held", base.Add(3*time.Minute))
	claimed := create("equivalence claimed work", "", base.Add(4*time.Minute))
	_, err = s.Claim(claimed, "codex", base.Add(5*time.Minute))
	must(err)
	running := create("equivalence running work", "", base.Add(6*time.Minute))
	_, err = s.Claim(running, "claude", base.Add(7*time.Minute))
	must(err)
	_, err = s.Advance(running, AdvanceInput{Actor: "claude", To: "running"}, base.Add(8*time.Minute))
	must(err)
	verifying := create("equivalence verifying work", "", base.Add(9*time.Minute))
	_, err = s.Claim(verifying, "codex", base.Add(10*time.Minute))
	must(err)
	_, err = s.Advance(verifying, AdvanceInput{To: "running"}, base.Add(11*time.Minute))
	must(err)
	_, err = s.Advance(verifying, AdvanceInput{To: "verifying", VerificationID: "ver_equivalence"}, base.Add(12*time.Minute))
	must(err)
	blocked := create("equivalence blocked work", "", base.Add(13*time.Minute))
	_, err = s.Claim(blocked, "codex", base.Add(14*time.Minute))
	must(err)
	_, err = s.Advance(blocked, AdvanceInput{To: "running"}, base.Add(15*time.Minute))
	must(err)
	_, err = s.Advance(blocked, AdvanceInput{To: "blocked"}, base.Add(16*time.Minute))
	must(err)
	fx.Open = []string{packetLive.ID, ready, readyTwo, proposed, held, claimed, running, verifying, blocked}

	// Closed-but-not-archived rows: completed at spread-out times (some out of
	// creation order, so recency is not ID order), one failed, one cancelled.
	for i := 0; i < 9; i++ {
		id := create(fmt.Sprintf("equivalence closed work %02d", i), "", base.Add(time.Duration(20+i)*time.Minute))
		closeAt := base.Add(time.Duration((i*7)%9) * time.Hour)
		closeWorkbenchTestWork(t, s, id, closeAt, fmt.Sprintf("equivalence close %02d", i))
		fx.Terminal = append(fx.Terminal, id)
	}
	failed := create("equivalence failed work", "", base.Add(40*time.Minute))
	_, err = s.Claim(failed, "codex", base.Add(41*time.Minute))
	must(err)
	_, err = s.Advance(failed, AdvanceInput{To: "running"}, base.Add(42*time.Minute))
	must(err)
	_, err = s.Close(failed, CloseInput{Outcome: "failed", MoveType: "audit_only", Summary: "equivalence failed close"}, base.Add(43*time.Minute))
	must(err)
	cancelled := create("equivalence cancelled work", "", base.Add(44*time.Minute))
	_, err = s.Close(cancelled, CloseInput{Outcome: "cancelled", MoveType: "audit_only", Summary: "equivalence cancelled close"}, base.Add(45*time.Minute))
	must(err)
	fx.Terminal = append(fx.Terminal, failed, cancelled)

	// Continue runs: many files, few kept. Every fourth run waits only on
	// terminal work (resolves), others wait on open work, on missing work, or on
	// a non-work blocker, or are not waiting at all.
	const continueRuns = 40
	for i := 0; i < continueRuns; i++ {
		generated := at.Add(-time.Duration(i%13) * time.Hour)
		var blockedOn, stepWorks []string
		status, wait := "waiting", "run_evidence"
		switch i % 5 {
		case 0:
			target := fx.Terminal[i%len(fx.Terminal)]
			blockedOn = []string{"work.run_evidence:" + target}
			stepWorks = []string{target, fx.Terminal[(i+1)%len(fx.Terminal)]}
			fx.ResolvableRuns++
		case 1:
			target := fx.Open[i%len(fx.Open)]
			blockedOn = []string{"work.run_evidence:" + target}
			stepWorks = []string{target}
		case 2:
			blockedOn = []string{"lane.capacity:codex"}
			wait = "lane_capacity"
		case 3:
			blockedOn = []string{"work.run_evidence:work_equivalence_missing"}
		default:
			status, wait = "stopped", ""
		}
		result := ContinueRunResult{
			SchemaVersion: "compound.work-continue-run.v0.1",
			ID:            fmt.Sprintf("wconr_eqv%03d", i),
			GeneratedAt:   generated.UTC().Format(time.RFC3339),
			Status:        status,
			Actor:         []string{"codex", "claude"}[i%2],
			MaxSteps:      5,
			StepCount:     len(stepWorks),
			CanContinue:   status == "waiting",
			WaitReason:    wait,
			BlockedOn:     blockedOn,
			NextAction:    "equivalence next action",
		}
		for _, workID := range stepWorks {
			result.Steps = append(result.Steps, ContinueResult{Action: "advance", WorkID: workID, Status: "advanced"})
		}
		_, err := s.RecordContinueRun(result)
		must(err)
	}
	fx.ContinueRuns = continueRuns
	continueDir := filepath.Join(s.home, "continue-runs")
	must(os.WriteFile(filepath.Join(continueDir, "wconr_eqvbad.json"), []byte("{not json\n"), 0o644))
	must(os.WriteFile(filepath.Join(continueDir, "wconr_eqvtype.json"), []byte(`{"id":"wconr_eqvtype","generated_at":"`+at.UTC().Format(time.RFC3339)+`","step_count":"three"}`+"\n"), 0o644))

	// Dispatch step runs: same shapes plus the no-open-work resolution.
	const dispatchRuns = 12
	for i := 0; i < dispatchRuns; i++ {
		generated := at.Add(-time.Duration(i%4) * time.Hour)
		result := DispatchStepRunResult{
			SchemaVersion: "compound.dispatch-step-run.v0.1",
			ID:            fmt.Sprintf("wdsr_eqv%03d", i),
			GeneratedAt:   generated.UTC().Format(time.RFC3339),
			Status:        "waiting",
			Actor:         "codex",
			MaxSteps:      3,
			CanContinue:   true,
			NextAction:    "equivalence dispatch next action",
		}
		switch i % 4 {
		case 0:
			result.WaitReason = "no_open_work"
			result.BlockedOn = []string{"work.ready_or_source_packet"}
		case 1:
			result.WaitReason = "run_evidence"
			result.BlockedOn = []string{"work.run_evidence:" + fx.Terminal[i%len(fx.Terminal)]}
		case 2:
			result.WaitReason = "run_evidence"
			result.BlockedOn = []string{"work.run_evidence:" + fx.Open[i%len(fx.Open)]}
		default:
			result.Status = "stopped"
			result.CanContinue = false
		}
		result.Steps = []DispatchStepResult{{Action: "claim", WorkID: fx.Open[i%len(fx.Open)], Status: "claimed"}}
		result.StepCount = len(result.Steps)
		_, err := s.RecordDispatchStepRun(result)
		must(err)
	}
	fx.DispatchRuns = dispatchRuns
	must(os.WriteFile(filepath.Join(s.home, "dispatch-step-runs", "wdsr_eqvbad.json"), []byte("[1,2,3]\n"), 0o644))
	return fx
}

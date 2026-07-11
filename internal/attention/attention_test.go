package attention

import (
	"math"
	"testing"
	"time"

	"compound/internal/reviewartifact"
	"compound/internal/work"
)

func TestWorkbenchSafeWorkWatchesWhileOperatorGatesStillNeedYou(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	safeIDs := []string{"work_ready", "work_admitted", "work_proposed"}
	report := Build(Inputs{Workbench: &work.WorkbenchReport{
		GeneratedAt: now.Format(time.RFC3339),
		Open: []work.WorkbenchItem{
			{WorkID: safeIDs[0], State: "ready", SuccessPredicate: "run a local reversible check without publishing", ActionContext: &work.WorkActionContext{Boundary: "stop before public or external side effects"}},
			{WorkID: safeIDs[1], State: "admitted", SuccessPredicate: "prepare admitted local work"},
			{WorkID: safeIDs[2], State: "proposed", SuccessPredicate: "prepare proposed local work"},
			{WorkID: "work_held", State: "held", OpenReason: "operator decision required"},
			{WorkID: "work_blocked", State: "blocked", OpenReason: "conflict requires resolution"},
			{WorkID: "work_conflict", State: "ready", OpenReason: "merge conflict requires resolution"},
			{WorkID: "work_explicit_gate", State: "ready", ActionContext: &work.WorkActionContext{RequiresOperator: true}},
			{WorkID: "work_review_card_gate", State: "proposed", ReviewCards: []work.WorkbenchReviewCard{{RequiresOperator: true}}},
			{WorkID: "work_public", State: "ready", ActionContext: &work.WorkActionContext{ActionSummary: "publish the public release"}},
			{WorkID: "work_external", State: "admitted", ActionContext: &work.WorkActionContext{ActionSummary: "send an external submission"}},
			{WorkID: "work_destructive", State: "proposed", ActionContext: &work.WorkActionContext{ActionSummary: "delete production records"}},
			{WorkID: "work_financial", State: "ready", ActionContext: &work.WorkActionContext{ActionSummary: "approve financial spend"}},
			{WorkID: "work_security", State: "admitted", ActionContext: &work.WorkActionContext{ActionSummary: "rotate a security credential"}},
		},
		DispatchNext: work.WorkbenchDispatchNext{
			CanRun: true, Action: "claim", WorkID: safeIDs[0],
			Command: []string{"compound", "work", "continue", "--expect-work", safeIDs[0]},
		},
	}}, now)

	watching := groupByKey(t, report, "workbench:can-move")
	if watching.Band != BandWatching || watching.Count != len(safeIDs) || !sameStringSet(watching.ItemIDs, safeIDs) {
		t.Fatalf("safe work = band %q count %d ids %v, want %q %d %v", watching.Band, watching.Count, watching.ItemIDs, BandWatching, len(safeIDs), safeIDs)
	}
	if watching.Action == nil || watching.Action.ID != "workbench.dispatch_next" || watching.Action.RequiresConfirmation {
		t.Fatalf("safe work must expose the runnable can-move action without operator confirmation: %+v", watching.Action)
	}

	needsYou := groupByKey(t, report, "workbench:blocked-on-you")
	wantNeedsYou := []string{"work_held", "work_blocked", "work_conflict", "work_explicit_gate", "work_review_card_gate", "work_public", "work_external", "work_destructive", "work_financial", "work_security"}
	if needsYou.Band != BandNeedsYou || needsYou.Count != len(wantNeedsYou) || !sameStringSet(needsYou.ItemIDs, wantNeedsYou) {
		t.Fatalf("operator-gated work = band %q count %d ids %v, want %q %d %v", needsYou.Band, needsYou.Count, needsYou.ItemIDs, BandNeedsYou, len(wantNeedsYou), wantNeedsYou)
	}
	if needsYou.BlastRadius != BlastSecurity || needsYou.Reversibility != Irreversible {
		t.Fatalf("highest gate risk = %q/%q, want %q/%q", needsYou.BlastRadius, needsYou.Reversibility, BlastSecurity, Irreversible)
	}
	if report.Counts.NeedsYou != len(wantNeedsYou) || report.Counts.Watching != len(safeIDs) || report.Counts.BlockedOnYou != len(wantNeedsYou) {
		t.Fatalf("counts = %+v, want needs_you=%d watching=%d blocked_on_you=%d", report.Counts, len(wantNeedsYou), len(safeIDs), len(wantNeedsYou))
	}
}

func TestExecutorProjectionDoesNotRepeatWorkbenchHeldBurden(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	report := Build(Inputs{
		Workbench: &work.WorkbenchReport{GeneratedAt: now.Format(time.RFC3339), Open: []work.WorkbenchItem{{WorkID: "work_held", State: "held"}}},
		Executors: &work.ExecutorsProjection{GeneratedAt: now.Format(time.RFC3339), Executors: []work.ExecutorRow{{
			ID: "codex", Label: "Codex", NeedsMeCount: 1,
			CurrentWork: []work.ExecutorWorkRef{{WorkID: "work_held", State: "held"}},
		}}},
	}, now)

	if len(report.Groups) != 1 || report.Groups[0].Key != "workbench:blocked-on-you" {
		t.Fatalf("held work must appear once from its owning projection, got %+v", report.Groups)
	}
	if report.Counts.NeedsYou != 1 || report.Counts.NeedsYouGroups != 1 || report.Counts.BlockedOnYou != 1 {
		t.Fatalf("duplicate executor burden inflated counts: %+v", report.Counts)
	}
}

func TestExecutorProjectionKeepsIndependentDeadExecutorBurden(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	report := Build(Inputs{
		Workbench: &work.WorkbenchReport{GeneratedAt: now.Format(time.RFC3339), Open: []work.WorkbenchItem{{WorkID: "work_held", State: "held"}}},
		Executors: &work.ExecutorsProjection{GeneratedAt: now.Format(time.RFC3339), Executors: []work.ExecutorRow{{
			ID: "codex", Label: "Codex", NeedsMeCount: 2,
			CurrentWork: []work.ExecutorWorkRef{
				{WorkID: "work_held", State: "held"},
				{WorkID: "work_dead_executor", State: "running", NeedsMe: "executor process is not alive"},
			},
		}}},
	}, now)

	executor := groupByKey(t, report, "executor:codex")
	if executor.Count != 1 || !sameStringSet(executor.ItemIDs, []string{"work_dead_executor"}) {
		t.Fatalf("independent executor burden = count %d ids %v, want the dead executor only", executor.Count, executor.ItemIDs)
	}
	if report.Counts.NeedsYou != 2 || report.Counts.NeedsYouGroups != 2 {
		t.Fatalf("independent workbench and executor burdens must both remain visible: %+v", report.Counts)
	}
}

func groupByKey(t *testing.T, report Report, key string) Group {
	t.Helper()
	for _, group := range report.Groups {
		if group.Key == key {
			return group
		}
	}
	t.Fatalf("group %q missing from %+v", key, report.Groups)
	return Group{}
}

func sameStringSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]bool, len(got))
	for _, value := range got {
		seen[value] = true
	}
	for _, value := range want {
		if !seen[value] {
			return false
		}
	}
	return true
}

func TestClassAGateOutranksHighVolumeClassB(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	report := Build(Inputs{Reviews: &reviewartifact.QueueReport{
		GeneratedAt: now.Format(time.RFC3339),
		Groups: []reviewartifact.QueueGroup{
			{
				Key:             "judge",
				Area:            "panel-verdicts",
				Band:            reviewartifact.BandNeedsYou,
				Count:           120,
				OperatorSummary: "Panel verdicts need reversible review.",
				ItemIDs:         []string{"judge"},
				Sample:          &reviewartifact.QueueItem{ID: "judge", Area: "panel-verdicts", CreatedAt: now.Add(-14 * 24 * time.Hour).Format(time.RFC3339)},
			},
			{
				Key:             "gate",
				Area:            "route-remorse",
				Band:            reviewartifact.BandNeedsYou,
				Count:           1,
				OperatorSummary: "An irreversible route demotion needs approval.",
				ItemIDs:         []string{"gate"},
				Sample:          &reviewartifact.QueueItem{ID: "gate", Area: "route-remorse", CreatedAt: now.Add(-24 * time.Hour).Format(time.RFC3339)},
			},
		},
	}}, now)

	if report.Top == nil {
		t.Fatal("top group missing")
	}
	if report.Top.Key != "gate" {
		t.Fatalf("top key = %q, want gate; groups=%+v", report.Top.Key, report.Groups)
	}
	if got := Class(*report.Top); got != "gate" {
		t.Fatalf("top class = %q, want gate", got)
	}
}

func TestLeverageVolumeDampeningIsLogarithmic(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	oldest := now.Add(-24 * time.Hour).Format(time.RFC3339)
	one := leverage(1, oldest, "", now)
	four := leverage(4, oldest, "", now)
	if four <= one {
		t.Fatalf("count=4 leverage %.2f should exceed count=1 %.2f", four, one)
	}
	if four >= one*4 {
		t.Fatalf("count=4 leverage %.2f should be damped below linear %.2f", four, one*4)
	}
	if diff := math.Abs(four - one*3); diff > 0.0001 {
		t.Fatalf("count=4 leverage = %.4f, want %.4f", four, one*3)
	}
}

func TestLeverageStaleBumpPast48Hours(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	at48h := now.Add(-48 * time.Hour).Format(time.RFC3339)
	past48h := now.Add(-49 * time.Hour).Format(time.RFC3339)
	got := leverage(1, past48h, "", now)
	withoutBumpAtSameAge := (1 + math.Log2(1)) * clamp(49.0/24.0, 1, 14)
	if diff := math.Abs(got - withoutBumpAtSameAge*1.25); diff > 0.0001 {
		t.Fatalf("stale leverage = %.4f, want %.4f", got, withoutBumpAtSameAge*1.25)
	}
	notStale := leverage(1, at48h, "", now)
	if notStale != 2 {
		t.Fatalf("48h exact leverage = %.4f, want 2.0000 without stale bump", notStale)
	}
}

func TestCountsAddUp(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	cleanAction := reviewartifact.Action{ID: "record", Label: "Record", Argv: []string{"compound", "reviews", "record-finding"}}
	report := Build(Inputs{Reviews: &reviewartifact.QueueReport{
		GeneratedAt: now.Format(time.RFC3339),
		Counts: reviewartifact.QueueCounts{
			NeedsYouFallbackGroupCount: 1,
		},
		Groups: []reviewartifact.QueueGroup{
			{
				Key:             "actionable",
				Area:            "panel-verdicts",
				Band:            reviewartifact.BandNeedsYou,
				Count:           2,
				OperatorSummary: "Two verdicts need review.",
				ItemIDs:         []string{"a", "b"},
				SafeBatchAction: &cleanAction,
			},
			{
				Key:             "fallback",
				Area:            "external-repo-intake",
				Band:            reviewartifact.BandNeedsYou,
				Count:           3,
				OperatorSummary: "Three intake items need review.",
				SummarySource:   reviewartifact.SummarySourceFallback,
				ItemIDs:         []string{"c", "d", "e"},
			},
			{
				Key:             "watching",
				Area:            "job-health",
				Band:            reviewartifact.BandWatching,
				Count:           4,
				OperatorSummary: "Four jobs are being watched.",
				ItemIDs:         []string{"f", "g", "h", "i"},
			},
		},
	}}, now)

	if report.Counts.NeedsYou != 5 {
		t.Fatalf("needs_you = %d, want 5", report.Counts.NeedsYou)
	}
	if report.Counts.NeedsYouGroups != 2 {
		t.Fatalf("needs_you_groups = %d, want 2", report.Counts.NeedsYouGroups)
	}
	if report.Counts.ActionableNow != 1 {
		t.Fatalf("actionable_now = %d, want 1", report.Counts.ActionableNow)
	}
	if report.Counts.Watching != 4 {
		t.Fatalf("watching = %d, want 4", report.Counts.Watching)
	}
	if report.Counts.FallbackGroups != 1 {
		t.Fatalf("fallback_groups = %d, want 1", report.Counts.FallbackGroups)
	}
}

func TestAreaMappingCoversKnownAreasAndUnknownDefault(t *testing.T) {
	known := []string{
		"panel-verdicts",
		"route-promotions",
		"synthesis-contradictions",
		"external-repo-intake",
		"job-health",
		"product-action-cards",
		"bench",
		"synthesis-sweeps",
		"capability-evaluation-intake",
		"source-control-cleanup",
		"capability-lifecycle-proposals",
		"observation-candidates",
		"source-observation-verdicts",
		"distiller-improvements",
		"distiller-requests",
		"parity",
		"route-remorse",
	}
	if len(knownAreaClassifications) != len(known) {
		t.Fatalf("knownAreaClassifications has %d entries, want %d", len(knownAreaClassifications), len(known))
	}
	for _, area := range known {
		t.Run(area, func(t *testing.T) {
			kind, blast, reversibility := ClassifyArea(area)
			if _, ok := knownAreaClassifications[area]; !ok {
				t.Fatalf("area %q missing from mapping", area)
			}
			if kind == "" || blast == "" || reversibility == "" {
				t.Fatalf("classification for %q has empty field: kind=%q blast=%q reversibility=%q", area, kind, blast, reversibility)
			}
		})
	}
	kind, blast, reversibility := ClassifyArea("unknown-area")
	if kind != KindAdjudicate || blast != BlastLocalReversible || reversibility != Reversible {
		t.Fatalf("unknown default = %q/%q/%q, want %q/%q/%q", kind, blast, reversibility, KindAdjudicate, BlastLocalReversible, Reversible)
	}
}

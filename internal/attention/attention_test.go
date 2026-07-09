package attention

import (
	"math"
	"testing"
	"time"

	"compound/internal/reviewartifact"
)

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

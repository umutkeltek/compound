package lanebeliefs

import (
	"testing"
)

func posteriorFor(t *testing.T, homePath, nodeID, class string) CompletionPosteriorReport {
	t.Helper()
	post, err := CompletionPosterior(homePath, nodeID, class)
	if err != nil {
		t.Fatal(err)
	}
	return post
}

// An audit_only close that DECLARES its capability class and resolves to an agent-lane node is one
// completion trial in that class — and nothing else: no capability/tendency belief observation, no
// candidate. A failed one is a miss. An unresolvable lane stays zero evidence. Reds against the
// fold that drops every audit_only receipt (the product loop closes every lane job audit_only, so
// every lane read 0 attempts).
func TestFoldAuditOnlyDeclaredClassMintsCompletionTrialOnly(t *testing.T) {
	homePath := t.TempDir()
	nodeID := seedLaneNode(t, homePath, "fixture/judge")
	writeLaneReceiptEvent(t, homePath, laneReceiptFixture{
		ReceiptID: "rec_audit_cls_done", WorkID: "work_audit_cls_done", LaneID: "fixture/judge",
		MoveType: "audit_only", Outcome: "completed", To: "completed", CapabilityClass: "build-implement",
	})
	writeLaneReceiptEvent(t, homePath, laneReceiptFixture{
		ReceiptID: "rec_audit_cls_fail", WorkID: "work_audit_cls_fail", LaneID: "fixture/judge",
		MoveType: "audit_only", Outcome: "failed", To: "failed", CapabilityClass: "build-implement",
	})
	writeLaneReceiptEvent(t, homePath, laneReceiptFixture{
		ReceiptID: "rec_audit_cls_nolane", WorkID: "work_audit_cls_nolane", LaneID: "nobody/unseeded",
		MoveType: "audit_only", Outcome: "completed", To: "completed", CapabilityClass: "build-implement",
	})

	report, err := Fold(homePath, FoldOptions{Now: foldTestTime})
	if err != nil {
		t.Fatal(err)
	}
	if report.IgnoredReceiptCount != 0 || report.UnresolvedReceiptCount != 1 {
		t.Fatalf("ignored/unresolved = %d/%d, want 0/1 (declared audit_only counts; unseeded lane is zero evidence): %+v", report.IgnoredReceiptCount, report.UnresolvedReceiptCount, report)
	}
	if report.NewCompletionObservationCount != 2 {
		t.Fatalf("completion trials = %d, want 2 (one per attempted work)", report.NewCompletionObservationCount)
	}
	if report.NewObservationCount != 0 || report.CandidateCount != 0 {
		t.Fatalf("audit_only minted belief signals: observations=%d candidates=%d, want 0/0", report.NewObservationCount, report.CandidateCount)
	}
	build := posteriorFor(t, homePath, nodeID, "build-implement")
	if build.Attempts != 2 || build.Completions != 1 || build.Failures != 1 {
		t.Fatalf("build-implement cell = %d/%d/%d, want 2 attempts / 1 completion / 1 failure: %+v", build.Attempts, build.Completions, build.Failures, build)
	}
	if unknown := posteriorFor(t, homePath, nodeID, capabilityClassUnknown); unknown.Attempts != 0 {
		t.Fatalf("declared-class trials leaked into the unknown bucket: %+v", unknown)
	}
}

// An audit_only close that declares no class stays ignored exactly as before (IgnoredReceiptCount,
// no trial anywhere) — and so does one declaring the literal "unknown" sentinel: audit_only only ever
// enters a REAL class cell. Guards the no-guessing boundary against a fold that started counting
// every audit_only receipt.
func TestFoldAuditOnlyWithoutClassStaysIgnored(t *testing.T) {
	homePath := t.TempDir()
	nodeID := seedLaneNode(t, homePath, "fixture/judge")
	writeLaneReceiptEvent(t, homePath, laneReceiptFixture{
		ReceiptID: "rec_audit_plain", WorkID: "work_audit_plain", LaneID: "fixture/judge",
		MoveType: "audit_only", Outcome: "completed", To: "completed",
	})
	writeLaneReceiptEvent(t, homePath, laneReceiptFixture{
		ReceiptID: "rec_audit_sentinel", WorkID: "work_audit_sentinel", LaneID: "fixture/judge",
		MoveType: "audit_only", Outcome: "completed", To: "completed", CapabilityClass: capabilityClassUnknown,
	})

	report, err := Fold(homePath, FoldOptions{Now: foldTestTime})
	if err != nil {
		t.Fatal(err)
	}
	if report.IgnoredReceiptCount != 2 {
		t.Fatalf("ignored = %d, want 2 (class-less audit_only stays ignored): %+v", report.IgnoredReceiptCount, report)
	}
	if report.NewCompletionObservationCount != 0 || report.NewObservationCount != 0 {
		t.Fatalf("class-less audit_only minted evidence: completions=%d observations=%d", report.NewCompletionObservationCount, report.NewObservationCount)
	}
	if unknown := posteriorFor(t, homePath, nodeID, capabilityClassUnknown); unknown.Attempts != 0 {
		t.Fatalf("class-less audit_only reached the unknown bucket: %+v", unknown)
	}
}

// A non-audit receipt keeps today's behaviour (belief signals + one completion trial) but its trial
// now lands under the DECLARED class, not "unknown"; a legacy receipt without the key still lands in
// "unknown". Reds against completionCapabilityClass ignoring the receipt.
func TestFoldNonAuditReceiptBucketsUnderDeclaredClass(t *testing.T) {
	homePath := t.TempDir()
	nodeID := seedLaneNode(t, homePath, "fixture/judge")
	writeLaneReceiptEvent(t, homePath, laneReceiptFixture{
		ReceiptID: "rec_class_judge", WorkID: "work_class_judge", LaneID: "fixture/judge",
		MoveType: "control_plane_repair", Outcome: "completed", Changed: true, To: "completed", CapabilityClass: "judge-critique",
	})
	writeLaneReceiptEvent(t, homePath, laneReceiptFixture{
		ReceiptID: "rec_class_legacy", WorkID: "work_class_legacy", LaneID: "fixture/judge",
		MoveType: "control_plane_repair", Outcome: "completed", Changed: true, To: "completed",
	})

	report, err := Fold(homePath, FoldOptions{Now: foldTestTime})
	if err != nil {
		t.Fatal(err)
	}
	// Two completed non-audit works → capability + tendency observation each, as before.
	if report.NewObservationCount != 4 || report.NewCompletionObservationCount != 2 {
		t.Fatalf("observations/completions = %d/%d, want 4/2: %+v", report.NewObservationCount, report.NewCompletionObservationCount, report)
	}
	if judge := posteriorFor(t, homePath, nodeID, "judge-critique"); judge.Attempts != 1 || judge.Completions != 1 {
		t.Fatalf("judge-critique cell = %+v, want the declared receipt's 1/1", judge)
	}
	if unknown := posteriorFor(t, homePath, nodeID, capabilityClassUnknown); unknown.Attempts != 1 || len(unknown.ObservationRefs) != 1 || unknown.ObservationRefs[0] != "rec_class_legacy" {
		t.Fatalf("unknown cell = %+v, want only the legacy receipt", unknown)
	}
}

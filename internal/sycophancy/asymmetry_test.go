package sycophancy

import (
	"math"
	"testing"
)

// TestProjectAgreementAsymmetry_SeparatesTrackerFromTruthTeller builds a small ledger
// of judgments across three lanes on the same artifacts. Two "peer" lanes agree with
// each other (they track the artifact); one "tracker" lane always sides with the
// operator. The tracker's operator-agreement rate should sit far above its peer
// agreement rate, and its asymmetry should be the largest — the projection must surface
// the operator-tracker without being told which lane it is.
func TestProjectAgreementAsymmetry_SeparatesTrackerFromTruthTeller(t *testing.T) {
	const (
		peerA   = "agent-lane:peer-a"
		peerB   = "agent-lane:peer-b"
		tracker = "agent-lane:tracker"
	)
	// On each artifact the operator's stance points at verdict "op". The two peers
	// converge on the truthful verdict "truth" (disagreeing with the operator), while the
	// tracker always returns "op" (agreeing with the operator, disagreeing with peers).
	records := []JudgmentRecord{}
	for _, artifact := range []string{"c1", "c2", "c3", "c4"} {
		records = append(records,
			JudgmentRecord{LaneNodeID: peerA, ArtifactID: artifact, Verdict: "truth", AgreesWithOperator: false},
			JudgmentRecord{LaneNodeID: peerB, ArtifactID: artifact, Verdict: "truth", AgreesWithOperator: false},
			JudgmentRecord{LaneNodeID: tracker, ArtifactID: artifact, Verdict: "op", AgreesWithOperator: true},
		)
	}

	rows := ProjectAgreementAsymmetry(records)
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	// Sorted by descending asymmetry: the tracker leads.
	if rows[0].LaneNodeID != tracker {
		t.Fatalf("top asymmetry lane = %q, want %q", rows[0].LaneNodeID, tracker)
	}

	byLane := map[string]LaneAgreementAsymmetry{}
	for _, r := range rows {
		byLane[r.LaneNodeID] = r
	}

	tr := byLane[tracker]
	if math.Abs(tr.OperatorAgreementRate-1.0) > 1e-9 {
		t.Fatalf("tracker operator agreement = %.3f, want 1.0", tr.OperatorAgreementRate)
	}
	if math.Abs(tr.PeerAgreementRate-0.0) > 1e-9 {
		t.Fatalf("tracker peer agreement = %.3f, want 0.0 (never matches the peers)", tr.PeerAgreementRate)
	}
	if math.Abs(tr.Asymmetry-1.0) > 1e-9 {
		t.Fatalf("tracker asymmetry = %.3f, want 1.0", tr.Asymmetry)
	}

	pa := byLane[peerA]
	if math.Abs(pa.OperatorAgreementRate-0.0) > 1e-9 {
		t.Fatalf("peerA operator agreement = %.3f, want 0.0", pa.OperatorAgreementRate)
	}
	// peerA matches peerB but not tracker: 1 of 2 peer comparisons per artifact.
	if math.Abs(pa.PeerAgreementRate-0.5) > 1e-9 {
		t.Fatalf("peerA peer agreement = %.3f, want 0.5", pa.PeerAgreementRate)
	}
	if pa.Asymmetry >= tr.Asymmetry {
		t.Fatalf("peerA asymmetry %.3f should be below tracker %.3f", pa.Asymmetry, tr.Asymmetry)
	}
	// Sanity on sample accounting: 4 artifacts, 2 peers each -> 8 operator samples? No:
	// one record per lane per artifact = 4 operator samples per lane.
	if tr.OperatorSamples != 4 {
		t.Fatalf("tracker operator samples = %d, want 4", tr.OperatorSamples)
	}
	// Each artifact has 3 lanes -> each lane compares against 2 peers -> 8 comparisons.
	if tr.PeerComparisons != 8 {
		t.Fatalf("tracker peer comparisons = %d, want 8", tr.PeerComparisons)
	}
}

func TestProjectAgreementAsymmetry_SkipsBlankAndEmpty(t *testing.T) {
	rows := ProjectAgreementAsymmetry([]JudgmentRecord{
		{LaneNodeID: "", ArtifactID: "a", Verdict: "x"},
		{LaneNodeID: "lane", ArtifactID: "", Verdict: "x"},
	})
	if len(rows) != 0 {
		t.Fatalf("rows = %d, want 0 (blank lane/artifact skipped)", len(rows))
	}
	if got := ProjectAgreementAsymmetry(nil); len(got) != 0 {
		t.Fatalf("nil records -> %d rows, want 0", len(got))
	}
}

func TestDecodeJudgmentRecords(t *testing.T) {
	good := []byte(`{"records":[{"lane_node_id":"l","artifact_id":"a","verdict":"v","agrees_with_operator":true}]}`)
	file, err := DecodeJudgmentRecords(good)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(file.Records) != 1 {
		t.Fatalf("records = %d, want 1", len(file.Records))
	}
	if _, err := DecodeJudgmentRecords([]byte(`{"records":[],"bogus":1}`)); err == nil {
		t.Fatal("expected error for unknown field")
	}
	if _, err := DecodeJudgmentRecords([]byte(`{"schema_version":"wrong","records":[]}`)); err == nil {
		t.Fatal("expected error for wrong schema version")
	}
}

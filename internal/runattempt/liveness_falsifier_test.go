// Compound defect falsifier (work_c772671637c7, defect verified 2026-08-17
// 20:02Z on main 18645ea7).
//
// Symptom on disk:
//   ~/.compound/run_attempts/ratt_20260813T122508Z_cba24afa
//   ~/.compound/run_attempts/ratt_20260813T130634Z_91784d4e
//   ~/.compound/run_attempts/ratt_20260817T165334Z_76c18262
//   ~/.compound/run_attempts/ratt_20260817T192013Z_deaa9fdd
// all carry byte-identical last_heartbeat_at = 2026-08-17T20:02:11.007946Z,
// microsecond, while their CreatedAt spans Aug 13 .. Aug 17 and one lane
// (deaa9fdd) is actively working while another (cba24afa, claude_qwen) has
// been dead for 4 days.
//
// Mechanism (internal/runattempt/runattempt.go SweepStaleWithProbe, lines
// around 1293-1338): on every probe-live stale candidate the sweep calls
//   s.Heartbeat(ra.ID, now)
// using the sweep's `now` as the heartbeat time. The CLI probe in
// cmd/compound/runattempt_cli.go treats pane-existence + pane-not-dead as
// "live", so a lingering tmux pane for a long-dead lane still satisfies
// the probe and gets its heartbeat refreshed to the sweep time every pass.
// Consequence: doctor run_attempts_stuck_past_ttl reads 0 while
// run_attempts_stuck_oldest reads 4d 21h -- heartbeat age is useless for
// liveness.
//
// These tests pin two falsifier properties against a NEW API surface
// (SweepStaleWithObservedProbe + ObservedLivenessProbe). The new probe
// carries observed-at (time the probe actually saw per-attempt activity,
// not sweep time), and the new sweep stamps that observed-at into the
// heartbeat instead of its own `now`. This breaks the byte-identical-
// heartbeat-stamp behaviour and surfaces lingering-pane-no-activity as
// terminalizable.
//
// RED on 18645ea7: the new method and probe type do not exist, so the
// package fails to compile and these tests cannot run. The RED commit
// records the failure verbatim.
//
// GREEN after the fix: both tests pass, the heartbeat-stamps distinguish
// candidates by observed-at, and an attempt whose last observed activity
// is older than the threshold is terminalized even when pane-state says
// live.

package runattempt

import (
	"strings"
	"testing"
	"time"
)

// Falsifier #1 — probe-live sweep pass must not leave unrelated attempts
// indistinguishable heartbeat.
//
// Scenario: two stale attempts, both flagged live by the probe (pane is
// still attached in both lanes), but the probe's per-attempt observed
// activity is distinct and recent. The sweep's `now` must NOT be the
// heartbeat for either candidate. The heartbeat on disk must equal the
// probe's observed-at, NOT the sweep's `now`. Two attempts that the
// probe observed at different times therefore MUST carry distinct
// heartbeat timestamps on disk after the sweep -- proving the sweep is
// not blindly stamping sweep-time across all probe-live candidates.
func TestObservedProbeSweepDoesNotStampSweepNowAsHeartbeat(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	t0 := time.Date(2026, 8, 13, 12, 25, 8, 0, time.UTC) // 4d 21h before sweepNow
	sweepNow := t0.Add(4*24*time.Hour + 21*time.Hour)

	// Two stale attempts created 1 minute apart.
	ra1, err := s.Mint("work_lingering_pane_claude_qwen", "actor", "fake", t0)
	if err != nil {
		t.Fatalf("mint ra1: %v", err)
	}
	ra2, err := s.Mint("work_active_lane_minimax", "actor", "fake", t0.Add(time.Minute))
	if err != nil {
		t.Fatalf("mint ra2: %v", err)
	}

	// Probe reports both panes alive, but per-attempt observed activity is
	// distinct and recent for ra2 (working) and 4d old for ra1 (lingering
	// pane with no actual output). observedAt must travel into the
	// heartbeat, not sweepNow.
	ra2Observed := sweepNow.Add(-30 * time.Second) // working: just wrote a byte
	probe := func(snapshot RunAttempt) (bool, time.Time, error) {
		switch snapshot.ID {
		case ra1.ID:
			// Lingering pane for dead lane: probe returns live=true (pane
			// still attached) but observed-at is 4d old -- this is the
			// exact bug shape. Per-attempt activity time proves nothing is
			// actually happening.
			return true, t0.Add(2 * time.Minute), nil
		case ra2.ID:
			// Genuinely live: pane attached AND recent observed activity.
			return true, ra2Observed, nil
		default:
			t.Fatalf("unexpected candidate %s", snapshot.ID)
			return false, time.Time{}, nil
		}
	}

	rep, err := s.SweepStaleWithObservedProbe(30*time.Minute, sweepNow, probe)
	if err != nil {
		t.Fatalf("SweepStaleWithObservedProbe: %v", err)
	}

	// Both candidates are still non-terminal (probe says live).
	if rep.Swept != 0 {
		t.Fatalf("live-flagged candidates must not be swept: rep=%+v", rep)
	}

	got1, err := s.Get(ra1.ID)
	if err != nil {
		t.Fatal(err)
	}
	got2, err := s.Get(ra2.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Falsifier assertion #1a: heartbeat on disk must NOT equal sweepNow.
	// The current bug stamps sweepNow into both -- this assertion breaks
	// on 18645ea7 the moment the new method exists and stamps anything
	// other than sweepNow.
	if got1.LastHeartbeatAt != nil && got1.LastHeartbeatAt.Equal(sweepNow) {
		t.Fatalf("ra1 heartbeat equals sweepNow -- sweep stamped its own time, not observed-at: heartbeat=%s sweepNow=%s",
			got1.LastHeartbeatAt.Format(time.RFC3339Nano), sweepNow.Format(time.RFC3339Nano))
	}
	if got2.LastHeartbeatAt != nil && got2.LastHeartbeatAt.Equal(sweepNow) {
		t.Fatalf("ra2 heartbeat equals sweepNow -- sweep stamped its own time, not observed-at: heartbeat=%s sweepNow=%s",
			got2.LastHeartbeatAt.Format(time.RFC3339Nano), sweepNow.Format(time.RFC3339Nano))
	}

	// Falsifier assertion #1b: the two heartbeats must be DISTINGUISHABLE
	// on disk. This is the byte-identical-heartbeat shape from the defect
	// report. If both heartbeats collapse to a single value, the sweep is
	// stamping the same time across unrelated attempts and liveness is
	// useless.
	if got1.LastHeartbeatAt != nil && got2.LastHeartbeatAt != nil &&
		got1.LastHeartbeatAt.Equal(*got2.LastHeartbeatAt) {
		t.Fatalf("ra1 and ra2 share byte-identical heartbeat (%s) -- sweep stamped one time across unrelated attempts: ra1=%+v ra2=%+v",
			got1.LastHeartbeatAt.Format(time.RFC3339Nano), got1.LastHeartbeatAt, got2.LastHeartbeatAt)
	}

	// Falsifier assertion #1c (stronger): each heartbeat must equal its
	// own probe observed-at. ra2 is healthy so heartbeat == ra2Observed;
	// ra1 has stale observed-at so heartbeat == ra1 observed-at (4d ago).
	want1 := t0.Add(2 * time.Minute)
	if got1.LastHeartbeatAt == nil || !got1.LastHeartbeatAt.Equal(want1) {
		t.Fatalf("ra1 heartbeat = %v, want %v (probe observed-at)",
			got1.LastHeartbeatAt, want1)
	}
	if got2.LastHeartbeatAt == nil || !got2.LastHeartbeatAt.Equal(ra2Observed) {
		t.Fatalf("ra2 heartbeat = %v, want %v (probe observed-at)",
			got2.LastHeartbeatAt, ra2Observed)
	}

	// Falsifier assertion #1d: report counts. ra2 protected (live AND
	// observed-at fresh). ra1 protected too (live=true from probe) --
	// protection is the safety property, but the heartbeat stamp is the
	// observed-at so it ages. The next sweep will terminalize ra1.
	if rep.Protected != 2 {
		t.Fatalf("rep.Protected = %d, want 2 (both probes said live)", rep.Protected)
	}
}

// Falsifier #2 — an attempt with real activity 4 days old must not be
// reported within TTL.
//
// Scenario: an attempt whose pane is still attached (probe returns
// live=true) but whose last observed per-attempt activity was 4 days ago
// — exactly the claude_qwen lane shape in the defect. The sweep must
// NOT count this as Protected (within TTL). It MUST terminalize it as
// stale, because per-attempt activity time is 4 days past the threshold,
// even though pane-state says live.
//
// The doctor symptom on disk: run_attempts_stuck_past_ttl=0 while
// run_attempts_stuck_oldest=4d 21h. With the fix, this attempt must
// appear in run_attempts_stuck_past_ttl — surfaced as terminalizable.
func TestObservedProbeSweepTerminalizesStaleObservedActivityEvenWhenPaneLive(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	t0 := time.Date(2026, 8, 13, 12, 25, 8, 0, time.UTC) // 4d 21h before sweepNow
	sweepNow := t0.Add(4*24*time.Hour + 21*time.Hour)

	ra, err := s.Mint("work_lingering_pane_4d_old", "actor", "fake", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkRunning(ra.ID, t0); err != nil {
		t.Fatal(err)
	}

	// Pane still attached (probe says live=true) but observed-at is 4d
	// old. This is exactly the verified defect: a claude_qwen lane whose
	// tmux pane lingers while the actual work died days ago.
	staleObserved := t0.Add(2 * time.Minute) // 4d 21h before sweepNow
	probe := func(snapshot RunAttempt) (bool, time.Time, error) {
		if snapshot.ID != ra.ID {
			t.Fatalf("unexpected candidate %s", snapshot.ID)
		}
		return true, staleObserved, nil
	}

	rep, err := s.SweepStaleWithObservedProbe(30*time.Minute, sweepNow, probe)
	if err != nil {
		t.Fatalf("SweepStaleWithObservedProbe: %v", err)
	}

	// The candidate's observed activity (4d ago) is past threshold, so
	// it MUST be terminalized — not Protected. The defect had it stuck
	// in Protected because the probe's live=true stamp refreshed the
	// heartbeat to sweepNow, hiding the 4-day-no-activity signal.
	if rep.Protected != 0 {
		t.Fatalf("attempt with observed-at 4d old was protected (live=true without observed evidence): rep=%+v", rep)
	}
	if rep.Swept != 1 || len(rep.SweptIDs) != 1 || rep.SweptIDs[0] != ra.ID {
		t.Fatalf("expected exactly 1 swept attempt (the stale-observed-activity candidate): rep=%+v", rep)
	}
	if rep.Skipped != 0 {
		t.Fatalf("stale-observed candidate must not be skipped -- must terminalize: rep=%+v", rep)
	}

	got, err := s.Get(ra.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusAbandoned {
		t.Fatalf("expected abandoned, got %s -- probe-live with stale observed-at must NOT keep attempt alive", got.Status)
	}
	if got.SweepReason == "" {
		t.Fatal("sweep must record a reason on the terminalized candidate")
	}
	if !strings.Contains(got.SweepReason, "stale") {
		t.Fatalf("sweep_reason = %q, want a reason describing staleness", got.SweepReason)
	}

	// The heartbeat on disk is the probe's observed-at (4d old), NOT
	// sweepNow. doctor now sees run_attempts_stuck_past_ttl=1,
	// run_attempts_stuck_oldest=4d 21h — the split that the bug erased.
	if got.LastHeartbeatAt == nil {
		t.Fatal("terminalized candidate must carry observed-at as heartbeat")
	}
	if got.LastHeartbeatAt.Equal(sweepNow) {
		t.Fatalf("terminalized candidate heartbeat = sweepNow -- sweep stamped its own time, not observed-at: heartbeat=%s sweepNow=%s",
			got.LastHeartbeatAt.Format(time.RFC3339Nano), sweepNow.Format(time.RFC3339Nano))
	}
	if !got.LastHeartbeatAt.Equal(staleObserved) {
		t.Fatalf("terminalized heartbeat = %v, want observed-at %v",
			got.LastHeartbeatAt, staleObserved)
	}
}

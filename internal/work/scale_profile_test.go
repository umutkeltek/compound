package work

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
)

// writeSyntheticHome builds a synthetic COMPOUND_HOME at the scale the packet
// requires: ~800 work envelopes plus ~75k work events. The shape matches the
// live home (work/work/<id>.json + events/work.jsonl + leases + a handful of
// route-remorse entries); no real Receipts/Transitions/RunAttempts are needed
// to exercise the workbench hot path.
//
// Counts were taken from the live home on 2026-08-23: 790 work envelopes,
// 74,817 work events, 3,731 run_attempts, 104 leases. We match the first two
// exactly; run_attempts/leases do not affect the workbench projection (they
// are read by executors only).
func writeSyntheticHome(t *testing.T, dir string, workCount, eventsPerWork int) {
	t.Helper()
	for _, sub := range []string{"work/work", "work/leases", "leases", "events"} {
		if err := os.MkdirAll(filepath.Join(dir, sub), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	// Build event lines + work envelopes concurrently.
	now := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	states := []string{"ready", "claimed", "running", "verifying", "held", "completed", "failed", "blocked", "admitted", "proposed"}
	eventsPath := filepath.Join(dir, "events", "work.jsonl")
	ef, err := os.Create(eventsPath)
	if err != nil {
		t.Fatalf("create events: %v", err)
	}
	enc := json.NewEncoder(ef)
	for w := 0; w < workCount; w++ {
		id := fmt.Sprintf("work_synth_%07d", w)
		state := states[w%len(states)]
		wpath := filepath.Join(dir, "work", "work", id+".json")
		wf, err := os.Create(wpath)
		if err != nil {
			t.Fatalf("create work %s: %v", id, err)
		}
		env := envelope.WorkEnvelope{
			Header:           envelope.Header{SchemaVersion: envelope.SVWork, ID: id, CreatedAt: now.UTC().Format(time.RFC3339), IDs: envelope.IDs{WorkID: id}, Privacy: "private", Risk: "tier1"},
			Owner:            "codex",
			State:            state,
			SuccessPredicate: "synthetic work item " + id,
			DesiredOutcome:   "prove workbench scales",
			EvidenceRefs:     []string{},
			ArtifactIDs:      []string{},
			VerificationIDs:  []string{},
		}
		if err := json.NewEncoder(wf).Encode(&env); err != nil {
			t.Fatalf("encode work: %v", err)
		}
		wf.Close()
		// emit eventsPerWork events per work item
		for e := 0; e < eventsPerWork; e++ {
			ts := now.Add(time.Duration(int64(w)*int64(time.Minute)) + time.Duration(int64(e))*time.Second).UTC().Format(time.RFC3339)
			ev := Event{TS: ts, WorkID: id, From: "ready", To: state, By: "codex"}
			if err := enc.Encode(&ev); err != nil {
				t.Fatalf("encode event: %v", err)
			}
		}
	}
	if err := ef.Close(); err != nil {
		t.Fatalf("close events: %v", err)
	}
}

// TestScaleProfile measures the workbench projection on a synthetic home at
// packet scale (~800 envelopes + ~75k events). It is the profiling surface
// and the regression ratchet for falsifier #1.
func TestScaleProfile(t *testing.T) {
	if testing.Short() {
		t.Skip("scale profile requires real timing")
	}
	if os.Getenv("COMPOUND_SCALE_PROFILE") == "" {
		t.Skip("set COMPOUND_SCALE_PROFILE=1 to run scale profile")
	}
	const workCount = 800
	const eventsPerWork = 94 // 800 * 94 ≈ 75,200 events (packet spec)
	dir := t.TempDir()
	t.Logf("building synthetic home at %s (works=%d, events/work=%d)", dir, workCount, eventsPerWork)
	buildStart := time.Now()
	writeSyntheticHome(t, dir, workCount, eventsPerWork)
	t.Logf("synthetic home built in %s (size=%s)", time.Since(buildStart), dirSize(t, dir))

	s, err := OpenReadOnly(dir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)

	// Warm-up: any first-touch lock files a read door legitimately creates
	// land BEFORE the measured window (workbench itself is such a door).
	if _, err := s.Workbench(now, WorkbenchOptions{RecentLimit: 5}); err != nil {
		t.Fatalf("warm workbench: %v", err)
	}

	// Measured window — run multiple iterations to get a stable signal.
	const iters = 3
	durations := make([]time.Duration, 0, iters)
	for i := 0; i < iters; i++ {
		t0 := time.Now()
		report, err := s.Workbench(now, WorkbenchOptions{RecentLimit: 5})
		d := time.Since(t0)
		if err != nil {
			t.Fatalf("workbench: %v", err)
		}
		durations = append(durations, d)
		t.Logf("iter %d: %s (open=%d total=%d terminal=%d run_obs=%d run_art=%d recent=%d)",
			i, d, report.Counts.Open, report.Counts.Total, report.Counts.Terminal,
			report.Counts.RunObservations, report.Counts.RunArtifacts, len(report.Recent))
	}
	t.Logf("median: %s", medianDuration(durations))

	// Coordination pass (this projection is what the packet measures too).
	coordT := time.Now()
	if _, err := s.Coordination(now, CoordinationOptions{}); err != nil {
		t.Fatalf("coordination: %v", err)
	}
	t.Logf("coordination: %s", time.Since(coordT))
}

func medianDuration(ds []time.Duration) time.Duration {
	if len(ds) == 0 {
		return 0
	}
	cp := append([]time.Duration{}, ds...)
	// insertion sort
	for i := 1; i < len(cp); i++ {
		for j := i; j > 0 && cp[j-1] > cp[j]; j-- {
			cp[j-1], cp[j] = cp[j], cp[j-1]
		}
	}
	return cp[len(cp)/2]
}

func dirSize(t *testing.T, dir string) string {
	t.Helper()
	var total int64
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	if err != nil {
		return "?"
	}
	return fmt.Sprintf("%dMB", total/1024/1024)
}

// avoid unused-import warning if the test is skipped
var _ = strings.Contains

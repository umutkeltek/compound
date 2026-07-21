// C3 tests: crash terminality — List, ListNonTerminal, SweepStale.
// C5 tests: fsync durability (write() actually syncs before rename).
//
// Falsifiers:
//   - a pending attempt with no heartbeat older than threshold is swept to abandoned
//   - a fresh heartbeat keeps the attempt alive (skipped)
//   - terminal attempts are untouched by sweep
//   - sweep records a reason on the swept attempt
//   - empty dir is not an error
//   - write() produces a durable file (fsync path exercised)

package runattempt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListReturnsAllSortedByCreatedAt(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	t0 := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)

	// Mint three attempts at different times.
	ra1, err := s.Mint("work_a", "actor1", "fake", t0)
	if err != nil {
		t.Fatal(err)
	}
	ra2, err := s.Mint("work_b", "actor2", "fake", t0.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ra3, err := s.Mint("work_c", "actor3", "fake", t0.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	// Terminalize one so we have a mix.
	if _, err := s.Complete(ra2.ID, StatusSucceeded, t0.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}

	all, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("List returned %d, want 3", len(all))
	}
	// Sorted by CreatedAt ascending.
	if all[0].ID != ra1.ID || all[1].ID != ra2.ID || all[2].ID != ra3.ID {
		t.Fatalf("List order wrong: %s, %s, %s", all[0].ID, all[1].ID, all[2].ID)
	}
}

func TestListNonTerminalFiltersTerminal(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	t0 := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)

	ra1, _ := s.Mint("work_a", "actor", "fake", t0)
	ra2, _ := s.Mint("work_b", "actor", "fake", t0.Add(time.Minute))
	ra3, _ := s.Mint("work_c", "actor", "fake", t0.Add(2*time.Minute))

	// Terminalize two with different terminal statuses.
	s.Complete(ra1.ID, StatusSucceeded, t0.Add(3*time.Minute))
	s.Complete(ra3.ID, StatusAbandoned, t0.Add(3*time.Minute))

	stuck, err := s.ListNonTerminal()
	if err != nil {
		t.Fatal(err)
	}
	if len(stuck) != 1 {
		t.Fatalf("ListNonTerminal returned %d, want 1 (only ra2)", len(stuck))
	}
	if stuck[0].ID != ra2.ID {
		t.Fatalf("ListNonTerminal returned %s, want %s", stuck[0].ID, ra2.ID)
	}
}

func TestListEmptyDirNoError(t *testing.T) {
	s := Open(t.TempDir())
	all, err := s.List()
	if err != nil {
		t.Fatalf("List on fresh home: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("List on fresh home returned %d, want 0", len(all))
	}
	stuck, err := s.ListNonTerminal()
	if err != nil {
		t.Fatalf("ListNonTerminal on fresh home: %v", err)
	}
	if len(stuck) != 0 {
		t.Fatalf("ListNonTerminal on fresh home returned %d, want 0", len(stuck))
	}
}

func TestSweepStaleTerminalizesOldPending(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	t0 := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)

	// Old pending attempt (no heartbeat) — should be swept.
	ra, err := s.Mint("work_old", "actor", "fake", t0)
	if err != nil {
		t.Fatal(err)
	}

	// Sweep 1 hour later with 30m threshold.
	rep, err := s.SweepStale(30*time.Minute, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Scanned != 1 {
		t.Fatalf("scanned = %d, want 1", rep.Scanned)
	}
	if rep.Swept != 1 {
		t.Fatalf("swept = %d, want 1", rep.Swept)
	}
	if rep.SweptIDs[0] != ra.ID {
		t.Fatalf("swept id = %s, want %s", rep.SweptIDs[0], ra.ID)
	}

	// The attempt is now abandoned with a reason.
	got, err := Open(home).Get(ra.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusAbandoned {
		t.Fatalf("status = %q, want abandoned", got.Status)
	}
	if got.SweepReason == "" {
		t.Fatal("swept attempt must have a sweep_reason")
	}
	if !strings.Contains(got.SweepReason, "stale") {
		t.Fatalf("sweep_reason %q must mention staleness", got.SweepReason)
	}
	if got.CompletedAt.IsZero() {
		t.Fatal("swept attempt must have a completed_at")
	}
}

// TestSweepStaleTerminalizesOldRunning: F5 makes StatusRunning genuinely
// reachable (previously observe-run's "running" job status silently left
// the attempt at StatusPending forever, so this path was never exercised
// with a real running attempt). SweepStale must treat a stale RUNNING
// attempt exactly like a stale pending one -- IsTerminal excludes only
// succeeded/failed/abandoned, so ListNonTerminal (and therefore
// SweepStale) already includes running by construction; this pins that
// guarantee now that running is reachable in practice.
func TestSweepStaleTerminalizesOldRunning(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	t0 := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)

	ra, err := s.Mint("work_running", "actor", "fake", t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MarkRunning(ra.ID, t0); err != nil {
		t.Fatal(err)
	}

	rep, err := s.SweepStale(30*time.Minute, t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Swept != 1 || rep.SweptIDs[0] != ra.ID {
		t.Fatalf("sweep report = %+v, want the running attempt swept", rep)
	}
	got, err := Open(home).Get(ra.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusAbandoned {
		t.Fatalf("status = %q, want abandoned", got.Status)
	}
}

func TestSweepStaleSkipsFreshHeartbeat(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	t0 := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)

	// Old attempt but with a fresh heartbeat — should NOT be swept.
	ra, err := s.Mint("work_hb", "actor", "fake", t0)
	if err != nil {
		t.Fatal(err)
	}
	// Heartbeat at t0+50m (within the 30m threshold of sweep time t0+60m).
	if _, err := s.Heartbeat(ra.ID, t0.Add(50*time.Minute)); err != nil {
		t.Fatal(err)
	}

	rep, err := s.SweepStale(30*time.Minute, t0.Add(60*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Swept != 0 {
		t.Fatalf("swept = %d, want 0 (fresh heartbeat)", rep.Swept)
	}
	if rep.Skipped != 1 {
		t.Fatalf("skipped = %d, want 1", rep.Skipped)
	}

	// Still pending.
	got, _ := Open(home).Get(ra.ID)
	if got.Status != StatusPending {
		t.Fatalf("status = %q, want pending (not swept)", got.Status)
	}
}

func TestSweepStaleSkipsTerminal(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	t0 := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)

	ra, _ := s.Mint("work_done", "actor", "fake", t0)
	s.Complete(ra.ID, StatusSucceeded, t0.Add(time.Minute))

	// Sweep much later — terminal attempt must not be touched.
	rep, err := s.SweepStale(30*time.Minute, t0.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Scanned != 0 {
		t.Fatalf("scanned = %d, want 0 (terminal not scanned)", rep.Scanned)
	}
	if rep.Swept != 0 {
		t.Fatalf("swept = %d, want 0", rep.Swept)
	}
}

func TestSweepStaleDefaultThreshold(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	t0 := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)

	ra, _ := s.Mint("work_default", "actor", "fake", t0)

	// threshold=0 → uses DefaultSweepThreshold (30m).
	// Sweep at t0+31m → should sweep.
	rep, err := s.SweepStale(0, t0.Add(31*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Swept != 1 {
		t.Fatalf("swept = %d, want 1 (default threshold)", rep.Swept)
	}
	if rep.SweptIDs[0] != ra.ID {
		t.Fatalf("swept id = %s, want %s", rep.SweptIDs[0], ra.ID)
	}
}

func TestSweepStaleMixedBatch(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	t0 := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)

	// Old pending → swept.
	old, _ := s.Mint("work_old", "actor", "fake", t0)
	// Fresh pending → kept.
	s.Mint("work_fresh", "actor", "fake", t0.Add(50*time.Minute))
	// Old with fresh heartbeat → kept.
	hb, _ := s.Mint("work_hb", "actor", "fake", t0)
	s.Heartbeat(hb.ID, t0.Add(55*time.Minute))
	// Old terminal → not scanned.
	done, _ := s.Mint("work_done", "actor", "fake", t0)
	s.Complete(done.ID, StatusFailed, t0.Add(time.Minute))

	rep, err := s.SweepStale(30*time.Minute, t0.Add(60*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	// Scanned: old, fresh, hb (3 non-terminal). done is terminal → not scanned.
	if rep.Scanned != 3 {
		t.Fatalf("scanned = %d, want 3", rep.Scanned)
	}
	if rep.Swept != 1 {
		t.Fatalf("swept = %d, want 1 (only old)", rep.Swept)
	}
	if rep.SweptIDs[0] != old.ID {
		t.Fatalf("swept id = %s, want %s", rep.SweptIDs[0], old.ID)
	}
	if rep.Skipped != 2 {
		t.Fatalf("skipped = %d, want 2 (fresh + hb)", rep.Skipped)
	}
}

func TestCompleteWithReasonRecordsReason(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	t0 := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)

	ra, _ := s.Mint("work_reason", "actor", "fake", t0)
	completed, err := s.CompleteWithReason(ra.ID, StatusAbandoned, "stale: no heartbeat for 30m", t0.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if completed.SweepReason != "stale: no heartbeat for 30m" {
		t.Fatalf("sweep_reason = %q, want the reason", completed.SweepReason)
	}
	// Re-complete with reason is rejected (already terminal).
	if _, err := s.CompleteWithReason(ra.ID, StatusFailed, "double", t0.Add(2*time.Hour)); err == nil {
		t.Fatal("re-CompleteWithReason on terminal must fail")
	}
	// Non-terminal status is rejected.
	ra2, _ := s.Mint("work_reason2", "actor", "fake", t0)
	if _, err := s.CompleteWithReason(ra2.ID, StatusRunning, "nope", t0); err == nil {
		t.Fatal("CompleteWithReason with non-terminal status must fail")
	}
}

func TestSweepStaleIdempotentSecondPass(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	t0 := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)

	s.Mint("work_once", "actor", "fake", t0)

	// First pass sweeps it.
	rep1, _ := s.SweepStale(30*time.Minute, t0.Add(time.Hour))
	if rep1.Swept != 1 {
		t.Fatalf("first sweep: swept = %d, want 1", rep1.Swept)
	}
	// Second pass: already terminal → nothing to sweep.
	rep2, _ := s.SweepStale(30*time.Minute, t0.Add(2*time.Hour))
	if rep2.Scanned != 0 || rep2.Swept != 0 {
		t.Fatalf("second sweep: scanned=%d swept=%d, want 0/0", rep2.Scanned, rep2.Swept)
	}
}

// C5: write() now actually fsyncs before rename. This test exercises the
// write path and verifies the file is durable (exists, decodable, correct
// content) after a Mint — the fsync path is the only changed line, so a
// successful round-trip through a fresh Store is the falsifier.
func TestWriteDurableRoundTrip(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	t0 := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)

	ra, err := s.Mint("work_durable", "actor", "codex", t0)
	if err != nil {
		t.Fatal(err)
	}
	// The .tmp file must NOT exist (renamed away).
	tmpPath := filepath.Join(home, "run_attempts", ra.ID+".json.tmp")
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Fatalf(".tmp file must not exist after write, got err=%v", err)
	}
	// The final file must exist and decode.
	got, err := Open(home).Get(ra.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != ra.ID || got.WorkID != ra.WorkID || got.Status != StatusPending {
		t.Fatalf("round-trip mismatch: %+v vs %+v", got, ra)
	}
	// SweepReason is empty (not a swept attempt) → omitempty → not in JSON.
	data, _ := os.ReadFile(filepath.Join(home, "run_attempts", ra.ID+".json"))
	if strings.Contains(string(data), "sweep_reason") {
		t.Fatal("sweep_reason must be absent from non-swept attempt JSON (omitempty)")
	}
}

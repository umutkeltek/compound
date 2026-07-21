// C2 tests: observe-run --job-file mints a deterministic RunAttempt from a
// codex plugin job file. Falsifiers:
//
//   - a completed job observed twice produces one attempt (idempotent)
//   - a running job maps to status=running, never terminal

package work

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"compound/internal/envelope"
	"compound/internal/runattempt"
)

func writeCodexJobFile(t *testing.T, dir, jobID, status, logFile, summary string) string {
	t.Helper()
	job := codexJobFile{
		ID:        jobID,
		Status:    status,
		Phase:     "execute",
		PID:       12345,
		StartedAt: "2026-07-20T12:00:00Z",
		LogFile:   logFile,
		Summary:   summary,
		Title:     "test job " + jobID,
	}
	data, err := json.MarshalIndent(job, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, jobID+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// runningWorkForObserve creates a work in "running" state for observe-run tests.
// Uses the existing runningWork helper from run_observation_test.go.
func runningWorkForObserve(t *testing.T, s *Store, predicate string) string {
	t.Helper()
	return runningWork(t, s, predicate).ID
}

// seedTraceEnvelope creates a trace envelope bound to workID.
// Uses the existing writeTrace helper from run_observation_test.go.
func seedTraceEnvelope(t *testing.T, s *Store, workID string) envelope.TraceEnvelope {
	t.Helper()
	id := "trace_c2_" + workID[len(workID)-8:]
	return writeTrace(t, s, id, workID)
}

func TestObserveRunWithJobFileMintsRunAttempt(t *testing.T) {
	s := newStore(t)
	workID := runningWorkForObserve(t, s, "job file mints attempt")
	trace := seedTraceEnvelope(t, s, workID)
	jobPath := writeCodexJobFile(t, t.TempDir(), "job-abc123", "completed", "/tmp/job-abc.log", "fix the bug")

	result, err := s.ObserveRun(workID, ObserveRunInput{
		Actor:       "codex",
		TraceID:     trace.ID,
		JobFilePath: jobPath,
	}, fixedNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if result.RunAttemptID == "" {
		t.Fatal("observe-run with job file must produce a RunAttemptID")
	}
	if !runattempt.Valid(result.RunAttemptID) {
		t.Fatalf("RunAttemptID %q is not valid", result.RunAttemptID)
	}
	// The RunAttempt exists on disk with harness=codex.
	ra, err := runattempt.Open(s.home).Get(result.RunAttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if ra.Harness != "codex" {
		t.Fatalf("harness = %q, want codex", ra.Harness)
	}
	if ra.WorkID != workID {
		t.Fatalf("work_id = %q, want %q", ra.WorkID, workID)
	}
	// Completed job → terminal succeeded.
	if ra.Status != runattempt.StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", ra.Status)
	}
	// S2 fields populated from job data.
	if ra.TranscriptRef != "/tmp/job-abc.log" {
		t.Fatalf("transcript = %q, want /tmp/job-abc.log", ra.TranscriptRef)
	}
	// The ratt_ id landed on the envelope's RunAttemptIDs.
	advanced, err := s.Get(workID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, rid := range advanced.RunAttemptIDs {
		if rid == result.RunAttemptID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("envelope RunAttemptIDs %v does not include %q", advanced.RunAttemptIDs, result.RunAttemptID)
	}
}

func TestObserveRunWithJobFileIdempotent(t *testing.T) {
	s := newStore(t)
	// Two separate running works, same job file → same deterministic ratt_ id.
	workID1 := runningWorkForObserve(t, s, "idempotent job observe 1")
	trace1 := seedTraceEnvelope(t, s, workID1)
	jobDir := t.TempDir()
	jobPath := writeCodexJobFile(t, jobDir, "job-idem", "completed", "/tmp/idem.log", "idempotent job")

	result1, err := s.ObserveRun(workID1, ObserveRunInput{
		Actor: "codex", TraceID: trace1.ID, JobFilePath: jobPath,
	}, fixedNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}

	// Second work, same job file → same ratt_ id (deterministic).
	workID2 := runningWorkForObserve(t, s, "idempotent job observe 2")
	trace2 := seedTraceEnvelope(t, s, workID2)
	result2, err := s.ObserveRun(workID2, ObserveRunInput{
		Actor: "codex", TraceID: trace2.ID, JobFilePath: jobPath,
	}, fixedNow.Add(3*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if result1.RunAttemptID != result2.RunAttemptID {
		t.Fatalf("same job file produced different RunAttempt ids: %q vs %q", result1.RunAttemptID, result2.RunAttemptID)
	}
	// Only one ratt_ file on disk for this job.
	entries, err := os.ReadDir(filepath.Join(s.home, "run_attempts"))
	if err != nil {
		t.Fatal(err)
	}
	rattCount := 0
	for _, e := range entries {
		if e.Name() == result1.RunAttemptID+".json" {
			rattCount++
		}
	}
	if rattCount != 1 {
		t.Fatalf("expected exactly 1 file for the deterministic id, got %d", rattCount)
	}
}

func TestObserveRunWithRunningJobStaysNonTerminal(t *testing.T) {
	s := newStore(t)
	workID := runningWorkForObserve(t, s, "running job stays non-terminal")
	trace := seedTraceEnvelope(t, s, workID)
	jobPath := writeCodexJobFile(t, t.TempDir(), "job-running", "running", "/tmp/running.log", "still going")

	result, err := s.ObserveRun(workID, ObserveRunInput{
		Actor: "codex", TraceID: trace.ID, JobFilePath: jobPath,
	}, fixedNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ra, err := runattempt.Open(s.home).Get(result.RunAttemptID)
	if err != nil {
		t.Fatal(err)
	}
	// Running job → NOT terminal.
	if runattempt.IsTerminal(ra.Status) {
		t.Fatalf("running job produced terminal status %q; must stay non-terminal", ra.Status)
	}
}

func TestObserveRunWithFailedJobTerminalizesFailed(t *testing.T) {
	s := newStore(t)
	workID := runningWorkForObserve(t, s, "failed job terminalizes")
	trace := seedTraceEnvelope(t, s, workID)
	jobPath := writeCodexJobFile(t, t.TempDir(), "job-fail", "failed", "/tmp/fail.log", "broken")

	result, err := s.ObserveRun(workID, ObserveRunInput{
		Actor: "codex", TraceID: trace.ID, JobFilePath: jobPath,
	}, fixedNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ra, err := runattempt.Open(s.home).Get(result.RunAttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if ra.Status != runattempt.StatusFailed {
		t.Fatalf("status = %q, want failed", ra.Status)
	}
}

func TestObserveRunWithoutJobFileProducesNoRunAttempt(t *testing.T) {
	s := newStore(t)
	workID := runningWorkForObserve(t, s, "no job file no attempt")
	trace := seedTraceEnvelope(t, s, workID)

	result, err := s.ObserveRun(workID, ObserveRunInput{
		Actor: "codex", TraceID: trace.ID,
	}, fixedNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if result.RunAttemptID != "" {
		t.Fatalf("observe-run without job file must not produce RunAttemptID, got %q", result.RunAttemptID)
	}
}

// TestObserveRunStampsAttributionFromInput: the S3 attribution fields on
// ObserveRunInput land on the job-file-minted RunAttempt while it is still
// pending (before the status-map Complete), so the terminal-refusing setters
// accept them. This is the work-layer round-trip proof of the observe-run
// stamping policy; the CLI tests (work_cli_test.go) prove the flag plumbing
// and the no-keys omitempty case end-to-end.
func TestObserveRunStampsAttributionFromInput(t *testing.T) {
	s := newStore(t)
	workID := runningWorkForObserve(t, s, "attribution stamps from input")
	trace := seedTraceEnvelope(t, s, workID)
	jobPath := writeCodexJobFile(t, t.TempDir(), "job-attr", "completed", "/tmp/job-attr.log", "attribution job")

	result, err := s.ObserveRun(workID, ObserveRunInput{
		Actor:         "codex",
		TraceID:       trace.ID,
		JobFilePath:   jobPath,
		LaneID:        "lane-a:worker",
		ModelID:       "model-x",
		ProviderRoute: "provider-y",
		TokensIn:      100,
		TokensOut:     250,
	}, fixedNow.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	ra, err := runattempt.Open(s.home).Get(result.RunAttemptID)
	if err != nil {
		t.Fatal(err)
	}
	if ra.LaneID != "lane-a:worker" || ra.ModelID != "model-x" || ra.ProviderRoute != "provider-y" {
		t.Fatalf("attribution not stamped on the minted RunAttempt: lane=%q model=%q provider=%q", ra.LaneID, ra.ModelID, ra.ProviderRoute)
	}
	if ra.TokensIn != 100 || ra.TokensOut != 250 {
		t.Fatalf("token usage not stamped: in=%d out=%d", ra.TokensIn, ra.TokensOut)
	}
}

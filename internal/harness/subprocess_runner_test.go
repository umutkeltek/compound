package harness

import (
	"context"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
	"time"

	"compound/internal/runattempt"
)

// TestRunnerStdinReachesSubprocess: RunInput.Stdin is piped to the
// subprocess (cat) and its echoed stdout lands in the transcript. Pinned:
// the old runner ran io.Copy(io.Discard, in.Stdin), silently dropping the
// caller's input.
func TestRunnerStdinReachesSubprocess(t *testing.T) {
	home := t.TempDir()
	store := runattempt.Open(home)
	a := NewOSEntrypointsAdapter(store)

	out, err := a.Run(context.Background(), RunInput{
		WorkID:    "work_stdin",
		Harness:   "os-harness-entrypoints",
		Workspace: home,
		Command:   "cat",
		Stdin:     strings.NewReader("hello-stdin-payload\n"),
		Now:       time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, rerr := os.ReadFile(out.TranscriptPath)
	if rerr != nil {
		t.Fatalf("read transcript: %v", rerr)
	}
	if !strings.Contains(string(body), "stdout: hello-stdin-payload") {
		t.Fatalf("transcript missing echoed stdin; got:\n%s", body)
	}
}

// TestRunnerActionIntentBindsBeforeTerminal: when RunInput.ActionIntentID
// is supplied, the terminal RunAttempt carries the binding. Pinned: the
// old runner called AttachActionIntent AFTER Complete, and
// AttachActionIntent refuses a terminal attempt -- so the binding was a
// guaranteed silent no-op and the attempt->intent->policy-decision chain
// was broken for every subprocess adapter.
func TestRunnerActionIntentBindsBeforeTerminal(t *testing.T) {
	home := t.TempDir()
	store := runattempt.Open(home)
	a := NewOSEntrypointsAdapter(store)

	out, err := a.Run(context.Background(), RunInput{
		WorkID:         "work_intent",
		Harness:        "os-harness-entrypoints",
		Workspace:      home,
		Command:        "echo",
		Args:           []string{"ok"},
		ActionIntentID: "act_test_binding_123",
		Now:            time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.RunAttempt.ActionIntentID != "act_test_binding_123" {
		t.Fatalf("terminal RunAttempt action_intent_id = %q, want act_test_binding_123 (must bind before Complete)", out.RunAttempt.ActionIntentID)
	}
	// Re-Get from disk to prove the binding persisted through the
	// terminal transition, not just on the in-memory return value.
	got, gerr := store.Get(out.RunAttempt.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if got.ActionIntentID != "act_test_binding_123" {
		t.Fatalf("persisted action_intent_id = %q, want act_test_binding_123", got.ActionIntentID)
	}
}

// TestRunnerStampsLaneIDPreTerminal: when RunInput.LaneID is set, the
// terminal RunAttempt carries lane_id = that value, stamped via
// SetAttribution BEFORE Complete (a post-Complete stamp is refused by the
// terminal guard, so it would be a silent no-op). Re-Get from disk proves
// the stamp persisted through the terminal transition. Complements the
// CLI-level TestWorkAdvanceExecuteStampsLaneIDOnExecAttempt at the rail
// level -- no CLI or work-advance machinery in the path.
func TestRunnerStampsLaneIDPreTerminal(t *testing.T) {
	home := t.TempDir()
	store := runattempt.Open(home)
	a := NewOSEntrypointsAdapter(store)

	out, err := a.Run(context.Background(), RunInput{
		WorkID:    "work_lane",
		Harness:   "os-harness-entrypoints",
		Workspace: home,
		Command:   "echo",
		Args:      []string{"ok"},
		LaneID:    "lane-a:worker",
		Now:       time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if out.RunAttempt.LaneID != "lane-a:worker" {
		t.Fatalf("terminal RunAttempt lane_id = %q, want lane-a:worker (must stamp before Complete)", out.RunAttempt.LaneID)
	}
	got, gerr := store.Get(out.RunAttempt.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if got.LaneID != "lane-a:worker" {
		t.Fatalf("persisted lane_id = %q, want lane-a:worker", got.LaneID)
	}
}

// TestRunnerEmptyLaneIDOmitsStamp: an empty LaneID stamps NOTHING -- the
// runner skips SetAttribution entirely, so the persisted RunAttempt JSON
// has NO lane_id key (omitempty at the rail level). Falsifier: if the
// runner called SetAttribution unconditionally, the field would still
// round-trip; asserting on the raw JSON bytes (not just the struct) pins
// that an unattributed run leaves no lane_id trace.
func TestRunnerEmptyLaneIDOmitsStamp(t *testing.T) {
	home := t.TempDir()
	store := runattempt.Open(home)
	a := NewOSEntrypointsAdapter(store)

	out, err := a.Run(context.Background(), RunInput{
		WorkID:    "work_no_lane",
		Harness:   "os-harness-entrypoints",
		Workspace: home,
		Command:   "echo",
		Args:      []string{"ok"},
		// LaneID intentionally empty: unattributed run.
		Now: time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, rerr := os.ReadFile(storePath(store, out.RunAttempt))
	if rerr != nil {
		t.Fatalf("read RunAttempt JSON: %v", rerr)
	}
	if strings.Contains(string(body), "lane_id") {
		t.Fatalf("unattributed RunAttempt JSON must omit lane_id (empty LaneID stamps nothing); got:\n%s", body)
	}
}

// TestRunnerInfraFailureTerminalizes: a Start failure (bad command path)
// terminalizes the RunAttempt failed and returns BOTH the RunAttempt and
// an error. Pinned: the old runner returned RunOutput{} + err on every
// post-Mint infra failure, leaving a permanent status=pending record.
func TestRunnerInfraFailureTerminalizes(t *testing.T) {
	home := t.TempDir()
	store := runattempt.Open(home)
	a := NewOSEntrypointsAdapter(store)

	out, err := a.Run(context.Background(), RunInput{
		WorkID:    "work_infra",
		Harness:   "os-harness-entrypoints",
		Workspace: home,
		Command:   "/this/binary/does/not/exist/anywhere",
		Now:       time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
	})
	if err == nil {
		t.Fatalf("expected infrastructure error for a bad command path, got nil")
	}
	if out.RunAttempt.ID == "" {
		t.Fatalf("infra-failure RunOutput must still carry the minted RunAttempt id, got empty")
	}
	// The persisted record must be terminal (failed), never pending.
	got, gerr := store.Get(out.RunAttempt.ID)
	if gerr != nil {
		t.Fatal(gerr)
	}
	if got.Status != runattempt.StatusFailed {
		t.Fatalf("persisted status = %q, want failed (infra failure must terminalize, not leave pending)", got.Status)
	}
}

// TestRunnerConcurrentTranscriptWrites: a subprocess that writes many
// interleaved stdout and stderr lines produces a well-formed transcript
// -- every line is exactly "stdout: out<N>" or "stderr: err<N>". Pinned:
// the old runner had two capture goroutines writing one bufio.Writer with
// no lock, which can interleave or corrupt under contention.
func TestRunnerConcurrentTranscriptWrites(t *testing.T) {
	home := t.TempDir()
	store := runattempt.Open(home)
	a := NewOSEntrypointsAdapter(store)

	const lines = 300
	out, err := a.Run(context.Background(), RunInput{
		WorkID:    "work_concurrent",
		Harness:   "os-harness-entrypoints",
		Workspace: home,
		Command:   "sh",
		Args:      []string{"-c", "for i in $(seq 1 " + itoa(lines) + "); do echo \"out$i\"; echo \"err$i\" 1>&2; done"},
		Now:       time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	body, rerr := os.ReadFile(out.TranscriptPath)
	if rerr != nil {
		t.Fatalf("read transcript: %v", rerr)
	}
	lineRE := regexp.MustCompile(`^(stdout|stderr): (out|err)[0-9]+$`)
	var stdoutCount, stderrCount int
	for _, ln := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
		if ln == "" {
			continue
		}
		if !lineRE.MatchString(ln) {
			t.Fatalf("garbled transcript line (concurrent write corruption?): %q", ln)
		}
		if strings.HasPrefix(ln, "stdout:") {
			stdoutCount++
		} else {
			stderrCount++
		}
	}
	if stdoutCount != lines || stderrCount != lines {
		t.Fatalf("transcript line counts = stdout:%d stderr:%d, want %d each (lost lines = dropped capture)", stdoutCount, stderrCount, lines)
	}
}

// TestRunnerTimeoutOwnedByRunner: the shared runner enforces
// RunInput.Timeout even when buildCmd does NOT use exec.CommandContext.
// Pinned: the old runner delegated timeout to buildCmd and ran
// capWG.Wait() before waitWithTimeout, so a future adapter that forgets
// CommandContext would hang forever inside capWG.Wait.
func TestRunnerTimeoutOwnedByRunner(t *testing.T) {
	home := t.TempDir()
	store := runattempt.Open(home)
	// Deliberately build with exec.Command (no context) to simulate an
	// adapter that forgot CommandContext. The runner must still kill it.
	naiveBuild := func(_ context.Context, in RunInput) *exec.Cmd {
		return exec.Command(in.Command, in.Args...)
	}
	start := time.Now()
	out, err := runWithSubprocess(context.Background(), store, "naive",
		RunInput{
			WorkID:    "work_timeout_owner",
			Harness:   "naive",
			Workspace: home,
			Command:   "sleep",
			Args:      []string{"30"},
			Timeout:   300 * time.Millisecond,
			Now:       time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC),
		}, naiveBuild, defaultHeartbeatInterval)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("timeout must NOT surface as a Run-level error: %v", err)
	}
	if !out.TimedOut {
		t.Fatalf("out.TimedOut = false, want true (runner must enforce timeout even without CommandContext)")
	}
	if out.RunAttempt.Status != runattempt.StatusFailed {
		t.Fatalf("status = %q, want failed", out.RunAttempt.Status)
	}
	// Sanity: 30s sleep killed at 300ms should return well under 5s.
	if elapsed > 5*time.Second {
		t.Fatalf("runner did not enforce timeout: elapsed = %v (process was not killed)", elapsed)
	}
}

// itoa is a strconv-free int->string for test arg assembly (matches the
// style of intToStr in claude_adapter_test.go, kept local to avoid an
// import for a single call site).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

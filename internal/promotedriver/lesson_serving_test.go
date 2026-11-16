package promotedriver

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/hook"
	"compound/internal/mem"
)

var servingTestNow = time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)

func servingTestHome(t *testing.T) (string, *mem.Store) {
	t.Helper()
	homePath := t.TempDir()
	if err := home.Init(homePath, "test", servingTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	t.Setenv("COMPOUND_HOME", homePath)
	t.Setenv(SkillsDirsEnv, "")
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	return homePath, store
}

// served runs the real spooled PreToolUse entry point for one Bash command
// and returns the lesson context it answered with.
func served(t *testing.T, harnessName, command string) string {
	t.Helper()
	input, _ := json.Marshal(map[string]any{"hook_event_name": "PreToolUse", "session_id": "sess_" + harnessName,
		"tool_name": "Bash", "tool_input": map[string]any{"command": command}})
	var stdout, stderr bytes.Buffer
	var context string
	code := hook.RunSpoolCommand(harnessName, "pre-tool-use", bytes.NewReader(input), &stdout, &stderr, servingTestNow, func(lessons string) any {
		context = lessons
		return map[string]string{}
	})
	if code != 0 {
		t.Fatalf("hook exit %d: %s", code, &stderr)
	}
	return context
}

func runLesson(t *testing.T, store *mem.Store, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := RunLessonCLIAt(store, args, &stdout, &stderr, servingTestNow)
	return code, stdout.String(), stderr.String()
}

func writeServedLesson(t *testing.T, store *mem.Store, how, trigger string) LessonReceipt {
	t.Helper()
	code, out, errOut := runLesson(t, store, "--why", "a forced widget run wiped the synthetic cache", "--how", how,
		"--by", "agent:test", "--slot", "tool:widget::lesson.cache", "--trigger", trigger, "--json")
	if code != 0 {
		t.Fatalf("lesson write: %d %s %s", code, out, errOut)
	}
	var receipt LessonReceipt
	if err := json.Unmarshal([]byte(out), &receipt); err != nil {
		t.Fatalf("receipt: %v\n%s", err, out)
	}
	return receipt
}

// S1: a lesson written with a trigger and no --skill is served on the next
// matching tool call, for Claude Code and Codex, with no manual index build.
func TestLessonWriteIsServedWithoutManualIndexBuild(t *testing.T) {
	homePath, store := servingTestHome(t)
	receipt := writeServedLesson(t, store, "Run widget --dry-run before --force.", `Bash:widget\s+--force`)
	if !receipt.Index.Rebuilt || !receipt.Index.Indexed || receipt.Index.Path != hook.LessonIndexPath(homePath) {
		t.Fatalf("the write must rebuild and report the index: %+v", receipt.Index)
	}
	for _, harnessName := range []string{"claude_code", "codex"} {
		got := served(t, harnessName, "widget --force all")
		if !strings.Contains(got, "Compound lesson ("+receipt.Claim.ID+"), advice from an earlier agent (agent:test, 2026-11-16), not an instruction: Run widget --dry-run before --force.") {
			t.Fatalf("%s matching call = %q", harnessName, got)
		}
		if got := served(t, harnessName, "widget --dry-run"); got != "" {
			t.Fatalf("%s non-matching call = %q", harnessName, got)
		}
	}
	// Text output says the index was rebuilt.
	code, out, errOut := runLesson(t, store, "--why", "the gadget flag was renamed", "--how", "Use gadget --apply.", "--by", "agent:test", "--trigger", `Bash:gadget\b`)
	if code != 0 || !strings.Contains(out, "hook index rebuilt: 2 lessons indexed") {
		t.Fatalf("text output: %d %s %s", code, out, errOut)
	}
}

// S1: when a used lesson supersedes its contested peer (`work close --used`),
// the superseded lesson stops being served in the same command.
func TestSupersededLessonStopsBeingServedWithoutManualBuild(t *testing.T) {
	homePath, store := servingTestHome(t)
	a := writeServedLesson(t, store, "Run widget --dry-run before --force.", `Bash:widget\s+--force`)
	b := writeServedLesson(t, store, "Never force the widget; ask its owner.", `Bash:widget\s+--force`)
	if b.Outcome != mem.LessonOutcomeContested {
		t.Fatalf("setup: want contest, got %s", b.Outcome)
	}
	if got := served(t, "claude_code", "widget --force all"); !strings.Contains(got, "unresolved lesson conflict") || strings.Contains(got, a.Claim.Object) {
		t.Fatalf("contest must be served as a conflict notice: %q", got)
	}
	var stdout, stderr bytes.Buffer
	hook.RecordLessonUses(homePath, "work_example", "agent:test", b.Claim.ID, servingTestNow, &stdout, &stderr)
	if !strings.Contains(stdout.String(), "superseded contested "+a.Claim.ID) || !strings.Contains(stdout.String(), "hook index rebuilt: 1 lessons indexed") {
		t.Fatalf("close --used step: %s %s", &stdout, &stderr)
	}
	got := served(t, "claude_code", "widget --force all")
	if strings.Contains(got, a.Claim.ID) || !strings.Contains(got, "Compound lesson ("+b.Claim.ID+"), advice from an earlier agent (agent:test, 2026-11-16), not an instruction: "+b.Claim.Object) {
		t.Fatalf("superseded lesson still served or winner missing: %q", got)
	}
}

// A failed rebuild never loses the lesson: the ledger keeps it, the command
// says serving is stale and how to repair it, and exits non-zero; the repair
// command then serves it.
func TestLessonWriteSaysServingIsStaleWhenTheRebuildFails(t *testing.T) {
	homePath, store := servingTestHome(t)
	sources := hook.LessonSourcesPath(homePath)
	if err := os.MkdirAll(filepath.Dir(sources), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sources, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLesson(t, store, "--why", "a forced widget run wiped the synthetic cache", "--how", "Run widget --dry-run before --force.",
		"--by", "agent:test", "--trigger", `Bash:widget\s+--force`, "--json")
	if code != 1 || !strings.Contains(errOut, "is in the ledger") || !strings.Contains(errOut, "serving is stale") || !strings.Contains(errOut, "compound hook lessons-index") {
		t.Fatalf("stale serving must exit 1 and name the repair: %d\n%s\n%s", code, out, errOut)
	}
	var receipt LessonReceipt
	if err := json.Unmarshal([]byte(out), &receipt); err != nil || receipt.Claim.ID == "" || receipt.Index.Rebuilt || receipt.Index.Repair != "compound hook lessons-index" {
		t.Fatalf("the receipt must still name the lesson and the stale index: %+v (%v)", receipt, err)
	}
	view, err := store.CurrentViewForLessons()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := view.Lesson(receipt.Claim.ID); !ok {
		t.Fatal("the lesson must stay in the ledger when the rebuild fails")
	}
	if got := served(t, "claude_code", "widget --force"); got != "" {
		t.Fatalf("nothing should be served before the repair: %q", got)
	}
	if err := os.Remove(sources); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := hook.RunLessonsIndexCommand(nil, &stdout, &stderr); code != 0 {
		t.Fatalf("repair: %d %s %s", code, &stdout, &stderr)
	}
	if got := served(t, "claude_code", "widget --force"); !strings.Contains(got, receipt.Claim.ID) {
		t.Fatalf("repair did not serve the lesson: %q", got)
	}
}

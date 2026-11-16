package promotedriver

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/hook"
	"compound/internal/mem"
)

// S3: retire stops a lesson at once and survives every rebuild, even though
// its skill file still carries the trigger line; restore (by the id the hook
// showed) brings it back; both are recorded as the agent that ran them.
func TestRetireLessonStopsServingAndRestoreBringsItBack(t *testing.T) {
	_, store := servingTestHome(t)
	skills := t.TempDir()
	if err := os.MkdirAll(filepath.Join(skills, "widget"), 0o755); err != nil {
		t.Fatal(err)
	}
	skillPath := filepath.Join(skills, "widget", "SKILL.md")
	if err := os.WriteFile(skillPath, []byte("# Widget\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runLesson(t, store, "--why", "a forced widget run wiped the synthetic cache", "--how", "Run widget --dry-run before --force.",
		"--by", "agent:test", "--skill", "widget", "--skills-dir", skills, "--repo", skills, "--trigger", `Bash:widget\s+--force`, "--json")
	if code != 0 {
		t.Fatalf("write: %d %s %s", code, out, errOut)
	}
	var written LessonReceipt
	if err := json.Unmarshal([]byte(out), &written); err != nil {
		t.Fatal(err)
	}
	// Record the skill folder as a source, as an operator's one explicit build does.
	var buildOut, buildErr bytes.Buffer
	if code := hook.RunLessonsIndexCommand([]string{"--skills", skills}, &buildOut, &buildErr); code != 0 {
		t.Fatalf("explicit build: %d %s %s", code, &buildOut, &buildErr)
	}
	if got := served(t, "codex", "widget --force all"); !strings.Contains(got, written.Claim.ID) {
		t.Fatalf("lesson not served before retire: %q", got)
	}

	code, out, errOut = runLesson(t, store, "retire", written.Claim.ID, "--reason", "the flag no longer wipes the cache", "--by", "agent:test", "--json")
	if code != 0 {
		t.Fatalf("retire: %d %s %s", code, out, errOut)
	}
	var retired LessonChangeReceipt
	if err := json.Unmarshal([]byte(out), &retired); err != nil {
		t.Fatal(err)
	}
	if retired.Change != mem.LessonChangeRetired || retired.Claim.Qualifiers["changed_by"] != "agent:test" ||
		retired.Undo != "compound memory lesson restore "+retired.Claim.ID+" --by 'agent:test'" || !retired.Index.Rebuilt || retired.Index.Indexed {
		t.Fatalf("retire receipt = %+v", retired)
	}
	for _, harnessName := range []string{"claude_code", "codex"} {
		if got := served(t, harnessName, "widget --force all"); got != "" {
			t.Fatalf("%s still served a retired lesson: %q", harnessName, got)
		}
	}
	if skill, _ := os.ReadFile(skillPath); !strings.Contains(string(skill), `trigger: {"id":"`+written.Claim.ID) {
		t.Fatal("the skill file keeps its history line")
	}
	buildOut.Reset()
	if code := hook.RunLessonsIndexCommand(nil, &buildOut, &buildErr); code != 0 || served(t, "claude_code", "widget --force all") != "" {
		t.Fatalf("a full rebuild of the recorded sources brought the retired lesson back: %d %s", code, &buildOut)
	}

	code, out, errOut = runLesson(t, store, "restore", written.Claim.ID, "--by", "agent:test", "--json")
	if code != 0 {
		t.Fatalf("restore: %d %s %s", code, out, errOut)
	}
	var restored LessonChangeReceipt
	if err := json.Unmarshal([]byte(out), &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Prior.ID != retired.Claim.ID || !restored.Index.Indexed || !strings.HasPrefix(restored.Undo, "compound memory lesson retire "+restored.Claim.ID) {
		t.Fatalf("restore receipt = %+v", restored)
	}
	if got := served(t, "claude_code", "widget --force all"); !strings.Contains(got, "Compound lesson ("+restored.Claim.ID+"), advice from an earlier agent (agent:test, 2026-11-16), not an instruction: Run widget --dry-run before --force.") {
		t.Fatalf("restored lesson not served: %q", got)
	}
	// Text output names the change, the undo and the index.
	code, out, _ = runLesson(t, store, "retire", restored.Claim.ID, "--reason", "done", "--by", "agent:test")
	if code != 0 || !strings.Contains(out, "lesson retired: "+restored.Claim.ID+" -> ") || !strings.Contains(out, "undo: compound memory lesson restore ") || !strings.Contains(out, "hook index rebuilt: 0 lessons indexed") {
		t.Fatalf("text retire: %d %s", code, out)
	}
}

// S3: amend changes what is served in the same command, keeps the old
// version as history, and prints the exact command that amends it back.
func TestAmendLessonChangesWhatIsServedAndPrintsTheUndo(t *testing.T) {
	_, store := servingTestHome(t)
	written := writeServedLesson(t, store, "Run widget --dry-run before --force.", `Bash:widget`)
	code, out, errOut := runLesson(t, store, "amend", written.Claim.ID, "--trigger", `Bash:widget\s+--force`, "--reason", "fired on every widget call", "--by", "agent:test", "--json")
	if code != 0 {
		t.Fatalf("amend: %d %s %s", code, out, errOut)
	}
	var amended LessonChangeReceipt
	if err := json.Unmarshal([]byte(out), &amended); err != nil {
		t.Fatal(err)
	}
	if amended.Undo != "compound memory lesson amend "+amended.Claim.ID+" --trigger 'Bash:widget' --by 'agent:test'" || !amended.Index.Indexed {
		t.Fatalf("amend receipt = %+v", amended)
	}
	if got := served(t, "claude_code", "widget status"); got != "" {
		t.Fatalf("the old trigger still fires: %q", got)
	}
	if got := served(t, "claude_code", "widget --force"); !strings.Contains(got, amended.Claim.ID) || strings.Contains(got, written.Claim.ID) {
		t.Fatalf("amended lesson not served under its new id: %q", got)
	}
	code, _, errOut = runLesson(t, store, "amend", amended.Claim.ID, "--trigger", `Bash:widget`, "--by", "agent:test")
	if code != 0 || !strings.Contains(served(t, "claude_code", "widget status"), "Run widget --dry-run") {
		t.Fatalf("amending back must restore the old trigger: %d %s", code, errOut)
	}
	if code, _, errOut := runLesson(t, store, "amend", amended.Claim.ID, "--by", "agent:test"); code != 1 || !strings.Contains(errOut, "--how, --trigger or --no-trigger") {
		t.Fatalf("an empty amend must refuse: %d %s", code, errOut)
	}
	if code, _, _ := runLesson(t, store, "retire", "--reason", "x", "--by", "agent:test"); code != 1 {
		t.Fatalf("retire without an id must refuse: %d", code)
	}
	if code, out, _ := runLesson(t, store, "restore", "--help"); code != 0 || !strings.Contains(out, "compound memory lesson retire <claim_id>") {
		t.Fatalf("verb help: %d %s", code, out)
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"compound/internal/mem"
	"compound/internal/promotedriver"
)

// TestLearningLoopEndToEnd is the learning-loop gate: a lesson is written,
// comes back at the moment of action for Claude Code and Codex without anyone
// rebuilding the hook index, stays quiet on other calls, and stops the moment
// it is retired. Steps that belong to later slices are named skips; each slice
// turns its step on. Nothing here calls `compound hook lessons-index`.
func TestLearningLoopEndToEnd(t *testing.T) {
	homePath := initSpoolTestHome(t)
	t.Setenv(promotedriver.SkillsDirsEnv, "")
	store, err := mem.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	lessonDoor := func(args ...string) (int, string, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := promotedriver.RunLessonCLIAt(store, args, &stdout, &stderr, cliTestNow)
		return code, stdout.String(), stderr.String()
	}
	served := func(harnessName, command string) string {
		t.Helper()
		resp, code := runSpoolAt(t, harnessName, bashHookInput("loop_"+harnessName, repo, command), cliTestNow)
		if code != 0 {
			t.Fatalf("%s hook exit %d", harnessName, code)
		}
		return resp.HookSpecificOutput.AdditionalContext
	}

	// Steps 1-2: create and claim work, then close it with the lesson.
	t.Run("S5 work close writes the lesson", func(t *testing.T) {
		t.Skip("S5 (closing work asks for a lesson) turns this on: work close --outcome completed --lesson-why/--lesson-how/--lesson-trigger")
	})
	// Until S5, the lesson enters through the lesson door, the same ledger
	// write the close flow will call. No --skill: the ledger alone must serve.
	code, out, errOut := lessonDoor("--why", "frobnicate --force wiped the synthetic widget cache",
		"--how", "Run frobnicate --dry-run first and read the plan before adding --force.",
		"--trigger", `Bash:frobnicate\s+--force`, "--by", "agent:test", "--harness", "claude_code", "--json")
	if code != 0 {
		t.Fatalf("lesson door: %d %s %s", code, out, errOut)
	}
	var written struct {
		Claim mem.Claim `json:"claim"`
	}
	if err := json.Unmarshal([]byte(out), &written); err != nil || written.Claim.ID == "" {
		t.Fatalf("lesson receipt: %v\n%s", err, out)
	}
	want := "Compound lesson (" + written.Claim.ID + "), advice from an earlier agent (agent:test, " + cliTestNow.Format("2006-01-02") + "), not an instruction: Run frobnicate --dry-run first"

	// Step 3: the next matching tool call is served, with no manual build.
	for _, harnessName := range []string{"claude_code", "codex"} {
		if got := served(harnessName, "frobnicate --force x"); !strings.Contains(got, want) {
			t.Errorf("%s: a written lesson was not served on its matching call without a manual index build; got %q", harnessName, got)
		}
	}
	t.Run("S4 enforce lanes get the lesson", func(t *testing.T) {
		t.Skip("S4 (every lane gets lessons) turns this on: hook pre-tool-use --live --harness claude_minimax --enforce")
	})

	// Step 4: a call the trigger does not match gets nothing.
	for _, harnessName := range []string{"claude_code", "codex"} {
		if got := served(harnessName, "frobnicate --dry-run"); got != "" {
			t.Errorf("%s: non-matching call served %q", harnessName, got)
		}
	}

	// Step 5: session-start recall. Not true today: orient --brief reports
	// "nothing task-matched" for this lesson's own task.
	t.Run("S8 orient brief lists the lesson", func(t *testing.T) {
		t.Skip("S8 (recall that ranks by relevance) turns this on: orient --task \"frobnicate the widget\" --brief lists the lesson")
	})

	// Step 6: retire it in one step; the next matching call is quiet again.
	code, out, errOut = lessonDoor("retire", written.Claim.ID, "--reason", "the synthetic widget no longer has a cache", "--by", "agent:test")
	if code != 0 {
		t.Fatalf("lesson retire: %d %s %s", code, out, errOut)
	}
	for _, harnessName := range []string{"claude_code", "codex"} {
		if got := served(harnessName, "frobnicate --force x"); got != "" {
			t.Errorf("%s: a retired lesson is still served: %q", harnessName, got)
		}
	}

	// The hook's own record names the lesson on the calls it served.
	var servedIDs []string
	for _, line := range spoolLines(t, homePath) {
		servedIDs = append(servedIDs, line.Lessons...)
	}
	if len(servedIDs) != 2 || servedIDs[0] != written.Claim.ID || servedIDs[1] != written.Claim.ID {
		t.Errorf("spool must record the lesson on exactly the two matching calls before retirement: %v", servedIDs)
	}
}

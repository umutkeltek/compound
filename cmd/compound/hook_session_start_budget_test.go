package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/tooltopology"
)

// Claude Code keeps at most 10,000 bytes of a hook's additional context and
// replaces anything longer with a 2,000-byte preview. These tests pin the
// session-start text that really reaches the harness (the live JSON response),
// built from a worst-case door map and an operator model the size of a real one.
const (
	sessionStartHardLimit   = 10000
	sessionStartPreviewSize = 2000
)

// syntheticSessionStartModel is shaped like a real operator model: rule
// sections that must be served whole, then background that may be left out.
func syntheticSessionStartModel() (string, string) {
	rule := func(heading string, n int) string {
		var b strings.Builder
		b.WriteString("## " + heading + "\n")
		for i := 1; i <= n; i++ {
			fmt.Fprintf(&b, "- Standing rule %d under %q: a plain sentence the operator wrote, about ninety characters long.\n", i, heading)
		}
		return b.String()
	}
	firstRule := "Standing rule 1 under \"Standing constraints\""
	model := "# Operator model\n" +
		rule("Standing constraints", 14) +
		rule("What agents may do without asking", 6) +
		rule("How to write to him", 8) +
		rule("Autonomy", 6) +
		rule("Standing decisions", 8) +
		rule("Who", 12)
	return model, firstRule
}

func worstCaseDoorMap(t *testing.T, homePath string) {
	t.Helper()
	var doors []string
	for i := 0; i < 40; i++ {
		doors = append(doors, fmt.Sprintf(`{"door":"door%02d","status":"PASS","detail":"%s","use":"tool%02d run \"<arg>\" --flag %s"}`,
			i, strings.Repeat("d", 120), i, strings.Repeat("u", 100)))
	}
	if _, err := tooltopology.Refresh(homePath, tooltopology.Inputs{
		DoorsJSON:       []byte("[" + strings.Join(doors, ",") + "]"),
		DoorsObservedAt: time.Now().Add(-time.Hour),
	}, time.Now()); err != nil {
		t.Fatal(err)
	}
}

// liveSessionStartText runs the real live session-start hook for the harness and
// returns exactly the text the harness receives.
func liveSessionStartText(t *testing.T, harnessFlag string) string {
	t.Helper()
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	t.Setenv("COMPOUND_SESSION_PACK", "")
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	worstCaseDoorMap(t, homePath)
	model, _ := syntheticSessionStartModel()
	modelPath := filepath.Join(homePath, filepath.FromSlash(tooltopology.OperatorModelPath))
	if err := os.MkdirAll(filepath.Dir(modelPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modelPath, []byte(model), 0o644); err != nil {
		t.Fatal(err)
	}
	repo, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	input := fmt.Sprintf(`{"hook_event_name":"SessionStart","session_id":"sess_budget","cwd":%q,"source":"startup"}`, repo)
	var code int
	out := captureStdout(t, func() {
		withStdin(t, input, func() {
			code = runHookCommand([]string{"session-start", "--live", "--harness", harnessFlag})
		})
	})
	if code != 0 {
		t.Fatalf("live session-start exit = %d\n%s", code, out)
	}
	var resp claudeLiveHookResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("parse live response: %v\n%s", err, out)
	}
	return resp.HookSpecificOutput.AdditionalContext
}

// The parts that have caps must leave room for the ones that do not: the ask-first
// line (about 450 bytes with a long path) and the compact laws (about 700).
func TestSessionStartPartBudgetsLeaveRoomForTheFixedLines(t *testing.T) {
	const fixedLines = 1300
	if sum := tooltopology.MaxOperatorModelBytes + tooltopology.MaxServedBytes + fixedLines; sum >= sessionStartHardLimit {
		t.Fatalf("operator model budget %d + door map cap %d + %d fixed bytes = %d, want under %d",
			tooltopology.MaxOperatorModelBytes, tooltopology.MaxServedBytes, fixedLines, sum, sessionStartHardLimit)
	}
}

func TestLiveSessionStartTextFitsTheHarnessLimit(t *testing.T) {
	_, firstRule := syntheticSessionStartModel()
	for _, harnessFlag := range []string{"claude-code", "codex"} {
		t.Run(harnessFlag, func(t *testing.T) {
			text := liveSessionStartText(t, harnessFlag)
			t.Logf("%s session-start text = %d bytes", harnessFlag, len(text))
			if len(text) >= sessionStartHardLimit {
				t.Fatalf("session-start text is %d bytes, want under %d; the harness would keep only a %d-byte preview", len(text), sessionStartHardLimit, sessionStartPreviewSize)
			}
			// The door map is at its cap, so this is the worst case for its part.
			if i := strings.Index(text, "## Door map"); i < 0 || len(text)-i < 2000 {
				t.Fatalf("worst-case fixture no longer fills the door map")
			}
			head := text
			if len(head) > sessionStartPreviewSize {
				head = head[:sessionStartPreviewSize]
			}
			// (a) the operator's rules open inside the preview.
			for _, want := range []string{"## Operator model", "## Standing constraints", firstRule} {
				if !strings.Contains(head, want) {
					t.Fatalf("%q is not inside the first %d bytes:\n%s", want, sessionStartPreviewSize, head)
				}
			}
			// (b) one ask-Compound-first line, naming commands that exist, is inside it too.
			ask := ""
			for _, line := range strings.Split(head, "\n") {
				if strings.Contains(line, "Ask Compound first") {
					ask = line
				}
			}
			if ask == "" {
				t.Fatalf("no ask-Compound-first line inside the first %d bytes:\n%s", sessionStartPreviewSize, head)
			}
			for _, want := range []string{"compound orient", "compound system depends|impact|inventory", "compound doors serve"} {
				if !strings.Contains(ask, want) {
					t.Fatalf("ask-first line lacks %q: %s", want, ask)
				}
			}
			// What the door map and the laws carry still arrives.
			for _, want := range []string{"## Door map", "Tier-0 constitution: Laws"} {
				if !strings.Contains(text, want) {
					t.Fatalf("session-start text lost %q", want)
				}
			}
			// Every rule section arrives whole; only background may be left out.
			for _, want := range []string{"Standing rule 8 under \"Standing decisions\"", "Standing rule 6 under \"Autonomy\""} {
				if !strings.Contains(text, want) {
					t.Fatalf("a rule line was lost: %q", want)
				}
			}
		})
	}
}

// Every command the ask-first line names must be a command the CLI has.
func TestSessionStartAskLineNamesRealCommands(t *testing.T) {
	for _, verb := range []string{"depends", "impact", "inventory"} {
		if !strings.Contains(string(mustReadFile(t, "system.go")), `"`+verb+`"`) {
			t.Fatalf("compound system %s is not a command", verb)
		}
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

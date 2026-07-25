package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"compound/internal/envelope"
)

// TestGateRefusalRendersAllFourParts pins the shape: a refusal that drops a part
// degrades back into the one-line "error: ...; pass --flag" it replaced.
func TestGateRefusalRendersAllFourParts(t *testing.T) {
	text := gateRefusal{
		Command:  "work advance --execute",
		Gate:     "workspace_resolution",
		Refused:  "no execution workspace could be resolved",
		Tried:    []string{"--workspace flag: not passed", "current working directory: unavailable (getwd: gone)"},
		DoThis:   []string{"compound work advance work_x --execute --harness claude --workspace <project-path>"},
		Prevents: "running a code-editing harness against COMPOUND_HOME",
	}.String()

	for _, want := range []string{
		"error: work advance --execute refused at gate workspace_resolution",
		"refused: no execution workspace could be resolved",
		"tried in order:",
		"1. --workspace flag: not passed",
		"2. current working directory: unavailable (getwd: gone)",
		"do this:",
		"compound work advance work_x --execute --harness claude --workspace <project-path>",
		"prevents: running a code-editing harness against COMPOUND_HOME",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("refusal text missing %q:\n%s", want, text)
		}
	}

	// The tried-list is ordered and numbered: the reader must be able to see WHICH
	// rung failed, which is the whole reason the trace is threaded out of the resolver.
	if strings.Index(text, "1. --workspace") > strings.Index(text, "2. current working") {
		t.Errorf("tried steps rendered out of attempt order:\n%s", text)
	}
}

// TestGateRefusalKeepsSuggestedCommandsRunnable pins that a missing interpolated
// value becomes a visible placeholder rather than a gap. "compound work show  #
// confirm..." reads as a complete command and silently is not one.
func TestGateRefusalKeepsSuggestedCommandsRunnable(t *testing.T) {
	if got := firstNonEmptyRefusalValue("", "<work-id>"); got != "<work-id>" {
		t.Errorf("empty value must render a placeholder, got %q", got)
	}
	if got := firstNonEmptyRefusalValue("   ", "<work-id>"); got != "<work-id>" {
		t.Errorf("blank value must render a placeholder, got %q", got)
	}
	if got := firstNonEmptyRefusalValue("work_abc", "<work-id>"); got != "work_abc" {
		t.Errorf("present value must survive, got %q", got)
	}
}

// TestGateRefusalSuggestedCommandsAreInvocable pins that every command a refusal
// tells the reader to run is one they can paste.
//
// A refusal's whole value is the exact command, so a suggestion that omits the
// binary name ("harness run --harness x" when the command is "compound harness run")
// is worse than no suggestion: it looks authoritative and fails. Caught in review on
// this file's first draft, which is why the check reads the real sources below
// instead of a list of examples restated here — a hand-maintained list would pass
// while the actual refusal sites drifted.
func TestGateRefusalSuggestedCommandsAreInvocable(t *testing.T) {
	sources, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}

	literal := regexp.MustCompile(`"([^"\\]{4,})"`)
	checked := 0

	for _, path := range sources {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, block := range doThisBlocks(string(body)) {
			for _, m := range literal.FindAllStringSubmatch(block, -1) {
				cmd := m[1]
				// Only the command-shaped literals; a bare flag name or a fragment
				// interpolated by Sprintf is not a suggestion on its own.
				if !strings.Contains(cmd, " ") || strings.HasPrefix(cmd, "#") {
					continue
				}
				checked++
				if !strings.HasPrefix(cmd, "compound ") {
					t.Errorf("%s: suggested command is not invocable as written (missing the binary name): %q", path, cmd)
				}
				if strings.Contains(cmd, "one-of-the-above") || strings.Contains(cmd, "see above") {
					t.Errorf("%s: suggested command defers to prose instead of naming a value: %q", path, cmd)
				}
			}
		}
	}

	// Teeth: if the extraction silently matched nothing, the loop above would pass
	// vacuously and the convention would go unenforced.
	if checked == 0 {
		t.Fatal("found no DoThis command literals to check — the extraction is broken, so this test proves nothing")
	}
	t.Logf("checked %d suggested commands across refusal sites", checked)
}

// doThisBlocks returns the source text of each `DoThis: []string{...}` literal,
// matched by brace depth so a nested Sprintf call does not end the block early.
func doThisBlocks(src string) []string {
	const marker = "DoThis: []string{"
	var out []string
	for idx := strings.Index(src, marker); idx >= 0; idx = strings.Index(src, marker) {
		rest := src[idx+len(marker):]
		depth := 1
		end := -1
		for i, r := range rest {
			switch r {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					end = i
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			break
		}
		out = append(out, rest[:end])
		src = rest[end:]
	}
	return out
}

// TestResolveExecuteWorkspaceReportsWhichRungResolved pins the trace the refusal
// quotes. A ladder that reports only its final verdict cannot be distinguished from
// one that never ran, so each rung must name itself AND its observed result.
func TestResolveExecuteWorkspaceReportsWhichRungResolved(t *testing.T) {
	t.Run("explicit flag wins and is traced", func(t *testing.T) {
		ws, tried := resolveExecuteWorkspace([]string{"--workspace", "/repo/project"}, nil)
		if ws != "/repo/project" {
			t.Fatalf("workspace = %q, want /repo/project", ws)
		}
		if len(tried) != 1 || !strings.Contains(tried[0], "/repo/project") {
			t.Fatalf("expected the flag rung traced with its value, got %v", tried)
		}
	})

	t.Run("git delivery rung records why the flag rung was skipped", func(t *testing.T) {
		gd := &envelope.GitDeliveryContract{TargetWorktreeRoot: "/repo/worktree"}
		ws, tried := resolveExecuteWorkspace(nil, gd)
		if ws != "/repo/worktree" {
			t.Fatalf("workspace = %q, want /repo/worktree", ws)
		}
		if len(tried) != 2 {
			t.Fatalf("expected both rungs traced, got %v", tried)
		}
		if !strings.Contains(tried[0], "not passed") {
			t.Fatalf("first rung must record that --workspace was absent, got %q", tried[0])
		}
	})

	t.Run("empty target root is distinguished from an absent contract", func(t *testing.T) {
		_, absent := resolveExecuteWorkspace(nil, nil)
		_, empty := resolveExecuteWorkspace(nil, &envelope.GitDeliveryContract{})
		if !strings.Contains(absent[1], "no git delivery contract") {
			t.Errorf("absent contract must say so, got %q", absent[1])
		}
		if !strings.Contains(empty[1], "target_worktree_root is empty") {
			t.Errorf("present-but-empty contract must be distinguished from an absent one, got %q", empty[1])
		}
		// The two cases need different remedies (attach a contract vs fix its field),
		// so collapsing them into one message sends the reader to the wrong fix.
		if absent[1] == empty[1] {
			t.Errorf("absent and empty contracts rendered the same trace line: %q", absent[1])
		}
	})
}

package main

import (
	"strings"
	"testing"
)

func TestRootUsageListsDispatchExecute(t *testing.T) {
	out := captureStderr(t, usage)
	if !strings.Contains(out, "dispatch-claim|dispatch-execute|dispatch-advance") {
		t.Fatalf("root usage omits selector execution:\n%s", out)
	}
}

func TestDispatchExecuteHelpExcludesHumanRouteSelection(t *testing.T) {
	out := captureStdout(t, func() {
		cmdWork([]string{"dispatch-execute", "--help"})
	})
	for _, forbidden := range []string{"--actor", "--harness", "--model"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("dispatch-execute exposes human route selector %q:\n%s", forbidden, out)
		}
	}
}

func TestEveryWorkVerbHelpIncludesUsageAndExecutableExample(t *testing.T) {
	verbs := []string{
		"create-plan", "admit-plan", "review-card-plan", "create", "import-packet",
		"show", "list", "leases", "active-packet-proof", "proof-chain-doctor", "reconcile-run-attempts", "continue",
		"dispatch-plan", "dispatch-claim", "dispatch-execute", "dispatch-advance", "dispatch-close-plan",
		"dispatch-close", "dispatch-step", "claim-plan", "claim", "advance-plan", "advance",
		"delegate", "steer", "attach", "observe-run", "land-check", "salvage-plan",
		"action-plan", "act", "close-plan", "close",
		"set-aside",
	}
	for _, verb := range verbs {
		t.Run(verb, func(t *testing.T) {
			out := captureStdout(t, func() {
				cmdWork([]string{verb, "--help"})
			})
			if !strings.Contains(out, "usage: compound work "+verb) {
				t.Fatalf("%s help lacks verb-specific usage:\n%s", verb, out)
			}
			example := "example: compound work " + verb
			if !strings.Contains(out, example) {
				t.Fatalf("%s help lacks executable example prefix %q:\n%s", verb, example, out)
			}
			if strings.Contains(out, "<create-plan|") {
				t.Fatalf("%s help fell back to generic banner:\n%s", verb, out)
			}
		})
	}
}

func TestWorkVerbHelpShowsOwnFlags(t *testing.T) {
	tests := map[string][]string{
		"dispatch-claim":   {"--confirm", "without --confirm previews"},
		"dispatch-execute": {"--workspace", "--permission-mode", "--confirm", "without --confirm previews"},
		"delegate":         {"--harness", "--prompt", "--dry-run"},
		"steer":            {"--text", "--enter"},
		"show":             {"--json"},
		"close":            {"--outcome", "--move-type"},
	}
	for verb, fragments := range tests {
		out := captureStdout(t, func() {
			cmdWork([]string{verb, "--help"})
		})
		for _, fragment := range fragments {
			if !strings.Contains(out, fragment) {
				t.Fatalf("%s help lacks %q:\n%s", verb, fragment, out)
			}
		}
	}
}

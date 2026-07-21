package main

import (
	"strings"
	"testing"
)

func TestEveryWorkVerbHelpIncludesUsageAndExecutableExample(t *testing.T) {
	verbs := []string{
		"create-plan", "admit-plan", "review-card-plan", "create", "import-packet",
		"show", "list", "leases", "active-packet-proof", "proof-chain-doctor", "continue",
		"dispatch-plan", "dispatch-claim", "dispatch-advance", "dispatch-close-plan",
		"dispatch-close", "dispatch-step", "claim-plan", "claim", "advance-plan", "advance",
		"delegate", "steer", "attach", "observe-run", "land-check", "salvage-plan",
		"action-plan", "act", "close-plan", "close",
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
		"dispatch-claim": {"--confirm", "without --confirm previews"},
		"delegate":       {"--harness", "--prompt", "--dry-run"},
		"steer":          {"--text", "--enter"},
		"show":           {"--json"},
		"close":          {"--outcome", "--move-type"},
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

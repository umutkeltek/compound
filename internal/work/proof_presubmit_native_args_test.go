package work

import (
	"strings"
	"testing"
)

func TestGovernedPresubmitArgumentsRequireCompleteBoundedAuthority(t *testing.T) {
	base := strings.Repeat("a", 40)
	valid := []string{"--presubmit", "--base", base, "--work", "work_presubmit_synthetic", "--actor", "actor-synthetic", "--execution-timeout", "4h", "--out", "/synthetic/presubmit.json"}
	if err := ValidateProofCheckNonCertifyingArgs(valid); err != nil {
		t.Fatalf("complete governed noncertifying presubmit arguments refused: %v", err)
	}
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"missing-actor", []string{"--presubmit", "--base", base, "--work", "work_presubmit_synthetic", "--execution-timeout", "4h", "--out", "/synthetic/presubmit.json"}},
		{"missing-budget", []string{"--presubmit", "--base", base, "--work", "work_presubmit_synthetic", "--actor", "actor-synthetic", "--out", "/synthetic/presubmit.json"}},
		{"missing-output", []string{"--presubmit", "--base", base, "--work", "work_presubmit_synthetic", "--actor", "actor-synthetic", "--execution-timeout", "4h"}},
		{"unbounded", append(append([]string{}, valid[:8]...), "0", "--out", "/synthetic/presubmit.json")},
		{"above-ceiling", append(append([]string{}, valid[:8]...), "4h1s", "--out", "/synthetic/presubmit.json")},
		{"dev", append([]string{"--dev"}, valid[1:]...)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateProofCheckNonCertifyingArgs(tc.args); err == nil {
				t.Fatal("incomplete or invalid producer authority accepted")
			}
		})
	}
}

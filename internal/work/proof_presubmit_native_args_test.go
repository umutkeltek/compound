package work

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
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

func TestGovernedPresubmitExplicitWaitKeepsExecutionBudget(t *testing.T) {
	args := []string{"--presubmit", "--repo", t.TempDir(), "--base", strings.Repeat("a", 40), "--work", "work_presubmit_wait", "--actor", "synthetic-owner", "--execution-timeout", "1m", "--out", t.TempDir() + "/report.json", "--wait", "3m"}
	request, err := ParsePresubmitExecutionRequest(args)
	if err != nil {
		t.Fatalf("explicit bounded admission wait refused: %v", err)
	}
	if request.Budget.String() != "1m0s" {
		t.Fatalf("queue wait changed active execution allowance: %v", request.Budget)
	}
	if err := ValidateProofCheckNonCertifyingArgs(args); err != nil {
		t.Fatalf("native CLI selector rejected bounded wait: %v", err)
	}
}

func TestGovernedPresubmitWaitArgumentsAndOldBinding(t *testing.T) {
	valid := []string{"--presubmit", "--base", strings.Repeat("a", 40), "--work", "work_wait_args", "--actor", "owner", "--out", "/synthetic/report.json", "--execution-timeout", "1m"}
	base, err := ParsePresubmitExecutionRequest(valid)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(base)
	if err != nil || strings.Contains(string(body), "admission_wait") {
		t.Fatalf("old binding serialization changed: %s %v", body, err)
	}
	for _, invalid := range [][]string{{"--wait"}, {"--wait", "0"}, {"--wait", "-1s"}, {"--wait", "no"}, {"--wait", "999999999999999999999h"}, {"--wait", "1m", "--wait", "2m"}, {"--wait=1m"}} {
		if _, err := ParsePresubmitExecutionRequest(append(append([]string{}, valid...), invalid...)); err == nil {
			t.Fatalf("invalid wait accepted: %v", invalid)
		}
	}
	r, err := ParsePresubmitExecutionRequest(append(valid, "--wait", "3m"))
	if err != nil || r.Wait != 3*time.Minute || r.Budget != time.Minute {
		t.Fatalf("wait/budget changed: %+v %v", r, err)
	}
	before, _ := presubmitBindingDigest(PresubmitExecutionResult{Request: base})
	after, _ := presubmitBindingDigest(PresubmitExecutionResult{Request: r})
	if before == after {
		t.Fatal("explicit wait omitted from native binding")
	}
}

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/routeevidence"
)

func TestRouteExecutionProfileJSONIsStandaloneAndReadOnly(t *testing.T) {
	missingHome := filepath.Join(t.TempDir(), "must-remain-absent")
	t.Setenv("COMPOUND_HOME", missingHome)

	out := captureStdout(t, func() {
		cmdRoute([]string{
			"execution-profile",
			"--profile", "mining-bulk",
			"--role", "miner",
			"--capability-class", "classify-extract",
			"--harness", "codex",
			"--transport", "switchback",
			"--model", "openai/gpt-5.6-sol",
			"--json",
		})
	})
	if _, err := os.Stat(missingHome); !os.IsNotExist(err) {
		t.Fatalf("read-only execution-profile command mutated COMPOUND_HOME: %v", err)
	}

	var plan routeevidence.ExecutionProfilePlan
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatalf("parse standalone JSON: %v\n%s", err, out)
	}
	if plan.ProfileID != "mining-bulk" ||
		plan.ClientHarnessID != "codex" ||
		plan.TransportID != "switchback" ||
		plan.ModelConstraint != "openai/gpt-5.6-sol" ||
		plan.SemanticEffort != "economy" ||
		plan.NativeEffort.Requested != "low" {
		t.Fatalf("CLI output lost requested/resolved dimensions: %+v", plan)
	}
	if plan.EffectiveProvider != routeevidence.UnknownUntilReceipt || plan.EffectiveModel != routeevidence.UnknownUntilReceipt {
		t.Fatalf("CLI output overclaimed effective execution: %+v", plan)
	}
}

func TestRouteExecutionProfileRejectsClaudeUltraWithPath(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runRouteExecutionProfileCommand([]string{
		"--profile", "coding-implementation",
		"--harness", "claude-code",
		"--transport", "native",
		"--effort", "ultra",
		"--json",
	}, &stdout, &stderr)
	if code == 0 || stdout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if got := stderr.String(); !strings.Contains(got, "requested_intent.native_effort") || !strings.Contains(got, "claude-code") {
		t.Fatalf("missing path-addressed compatibility reason: %s", got)
	}
}

func TestRouteExecutionProfileListsBuiltIns(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runRouteExecutionProfileCommand([]string{"--list", "--json"}, &stdout, &stderr)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	var profiles []routeevidence.ExecutionProfileDefinition
	if err := json.Unmarshal(stdout.Bytes(), &profiles); err != nil {
		t.Fatalf("parse list JSON: %v\n%s", err, stdout.String())
	}
	if len(profiles) != 5 || profiles[0].ID != "coding-implementation" {
		t.Fatalf("built-in catalog is not deterministic: %+v", profiles)
	}
}

func TestRouteUsageMentionsExecutionProfile(t *testing.T) {
	if !strings.Contains(routeUsage(""), "execution-profile") || !strings.Contains(routeUsage("execution-profile"), "--profile") {
		t.Fatalf("route usage omits execution-profile:\n%s\n%s", routeUsage(""), routeUsage("execution-profile"))
	}
}

package main

import (
	"compound/internal/work"
	"testing"
)

func TestWorkAdvanceExecuteForwardsExplicitLaunchProfile(t *testing.T) {
	f := newClaimedExecutionFixture(t, "codex", "explicit owner profile reaches the governed attempt")
	t.Cleanup(work.SwapExecutionSelectionResolver(func(req work.ExecutionSelectionResolveRequest) (work.ExecutionSelectionResolution, error) {
		return work.ExecutionSelectionResolution{
			LaunchProfileID: req.LaunchProfileID, Harness: "os-harness-entrypoints",
			ProviderRoute: "synthetic/route", RequestedModelID: "synthetic-model",
		}, nil
	}))
	captureStdout(t, func() {
		cmdWork([]string{"advance", f.workID, "--to", "running", "--by", "codex",
			"--execute", "--harness", "os-harness-entrypoints", "--launch-profile", "fixture-owner",
			"--", "echo", "profile-forwarded"})
	})
	attempt := f.executionAttempt(t)
	if attempt.LaunchProfileID != "fixture-owner" {
		t.Fatalf("RunAttempt.LaunchProfileID=%q, want explicit owner profile", attempt.LaunchProfileID)
	}
}

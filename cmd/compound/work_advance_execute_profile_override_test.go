package main

import (
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
)

func TestWorkAdvanceExecuteSavedProfileFollowsSelectedHarness(t *testing.T) {
	for _, override := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved_harness", true: "different_harness_override"}[override], func(t *testing.T) {
			f := newClaimedExecutionFixture(t, "codex", "saved profile belongs to selected harness")
			savedHarness := "os-harness-entrypoints"
			if override {
				savedHarness = "codex"
			}
			sel := envelope.ExecutionSelection{
				LaunchProfileID: "fixture-saved", LaunchProfileRevision: "sha256:" + strings.Repeat("a", 64),
				Harness: savedHarness, ProviderRoute: "synthetic/route", RequestedModelID: "synthetic-model",
				Source: "explicit", Revision: 1, SelectedBy: "codex", SelectedAt: cliTestNow.Format(time.RFC3339),
			}
			if _, err := f.store.SaveExecutionSelection(f.workID, 0, sel, "codex", cliTestNow); err != nil {
				t.Fatal(err)
			}
			args := []string{"advance", f.workID, "--to", "running", "--by", "codex", "--execute"}
			if override {
				args = append(args, "--harness", "os-harness-entrypoints")
			}
			args = append(args, "--", "echo", "profile-selection")
			captureStdout(t, func() { cmdWork(args) })
			want := "fixture-saved"
			if override {
				want = ""
			}
			if got := f.executionAttempt(t).LaunchProfileID; got != want {
				t.Fatalf("requested launch profile=%q, want %q", got, want)
			}
		})
	}
}

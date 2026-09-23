package executewire

import (
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
	"compound/internal/harness"
)

// BuildSelectionSnapshot contract (2026-09-15): the public-CLI selection
// snapshot builder resolves override > saved > project_default and keeps
// requested vs applied apart.

func cliSavedSelection() *envelope.ExecutionSelection {
	return &envelope.ExecutionSelection{
		LaunchProfileID:       "saved-profile",
		LaunchProfileRevision: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Harness:               "claude_code",
		ProviderRoute:         "minimax/fixture-route",
		RequestedModelID:      "fixture-model",
		RequestedEffort:       "high",
		Source:                "explicit",
		Revision:              3,
		SelectedBy:            "operator",
		SelectedAt:            time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
	}
}

func TestBindCodexSelectionBeforePreflightPreservesRequestedEffort(t *testing.T) {
	selection := cliSavedSelection()
	selection.Harness = "codex"
	selection.RequestedEffort = "medium"
	input, err := BindSelectionBeforePreflight(
		envelope.WorkEnvelope{ExecutionSelection: selection}, "codex", nil,
		harness.RunInput{Harness: "codex"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if input.ExecutionSelectionSnapshot == nil || input.ExecutionSelectionSnapshot.RequestedEffort != "medium" || input.ExecutionSelectionSnapshot.AppliedEffort != "" {
		t.Fatalf("Codex effort was silently discarded or prematurely claimed applied: %+v", input.ExecutionSelectionSnapshot)
	}
}

func TestBuildSelectionSnapshotSavedConsumesRevisionAndCarriesEffortForClaudeFamily(t *testing.T) {
	wenv := envelope.WorkEnvelope{ExecutionSelection: cliSavedSelection()}
	snap, err := BuildSelectionSnapshot(wenv, "claude_code", nil)
	if err != nil {
		t.Fatalf("BuildSelectionSnapshot: %v", err)
	}
	if snap.Source != "saved" || snap.SelectionRevision != wenv.ExecutionSelection.Revision {
		t.Fatalf("saved snapshot must stamp the consumed revision: %+v", snap)
	}
	if snap.LaunchProfileID != "saved-profile" || snap.Harness != "claude_code" {
		t.Fatalf("saved snapshot identity wrong: %+v", snap)
	}
	if snap.ProviderRoute != "minimax/fixture-route" || snap.RequestedModelID != "fixture-model" {
		t.Fatalf("saved snapshot must carry the saved route/model: %+v", snap)
	}
	// claude family carries effort via env only: the request survives and the
	// evidence is unobservable (never a claimed applied value).
	if snap.RequestedEffort != "high" || !strings.HasPrefix(snap.EffortEvidence, "unobservable:") || snap.AppliedEffort != "" {
		t.Fatalf("claude-family effort must be requested+unobservable: %+v", snap)
	}
}

func TestBuildSelectionSnapshotNoSavedSelectionFallsThroughToProjectDefault(t *testing.T) {
	snap, err := BuildSelectionSnapshot(envelope.WorkEnvelope{}, "claude_code", nil)
	if err != nil {
		t.Fatalf("BuildSelectionSnapshot: %v", err)
	}
	if snap.Source != "project_default" || snap.Harness != "claude_code" {
		t.Fatalf("project_default snapshot wrong: %+v", snap)
	}
	if snap.SelectionRevision != 0 {
		t.Fatalf("project_default must not carry a selection revision: %+v", snap)
	}
}

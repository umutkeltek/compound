package main

import (
	"encoding/json"
	"testing"

	"compound/internal/attention"
	"compound/internal/home"
	"compound/internal/work"
)

func TestAttentionCLILiveProjectionSeparatesCanMoveFromNeedsOperator(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatalf("open work store: %v", err)
	}
	ready, err := store.Create(work.CreateInput{
		WorkID: "work_safe_ready", Owner: "compound-core", State: "ready",
		SuccessPredicate: "safe local work can be claimed",
	}, cliTestNow)
	if err != nil {
		t.Fatalf("create ready work: %v", err)
	}
	if _, err := store.Create(work.CreateInput{
		WorkID: "work_real_hold", Owner: "compound-core", State: "held",
		SuccessPredicate: "held work waits for an operator decision",
	}, cliTestNow); err != nil {
		t.Fatalf("create held work: %v", err)
	}

	output := captureStdout(t, func() { cmdAttention([]string{"--json", "--home", homePath}) })
	var report attention.Report
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("parse attention JSON: %v\n%s", err, output)
	}
	if report.Counts.NeedsYou != 1 || report.Counts.NeedsYouGroups != 1 || report.Counts.Watching != 1 {
		t.Fatalf("live counts = %+v, want one held needs_you and one safe watching", report.Counts)
	}
	foundCanMove := false
	foundHeld := false
	for _, group := range report.Groups {
		switch group.Key {
		case "workbench:can-move":
			foundCanMove = true
			if group.Band != attention.BandWatching || group.Action == nil || group.Action.ID != "workbench.dispatch_next" || !containsAttentionString(group.ItemIDs, ready.ID) {
				t.Fatalf("safe live work did not project as watching/can-move: %+v", group)
			}
		case "workbench:blocked-on-you":
			foundHeld = true
			if group.Band != attention.BandNeedsYou || !containsAttentionString(group.ItemIDs, "work_real_hold") || containsAttentionString(group.ItemIDs, ready.ID) {
				t.Fatalf("held live work did not remain the sole needs_you burden: %+v", group)
			}
		case "executor:unassigned", "executor:codex":
			t.Fatalf("executor projection duplicated workbench burden: %+v", group)
		}
	}
	if !foundCanMove || !foundHeld {
		t.Fatalf("live projection missing required groups: can_move=%v held=%v groups=%+v", foundCanMove, foundHeld, report.Groups)
	}
}

func containsAttentionString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

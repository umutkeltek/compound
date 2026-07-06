package main

import (
	"encoding/json"
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/work"
)

func TestExecutorsCLIProjectsWorkbenchAndLeases(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatalf("init home: %v", err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	codexWork, err := store.Create(work.CreateInput{
		Owner:            "codex",
		Actor:            "operator-test",
		SuccessPredicate: "Codex edits code with proof",
		DesiredOutcome:   "Projection row shows Codex as running",
	}, cliTestNow)
	if err != nil {
		t.Fatalf("create codex work: %v", err)
	}
	if _, err := store.Claim(codexWork.ID, "codex", cliTestNow.Add(time.Minute)); err != nil {
		t.Fatalf("claim codex work: %v", err)
	}
	readyWork, err := store.Create(work.CreateInput{
		Owner:            "operator",
		Actor:            "operator-test",
		SuccessPredicate: "Ready work needs executor choice",
		DesiredOutcome:   "Projection row shows unassigned ready work",
	}, cliTestNow.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("create ready work: %v", err)
	}

	out := captureStdout(t, func() {
		cmdExecutors([]string{"--json"})
	})
	var projection work.ExecutorsProjection
	if err := json.Unmarshal([]byte(out), &projection); err != nil {
		t.Fatalf("parse executors projection: %v\n%s", err, out)
	}
	if projection.SchemaVersion != work.ExecutorsProjectionSchemaVersion ||
		projection.AuthorityRef != work.ExecutorsAuthorityRef ||
		projection.Status != "ok" {
		t.Fatalf("unexpected projection header: %+v", projection)
	}
	if !containsString(projection.SourceRefs, "compound.workbench") ||
		!containsString(projection.SourceRefs, "compound.work.leases") ||
		!containsString(projection.SourceRefs, "compound.executor.profile_guide") {
		t.Fatalf("projection missing source refs: %+v", projection.SourceRefs)
	}
	codex := requireExecutorRow(t, projection, "codex")
	if codex.StatusSource != "profile_guide" || codex.RunningCount != 1 || codex.LeaseCount != 1 || len(codex.CurrentWork) != 1 {
		t.Fatalf("codex row should be profile-guided with claimed work and lease: %+v", codex)
	}
	if codex.CurrentWork[0].WorkID != codexWork.ID || codex.CurrentWork[0].State != "claimed" || codex.CurrentWork[0].Title == "" {
		t.Fatalf("codex current work mismatch: %+v", codex.CurrentWork)
	}
	unassigned := requireExecutorRow(t, projection, "unassigned")
	if unassigned.StatusSource != "workbench" || unassigned.ReadyCount != 1 || unassigned.NextAssigned == nil || unassigned.NextAssigned.WorkID != readyWork.ID {
		t.Fatalf("unassigned row should expose ready work from Workbench: %+v", unassigned)
	}
	if projection.Counts.ProfileGuide != 6 || projection.Counts.Running != 1 || projection.Counts.Ready != 1 || projection.Counts.Leases != 1 {
		t.Fatalf("unexpected projection counts: %+v", projection.Counts)
	}
}

func requireExecutorRow(t *testing.T, projection work.ExecutorsProjection, id string) work.ExecutorRow {
	t.Helper()
	for _, row := range projection.Executors {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("missing executor row %q in %+v", id, projection.Executors)
	return work.ExecutorRow{}
}

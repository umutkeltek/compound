package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/work"
)

func TestWorkLandCheckDefaultTextReportsForeignDirtPreservation(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	repo, base, tip := initLandingCLIRepo(t)
	remote := filepath.Join(t.TempDir(), "origin.git")
	runLandingCLIGit(t, filepath.Dir(remote), "init", "--bare", remote)
	runLandingCLIGit(t, repo, "remote", "add", "origin", remote)
	runLandingCLIGit(t, repo, "push", "-u", "origin", "main")
	runLandingCLIGit(t, repo, "merge", "--ff-only", tip)
	runLandingCLIGit(t, repo, "push", "origin", "main")

	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(work.CreateInput{
		Owner:            "codex",
		Actor:            "codex",
		SuccessPredicate: "default land-check text preserves foreign WIP visibility",
		GitDelivery: &envelope.GitDeliveryContract{
			TargetWorktreeRoot: repo,
			TargetLocalRef:     "refs/heads/main",
			ExpectedBaseSHA:    base,
			PublicationMode:    "required",
			PublicationRef:     "refs/remotes/origin/main",
		},
	}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: w.State, Actor: "codex", ArtifactID: "commit:" + tip}, cliTestNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "foreign-lane-wip.txt"), []byte("neighbor lane WIP\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		cmdWork([]string{"land-check", w.ID, "--tip", tip, "--dry-run"})
	})
	if !strings.Contains(out, "status=landed") ||
		!strings.Contains(out, "next_action:") ||
		!strings.Contains(out, "preserve") ||
		!strings.Contains(out, "foreign-lane-wip.txt") {
		t.Fatalf("default CLI text must expose the foreign-WIP preservation warning:\n%s", out)
	}
}

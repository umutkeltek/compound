package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/work"
)

func TestWorkCreateCLIPropagatesExpectedBaseSHA(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	base := strings.Repeat("a", 40)

	planOut := captureStdout(t, func() {
		cmdWork([]string{
			"create-plan", "--owner", "codex", "--predicate", "land exactly once",
			"--expected-base-sha", base, "--by", "codex", "--dry-run", "--json",
		})
	})
	var plan work.CreatePlan
	if err := json.Unmarshal([]byte(planOut), &plan); err != nil {
		t.Fatalf("parse create-plan JSON: %v\n%s", err, planOut)
	}
	if plan.Work.ExpectedBaseSHA != base {
		t.Fatalf("planned expected_base_sha = %q, want %q", plan.Work.ExpectedBaseSHA, base)
	}
	if !landingCommandHasPair(plan.CreateCommand, "--expected-base-sha", base) {
		t.Fatalf("create command dropped expected base: %v", plan.CreateCommand)
	}

	createOut := captureStdout(t, func() {
		cmdWork([]string{
			"create", "--owner", "codex", "--predicate", "persist exact base",
			"--expected-base-sha", base, "--by", "codex",
		})
	})
	if !strings.Contains(createOut, `"expected_base_sha": "`+base+`"`) {
		t.Fatalf("created WorkEnvelope dropped expected base:\n%s", createOut)
	}
}

func TestWorkLandCheckCLIUsesExplicitRepoAndRecordsProof(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	if err := home.Init(homePath, "test", cliTestNow); err != nil {
		t.Fatal(err)
	}
	repo, base, tip := initLandingCLIRepo(t)
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(work.CreateInput{
		Owner:            "codex",
		Actor:            "codex",
		SuccessPredicate: "candidate is visibly ready to land",
		ExpectedBaseSHA:  base,
	}, cliTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(w.ID, work.AdvanceInput{To: w.State, Actor: "codex", ArtifactID: "commit:" + tip}, cliTestNow.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	dryOut := captureStdout(t, func() {
		cmdWork([]string{
			"land-check", w.ID, "--repo", repo, "--tip", tip, "--target-ref", "main",
			"--expected-main-sha", base, "--dry-run", "--json",
		})
	})
	var dry work.LandCheckReport
	if err := json.Unmarshal([]byte(dryOut), &dry); err != nil {
		t.Fatalf("parse dry land-check JSON: %v\n%s", err, dryOut)
	}
	if dry.Status != "ready_to_land" || dry.Recorded || dry.RepoPath != repo {
		t.Fatalf("dry land-check = %+v", dry)
	}

	runLandingCLIGit(t, repo, "merge", "--ff-only", tip)
	landedOut := captureStdout(t, func() {
		cmdWork([]string{
			"land-check", w.ID, "--repo", repo, "--tip", tip, "--target-ref", "main", "--json",
		})
	})
	var landed work.LandCheckReport
	if err := json.Unmarshal([]byte(landedOut), &landed); err != nil {
		t.Fatalf("parse landed land-check JSON: %v\n%s", err, landedOut)
	}
	if landed.Status != "landed" || !landed.Recorded || !strings.HasPrefix(landed.EvidenceRef, "land-check:") {
		t.Fatalf("recorded land-check = %+v", landed)
	}
	if _, err := os.Stat(landed.ProofPath); err != nil {
		t.Fatalf("recorded proof missing: %v", err)
	}
}

func landingCommandHasPair(command []string, flag, value string) bool {
	for i := 0; i+1 < len(command); i++ {
		if command[i] == flag && command[i+1] == value {
			return true
		}
	}
	return false
}

func initLandingCLIRepo(t *testing.T) (repo, base, tip string) {
	t.Helper()
	repo = filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runLandingCLIGit(t, repo, "init", "-b", "main")
	runLandingCLIGit(t, repo, "config", "user.name", "Compound Test")
	runLandingCLIGit(t, repo, "config", "user.email", "compound@example.invalid")
	if err := os.WriteFile(filepath.Join(repo, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runLandingCLIGit(t, repo, "add", "base.txt")
	runLandingCLIGit(t, repo, "commit", "-m", "base")
	base = landingCLIOutput(t, repo, "rev-parse", "HEAD")
	runLandingCLIGit(t, repo, "switch", "-c", "candidate")
	if err := os.WriteFile(filepath.Join(repo, "candidate.txt"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runLandingCLIGit(t, repo, "add", "candidate.txt")
	runLandingCLIGit(t, repo, "commit", "-m", "candidate")
	tip = landingCLIOutput(t, repo, "rev-parse", "HEAD")
	runLandingCLIGit(t, repo, "switch", "main")
	return repo, base, tip
}

func runLandingCLIGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func landingCLIOutput(t *testing.T, repo string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

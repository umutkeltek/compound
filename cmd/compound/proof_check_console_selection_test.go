package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"compound/internal/authority/gitrepository"
	"compound/internal/work"
)

// The console gates the default battery drops are exactly the named console
// subset that --ci-console runs: one rule (Dir=console), one list, no drift.
func TestDefaultBatteryDropsExactlyTheConsoleSubset(t *testing.T) {
	t.Parallel()
	full, err := proofCheckSteps("")
	if err != nil {
		t.Fatal(err)
	}
	var dropped []string
	for _, step := range full {
		if work.IsConsoleProofGate(step) {
			dropped = append(dropped, step.Name)
		}
	}
	slices.Sort(dropped)
	if want := work.CIConsoleGateNames(); !slices.Equal(dropped, want) {
		t.Fatalf("default battery drops %v, want the console subset %v", dropped, want)
	}
	kept := work.WithoutConsoleProofGates(full)
	if len(kept)+len(dropped) != len(full) || work.HasConsoleProofGate(kept) {
		t.Fatalf("selection lost or kept the wrong gates: full=%d kept=%d dropped=%d", len(full), len(kept), len(dropped))
	}
	for _, name := range []string{"go-test", "go-test-home", "go-vet", "go-build", "gofmt", "diff-check", "bun-tools-test", "architecture-doctor", "redact-public"} {
		if !slices.ContainsFunc(kept, func(step proofCheckStep) bool { return step.Name == name }) {
			t.Fatalf("default battery lost non-console gate %s", name)
		}
	}
}

// TestCanonicalPlanSelectsConsoleGatesFromTheSealedRange drives the certifying
// execution path (a sealed source snapshot with no stored plan) and proves the
// plan follows the proved range: zero console gates when console/ is untouched,
// the full console subset when it changed, and the full subset on request.
func TestCanonicalPlanSelectsConsoleGatesFromTheSealedRange(t *testing.T) {
	t.Parallel()
	repo := initCompoundCommandGitRepo(t)
	consoleGates := func(t *testing.T, ctx context.Context, baseSHA string) int {
		t.Helper()
		source, err := gitrepository.CaptureCleanSnapshot(repo, baseSHA)
		if err != nil {
			t.Fatal(err)
		}
		report, err := runProofCheckWithRangeContext(ctx, repo, true, source.BaseSHA, source.HeadSHA, &source, false, false, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, step := range report.Steps {
			if work.IsConsoleProofGate(step) || strings.HasPrefix(step.Name, "console-") {
				count++
			}
		}
		return count
	}
	want := len(work.CIConsoleGateNames())

	base := proofSourceGitOutput(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("docs only\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCommandGit(t, repo, "commit", "-q", "-am", "docs only")
	if got := consoleGates(t, context.Background(), base); got != 0 {
		t.Fatalf("range without a console/ change planned %d console gates, want 0", got)
	}

	docsHead := proofSourceGitOutput(t, repo, "rev-parse", "HEAD")
	if err := os.MkdirAll(filepath.Join(repo, "console"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "console", "page.ts"), []byte("export const page = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCommandGit(t, repo, "add", "console/page.ts")
	runCommandGit(t, repo, "commit", "-q", "-m", "console change")
	if got := consoleGates(t, context.Background(), docsHead); got != want {
		t.Fatalf("range with a console/ change planned %d console gates, want %d", got, want)
	}

	head := proofSourceGitOutput(t, repo, "rev-parse", "HEAD")
	requested := work.ContextWithConsoleProofRequest(context.Background(), true)
	if got := consoleGates(t, requested, head); got != want {
		t.Fatalf("--with-console on an empty range planned %d console gates, want %d", got, want)
	}
	if got := consoleGates(t, context.Background(), head); got != 0 {
		t.Fatalf("empty range without --with-console planned %d console gates, want 0", got)
	}
}

// The certifying path seals a console-selected plan, then asks whether the
// admitted profile may publish the installed-runtime artifact. That question
// must be asked of the same selected inventory: re-deriving from the full
// inventory would silently refuse publication on every console-free battery.
func TestConsoleSelectedPlanKeepsRuntimeArtifactPublication(t *testing.T) {
	t.Parallel()
	repo := initCompoundCommandGitRepo(t)
	base := proofSourceGitOutput(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "README.md"), []byte("docs only\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCommandGit(t, repo, "commit", "-q", "-am", "docs only")
	source, err := gitrepository.CaptureCleanSnapshot(repo, base)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := work.ResolveConsoleProofSelection(context.Background(), repo, source.BaseSHA, source.HeadSHA, false)
	if err != nil || selection.Include {
		t.Fatalf("docs-only range selection = %+v err=%v, want console gates excluded", selection, err)
	}
	inventory := work.ConsoleSelectedProofInventory(proofCheckSteps, selection)
	plan, err := work.ResolveCanonicalProofPlan(repo, source, inventory)
	if err != nil {
		t.Fatal(err)
	}
	if work.HasConsoleProofGate(plan.Steps) {
		t.Fatal("sealed plan for a docs-only range carries console gates")
	}
	profile, err := work.ProofResourceProfileForCanonicalPlan(plan)
	if err != nil {
		t.Fatal(err)
	}
	publishes, err := work.CanonicalProofProfilePublishesCompoundRuntimeArtifact(plan, profile, inventory)
	if err != nil || !publishes {
		t.Fatalf("console-free admitted profile lost runtime artifact publication: publishes=%t err=%v", publishes, err)
	}
	publishes, err = work.CanonicalProofProfilePublishesCompoundRuntimeArtifact(plan, profile, proofCheckSteps)
	if err != nil || publishes {
		t.Fatalf("full-inventory re-derivation must disagree with the selected profile: publishes=%t err=%v", publishes, err)
	}
}

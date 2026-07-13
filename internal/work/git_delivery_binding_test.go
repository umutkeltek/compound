package work

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
)

func TestLegacyNonterminalWorkBindsGitDeliveryThroughAdvance(t *testing.T) {
	now := time.Date(2026, 7, 14, 1, 0, 0, 0, time.UTC)
	repo, base := initLandingRepo(t)
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "legacy work gains exact delivery authority"}, now)
	if err != nil {
		t.Fatal(err)
	}
	activateLandingWork(t, store, w.ID, now)
	contract := localGitDelivery(repo, base)
	if _, err := store.Advance(w.ID, AdvanceInput{
		To:          "running",
		Actor:       "codex",
		GitDelivery: contract,
	}, now.Add(3*time.Minute)); err == nil || !strings.Contains(err.Error(), "operator provenance") {
		t.Fatalf("ordinary executor must not bind immutable delivery authority, got %v", err)
	}

	plan, err := store.AdvancePlan(w.ID, now.Add(4*time.Minute), AdvancePlanOptions{
		Actor:       "codex",
		To:          "running",
		GitDelivery: contract,
		Steal:       true,
		StealReason: "operator-approved legacy delivery migration",
	})
	if err != nil {
		t.Fatal(err)
	}
	canonicalRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if plan.GitDelivery == nil || plan.GitDelivery.TargetWorktreeRoot != canonicalRepo || plan.GitDelivery.ExpectedBaseSHA != base {
		t.Fatalf("normalized delivery binding plan = %+v", plan)
	}
	if !commandHasFlagValue(plan.AdvanceCommand, "--target-worktree-root", canonicalRepo) ||
		!commandHasFlagValue(plan.AdvanceCommand, "--expected-base-sha", base) ||
		!contains(plan.AdvanceCommand, "--local-only") {
		t.Fatalf("advance command dropped delivery binding: %v", plan.AdvanceCommand)
	}

	bound, err := store.Advance(w.ID, AdvanceInput{
		To:          "running",
		Actor:       "operator:test",
		GitDelivery: contract,
		Steal:       true,
		StealReason: "operator-approved legacy delivery migration",
	}, now.Add(5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if bound.State != "running" || bound.GitDelivery == nil || *bound.GitDelivery != *plan.GitDelivery || bound.ExpectedBaseSHA != "" {
		t.Fatalf("bound legacy work = %+v", bound)
	}
	var action envelope.ActionIntent
	if err := store.readJSON(store.envPath(bound.RunIDs[len(bound.RunIDs)-1]), &action); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(action.Preview, "bind Git delivery") || !strings.Contains(action.Preview, canonicalRepo) || !strings.Contains(action.Preview, base) || action.Reversibility != "hard" {
		t.Fatalf("binding action does not carry exact authority: %+v", action)
	}

	if _, err := store.Advance(w.ID, AdvanceInput{To: "running", Actor: "operator:test", GitDelivery: contract}, now.Add(6*time.Minute)); err == nil || !strings.Contains(err.Error(), "already has git_delivery") {
		t.Fatalf("delivery rebinding must fail closed, got %v", err)
	}
}

func TestDeliveryBindingIsSameStateAndCompleteOnly(t *testing.T) {
	now := time.Date(2026, 7, 14, 1, 0, 0, 0, time.UTC)
	repo, base := initLandingRepo(t)
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "binding cannot hide a lifecycle transition"}, now)
	if err != nil {
		t.Fatal(err)
	}
	activateLandingWork(t, store, w.ID, now)

	if _, err := store.AdvancePlan(w.ID, now.Add(3*time.Minute), AdvancePlanOptions{
		Actor:       "operator:test",
		To:          "verifying",
		GitDelivery: localGitDelivery(repo, base),
	}); err == nil || !strings.Contains(err.Error(), "same-state") {
		t.Fatalf("delivery binding plus lifecycle transition should fail, got %v", err)
	}
	if _, err := store.AdvancePlan(w.ID, now.Add(4*time.Minute), AdvancePlanOptions{
		Actor: "operator:test",
		To:    "running",
		GitDelivery: &envelope.GitDeliveryContract{
			TargetWorktreeRoot: repo,
		},
	}); err == nil || !strings.Contains(err.Error(), "git_delivery") {
		t.Fatalf("partial delivery binding should fail, got %v", err)
	}
	unchanged, err := store.Get(w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.GitDelivery != nil || unchanged.State != "running" {
		t.Fatalf("rejected binding mutated work: %+v", unchanged)
	}
}

func TestRepoScopedControlPlaneCloseRequiresDeclaredDeliveryWithoutCommitProof(t *testing.T) {
	now := time.Date(2026, 7, 14, 1, 0, 0, 0, time.UTC)
	repo, _ := initLandingRepo(t)
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	contextID := "ctx_repo_scoped_close"
	writeContextPack(t, store, contextID)
	var pack envelope.ContextPack
	packPath := filepath.Join(store.home, "context", "packs", contextID+".json")
	if err := store.readJSON(packPath, &pack); err != nil {
		t.Fatal(err)
	}
	pack.Scope.Surface = repo
	if err := store.writeJSON(packPath, &pack); err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(CreateInput{
		Owner:            "codex",
		SuccessPredicate: "repo-scoped control-plane work cannot omit delivery evidence",
		ContextPackID:    contextID,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	activateLandingWork(t, store, w.ID, now)

	plan, err := store.ClosePlan(w.ID, now.Add(3*time.Minute), ClosePlanOptions{
		Actor:    "codex",
		Outcome:  "completed",
		MoveType: "control_plane_repair",
	})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != "not_closeable" || len(plan.CloseCommand) != 0 || !strings.Contains(plan.NoAction, "git_delivery") || !strings.Contains(plan.NoAction, contextID) {
		t.Fatalf("repo-scoped close plan must require declared delivery: %+v", plan)
	}
	if _, err := store.Close(w.ID, CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Actor:     "codex",
		Summary:   "must not close by omitting commit proof",
		ProofRefs: []string{"check:focused-tests"},
	}, now.Add(4*time.Minute)); err == nil || !strings.Contains(err.Error(), "git_delivery") {
		t.Fatalf("repo-scoped close omission should fail closed, got %v", err)
	}
}

func TestRepoTargetActionRequiresDeclaredDeliveryButAuditDoesNot(t *testing.T) {
	now := time.Date(2026, 7, 14, 1, 0, 0, 0, time.UTC)
	repo, _ := initLandingRepo(t)
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	w, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "repo target action cannot hide delivery"}, now)
	if err != nil {
		t.Fatal(err)
	}
	activateLandingWork(t, store, w.ID, now)
	action := envelope.ActionIntent{
		Header:        newHeader(envelope.SVActionIntent, "act_repo_target_close", now.Add(3*time.Minute), w.ID, "", "private", "tier1"),
		Actor:         "codex",
		Capability:    "source.edit",
		Action:        "edit_repo_file",
		Target:        envelope.ActionTarget{Kind: "file", ID: "README.md", Path: filepath.Join(repo, "README.md")},
		MutationClass: "local_reversible",
		Reversibility: "easy",
		BlastRadius:   "local",
		Preview:       "edit a tracked repository file",
		RollbackPlan:  "revert the file edit",
	}
	if err := action.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := store.writeJSON(store.envPath(action.ID), &action); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(w.ID, AdvanceInput{To: "running", Actor: "codex", RunID: action.ID}, now.Add(4*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Close(w.ID, CloseInput{
		Outcome:   "completed",
		MoveType:  "control_plane_repair",
		Actor:     "codex",
		Summary:   "must bind repo delivery",
		ProofRefs: []string{"check:focused-tests"},
	}, now.Add(5*time.Minute)); err == nil || !strings.Contains(err.Error(), "git_delivery") || !strings.Contains(err.Error(), action.ID) {
		t.Fatalf("repo-target action omission should fail closed, got %v", err)
	}

	audit, err := store.Create(CreateInput{Owner: "codex", SuccessPredicate: "repo audit remains non-mutating"}, now.Add(6*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	activateLandingWork(t, store, audit.ID, now.Add(6*time.Minute))
	closed, err := store.Close(audit.ID, CloseInput{
		Outcome:   "completed",
		MoveType:  "audit_only",
		Actor:     "codex",
		Summary:   "read-only audit",
		ProofRefs: []string{"check:audit"},
	}, now.Add(10*time.Minute))
	if err != nil {
		t.Fatalf("audit-only work should not require Git delivery: %v", err)
	}
	if closed.Work.State != "completed" {
		t.Fatalf("audit close = %+v", closed.Work)
	}
}

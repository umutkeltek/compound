package work

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/authority/gitrepository"
	"compound/internal/proofscope"
)

// nativePresubmitPlanReport runs one passing synthetic gate under the plan
// that build returns for the bound head. afterSeal, when set, edits the
// report's plan after it was sealed.
func nativePresubmitPlanReport(build func(head string) *ProofCheckPresubmit, afterSeal func(*ProofCheckPresubmit)) func(context.Context, PresubmitExecutionRequest) (ProofCheckReport, error) {
	return func(ctx context.Context, request PresubmitExecutionRequest) (ProofCheckReport, error) {
		source, steps, _, err := nativeSealFixture(ctx, request)
		if err != nil {
			return ProofCheckReport{}, err
		}
		plan := build(source.HeadSHA)
		if err = SealPresubmitInventory(ctx, steps, plan); err != nil {
			return ProofCheckReport{}, err
		}
		if afterSeal != nil {
			afterSeal(plan)
		}
		report := ProofCheckReport{SchemaVersion: ProofCheckPresubmitSchemaVersion, ProfileKind: ProofCheckProfilePresubmit, Status: "ok", SourceSnapshot: &source, Presubmit: plan}
		return RunNonCertifyingProofGates(ctx, report, steps, func(ctx context.Context, step ProofCheckStep) ProofCheckStep {
			step.Status = "ok"
			if err := gitrepository.RunCommand(ctx, exec.Command(step.Command[0], step.Command[1:]...)); err != nil {
				step.Status = "failed"
				step.OutputTail = err.Error()
			}
			return step
		})
	}
}

// emptyDeferralPlan is the plan proofscope derives for a governance-core
// change whose reached set already holds every root reader: nothing is
// deferred, and the deferral list is empty but not nil. NoGoFiles is set the
// same way to cover the other omitempty slice of the sealed plan.
func emptyDeferralPlan(head string) *ProofCheckPresubmit {
	scope := proofscope.Scope{
		Mode: proofscope.Affected, Reason: "diff", Base: strings.Repeat("a", 40), Head: head,
		Changed:        []string{"compound/internal/envelope"},
		Reached:        []string{"compound/internal/arch", "compound/internal/envelope"},
		RootReaders:    []string{"compound/internal/arch"},
		GovernanceCore: "internal/envelope/envelope.go",
	}
	return &ProofCheckPresubmit{Head: head, Plan: proofscope.Presubmit(scope, nil), NoGoFiles: []string{}}
}

func TestPresubmitSealedPlanWithEmptyDeferralSucceeds(t *testing.T) {
	if plan := emptyDeferralPlan(strings.Repeat("c", 40)); plan.Plan.Deferred == nil || len(plan.Plan.Deferred) != 0 {
		t.Fatalf("fixture no longer derives an empty non-nil deferral list: %#v", plan.Plan.Deferred)
	}
	s, request := nativePresubmitFixture(t)
	if _, err := s.RunPresubmitProducer(context.Background(), request, nativePresubmitPlanReport(emptyDeferralPlan, nil)); err != nil {
		t.Fatalf("a plan that defers nothing failed its own sealed inventory: %v", err)
	}
}

func TestPresubmitPlanChangedAfterSealIsRefused(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	_, err := s.RunPresubmitProducer(context.Background(), request, nativePresubmitPlanReport(emptyDeferralPlan, func(plan *ProofCheckPresubmit) {
		plan.Plan.Deferred = append(plan.Plan.Deferred, "compound/internal/synthetic")
	}))
	if err == nil || !strings.Contains(err.Error(), "differs from sealed inventory") {
		t.Fatalf("a deferral written after sealing was accepted: %v", err)
	}
}

func nativeSealFixture(ctx context.Context, request PresubmitExecutionRequest) (gitrepository.Snapshot, []ProofCheckStep, *ProofCheckPresubmit, error) {
	source, err := gitrepository.CaptureCleanSnapshotContext(ctx, request.Repo, request.BaseRef)
	if err != nil {
		return source, nil, nil, err
	}
	if err = BindPresubmitSource(ctx, source); err != nil {
		return source, nil, nil, err
	}
	return source, []ProofCheckStep{{Name: "synthetic-gate", Command: []string{"sh", "-c", "exit 0"}, ProofRef: "presubmit:synthetic"}}, &ProofCheckPresubmit{Head: source.HeadSHA}, nil
}

func TestPresubmitSealRejectsMismatchedPlanHead(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	_, err := s.RunPresubmitProducer(context.Background(), request, func(ctx context.Context, request PresubmitExecutionRequest) (ProofCheckReport, error) {
		_, steps, plan, err := nativeSealFixture(ctx, request)
		if err != nil {
			return ProofCheckReport{}, err
		}
		plan.Head = strings.Repeat("b", 40)
		if err = SealPresubmitInventory(ctx, steps, plan); err == nil {
			t.Fatal("sealed plan for another head")
		}
		return ProofCheckReport{}, err
	})
	if err == nil {
		t.Fatal("mismatched plan produced successful native execution")
	}
}

func TestPresubmitSealOwnsCallerCommandAndPlanCopies(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	_, err := s.RunPresubmitProducer(context.Background(), request, func(ctx context.Context, request PresubmitExecutionRequest) (ProofCheckReport, error) {
		source, steps, plan, err := nativeSealFixture(ctx, request)
		if err != nil {
			return ProofCheckReport{}, err
		}
		if err = SealPresubmitInventory(ctx, steps, plan); err != nil {
			return ProofCheckReport{}, err
		}
		steps[0].Command[2] = "exit 91"
		plan.Head = strings.Repeat("b", 40)
		rt := ctx.Value(nativePresubmitRuntimeKey{}).(*presubmitRuntime)
		if rt.out.GateInventory[0].Command[2] != "exit 0" || rt.out.PresubmitPlan.Head != source.HeadSHA {
			t.Fatal("caller mutation changed sealed native inventory")
		}
		report := ProofCheckReport{}
		if err = PresubmitGateStarted(ctx, steps[0], &report); err == nil {
			t.Fatal("mutated caller command accepted under stale digest")
		}
		return ProofCheckReport{}, err
	})
	if err == nil {
		t.Fatal("mutated caller inventory succeeded")
	}
}

func TestPresubmitSourceDriftRefusesFirstGate(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	_, err := s.RunPresubmitProducer(context.Background(), request, func(ctx context.Context, request PresubmitExecutionRequest) (ProofCheckReport, error) {
		_, steps, plan, err := nativeSealFixture(ctx, request)
		if err != nil {
			return ProofCheckReport{}, err
		}
		if err = SealPresubmitInventory(ctx, steps, plan); err != nil {
			return ProofCheckReport{}, err
		}
		if err = os.WriteFile(filepath.Join(request.Repo, "seed.txt"), []byte("synthetic source drift\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		report := ProofCheckReport{}
		if err = PresubmitGateStarted(ctx, steps[0], &report); err == nil {
			t.Fatal("first gate accepted dirty source after scope derivation")
		}
		rt := ctx.Value(nativePresubmitRuntimeKey{}).(*presubmitRuntime)
		if rt.nextGate != 0 {
			t.Fatal("source drift consumed first gate start")
		}
		return ProofCheckReport{}, err
	})
	if err == nil {
		t.Fatal("source drift succeeded")
	}
}

func TestPresubmitContextAuthorityHasDetachedSource(t *testing.T) {
	s, request := nativePresubmitFixture(t)
	_, err := s.RunPresubmitProducer(context.Background(), request, func(ctx context.Context, request PresubmitExecutionRequest) (ProofCheckReport, error) {
		source, _, _, err := nativeSealFixture(ctx, request)
		if err != nil {
			return ProofCheckReport{}, err
		}
		authority, _ := PresubmitExecutionFromContext(ctx)
		authority.SourceSnapshot.HeadSHA = strings.Repeat("b", 40)
		again, _ := PresubmitExecutionFromContext(ctx)
		if again.SourceSnapshot.HeadSHA != source.HeadSHA {
			t.Fatal("context metadata aliases native source authority")
		}
		return ProofCheckReport{}, errors.New("synthetic completed accessor check")
	})
	if err == nil {
		t.Fatal("synthetic accessor-check outcome lost")
	}
}

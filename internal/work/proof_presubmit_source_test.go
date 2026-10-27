package work

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/authority/gitrepository"
)

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

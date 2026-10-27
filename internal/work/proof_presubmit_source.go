package work

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"

	"compound/internal/authority/gitrepository"
)

type PresubmitExecutionBinding struct {
	Request               PresubmitExecutionRequest `json:"request"`
	ExpectedHead          string                    `json:"expected_head"`
	RepositoryFingerprint string                    `json:"repository_fingerprint"`
	ExecutableSHA256      string                    `json:"executable_sha256"`
}

func presubmitBindingDigest(out PresubmitExecutionResult) (string, error) {
	body, err := json.Marshal(PresubmitExecutionBinding{Request: out.Request, ExpectedHead: out.ExpectedHead, RepositoryFingerprint: out.RepositoryFingerprint, ExecutableSHA256: out.ExecutableSHA256})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:]), nil
}

func BindPresubmitSource(ctx context.Context, source gitrepository.Snapshot) error {
	rt, ok := ctx.Value(nativePresubmitRuntimeKey{}).(*presubmitRuntime)
	if !ok {
		return fmt.Errorf("native presubmit source requires admitted runtime")
	}
	if err := gitrepository.ValidateSnapshotShape(source); err != nil {
		return err
	}
	if source.HeadSHA != rt.out.ExpectedHead || source.BaseSHA != rt.out.Request.BaseRef || source.RepositoryFingerprint != rt.out.RepositoryFingerprint || !source.Clean {
		return fmt.Errorf("presubmit source differs from exact Work delivery/commit binding")
	}
	if rt.out.SourceSnapshot != nil {
		return fmt.Errorf("native presubmit source already sealed")
	}
	rt.out.SourceSnapshot = &source
	return rt.checkpoint("source", "sealed")
}

type presubmitSealedInventory struct {
	Source gitrepository.Snapshot `json:"source"`
	Steps  []ProofCheckStep       `json:"steps"`
	Plan   ProofCheckPresubmit    `json:"plan"`
}

func (rt *presubmitRuntime) inventoryBytes() ([]byte, error) {
	if rt.out.SourceSnapshot == nil || rt.out.PresubmitPlan == nil {
		return nil, fmt.Errorf("native presubmit lacks sealed source/inventory")
	}
	return json.Marshal(presubmitSealedInventory{Source: *rt.out.SourceSnapshot, Steps: rt.out.GateInventory, Plan: *rt.out.PresubmitPlan})
}

func (rt *presubmitRuntime) checkInventory() error {
	body, err := rt.inventoryBytes()
	if err != nil {
		return err
	}
	sum := sha256.Sum256(body)
	if hex.EncodeToString(sum[:]) != rt.out.InventoryDigest {
		return fmt.Errorf("native presubmit sealed inventory digest changed")
	}
	if rt.out.SourceSnapshot.HeadSHA != rt.out.ExpectedHead || rt.out.SourceSnapshot.BaseSHA != rt.out.Request.BaseRef || rt.out.SourceSnapshot.RepositoryFingerprint != rt.out.RepositoryFingerprint || rt.out.PresubmitPlan.Head != rt.out.ExpectedHead {
		return fmt.Errorf("native presubmit inventory source binding changed")
	}
	return nil
}

func SealPresubmitInventory(ctx context.Context, steps []ProofCheckStep, plan *ProofCheckPresubmit) error {
	rt, ok := ctx.Value(nativePresubmitRuntimeKey{}).(*presubmitRuntime)
	if !ok {
		return nil
	}
	if rt.out.SourceSnapshot == nil || rt.out.InventoryDigest != "" || len(steps) == 0 || plan == nil {
		return fmt.Errorf("native presubmit inventory requires one source-bound nonempty derivation")
	}
	if plan.Head != rt.out.SourceSnapshot.HeadSHA {
		return fmt.Errorf("native presubmit plan head differs from sealed source")
	}
	if err := gitrepository.RevalidateSnapshotContext(ctx, rt.out.Request.Repo, *rt.out.SourceSnapshot); err != nil {
		return err
	}
	// Roundtrip at the ownership seam: neither caller commands nor its nested
	// plan slices/maps retain aliases into native authority.
	body, err := json.Marshal(presubmitSealedInventory{Source: *rt.out.SourceSnapshot, Steps: steps, Plan: *plan})
	if err != nil {
		return err
	}
	var owned presubmitSealedInventory
	if err = json.Unmarshal(body, &owned); err != nil {
		return err
	}
	rt.out.SourceSnapshot = &owned.Source
	rt.out.GateInventory = owned.Steps
	rt.out.PresubmitPlan = &owned.Plan
	sum := sha256.Sum256(body)
	rt.out.InventoryDigest = hex.EncodeToString(sum[:])
	return rt.checkpoint("inventory", "sealed")
}

func (rt *presubmitRuntime) validateReport(report ProofCheckReport) error {
	if err := rt.checkInventory(); err != nil {
		return err
	}
	if report.DryRun || rt.nextGate != len(rt.out.GateInventory) || rt.completedGates != len(rt.out.GateInventory) {
		return fmt.Errorf("native presubmit report lacks completed admitted gates")
	}
	if report.ProfileKind != ProofCheckProfilePresubmit || report.SchemaVersion != ProofCheckPresubmitSchemaVersion {
		return fmt.Errorf("native presubmit requires noncertifying presubmit report kind/schema")
	}
	if report.SourceSnapshot == nil || *rt.out.SourceSnapshot != *report.SourceSnapshot {
		return fmt.Errorf("native presubmit report lacks exact sealed source")
	}
	if len(report.Steps) != len(rt.out.GateInventory) || !reflect.DeepEqual(report.Presubmit, rt.out.PresubmitPlan) {
		return fmt.Errorf("native presubmit report differs from sealed inventory")
	}
	for i, step := range report.Steps {
		if !samePresubmitStep(step, rt.out.GateInventory[i]) {
			return fmt.Errorf("native presubmit report gate %d differs from sealed inventory", i)
		}
	}
	attempt, err := rt.store.runAttempts().Get(rt.out.RunAttemptID)
	if err != nil {
		return err
	}
	if attempt.Launch == nil || attempt.Launch.CommittedAt == nil {
		return fmt.Errorf("native presubmit success requires actual committed child launch")
	}
	return nil
}

func samePresubmitStep(a, b ProofCheckStep) bool {
	return a.Name == b.Name && reflect.DeepEqual(a.Command, b.Command) && a.Dir == b.Dir && a.ProofRef == b.ProofRef && a.SkipIfMissing == b.SkipIfMissing && a.CIPortable == b.CIPortable
}

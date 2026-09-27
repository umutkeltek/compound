package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/policy"
	"compound/internal/work"
)

func TestWorkDeliveryCLIRequestIsExactAndExclusive(t *testing.T) {
	base := []string{"--delivery-action", "act_original", "--delivery-kind", envelope.DeliveryPrivateRuntime, "--delivery-evidence", "artifact:sha256:proof"}
	request, err := parseWorkDeliveryRequest(base)
	if err != nil || request == nil || request.ActionIntentID != "act_original" || request.Kind != envelope.DeliveryPrivateRuntime || request.EvidenceRef != "artifact:sha256:proof" {
		t.Fatalf("request=%+v err=%v", request, err)
	}
	for _, args := range [][]string{nil, {"--to", "running"}, {"--execute", "--", "synthetic", "--delivery-action", "not-a-work-binding"}} {
		if request, err := parseWorkDeliveryRequest(args); err != nil || request != nil {
			t.Fatalf("ordinary advance: %+v %v", request, err)
		}
	}
	for _, args := range [][]string{base[:2], base[:4], {"--delivery-action", "", "--delivery-kind", envelope.DeliveryPrivateRuntime, "--delivery-evidence", "ref"}} {
		if _, err := parseWorkDeliveryRequest(args); err == nil {
			t.Fatalf("accepted partial request: %v", args)
		}
	}
	for _, flag := range []string{"--execute", "--lane", "--launch-profile", "--effort", "--expected-selection-revision", "--target-worktree-root", "--local-only", "--run-attempt", "--artifact", "--verification", "--context-pack", "--harness", "--prompt", "--workspace", "--permission-mode", "--"} {
		if _, err := parseWorkDeliveryRequest(append(append([]string(nil), base...), flag, "value")); err == nil {
			t.Errorf("accepted mixed operation %s", flag)
		}
	}
}

func TestWorkDeliveryCaptureCLIJourney(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)
	now := time.Now().UTC()
	if err := home.Init(homePath, "test", now); err != nil {
		t.Fatal(err)
	}
	s, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.Create(work.CreateInput{Owner: "operator:test", Actor: "operator:test", SuccessPredicate: "private readback is bound before close", State: "ready"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Claim(w.ID, "operator:test", now); err != nil {
		t.Fatal(err)
	}
	w, err = s.Advance(w.ID, work.AdvanceInput{To: "running", Actor: "operator:test"}, now)
	if err != nil {
		t.Fatal(err)
	}
	repo, base, _ := initLandingCLIRepo(t)
	repo, err = filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("docs_private/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(repo, "docs_private"), 0o700); err != nil {
		t.Fatal(err)
	}
	path := "docs_private/runtime.json"
	action := envelope.ActionIntent{
		Header: envelope.Header{SchemaVersion: envelope.SVActionIntent, ID: "act_private_cli", CreatedAt: now.Format(time.RFC3339Nano), Producer: envelope.Producer{Kind: "cli", Name: "compound"}, IDs: envelope.IDs{WorkID: w.ID}, Privacy: "private", Risk: "tier1", Uncertainty: envelope.Uncertainty{Level: "known"}},
		Actor:  "operator:test", Capability: "build-implement", Action: "update_private_runtime",
		Target:        envelope.ActionTarget{Kind: "path", ID: w.ID, Path: filepath.Join(repo, path)},
		MutationClass: "local_reversible", Reversibility: "easy", BlastRadius: "local", RollbackPlan: "restore exact prior absence",
	}
	action.Authority = envelope.Authority{SourceClass: "runtime", Owner: "compound", Status: "current"}
	cfg, _, err := policy.LoadConfig(homePath)
	if err != nil {
		t.Fatal(err)
	}
	w, err = s.RecordAction(w.ID, action, policy.Evaluate(action, cfg, now), now)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("observed private fixture")
	if err := os.WriteFile(filepath.Join(repo, path), body, 0o600); err != nil {
		t.Fatal(err)
	}
	request := work.PrivateRuntimeDeliveryCaptureRequest{
		SchemaVersion: work.PrivateRuntimeDeliveryCaptureSchema, CaptureID: "cli_readback", ActionIntentID: action.ID,
		RepositoryRoot: repo, RepositoryRef: base, ExpectedWorkState: w.State,
		ExpectedPolicyDecisionID: w.PolicyDecisionID, ExpectedActionIntentID: w.ActionIntentIDs[len(w.ActionIntentIDs)-1],
		Files: []work.PrivateRuntimeDeliveryCaptureFile{{Path: path, AfterSHA256: fmt.Sprintf("%x", sha256.Sum256(body)), BeforeAbsent: true}},
	}
	requestBody, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(t.TempDir(), "request.json")
	if err := os.WriteFile(requestPath, requestBody, 0o600); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		cmdWork([]string{"delivery-capture", w.ID, "--request", requestPath, "--by", "operator:test", "--json"})
	})
	var result work.PrivateRuntimeDeliveryCaptureResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("capture output: %v\n%s", err, out)
	}
	if result.Status != "captured_not_bound" || result.ReceiptID == "" {
		t.Fatalf("capture=%+v", result)
	}
	current, err := s.Get(w.ID)
	if err != nil || len(current.NonSourceDelivery) != 0 || current.State != "running" {
		t.Fatalf("capture changed binding or state: %+v %v", current, err)
	}
	out = captureStdout(t, func() {
		cmdWork([]string{"advance-plan", w.ID, "--to", "running", "--actor", "operator:test", "--delivery-action", action.ID, "--delivery-kind", result.Delivery.Kind, "--delivery-evidence", result.Delivery.EvidenceRef, "--dry-run", "--json"})
	})
	var plan work.AdvancePlan
	if err := json.Unmarshal([]byte(out), &plan); err != nil {
		t.Fatalf("plan: %v\n%s", err, out)
	}
	if len(plan.AdvanceCommand) < 3 {
		t.Fatalf("no guarded advance: %s", out)
	}
	captureStdout(t, func() { cmdWork(plan.AdvanceCommand[2:]) })
	current, err = s.Get(w.ID)
	if err != nil || len(current.NonSourceDelivery) != 1 {
		t.Fatalf("binding: %+v %v", current, err)
	}
	captureStdout(t, func() {
		cmdWork([]string{"close", w.ID, "--outcome", "completed", "--move-type", "control_plane_repair", "--by", "operator:test", "--summary", "observed and bound private readback", "--proof-ref", result.Delivery.EvidenceRef})
	})
	current, err = s.Get(w.ID)
	if err != nil || current.State != "completed" {
		t.Fatalf("close: %+v %v", current, err)
	}
}

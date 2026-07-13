package act

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
	"compound/internal/policy"
)

// These tests drive compound's ONE generic governed outbound-write wrapper against
// two SCRATCH configs (never a live config): one shaped like a switchback egress
// alias, one like a headroom config key. The same envelope governs both — only
// Target.Path/Target.ID differ — so proving the switchback shape and re-proving the
// headroom shape demonstrates outbound generality at zero schema cost. Every proof
// mutates only a mktemp file; nothing under a real operator home is touched.

// SEAL NOTE (structural proof — the guarantee is a compile error, not a runtime check):
// Adapter.write is unexported, so an external package — e.g. `package act_test` — is
// structurally UNABLE to call an adapter's write to mutate a config without a
// PolicyDecision. Both `act.NewConfigKeyAdapter().write(target, b)` and
// `act.ConfigKeyAdapter{}.write(target, b)` FAIL TO COMPILE ("write undefined (cannot
// refer to unexported method)"). That compile failure IS the by-construction seal:
// Execute — which refuses without an allowing PolicyDecision for the exact intent — is
// the only reachable path to a write. The tests below only ever mutate through Execute,
// obtaining the adapter via NewConfigKeyAdapter() exactly as an external caller must.

func fixedNow() time.Time { return time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC) }

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// mkIntent builds a valid repoint ActionIntent for a config-key write. It depends
// only on the exported envelope contract, so the test pins the PUBLIC shape of a
// governed outbound write, not any wrapper internal.
func mkIntent(t *testing.T, workID, capability, path, key string, now time.Time) envelope.ActionIntent {
	t.Helper()
	sum := sha256Hex(strings.Join([]string{workID, capability, path, key}, "|"))
	ai := envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            "act_" + sum[:12],
			CreatedAt:     now.UTC().Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Actor:         "codex",
		Capability:    capability,
		Action:        "repoint_config_key",
		Target:        envelope.ActionTarget{Kind: "config_key", ID: key, Path: path},
		MutationClass: "external_side_effect",
		Reversibility: "easy",
		BlastRadius:   "local",
		Preview:       "repoint " + key,
		RollbackPlan:  "restore the captured pre-image of " + key,
	}
	if err := ai.Validate(); err != nil {
		t.Fatalf("test intent invalid: %v", err)
	}
	return ai
}

// grantConfig returns the built-in policy plus ONE StandingGrant scoped to this
// intent's capability, external_side_effect class, easy reversibility ceiling,
// local blast radius, and exact target path — the D3 "set the envelope" shape.
func grantConfig(ai envelope.ActionIntent) policy.Config {
	cfg := policy.DefaultConfig()
	cfg.StandingGrants = append(cfg.StandingGrants, policy.StandingGrant{
		ID:                   "grant_outbound_config_key",
		Capability:           ai.Capability,
		MutationClass:        "external_side_effect",
		ReversibilityCeiling: "easy",
		BlastRadius:          "local",
		TargetPath:           ai.Target.Path,
	})
	return cfg
}

func scratch(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write scratch %s: %v", name, err)
	}
	return path
}

func newStore(t *testing.T) *ArtifactStore {
	t.Helper()
	store, err := NewArtifactStore(t.TempDir())
	if err != nil {
		t.Fatalf("new artifact store: %v", err)
	}
	return store
}

func mustRead(t *testing.T, adapter Adapter, target envelope.ActionTarget) string {
	t.Helper()
	v, err := adapter.ReadCurrent(target)
	if err != nil {
		t.Fatalf("read current: %v", err)
	}
	return string(v)
}

// Assertion 1: gate by construction — no valid allowing PolicyDecision => error,
// and the scratch config is byte-identical (Write was never called).
func TestExecuteRefusesWithoutPolicyDecision(t *testing.T) {
	now := fixedNow()
	path := scratch(t, "switchback.yaml", "egress:\n  auto/judge: providerA\n")
	adapter := NewConfigKeyAdapter()
	ai := mkIntent(t, "work_gate", "outbound.config_key.write", path, "egress.auto/judge", now)
	before, _ := os.ReadFile(path)

	_, err := Execute(newStore(t), adapter, ai, envelope.PolicyDecision{}, []byte("providerB"), now)
	if err == nil {
		t.Fatal("expected refusal when handed no valid PolicyDecision")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatalf("config mutated despite refusal:\nbefore=%q\nafter=%q", before, after)
	}
	if got := mustRead(t, adapter, ai.Target); got != "providerA" {
		t.Fatalf("value changed by a refused write: %q", got)
	}
}

// A decision minted for a DIFFERENT intent must not authorize this one. This test
// fails if the gate ignores action_intent_id (i.e. if depth is wrong).
func TestExecuteRefusesMismatchedDecision(t *testing.T) {
	now := fixedNow()
	path := scratch(t, "switchback.yaml", "egress:\n  auto/judge: providerA\n")
	adapter := NewConfigKeyAdapter()
	ai := mkIntent(t, "work_mismatch", "outbound.config_key.write", path, "egress.auto/judge", now)
	other := mkIntent(t, "work_other", "outbound.config_key.write", path, "egress.auto/judge", now)
	otherPD := policy.Evaluate(other, grantConfig(other), now)
	if !policy.Allowed(otherPD) {
		t.Fatalf("precondition: other decision should allow, got %s", otherPD.Outcome)
	}
	if otherPD.ActionIntentID == ai.ID {
		t.Fatal("precondition: the two intents must have distinct ids")
	}
	before, _ := os.ReadFile(path)
	if _, err := Execute(newStore(t), adapter, ai, otherPD, []byte("providerB"), now); err == nil {
		t.Fatal("expected refusal when the decision authorizes a different intent")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("config mutated despite a mismatched decision")
	}
}

// Assertion 2: ungranted external_side_effect => hold => refuse, scratch unchanged.
func TestExecuteRefusesUngranted(t *testing.T) {
	now := fixedNow()
	path := scratch(t, "switchback.yaml", "egress:\n  auto/judge: providerA\n")
	adapter := NewConfigKeyAdapter()
	ai := mkIntent(t, "work_hold", "outbound.config_key.write", path, "egress.auto/judge", now)

	pd := policy.Evaluate(ai, policy.DefaultConfig(), now)
	if policy.Allowed(pd) {
		t.Fatalf("precondition: ungranted external_side_effect must hold, got %s", pd.Outcome)
	}
	if pd.Outcome != "hold" {
		t.Fatalf("precondition: want hold, got %s", pd.Outcome)
	}
	before, _ := os.ReadFile(path)
	if _, err := Execute(newStore(t), adapter, ai, pd, []byte("providerB"), now); err == nil {
		t.Fatal("expected refusal on a hold decision")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("config mutated despite a hold decision")
	}
}

// runGovernedRoundTrip exercises assertions 3 + 4 against one scratch config:
// granted forward write flips A->B and emits a valid outward_delta receipt, then a
// revert built from the pre-image restores A with its own valid outward_delta
// receipt.
func runGovernedRoundTrip(t *testing.T, path, key, from, to string) {
	t.Helper()
	now := fixedNow()
	store := newStore(t)
	adapter := NewConfigKeyAdapter()
	ai := mkIntent(t, "work_"+key, "outbound.config_key.write", path, key, now)
	cfg := grantConfig(ai)

	// Assertion 3: granted => allow_with_obligations => write applies.
	pd := policy.Evaluate(ai, cfg, now)
	if !policy.Allowed(pd) {
		t.Fatalf("granted decision must allow, got %s (%v)", pd.Outcome, pd.Reasons)
	}
	if pd.Outcome != "allow_with_obligations" {
		t.Fatalf("granted non-read write must be allow_with_obligations, got %s", pd.Outcome)
	}
	if len(pd.Obligations) == 0 {
		t.Fatal("allow_with_obligations must carry obligations")
	}
	res, err := Execute(store, adapter, ai, pd, []byte(to), now)
	if err != nil {
		t.Fatalf("granted write failed: %v", err)
	}
	if got := mustRead(t, adapter, ai.Target); got != to {
		t.Fatalf("forward write did not apply: key=%q got %q want %q", key, got, to)
	}
	if res.Receipt.MoveType != "outward_delta" || res.Receipt.Outcome != "completed" {
		t.Fatalf("forward receipt shape wrong: move=%s outcome=%s", res.Receipt.MoveType, res.Receipt.Outcome)
	}
	if !res.Receipt.StateDelta.Changed {
		t.Fatal("forward receipt must set state_delta.changed=true")
	}
	if err := res.Receipt.Validate(); err != nil {
		t.Fatalf("forward receipt invalid: %v", err)
	}
	if err := res.Transition.Validate(); err != nil {
		t.Fatalf("forward transition invalid: %v", err)
	}
	assertProof(t, res, from, to)

	// Assertion 4: revert round-trips from the pre-image artifact.
	later := now.Add(time.Second)
	revert := RevertIntent(ai, res, later)
	if revert.ID == ai.ID {
		t.Fatal("revert must be a distinct intent")
	}
	revertPD := policy.Evaluate(revert, cfg, later)
	if !policy.Allowed(revertPD) {
		t.Fatalf("revert must be governed the same way, got %s", revertPD.Outcome)
	}
	preImage, err := store.Get(res.PreImageRef)
	if err != nil {
		t.Fatalf("load pre-image artifact: %v", err)
	}
	if string(preImage) != from {
		t.Fatalf("pre-image artifact wrong: got %q want %q", preImage, from)
	}
	revRes, err := Execute(store, adapter, revert, revertPD, preImage, later)
	if err != nil {
		t.Fatalf("revert write failed: %v", err)
	}
	if got := mustRead(t, adapter, ai.Target); got != from {
		t.Fatalf("revert did not restore: key=%q got %q want %q", key, got, from)
	}
	if revRes.Receipt.MoveType != "outward_delta" {
		t.Fatalf("revert receipt must be outward_delta, got %s", revRes.Receipt.MoveType)
	}
	if err := revRes.Receipt.Validate(); err != nil {
		t.Fatalf("revert receipt invalid: %v", err)
	}
}

func assertProof(t *testing.T, res Result, from, to string) {
	t.Helper()
	if res.PreImageRef == "" || res.PostImageRef == "" {
		t.Fatal("result must carry pre and post image refs")
	}
	if !containsRef(res.Receipt.ProofRefs, res.PreImageRef) || !containsRef(res.Receipt.ProofRefs, res.PostImageRef) {
		t.Fatalf("receipt proof_refs must carry both artifact refs, got %v", res.Receipt.ProofRefs)
	}
	if len(res.Transition.BeforeRefs) != 1 || res.Transition.BeforeRefs[0] != res.PreImageRef {
		t.Fatalf("transition before_refs must be [pre], got %v", res.Transition.BeforeRefs)
	}
	if len(res.Transition.AfterRefs) != 1 || res.Transition.AfterRefs[0] != res.PostImageRef {
		t.Fatalf("transition after_refs must be [post], got %v", res.Transition.AfterRefs)
	}
	if string(res.PreImage) != from {
		t.Fatalf("captured pre-image wrong: got %q want %q", res.PreImage, from)
	}
	if string(res.PostImage) != to {
		t.Fatalf("captured post-image wrong: got %q want %q", res.PostImage, to)
	}
}

func containsRef(refs []string, want string) bool {
	for _, r := range refs {
		if r == want {
			return true
		}
	}
	return false
}

// Assertion 3+4, first consumer: switchback-shaped egress alias, string value.
func TestGovernedWriteSwitchbackShape(t *testing.T) {
	path := scratch(t, "switchback.yaml", "egress:\n  auto/judge: providerA\n")
	runGovernedRoundTrip(t, path, "egress.auto/judge", "providerA", "providerB")
}

// Assertion 5, second consumer: headroom-shaped config key, SAME envelope, only
// Target.Path/Target.ID differ; the value flips true->false and back.
func TestGovernedWriteHeadroomShape(t *testing.T) {
	path := scratch(t, "headroom.yaml", "memory:\n  inject: true\n")
	runGovernedRoundTrip(t, path, "memory.inject", "true", "false")
}

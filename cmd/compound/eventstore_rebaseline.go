package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"compound/internal/act"
	"compound/internal/envelope"
	"compound/internal/eventstore"
	"compound/internal/finding"
	"compound/internal/home"
	"compound/internal/policy"
)

// eventStoreRebaselineCapability scopes the ActionIntent this verb mints; an
// operator's standing grant covers exactly this capability + external_side_effect
// class + the sidecar path, mirroring how `compound act repoint` scopes its own
// default capability (see act_cli.go).
const eventStoreRebaselineCapability = "eventstore.stream_baseline.rewrite"

const rebaselinePendingSchema = "compound.eventstore.rebaseline_pending.v0.1"

type rebaselinePending struct {
	SchemaVersion     string              `json:"schema_version"`
	SidecarPath       string              `json:"sidecar_path"`
	PriorExisted      bool                `json:"prior_existed"`
	PreImageRef       string              `json:"pre_image_ref"`
	PostImageRef      string              `json:"post_image_ref"`
	BeforeEvidenceRef string              `json:"before_evidence_ref"`
	AfterEvidenceRef  string              `json:"after_evidence_ref"`
	ActionIntentID    string              `json:"action_intent_id"`
	PolicyDecisionID  string              `json:"policy_decision_id"`
	AuthorityRevision string              `json:"authority_revision"`
	Receipt           envelope.Receipt    `json:"receipt"`
	Transition        envelope.Transition `json:"transition"`
}

func (p rebaselinePending) Validate(expectedSidecar string) error {
	if p.SchemaVersion != rebaselinePendingSchema {
		return fmt.Errorf("rebaseline pending schema_version=%q, want %q", p.SchemaVersion, rebaselinePendingSchema)
	}
	if strings.TrimSpace(p.SidecarPath) == "" || p.SidecarPath != expectedSidecar {
		return fmt.Errorf("rebaseline pending sidecar_path=%q, want %q", p.SidecarPath, expectedSidecar)
	}
	for name, value := range map[string]string{
		"pre_image_ref": p.PreImageRef, "post_image_ref": p.PostImageRef,
		"before_evidence_ref": p.BeforeEvidenceRef, "after_evidence_ref": p.AfterEvidenceRef,
		"action_intent_id": p.ActionIntentID, "policy_decision_id": p.PolicyDecisionID,
		"authority_revision": p.AuthorityRevision,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("rebaseline pending %s is required", name)
		}
	}
	if err := p.Receipt.Validate(); err != nil {
		return fmt.Errorf("rebaseline pending receipt: %w", err)
	}
	if err := p.Transition.Validate(); err != nil {
		return fmt.Errorf("rebaseline pending transition: %w", err)
	}
	if len(p.Transition.ActionRefs) == 0 || p.Transition.ActionRefs[0] != p.ActionIntentID || p.Receipt.IDs.WorkID != p.Transition.IDs.WorkID {
		return fmt.Errorf("rebaseline pending proof identity mismatch")
	}
	if !hasRebaselineRef(p.Receipt.ProofRefs, p.PreImageRef) || !hasRebaselineRef(p.Receipt.ProofRefs, p.PostImageRef) ||
		!hasRebaselineRef(p.Receipt.StateDelta.Artifacts, p.PreImageRef) || !hasRebaselineRef(p.Receipt.StateDelta.Artifacts, p.PostImageRef) ||
		!hasRebaselineRef(p.Transition.BeforeRefs, p.PreImageRef) || !hasRebaselineRef(p.Transition.AfterRefs, p.PostImageRef) ||
		!hasRebaselineRef(p.Receipt.EvidenceRefs, p.BeforeEvidenceRef) || !hasRebaselineRef(p.Receipt.EvidenceRefs, p.AfterEvidenceRef) ||
		!hasRebaselineRef(p.Receipt.EvidenceRefs, p.PreImageRef) || !hasRebaselineRef(p.Receipt.EvidenceRefs, p.PostImageRef) ||
		!hasRebaselineRef(p.Transition.EvidenceRefs, p.BeforeEvidenceRef) || !hasRebaselineRef(p.Transition.EvidenceRefs, p.AfterEvidenceRef) ||
		!hasRebaselineRef(p.Transition.EvidenceRefs, p.PreImageRef) || !hasRebaselineRef(p.Transition.EvidenceRefs, p.PostImageRef) {
		return fmt.Errorf("rebaseline pending proof refs do not bind journaled pre/post images")
	}
	return nil
}

func hasRebaselineRef(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

const eventStoreRebaselineUsage = "usage:\n" +
	"  compound event-store rebaseline [--home P] [--work W] [--confirm] [--json]\n" +
	"    Governed re-baseline of the frozen-stream doctor: record hook-events.ndjson\n" +
	"    and traces.ndjson's CURRENT byte sizes as the new trusted baseline, so a home\n" +
	"    whose streams legitimately grew between migration completion and this call is\n" +
	"    accepted going forward (`migrate --verify` reports ok until a genuine\n" +
	"    post-rebaseline append). DRY-RUN by default (shows current -> proposed sizes +\n" +
	"    the PolicyDecision, writes nothing). --confirm performs the governed write —\n" +
	"    it REQUIRES an allowing PolicyDecision (a standing grant for the capability,\n" +
	"    external_side_effect class, scoped to the sidecar path). Without one it\n" +
	"    refuses and the sidecar is byte-identical. Refuses outright when the home's\n" +
	"    migration is not complete — there is nothing to re-baseline yet."

// runEventStoreRebaseline is the operator door onto StreamBaseline re-baselining
// (see internal/eventstore's stream-baseline.json sidecar). It mirrors the
// act_cli.go repoint shape: dry-run preview by default, --confirm mints an
// ActionIntent, evaluates policy, and refuses via act.Gate (the SAME
// by-construction refusal `compound act` uses) before ever calling the
// mutating eventstore method. It does not go through act.Execute itself
// because that requires an Adapter type declared inside package act (its
// mutating method is unexported/package-scoped by design); the sidecar's
// physical write instead lives on eventstore.Store, right next to the
// migration internals it re-baselines.
func runEventStoreRebaseline(args []string, stdout, stderr io.Writer) int {
	if hasUsageFlag(args) {
		fmt.Fprintln(stdout, eventStoreRebaselineUsage)
		return 0
	}
	homePath, err := home.Resolve(flagValue(args, "--home"))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err, "(run `compound home init`)")
		return 1
	}
	store, err := eventstore.Open(homePath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	jsonOut := hasFlag(args, "--json")
	if !hasFlag(args, "--confirm") {
		preview, err := store.PreviewStreamBaseline()
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		if !preview.HomeComplete {
			fmt.Fprintln(stderr, "event-store rebaseline: migration is not complete; nothing to re-baseline (run `compound event-store migrate --apply` first)")
			return 1
		}
		cfg, policySource, err := loadRebaselinePolicyFn(homePath)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		target := envelope.ActionTarget{Kind: "eventstore_stream_baseline", ID: "stream-baseline", Path: preview.SidecarPath}
		work := strings.TrimSpace(flagValue(args, "--work"))
		if work == "" {
			work = "work_dryrun"
		}
		now := rebaselineNowFn()
		ai := rebaselineIntent(work, target, now)
		evaluation := policy.EvaluateAuthority(ai, cfg, rebaselineAuthorityContext(target), policySource, now)
		return printRebaselinePreview(stdout, preview, evaluation, policySource, jsonOut)
	}

	work := strings.TrimSpace(flagValue(args, "--work"))
	if work == "" {
		fmt.Fprintln(stderr, "error: --work W is required for --confirm (a governed act belongs to a work)")
		return 2
	}
	if _, pendingExists, pendingErr := store.ReadRebaselinePending(); pendingErr != nil {
		fmt.Fprintln(stderr, "error: read rebaseline pending journal:", pendingErr)
		return 1
	} else if !pendingExists {
		// Literal no-write refusal: observe and evaluate before acquiring the
		// transaction flock (which itself creates .rebaseline.lock). An allowed
		// confirm still repeats this evaluation inside the lock below.
		preview, err := store.PreviewStreamBaseline()
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		if !preview.HomeComplete {
			fmt.Fprintln(stderr, "event-store rebaseline: migration is not complete; nothing to re-baseline (run `compound event-store migrate --apply` first)")
			return 1
		}
		cfg, policySource, err := loadRebaselinePolicyFn(homePath)
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		now := rebaselineNowFn()
		target := envelope.ActionTarget{Kind: "eventstore_stream_baseline", ID: "stream-baseline", Path: preview.SidecarPath}
		ai := rebaselineIntent(work, target, now)
		evaluation := policy.EvaluateAuthority(ai, cfg, rebaselineAuthorityContext(target), policySource, now)
		if !evaluation.Authority.Current || !evaluation.Authority.Allowed {
			fmt.Fprintf(stderr, "refused: %s\n", evaluation.Authority.Wording)
			return 1
		}
		if err := act.Gate(ai, evaluation.Decision); err != nil {
			fmt.Fprintf(stderr, "refused: %v\n", err)
			return 1
		}
	}
	var result rebaselineResult
	err = store.WithRebaselineTransaction(func(tx *eventstore.RebaselineTransaction) error {
		_, _, pendingExists, pendingReadErr := readRebaselinePending(tx)
		if pendingExists || pendingReadErr != nil {
			artifactStore, err := home.NewArtifactStore(homePath)
			if err != nil {
				return err
			}
			recovered, err := recoverPendingRebaseline(homePath, store, artifactStore, tx, rebaselineNowFn())
			if err != nil {
				return err
			}
			if recovered != nil {
				result = *recovered
				return nil
			}
		}

		preview, err := tx.FreshPreview()
		if err != nil {
			return err
		}
		if !preview.HomeComplete {
			return fmt.Errorf("event-store rebaseline: migration is not complete; nothing to re-baseline (run `compound event-store migrate --apply` first)")
		}
		cfg, policySource, err := loadRebaselinePolicyFn(homePath)
		if err != nil {
			return err
		}
		now := rebaselineNowFn()
		target := envelope.ActionTarget{Kind: "eventstore_stream_baseline", ID: "stream-baseline", Path: preview.SidecarPath}
		ai := rebaselineIntent(work, target, now)
		evaluation := policy.EvaluateAuthority(ai, cfg, rebaselineAuthorityContext(target), policySource, now)
		if !evaluation.Authority.Current || !evaluation.Authority.Allowed {
			return fmt.Errorf("authority refused: %s", evaluation.Authority.Wording)
		}
		if err := act.Gate(ai, evaluation.Decision); err != nil {
			return fmt.Errorf("refused: %w", err)
		}
		artifactStore, err := home.NewArtifactStore(homePath)
		if err != nil {
			return err
		}

		prior, priorExisted, err := tx.ReadSidecar()
		if err != nil {
			return err
		}
		preImageRef, err := artifactStore.Put(prior)
		if err != nil {
			return fmt.Errorf("store baseline pre-image: %w", err)
		}
		baseline, post, err := tx.Plan(now)
		if err != nil {
			return err
		}
		postImageRef, err := artifactStore.Put(post)
		if err != nil {
			return fmt.Errorf("store baseline post-image: %w", err)
		}
		beforeRef, afterRef := baselineBeforeRef(prior, priorExisted), baselineAfterRef(post)
		receipt, transition := buildRebaselineEnvelopesFn(ai, preview.Current, baseline.Streams, beforeRef, afterRef, preImageRef, postImageRef, now)
		if err := receipt.Validate(); err != nil {
			return fmt.Errorf("validate rebaseline receipt before mutation: %w", err)
		}
		if err := transition.Validate(); err != nil {
			return fmt.Errorf("validate rebaseline transition before mutation: %w", err)
		}
		pending := rebaselinePending{SchemaVersion: rebaselinePendingSchema, SidecarPath: preview.SidecarPath, PriorExisted: priorExisted, PreImageRef: preImageRef, PostImageRef: postImageRef, BeforeEvidenceRef: beforeRef, AfterEvidenceRef: afterRef, ActionIntentID: ai.ID, PolicyDecisionID: evaluation.Decision.ID, AuthorityRevision: evaluation.Authority.Revision, Receipt: receipt, Transition: transition}
		pendingRaw, err := encodeRebaselinePending(pending, preview.SidecarPath)
		if err != nil {
			return fmt.Errorf("encode rebaseline pending journal: %w", err)
		}
		if err := tx.WritePending(pendingRaw); err != nil {
			return fmt.Errorf("write rebaseline pending journal: %w", err)
		}
		if err := writeRebaselineSidecarFn(tx, prior, priorExisted, post); err != nil {
			current, currentExisted, readErr := tx.ReadSidecar()
			if readErr == nil && currentExisted == priorExisted && bytes.Equal(current, prior) {
				if clearErr := clearRebaselinePendingFn(tx); clearErr != nil {
					return fmt.Errorf("sidecar write failed before mutation (%v); journal clear: %w", err, clearErr)
				}
				return err
			}
			return recordRebaselineRecoveryFinding(homePath, artifactStore, tx, pending, pendingRaw, now, fmt.Errorf("sidecar write conflict: %w", err))
		}
		if err := tx.VerifySidecarCAS(post); err != nil {
			return recordRebaselineRecoveryFinding(homePath, artifactStore, tx, pending, pendingRaw, now, fmt.Errorf("verify post-image before receipt: %w", err))
		}
		if err := recordEventstoreEnvelopeFn(store, &receipt); err != nil {
			presence, proveErr := eventstoreProofPresence(store, &receipt)
			if proveErr != nil || presence == proofConflict {
				return recordRebaselineRecoveryFinding(homePath, artifactStore, tx, pending, pendingRaw, now, fmt.Errorf("record receipt returned %v and durable presence is unprovable: %v", err, proveErr))
			}
			if presence == proofAbsent {
				if rbErr := rollbackRebaselineFn(tx, post, prior, priorExisted); rbErr != nil {
					return recordRebaselineRecoveryFinding(homePath, artifactStore, tx, pending, pendingRaw, now, fmt.Errorf("record receipt: %w; rollback: %v", err, rbErr))
				}
				if clearErr := clearRebaselinePendingFn(tx); clearErr != nil {
					return fmt.Errorf("record receipt: %w; sidecar rolled back but pending journal clear failed: %v", err, clearErr)
				}
				return fmt.Errorf("record receipt %s: %w; stream baseline rolled back", receipt.ID, err)
			}
			// The write landed even though its caller saw an error. Keep the
			// truthful post-state and continue exact Transition finalization.
		}
		if err := tx.VerifySidecarCAS(post); err != nil {
			return recordRebaselineRecoveryFinding(homePath, artifactStore, tx, pending, pendingRaw, now, fmt.Errorf("verify post-image before transition: %w", err))
		}
		if err := recordEventstoreEnvelopeFn(store, &transition); err != nil {
			return fmt.Errorf("record transition %s: %w; rebaseline finalization pending (receipt and post-state remain truthful)", transition.ID, err)
		}
		if err := tx.VerifySidecarCAS(post); err != nil {
			return recordRebaselineRecoveryFinding(homePath, artifactStore, tx, pending, pendingRaw, now, fmt.Errorf("verify post-image before clearing journal: %w", err))
		}
		if err := clearRebaselinePendingFn(tx); err != nil {
			return fmt.Errorf("proof recorded but rebaseline finalization pending: %w", err)
		}
		result = rebaselineResult{Baseline: baseline, Receipt: receipt, Transition: transition, BeforeRef: beforeRef, AfterRef: afterRef, Evaluation: evaluation, PolicySource: policySource}
		return nil
	})
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	return printRebaselineResult(stdout, result, jsonOut)
}

// eventstore-baseline evidence-ref prefixes and the verification ref carried on every
// rebaseline receipt/transition, so the exact before/after sidecar content is reconstructable
// from proof alone (which content-address maps to which side of the mutation).
const (
	baselineBeforeRefPrefix = "eventstore-baseline-before"
	baselineAfterRefPrefix  = "eventstore-baseline-after"
	// rebaselineVerificationRef names the falsifier that proves the durable-proof-or-rollback
	// teeth (red-without → green-with). It is populated into the receipt's state_delta.verification.
	rebaselineVerificationRef = "falsifier:cmd/compound/eventstore_rebaseline_test.go#durable-proof-or-rollback"
)

// baselineBeforeRef content-addresses the prior sidecar bytes, or records explicit absence, so the
// pre-mutation trust state is reconstructable from the receipt alone.
func baselineBeforeRef(prior []byte, existed bool) string {
	if !existed {
		return baselineBeforeRefPrefix + ":absent"
	}
	return baselineBeforeRefPrefix + ":" + sha256Ref(prior)
}

// baselineAfterRef content-addresses the post-mutation sidecar bytes as they landed on disk.
func baselineAfterRef(after []byte) string {
	return baselineAfterRefPrefix + ":" + sha256Ref(after)
}

func sha256Ref(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

var (
	rebaselineNowFn            = time.Now
	loadRebaselinePolicyFn     = policy.LoadConfig
	buildRebaselineEnvelopesFn = rebaselineReceiptAndTransition
	recordEventstoreEnvelopeFn = recordEventstoreEnvelope
	clearRebaselinePendingFn   = func(tx *eventstore.RebaselineTransaction) error { return tx.ClearPending() }
	writeRebaselineSidecarFn   = func(tx *eventstore.RebaselineTransaction, prior []byte, priorExisted bool, post []byte) error {
		return tx.WriteSidecarCAS(prior, priorExisted, post)
	}
	rollbackRebaselineFn = func(tx *eventstore.RebaselineTransaction, post, prior []byte, priorExisted bool) error {
		return tx.RollbackSidecarCAS(post, prior, priorExisted)
	}
)

type rebaselineResult struct {
	Baseline     eventstore.StreamBaseline
	Receipt      envelope.Receipt
	Transition   envelope.Transition
	BeforeRef    string
	AfterRef     string
	Evaluation   policy.AuthorityEvaluation
	PolicySource string
}

func decodeRebaselinePending(raw []byte, expectedSidecar string) (rebaselinePending, error) {
	var pending rebaselinePending
	if err := json.Unmarshal(raw, &pending); err != nil {
		return rebaselinePending{}, fmt.Errorf("parse rebaseline pending: %w", err)
	}
	if err := pending.Validate(expectedSidecar); err != nil {
		return pending, err
	}
	return pending, nil
}

func encodeRebaselinePending(pending rebaselinePending, expectedSidecar string) ([]byte, error) {
	if err := pending.Validate(expectedSidecar); err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(pending, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func readRebaselinePending(tx *eventstore.RebaselineTransaction) (rebaselinePending, []byte, bool, error) {
	raw, exists, err := tx.ReadPending()
	if err != nil || !exists {
		return rebaselinePending{}, raw, exists, err
	}
	pending, err := decodeRebaselinePending(raw, tx.SidecarPath())
	return pending, raw, true, err
}

func rebaselineAuthorityContext(target envelope.ActionTarget) policy.EvaluationContext {
	return policy.EvaluationContext{Authority: &policy.AuthorityContext{
		TargetScope:               "private",
		VerifiedRecoverability:    policy.RecoverabilityVerified,
		OperatorControlContinuity: policy.OperatorControlContinuous,
		ExpectedReceipt:           "validated outward_delta Receipt",
		ExpectedProof:             []string{"artifact-backed pre-image", "artifact-backed post-image", "Receipt + Transition", "CAS rollback or recovery finding"},
		EvidenceRefs:              []string{rebaselineVerificationRef, "target:" + target.Path},
	}}
}

type proofPresence int

const (
	proofAbsent proofPresence = iota
	proofExact
	proofConflict
)

func recoverPendingRebaseline(homePath string, store *eventstore.Store, artifacts *home.ArtifactStore, tx *eventstore.RebaselineTransaction, now time.Time) (*rebaselineResult, error) {
	pending, pendingRaw, exists, err := readRebaselinePending(tx)
	if err != nil {
		return nil, recordRebaselineRecoveryFinding(homePath, artifacts, tx, pending, pendingRaw, now, err)
	}
	if !exists {
		return nil, nil
	}
	pre, err := artifacts.Get(pending.PreImageRef)
	if err != nil {
		return nil, recordRebaselineRecoveryFinding(homePath, artifacts, tx, pending, pendingRaw, now, fmt.Errorf("read pre-image: %w", err))
	}
	post, err := artifacts.Get(pending.PostImageRef)
	if err != nil {
		return nil, recordRebaselineRecoveryFinding(homePath, artifacts, tx, pending, pendingRaw, now, fmt.Errorf("read post-image: %w", err))
	}
	current, currentExists, err := tx.ReadSidecar()
	if err != nil {
		return nil, err
	}
	currentIsPre := currentExists == pending.PriorExisted && bytes.Equal(current, pre)
	currentIsPost := currentExists && bytes.Equal(current, post)
	receiptState, err := eventstoreProofPresence(store, &pending.Receipt)
	if err != nil {
		return nil, err
	}
	transitionState, err := eventstoreProofPresence(store, &pending.Transition)
	if err != nil {
		return nil, err
	}
	if receiptState == proofConflict || transitionState == proofConflict || (transitionState == proofExact && receiptState != proofExact) {
		return nil, recordRebaselineRecoveryFinding(homePath, artifacts, tx, pending, pendingRaw, now, fmt.Errorf("conflicting or causally incomplete canonical proof"))
	}
	if receiptState == proofAbsent && transitionState == proofAbsent {
		switch {
		case currentIsPre:
			if err := clearRebaselinePendingFn(tx); err != nil {
				return nil, err
			}
			return nil, nil
		case currentIsPost:
			if err := rollbackRebaselineFn(tx, post, pre, pending.PriorExisted); err != nil {
				return nil, recordRebaselineRecoveryFinding(homePath, artifacts, tx, pending, pendingRaw, now, err)
			}
			if err := clearRebaselinePendingFn(tx); err != nil {
				return nil, err
			}
			return nil, nil
		default:
			return nil, recordRebaselineRecoveryFinding(homePath, artifacts, tx, pending, pendingRaw, now, fmt.Errorf("current sidecar matches neither journaled pre-image nor post-image"))
		}
	}
	if !currentIsPost {
		return nil, recordRebaselineRecoveryFinding(homePath, artifacts, tx, pending, pendingRaw, now, fmt.Errorf("durable receipt exists but sidecar is not the journaled post-image"))
	}
	if transitionState == proofAbsent {
		if err := tx.VerifySidecarCAS(post); err != nil {
			return nil, recordRebaselineRecoveryFinding(homePath, artifacts, tx, pending, pendingRaw, now, fmt.Errorf("verify post-image before recovered transition: %w", err))
		}
		if err := recordEventstoreEnvelopeFn(store, &pending.Transition); err != nil {
			return nil, fmt.Errorf("rebaseline finalization pending: persist exact transition %s: %w", pending.Transition.ID, err)
		}
	}
	if err := tx.VerifySidecarCAS(post); err != nil {
		return nil, recordRebaselineRecoveryFinding(homePath, artifacts, tx, pending, pendingRaw, now, fmt.Errorf("verify post-image before recovered journal clear: %w", err))
	}
	if err := clearRebaselinePendingFn(tx); err != nil {
		return nil, fmt.Errorf("rebaseline proof complete but journal clear pending: %w", err)
	}
	var baseline eventstore.StreamBaseline
	if err := json.Unmarshal(post, &baseline); err != nil {
		return nil, err
	}
	return &rebaselineResult{Baseline: baseline, Receipt: pending.Receipt, Transition: pending.Transition, BeforeRef: pending.BeforeEvidenceRef, AfterRef: pending.AfterEvidenceRef}, nil
}

func eventstoreProofPresence(store *eventstore.Store, expected envelope.Envelope) (proofPresence, error) {
	id := envelopeID(expected)
	record, err := store.Read(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return proofAbsent, nil
		}
		return proofAbsent, err
	}
	switch expected.(type) {
	case *envelope.Receipt, *envelope.Transition:
	default:
		return proofConflict, fmt.Errorf("unsupported rebaseline proof %T", expected)
	}
	wantRaw, err := json.Marshal(expected)
	if err != nil {
		return proofConflict, err
	}
	wantJSON, err := canonicalRebaselineJSON(wantRaw)
	if err != nil {
		return proofConflict, err
	}
	gotJSON, err := canonicalRebaselineJSON(record.Bytes)
	if err != nil {
		return proofConflict, err
	}
	if bytes.Equal(wantJSON, gotJSON) {
		return proofExact, nil
	}
	return proofConflict, nil
}

func canonicalRebaselineJSON(data []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return json.Marshal(value)
}

func recordRebaselineRecoveryFinding(homePath string, artifacts *home.ArtifactStore, tx *eventstore.RebaselineTransaction, pending rebaselinePending, pendingRaw []byte, now time.Time, cause error) error {
	current, currentExisted, readErr := tx.ReadSidecar()
	if readErr != nil {
		return fmt.Errorf("rebaseline recovery required (%v); read actual state: %w", cause, readErr)
	}
	currentRef, artifactErr := artifacts.Put(current)
	if artifactErr != nil {
		return fmt.Errorf("rebaseline recovery required (%v); artifact actual state: %w", cause, artifactErr)
	}
	markerRef := ""
	if len(pendingRaw) > 0 {
		markerRef, artifactErr = artifacts.Put(pendingRaw)
		if artifactErr != nil {
			return fmt.Errorf("rebaseline recovery required (%v); artifact raw pending marker: %w", cause, artifactErr)
		}
	}
	body := fmt.Sprintf("event-store rebaseline manual recovery required.\nsidecar_path: %s\nprior_existed: %t\ncurrent_existed: %t\npre_image_ref: %s\nexpected_post_ref: %s\ncurrent_state_ref: %s\npending_marker_ref: %s\naction_intent_id: %s\nreceipt_id: %s\ntransition_id: %s\ncause: %v\n", tx.SidecarPath(), pending.PriorExisted, currentExisted, pending.PreImageRef, pending.PostImageRef, currentRef, markerRef, pending.ActionIntentID, pending.Receipt.ID, pending.Transition.ID, cause)
	evidenceRefs := nonEmptyRebaselineRefs(pending.PreImageRef, currentRef, markerRef)
	rec, err := finding.Record(homePath, finding.Finding{Topic: "eventstore-rebaseline-recovery/stream-baseline", Body: body, ProducedBy: "cmd/compound eventstore rebaseline", WorkID: pending.Receipt.IDs.WorkID, EvidenceRefs: evidenceRefs}, now)
	if err != nil {
		return fmt.Errorf("rebaseline recovery required (%v); recovery finding failed: %w", cause, err)
	}
	return fmt.Errorf("rebaseline recovery finding %s recorded; journal retained; MANUAL RECOVERY REQUIRED: %v", rec.ID, cause)
}

func nonEmptyRebaselineRefs(refs ...string) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if strings.TrimSpace(ref) != "" {
			out = append(out, ref)
		}
	}
	return out
}

// rebaselineIntent builds the external_side_effect / easy / local ActionIntent
// for one governed re-baseline, mirroring governedRepointIntent in act_cli.go.
func rebaselineIntent(workID string, target envelope.ActionTarget, now time.Time) envelope.ActionIntent {
	nowStr := now.UTC().Format(time.RFC3339Nano)
	return envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            actIDFor("act_", workID, eventStoreRebaselineCapability, target.Path, nowStr),
			CreatedAt:     nowStr,
			Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Actor:         "operator",
		Capability:    eventStoreRebaselineCapability,
		Action:        "rebaseline_stream_sizes",
		Target:        target,
		MutationClass: "external_side_effect",
		Reversibility: "easy",
		BlastRadius:   "local",
		Preview:       fmt.Sprintf("record current frozen-stream sizes as the new baseline at %s", target.Path),
		RollbackPlan:  fmt.Sprintf("restore the prior stream-baseline.json bytes at %s (or delete it if none existed before)", target.Path),
	}
}

// rebaselineReceiptAndTransition mirrors act.go's receiptAndTransition shape
// (an outward_delta Receipt + Transition over before/after state), built
// locally because act's builder is unexported. beforeRef/afterRef are the
// content-addressed sidecar hashes (or "absent"): they land in EvidenceRefs on
// both the receipt and the transition so the mutation is reconstructable from
// proof alone, alongside the existing human-readable size summary.
func rebaselineReceiptAndTransition(ai envelope.ActionIntent, before, after map[string]int64, beforeRef, afterRef, preImageRef, postImageRef string, now time.Time) (envelope.Receipt, envelope.Transition) {
	nowStr := now.UTC().Format(time.RFC3339Nano)
	summary := fmt.Sprintf("event-store rebaseline %s: %v -> %v", ai.Target.Path, before, after)
	baselineRefs := []string{beforeRef, afterRef}
	artifactRefs := []string{preImageRef, postImageRef}
	evidenceRefs := append(append([]string{}, baselineRefs...), artifactRefs...)
	verificationRefs := []string{rebaselineVerificationRef}

	receipt := envelope.Receipt{
		Header: envelope.Header{
			SchemaVersion: envelope.SVReceipt,
			ID:            actIDFor("rec_", ai.ID, preImageRef, postImageRef),
			CreatedAt:     nowStr,
			Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
			IDs:           envelope.IDs{WorkID: ai.IDs.WorkID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  append([]string{}, evidenceRefs...),
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Outcome:  "completed",
		MoveType: "outward_delta",
		StateDelta: envelope.StateDelta{
			Changed:      true,
			Summary:      summary,
			FilesChanged: []string{ai.Target.Path},
			Artifacts:    append([]string{}, artifactRefs...),
			Verification: verificationRefs,
		},
		Cost:      envelope.ReceiptCost{ModelCalls: []string{}},
		Skills:    envelope.ReceiptSkills{Used: []string{ai.Capability}, Missed: []string{}, Candidate: []string{}, UsedDeclared: true},
		Memory:    envelope.ReceiptMemory{Candidates: []string{}, Promoted: []string{}, Parked: []string{}},
		Next:      envelope.ReceiptNext{SuggestedWork: []string{}},
		ProofRefs: append([]string{}, artifactRefs...),
	}

	transition := envelope.Transition{
		Header: envelope.Header{
			SchemaVersion: envelope.SVTransition,
			ID:            actIDFor("tr_", ai.ID, preImageRef, postImageRef),
			CreatedAt:     nowStr,
			Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
			IDs:           envelope.IDs{WorkID: ai.IDs.WorkID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  append([]string{}, evidenceRefs...),
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		BeforeRefs: []string{preImageRef},
		ActionRefs: []string{ai.ID},
		AfterRefs:  []string{postImageRef},
		StateDelta: summary,
		Projections: envelope.Projections{
			Memory: []string{}, Skill: []string{ai.Capability}, Product: []string{}, Cost: []string{},
			Risk: []string{}, Taste: []string{}, Routing: []string{}, HumanAttention: []string{},
		},
		SecondOrder: envelope.SecondOrder{FutureBehaviorChanged: true},
	}
	return receipt, transition
}

// recordEventstoreEnvelope persists a receipt/transition through the canonical
// event store and proves it resolvable via a post-write Read, mirroring act's
// own recordEnvelopeWithStore (unexported there, so duplicated here rather
// than reaching into package act for it).
func recordEventstoreEnvelope(store *eventstore.Store, value envelope.Envelope) error {
	if err := value.Validate(); err != nil {
		return err
	}
	id := envelopeID(value)
	if id == "" {
		return fmt.Errorf("eventstore envelope id required for %T", value)
	}
	if _, err := store.PutJSON(id, value, " "); err != nil {
		return err
	}
	record, err := store.Read(id)
	if err != nil {
		return fmt.Errorf("prove event %s persistence through Store.Read: %w", id, err)
	}
	if record.ID != id {
		return fmt.Errorf("prove event %s persistence through Store.Read: resolved id %q", id, record.ID)
	}
	return nil
}

func envelopeID(value envelope.Envelope) string {
	switch typed := value.(type) {
	case *envelope.Receipt:
		return typed.ID
	case *envelope.Transition:
		return typed.ID
	default:
		return ""
	}
}

func printRebaselinePreview(stdout io.Writer, preview eventstore.StreamBaselinePreview, evaluation policy.AuthorityEvaluation, policySource string, asJSON bool) int {
	pd := evaluation.Decision
	allowed := evaluation.Authority.Current && evaluation.Authority.Allowed
	if asJSON {
		out := map[string]any{
			"mode": "dry-run", "sidecar_path": preview.SidecarPath, "has_sidecar": preview.HasSidecar,
			"current": preview.Current, "proposed": preview.Proposed,
			"policy_outcome": pd.Outcome, "allowed": allowed, "policy_source": policySource,
			"policy_authority": evaluation.Authority,
		}
		if err := encodeJSON(stdout, out); err != nil {
			fmt.Fprintln(stdout, "error:", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "- authority: %s (current=%t allowed=%t)\n", evaluation.Authority.Wording, evaluation.Authority.Current, evaluation.Authority.Allowed)
	fmt.Fprintf(stdout, "- policy_source: %s\n", policySource)
	fmt.Fprintln(stdout, "# event-store rebaseline (dry-run — nothing written)")
	fmt.Fprintf(stdout, "- sidecar: %s\n", preview.SidecarPath)
	for _, name := range sortedStreamNames(preview.Proposed) {
		fmt.Fprintf(stdout, "- %s: current=%s -> proposed=%d\n", name, streamSizeString(preview.Current, name), preview.Proposed[name])
	}
	if allowed {
		fmt.Fprintf(stdout, "- policy: %s (ALLOWED) — run again with --confirm --work W to perform the governed re-baseline\n", pd.Outcome)
	} else {
		fmt.Fprintf(stdout, "- policy: %s (BLOCKED) — no allowing PolicyDecision; add a standing grant for this capability/target first\n", pd.Outcome)
	}
	return 0
}

func printRebaselineResult(stdout io.Writer, result rebaselineResult, asJSON bool) int {
	baseline, receipt, transition := result.Baseline, result.Receipt, result.Transition
	if asJSON {
		out := map[string]any{
			"mode": "confirmed", "recorded_at": baseline.RecordedAt, "reason": baseline.Reason,
			"streams": baseline.Streams, "receipt_id": receipt.ID, "transition_id": transition.ID,
			"move_type": receipt.MoveType, "before_ref": result.BeforeRef, "after_ref": result.AfterRef,
		}
		if result.Evaluation.Authority.SchemaVersion != "" {
			out["policy_authority"] = result.Evaluation.Authority
			out["policy_source"] = result.PolicySource
		}
		if err := encodeJSON(stdout, out); err != nil {
			fmt.Fprintln(stdout, "error:", err)
			return 1
		}
		return 0
	}
	fmt.Fprintln(stdout, "# event-store rebaseline (confirmed — governed write applied)")
	for _, name := range sortedStreamNames(baseline.Streams) {
		fmt.Fprintf(stdout, "- %s: %d\n", name, baseline.Streams[name])
	}
	fmt.Fprintf(stdout, "- before_ref: %s\n- after_ref: %s\n", result.BeforeRef, result.AfterRef)
	fmt.Fprintf(stdout, "- receipt=%s transition=%s move=%s\n", receipt.ID, transition.ID, receipt.MoveType)
	return 0
}

func sortedStreamNames(sizes map[string]int64) []string {
	names := make([]string, 0, len(sizes))
	for name := range sizes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func streamSizeString(sizes map[string]int64, name string) string {
	if sizes == nil {
		return "<no sidecar>"
	}
	if v, ok := sizes[name]; ok {
		return fmt.Sprintf("%d", v)
	}
	return "<unrecorded>"
}

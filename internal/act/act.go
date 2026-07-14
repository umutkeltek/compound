package act

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/policy"
)

// ArtifactStore is the content-addressed blob store the outbound wrapper writes
// pre/post images to, under COMPOUND_HOME/artifacts/sha256 (the home's declared
// content-addressed dir — never the repo). Same bytes -> same ref, so capture is
// idempotent and a revert can restore the exact pre-image bytes by ref. It also carries
// the home root so Execute can RECORD each governed write's receipt/transition to the
// canonical event store (home/events), keeping the returned ids resolvable.
type ArtifactStore = home.ArtifactStore

// NewArtifactStore roots the store at COMPOUND_HOME/artifacts/sha256, creating it.
func NewArtifactStore(homePath string) (*ArtifactStore, error) {
	return home.NewArtifactStore(homePath)
}

// recordEnvelope persists a validated receipt/transition to the home's canonical event store at
// home/events/<id>.json — the same store product.RecordTransition and the executor/event readers
// use. Same id -> same file (idempotent). This is what makes a returned receipt_id/transition_id
// resolve to a real envelope, closing the "governed action invisible to the event layer" gap.
func recordEnvelope(home, id string, v any) error {
	if strings.TrimSpace(home) == "" {
		return fmt.Errorf("cannot record %q without a home", id)
	}
	dir := filepath.Join(home, "events")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, id+".json"), append(b, '\n'), 0o644)
}

// Result is the receipted outcome of one governed outbound write.
type Result struct {
	Receipt      envelope.Receipt
	Transition   envelope.Transition
	PreImageRef  string
	PostImageRef string
	PreImage     []byte
	PostImage    []byte
}

// Execute performs ONE governed outbound write and returns its receipt + transition.
//
// The gate is BY CONSTRUCTION: Execute refuses to call adapter.write unless pd is a
// valid PolicyDecision that authorizes THIS exact intent (pd.action_intent_id ==
// ai.ID) and allows it. A missing, mismatched, or non-allowing decision returns an
// error before the adapter is ever touched, so the target stays byte-identical — a
// mutation without a matching allowing decision cannot happen. Because write is
// unexported (package act only), this gate is not merely the intended path but the
// ONLY reachable one: no external package can call an adapter's write to bypass it.
//
// On allow it (iii) captures the pre-image via ReadCurrent into a content-addressed
// artifact, (iv) writes, (v) reads the post-image back into an artifact, (vi) emits
// the outward_delta Receipt (proof_refs=[pre,post], state_delta.changed=true), and
// (vii) records the Transition (before_refs=[pre], after_refs=[post]). The policy's
// obligations for the granted path — emit a Receipt, record a rollback_plan, capture
// the pre-image — are therefore satisfied by construction, not by prompt obedience.
func Execute(store *ArtifactStore, adapter Adapter, ai envelope.ActionIntent, pd envelope.PolicyDecision, newValue []byte, now time.Time) (Result, error) {
	if store == nil {
		return Result{}, fmt.Errorf("act: artifact store required")
	}
	if adapter == nil {
		return Result{}, fmt.Errorf("act: adapter required")
	}
	if err := gate(ai, pd); err != nil {
		return Result{}, err
	}

	pre, err := adapter.ReadCurrent(ai.Target)
	if err != nil {
		return Result{}, fmt.Errorf("act: capture pre-image: %w", err)
	}
	preRef, err := store.Put(pre)
	if err != nil {
		return Result{}, fmt.Errorf("act: store pre-image: %w", err)
	}

	if err := adapter.write(ai.Target, newValue); err != nil {
		return Result{}, fmt.Errorf("act: write: %w", err)
	}

	post, err := adapter.ReadCurrent(ai.Target)
	if err != nil {
		return Result{}, fmt.Errorf("act: read-back post-image: %w", err)
	}
	postRef, err := store.Put(post)
	if err != nil {
		return Result{}, fmt.Errorf("act: store post-image: %w", err)
	}

	receipt, transition := receiptAndTransition(ai, preRef, postRef, pre, post, now)
	if err := receipt.Validate(); err != nil {
		return Result{}, fmt.Errorf("act: emitted receipt invalid: %w", err)
	}
	if err := transition.Validate(); err != nil {
		return Result{}, fmt.Errorf("act: emitted transition invalid: %w", err)
	}
	// RECORD the receipt and transition to the home's canonical event store, so the returned
	// ids are resolvable envelope files the event readers (executorlens, eventsource, capgraph,
	// distiller) can see — not a dangling in-memory reference. A governed write that cannot be
	// recorded is a failed governed write, so a persistence error surfaces here.
	if err := recordEnvelope(store.Home(), receipt.ID, &receipt); err != nil {
		return Result{}, fmt.Errorf("act: record receipt %s: %w", receipt.ID, err)
	}
	if err := recordEnvelope(store.Home(), transition.ID, &transition); err != nil {
		return Result{}, fmt.Errorf("act: record transition %s: %w", transition.ID, err)
	}
	return Result{
		Receipt:      receipt,
		Transition:   transition,
		PreImageRef:  preRef,
		PostImageRef: postRef,
		PreImage:     pre,
		PostImage:    post,
	}, nil
}

// Gate reports whether Execute WOULD admit this (ai, pd) pair — the same
// by-construction refusal Execute applies — WITHOUT performing any write or touching
// the adapter. A caller that must materialize setup state (e.g. create the target key a
// repoint-only adapter needs) uses this to confirm the write is admissible BEFORE any
// mutation, so a refused decision leaves the target byte-identical. It returns nil iff
// Execute(store, adapter, ai, pd, ...) would proceed to the write. Execute re-checks the
// same gate internally, so this is an advisory pre-flight, never a substitute for it.
func Gate(ai envelope.ActionIntent, pd envelope.PolicyDecision) error { return gate(ai, pd) }

// gate is the by-construction refusal: an outbound write proceeds only if pd is a
// valid PolicyDecision that authorizes THIS exact intent and allows it. Anything
// else errors before the adapter is called.
func gate(ai envelope.ActionIntent, pd envelope.PolicyDecision) error {
	if err := ai.Validate(); err != nil {
		return fmt.Errorf("act: action intent invalid: %w", err)
	}
	if ai.MutationClass == "read" {
		return fmt.Errorf("act: %q is a read, not an outbound write", ai.ID)
	}
	if strings.TrimSpace(ai.RollbackPlan) == "" {
		return fmt.Errorf("act: action %q requires a rollback_plan (outbound writes must be revertible)", ai.ID)
	}
	if err := pd.Validate(); err != nil {
		return fmt.Errorf("act: no valid PolicyDecision for %q: %w", ai.ID, err)
	}
	if pd.ActionIntentID != ai.ID {
		return fmt.Errorf("act: PolicyDecision authorizes %q, not this intent %q — refusing to write", pd.ActionIntentID, ai.ID)
	}
	if err := policy.Gate(pd); err != nil {
		return fmt.Errorf("act: %w — refusing to write %q", err, ai.ID)
	}
	return nil
}

func receiptAndTransition(ai envelope.ActionIntent, preRef, postRef string, pre, post []byte, now time.Time) (envelope.Receipt, envelope.Transition) {
	proofRefs := []string{preRef, postRef}
	summary := fmt.Sprintf("outbound write %s %s: %q -> %q", ai.Target.Kind, ai.Target.ID, string(pre), string(post))

	receipt := envelope.Receipt{
		Header:   header(envelope.SVReceipt, idFor("rec_", ai.ID, preRef, postRef, ts(now)), ai, now),
		Outcome:  "completed",
		MoveType: "outward_delta",
		StateDelta: envelope.StateDelta{
			Changed:      true,
			Summary:      summary,
			FilesChanged: filesChanged(ai.Target.Path),
			Artifacts:    append([]string{}, proofRefs...),
			Verification: []string{},
		},
		Cost:      envelope.ReceiptCost{ModelCalls: []string{}},
		Skills:    envelope.ReceiptSkills{Used: []string{ai.Capability}, Missed: []string{}, Candidate: []string{}, UsedDeclared: true},
		Memory:    envelope.ReceiptMemory{Candidates: []string{}, Promoted: []string{}, Parked: []string{}},
		Next:      envelope.ReceiptNext{SuggestedWork: []string{}},
		ProofRefs: append([]string{}, proofRefs...),
	}
	receipt.EvidenceRefs = append([]string{}, proofRefs...)

	transition := envelope.Transition{
		Header:     header(envelope.SVTransition, idFor("tr_", ai.ID, preRef, postRef, ts(now)), ai, now),
		BeforeRefs: []string{preRef},
		ActionRefs: []string{ai.ID},
		AfterRefs:  []string{postRef},
		StateDelta: summary,
		Projections: envelope.Projections{
			Memory: []string{}, Skill: []string{ai.Capability}, Product: []string{}, Cost: []string{},
			Risk: []string{}, Taste: []string{}, Routing: []string{}, HumanAttention: []string{},
		},
		SecondOrder: envelope.SecondOrder{FutureBehaviorChanged: true},
	}
	transition.EvidenceRefs = append([]string{}, proofRefs...)

	if lane := strings.TrimSpace(ai.Producer.LaneID); lane != "" {
		receipt.Producer.LaneID = lane
		transition.Producer.LaneID = lane
	}
	return receipt, transition
}

// RevertIntent builds the governed revert of a completed outbound write: the same
// target, re-applying the captured pre-image. The caller writes it with the
// pre-image bytes (store.Get(res.PreImageRef)) as the new value; policy re-gates it
// exactly like the forward write, so a rollback is itself a governed outbound write,
// never a privileged bypass.
func RevertIntent(orig envelope.ActionIntent, res Result, now time.Time) envelope.ActionIntent {
	ai := orig
	ai.ID = idFor("act_", "revert", orig.ID, res.PreImageRef, res.PostImageRef, ts(now))
	ai.CreatedAt = now.UTC().Format(time.RFC3339)
	ai.Action = "revert:" + strings.TrimSpace(orig.Action)
	ai.Preview = "restore pre-image " + res.PreImageRef + " over " + res.PostImageRef
	ai.RollbackPlan = "restore post-image artifact " + res.PostImageRef
	return ai
}

func header(sv, id string, ai envelope.ActionIntent, now time.Time) envelope.Header {
	return envelope.Header{
		SchemaVersion: sv,
		ID:            id,
		CreatedAt:     now.UTC().Format(time.RFC3339),
		Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
		IDs:           envelope.IDs{WorkID: ai.IDs.WorkID, ContextPackID: ai.IDs.ContextPackID},
		Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
		Privacy:       orDefault(ai.Privacy, "private"),
		Risk:          orDefault(ai.Risk, "tier1"),
		EvidenceRefs:  []string{},
		Uncertainty:   envelope.Uncertainty{Level: "known"},
	}
}

func filesChanged(path string) []string {
	if strings.TrimSpace(path) == "" {
		return []string{}
	}
	return []string{path}
}

func idFor(prefix string, parts ...string) string {
	h := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return prefix + hex.EncodeToString(h[:])[:12]
}

func ts(now time.Time) string { return now.UTC().Format(time.RFC3339Nano) }

func orDefault(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

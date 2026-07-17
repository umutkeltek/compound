package main

import (
	"crypto/sha256"
	"encoding/hex"
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
	now := time.Now()

	preview, err := store.PreviewStreamBaseline()
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if !preview.HomeComplete {
		fmt.Fprintln(stderr, "event-store rebaseline: migration is not complete; nothing to re-baseline (run `compound event-store migrate --apply` first)")
		return 1
	}

	cfg, _, err := policy.LoadConfig(homePath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	target := envelope.ActionTarget{Kind: "eventstore_stream_baseline", ID: "stream-baseline", Path: preview.SidecarPath}

	if !hasFlag(args, "--confirm") {
		work := strings.TrimSpace(flagValue(args, "--work"))
		if work == "" {
			work = "work_dryrun"
		}
		ai := rebaselineIntent(work, target, now)
		pd := policy.Evaluate(ai, cfg, now)
		return printRebaselinePreview(stdout, preview, pd, jsonOut)
	}

	work := strings.TrimSpace(flagValue(args, "--work"))
	if work == "" {
		fmt.Fprintln(stderr, "error: --work W is required for --confirm (a governed act belongs to a work)")
		return 2
	}
	ai := rebaselineIntent(work, target, now)
	pd := policy.Evaluate(ai, cfg, now)
	if gateErr := act.Gate(ai, pd); gateErr != nil {
		fmt.Fprintf(stderr, "refused: %v\n", gateErr)
		return 1
	}

	before := preview.Current

	// Capture the sidecar's EXACT prior bytes (or explicit absence) BEFORE the mutation, so a
	// post-mutation record failure can restore it byte-identical. Read-only capture — never a write.
	priorBytes, hadSidecar, err := readSidecarRaw(preview.SidecarPath)
	if err != nil {
		fmt.Fprintln(stderr, "error: capture prior sidecar bytes:", err)
		return 1
	}
	beforeRef := baselineBeforeRef(priorBytes, hadSidecar)

	baseline, err := store.RebaselineStreams(now)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	// From here the sidecar is MUTATED. Every failure below is routed through rebaselineRollback,
	// which restores the prior bytes (or removes the sidecar if it was absent) and, only if that
	// restore ALSO fails, lands a durable recovery finding — so no path returns an error with the
	// trust sidecar left mutated but without durable proof.
	afterBytes, err := os.ReadFile(preview.SidecarPath)
	if err != nil {
		return rebaselineRollback(homePath, stderr, preview.SidecarPath, priorBytes, hadSidecar,
			ai, "", beforeRef, now, fmt.Errorf("read back new sidecar bytes: %w", err))
	}
	afterRef := baselineAfterRef(afterBytes)

	receipt, transition := rebaselineReceiptAndTransition(ai, before, baseline.Streams, beforeRef, afterRef, now)
	if err := recordEventstoreEnvelope(store, receipt.ID, &receipt); err != nil {
		return rebaselineRollback(homePath, stderr, preview.SidecarPath, priorBytes, hadSidecar,
			ai, receipt.ID, beforeRef, now, fmt.Errorf("record receipt %s: %w", receipt.ID, err))
	}
	if err := recordEventstoreEnvelope(store, transition.ID, &transition); err != nil {
		return rebaselineRollback(homePath, stderr, preview.SidecarPath, priorBytes, hadSidecar,
			ai, transition.ID, beforeRef, now, fmt.Errorf("record transition %s: %w", transition.ID, err))
	}
	return printRebaselineResult(stdout, baseline, receipt, transition, jsonOut)
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

// readSidecarRaw returns the sidecar's exact bytes and whether it existed. Absence is not an
// error: a home that has never been re-baselined after migration may legitimately have no
// operator-written sidecar yet. Read-only by construction.
func readSidecarRaw(path string) (data []byte, existed bool, err error) {
	data, err = os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return data, true, nil
}

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

// restoreSidecarFn indirects restoreSidecar so a test can force the restore failure that wedges the
// rollback (the recovery-finding path). Swapped ONLY in tests; the production path always calls the
// real restoreSidecar.
var restoreSidecarFn = restoreSidecar

// restoreSidecar returns the sidecar to its captured prior state: the exact prior bytes written back,
// or the file removed if there was no sidecar before. This is the obligation-bound cleanup of the
// mutation we just performed under the already-granted PolicyDecision — the RollbackPlan the
// ActionIntent staked, now actually executed — not a new governed act.
func restoreSidecar(path string, prior []byte, existed bool) error {
	if !existed {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return os.WriteFile(path, prior, 0o644)
}

// rebaselineRollback restores the trust sidecar's prior bytes after a post-mutation failure, so
// `event-store rebaseline` can never leave a mutated stream-baseline.json without durable proof. If
// the restore itself fails it lands a durable recovery finding (the SAME finding home
// act.afterWriteFailure uses) naming the sidecar path, the prior state, the receipt/transition id
// attempted, and the manual recovery required — and names that finding in the returned error. It
// always returns exit code 1 with a message that says the baseline was rolled back (or that manual
// recovery is required). It never re-gates: rollback is covered by the decision that admitted the
// forward write.
func rebaselineRollback(homePath string, stderr io.Writer, sidecarPath string, prior []byte, existed bool, ai envelope.ActionIntent, attemptedID, beforeRef string, now time.Time, cause error) int {
	priorState := "absent"
	if existed {
		priorState = beforeRef
	}
	if rbErr := restoreSidecarFn(sidecarPath, prior, existed); rbErr != nil {
		body := fmt.Sprintf(
			"event-store rebaseline mutated the trust sidecar but the receipt/transition could not be recorded AND the sidecar could not be rolled back — manual recovery required.\n"+
				"action_intent: %s\nsidecar_path: %s\nprior_state: %s\nattempted_event_id: %s\npersistence_error: %v\nrollback_error: %v\n",
			ai.ID, sidecarPath, priorState, attemptedID, cause, rbErr)
		rec, ferr := finding.Record(homePath, finding.Finding{
			Topic:        "eventstore-rebaseline-recovery/" + ai.Target.ID,
			Body:         body,
			ProducedBy:   "cmd/compound eventstore rebaseline",
			WorkID:       ai.IDs.WorkID,
			EvidenceRefs: []string{beforeRef},
		}, now)
		if ferr != nil {
			fmt.Fprintf(stderr, "error: rebaseline %v; rollback FAILED (%v); recovery finding could not be recorded (%v) — sidecar %s left MUTATED (prior state %s); MANUAL RECOVERY REQUIRED\n", cause, rbErr, ferr, sidecarPath, priorState)
			return 1
		}
		fmt.Fprintf(stderr, "error: rebaseline %v; rollback FAILED (%v) — recorded recovery finding %s; sidecar %s left MUTATED (prior state %s); MANUAL RECOVERY REQUIRED\n", cause, rbErr, rec.ID, sidecarPath, priorState)
		return 1
	}
	fmt.Fprintf(stderr, "error: rebaseline %v — stream-baseline.json rolled back to prior state (%s) at %s\n", cause, priorState, sidecarPath)
	return 1
}

// rebaselineIntent builds the external_side_effect / easy / local ActionIntent
// for one governed re-baseline, mirroring governedRepointIntent in act_cli.go.
func rebaselineIntent(workID string, target envelope.ActionTarget, now time.Time) envelope.ActionIntent {
	nowStr := now.UTC().Format(time.RFC3339)
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
func rebaselineReceiptAndTransition(ai envelope.ActionIntent, before, after map[string]int64, beforeRef, afterRef string, now time.Time) (envelope.Receipt, envelope.Transition) {
	nowStr := now.UTC().Format(time.RFC3339)
	summary := fmt.Sprintf("event-store rebaseline %s: %v -> %v", ai.Target.Path, before, after)
	baselineRefs := []string{beforeRef, afterRef}
	verificationRefs := []string{rebaselineVerificationRef}

	receipt := envelope.Receipt{
		Header: envelope.Header{
			SchemaVersion: envelope.SVReceipt,
			ID:            actIDFor("rec_", ai.ID, nowStr),
			CreatedAt:     nowStr,
			Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
			IDs:           envelope.IDs{WorkID: ai.IDs.WorkID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  append([]string{}, baselineRefs...),
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Outcome:  "completed",
		MoveType: "outward_delta",
		StateDelta: envelope.StateDelta{
			Changed:      true,
			Summary:      summary,
			FilesChanged: []string{ai.Target.Path},
			Artifacts:    []string{},
			Verification: verificationRefs,
		},
		Cost:   envelope.ReceiptCost{ModelCalls: []string{}},
		Skills: envelope.ReceiptSkills{Used: []string{ai.Capability}, Missed: []string{}, Candidate: []string{}, UsedDeclared: true},
		Memory: envelope.ReceiptMemory{Candidates: []string{}, Promoted: []string{}, Parked: []string{}},
		Next:   envelope.ReceiptNext{SuggestedWork: []string{}},
	}

	transition := envelope.Transition{
		Header: envelope.Header{
			SchemaVersion: envelope.SVTransition,
			ID:            actIDFor("tr_", ai.ID, nowStr),
			CreatedAt:     nowStr,
			Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
			IDs:           envelope.IDs{WorkID: ai.IDs.WorkID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  append([]string{}, baselineRefs...),
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		ActionRefs: []string{ai.ID},
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
func recordEventstoreEnvelope(store *eventstore.Store, id string, value any) error {
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

func printRebaselinePreview(stdout io.Writer, preview eventstore.StreamBaselinePreview, pd envelope.PolicyDecision, asJSON bool) int {
	allowed := policy.Allowed(pd)
	if asJSON {
		out := map[string]any{
			"mode": "dry-run", "sidecar_path": preview.SidecarPath, "has_sidecar": preview.HasSidecar,
			"current": preview.Current, "proposed": preview.Proposed,
			"policy_outcome": pd.Outcome, "allowed": allowed,
		}
		if err := encodeJSON(stdout, out); err != nil {
			fmt.Fprintln(stdout, "error:", err)
			return 1
		}
		return 0
	}
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

func printRebaselineResult(stdout io.Writer, baseline eventstore.StreamBaseline, receipt envelope.Receipt, transition envelope.Transition, asJSON bool) int {
	if asJSON {
		out := map[string]any{
			"mode": "confirmed", "recorded_at": baseline.RecordedAt, "reason": baseline.Reason,
			"streams": baseline.Streams, "receipt_id": receipt.ID, "transition_id": transition.ID,
			"move_type": receipt.MoveType,
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

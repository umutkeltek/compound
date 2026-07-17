package main

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"compound/internal/act"
	"compound/internal/envelope"
	"compound/internal/eventstore"
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
	baseline, err := store.RebaselineStreams(now)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	receipt, transition := rebaselineReceiptAndTransition(ai, before, baseline.Streams, now)
	if err := recordEventstoreEnvelope(store, receipt.ID, &receipt); err != nil {
		fmt.Fprintln(stderr, "error: rebaseline written but receipt could not be recorded:", err)
		return 1
	}
	if err := recordEventstoreEnvelope(store, transition.ID, &transition); err != nil {
		fmt.Fprintln(stderr, "error: rebaseline written but transition could not be recorded:", err)
		return 1
	}
	return printRebaselineResult(stdout, baseline, receipt, transition, jsonOut)
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
// locally because act's builder is unexported.
func rebaselineReceiptAndTransition(ai envelope.ActionIntent, before, after map[string]int64, now time.Time) (envelope.Receipt, envelope.Transition) {
	nowStr := now.UTC().Format(time.RFC3339)
	summary := fmt.Sprintf("event-store rebaseline %s: %v -> %v", ai.Target.Path, before, after)

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
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Outcome:  "completed",
		MoveType: "outward_delta",
		StateDelta: envelope.StateDelta{
			Changed:      true,
			Summary:      summary,
			FilesChanged: []string{ai.Target.Path},
			Artifacts:    []string{},
			Verification: []string{},
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
			EvidenceRefs:  []string{},
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

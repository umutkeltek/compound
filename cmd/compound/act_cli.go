package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"compound/internal/act"
	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/policy"
)

// cmdAct is the operator door onto compound's ONE generic governed outbound-write seam (internal/act).
// It performs a single PolicyDecision-gated config-key repoint: read the current value (pre-image),
// write the new value, capture a post-image, and record an outward_delta Receipt + Transition to the
// canonical event store. This is the operator-reachable outbound plane — the SAME seam lens-modulate
// drives internally, now exposed as a first-class verb so "outbound governed writes" is a real
// capability, not a library-only special case.
//
// DRY-RUN by default (shows current -> proposed and the PolicyDecision, writes nothing). --confirm
// performs the governed write, which REQUIRES an allowing PolicyDecision (a standing grant for the
// capability/class/target). Without one the act refuses and the target stays byte-identical. It makes
// NO provider/network call; it mutates only the named config file, only under an allowing decision.
func cmdAct(args []string) {
	if code := runAct(args, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

// actDefaultCapability is the capability a repoint declares unless --capability overrides it; an
// operator's standing grant scopes to exactly this capability + external_side_effect class + target.
const actDefaultCapability = "outbound.config_key.write"

func actUsage() string {
	return "usage:\n" +
		"  compound act repoint --target-path P --target-id KEY --value V --work W [--confirm] [--capability CAP] [--home P] [--json]\n" +
		"  compound act post (--from-work W | --content -) --work W [--transport NAME] [--confirm] [--capability CAP] [--home P] [--json]\n" +
		"    Governed outbound write: repoint the dotted KEY inside the YAML/JSON config at P to V.\n" +
		"    DRY-RUN by default (shows current -> proposed + the PolicyDecision, writes nothing).\n" +
		"    --confirm performs the write THROUGH the act seam — it REQUIRES an allowing PolicyDecision\n" +
		"    (a standing grant for --capability, external_side_effect class, easy reversibility, local\n" +
		"    blast, scoped to --target-path). Without one the act refuses and the target is byte-identical.\n" +
		"    The adapter REPOINTS an existing key; it never creates one. Pre/post images are captured as\n" +
		"    content-addressed artifacts and the receipt/transition land in $COMPOUND_HOME/events."
}

func runAct(args []string, stdout, stderr io.Writer) int {
	return runActWithInput(args, os.Stdin, stdout, stderr)
}

func runActWithInput(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || hasUsageFlag(args) {
		fmt.Fprintln(stderr, actUsage())
		return 2
	}
	switch args[0] {
	case "repoint":
		return runActRepoint(args[1:], stdout, stderr)
	case "post":
		return runActPost(args[1:], stdin, stdout, stderr)
	default:
		fmt.Fprintln(stderr, actUsage())
		return 2
	}
}

func runActRepoint(args []string, stdout, stderr io.Writer) int {
	targetPath := strings.TrimSpace(flagValue(args, "--target-path"))
	targetID := strings.TrimSpace(flagValue(args, "--target-id"))
	if targetPath == "" || targetID == "" || !hasFlag(args, "--value") {
		fmt.Fprintln(stderr, "usage: compound act repoint --target-path P --target-id KEY --value V --work W [--confirm] [--capability CAP]")
		return 2
	}
	value := flagValue(args, "--value")
	capability := strings.TrimSpace(flagValue(args, "--capability"))
	if capability == "" {
		capability = actDefaultCapability
	}
	homePath, err := home.Resolve(flagValue(args, "--home"))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	adapter := act.NewConfigKeyAdapter()
	target := envelope.ActionTarget{Kind: "config_key", ID: targetID, Path: targetPath}
	now := time.Now()

	// Read the current value (pre-image) for the preview — an absent key/file is reported, never invented.
	cur, readErr := adapter.ReadCurrent(target)

	cfg, _, err := policy.LoadConfig(homePath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	if !hasFlag(args, "--confirm") {
		// DRY-RUN default: show current -> proposed and the PolicyDecision; write nothing.
		work := strings.TrimSpace(flagValue(args, "--work"))
		if work == "" {
			work = "work_dryrun"
		}
		ai := governedRepointIntent(work, capability, target, value, now)
		pd := policy.Evaluate(ai, cfg, now)
		return printActPreview(stdout, target, string(cur), readErr, value, pd, hasFlag(args, "--json"))
	}

	work := strings.TrimSpace(flagValue(args, "--work"))
	if work == "" {
		fmt.Fprintln(stderr, "error: --work W is required for --confirm (a governed act belongs to a work)")
		return 2
	}
	if readErr != nil {
		fmt.Fprintf(stderr, "error: cannot repoint %q: %v (the adapter repoints an existing key, it does not create one)\n", targetID, readErr)
		return 1
	}
	store, err := act.NewArtifactStore(homePath)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	ai := governedRepointIntent(work, capability, target, value, now)
	pd := policy.Evaluate(ai, cfg, now)
	res, execErr := act.Execute(store, adapter, ai, pd, []byte(value), now)
	if execErr != nil {
		// No allowing PolicyDecision (hold/deny) => act refused, target byte-identical.
		fmt.Fprintf(stderr, "refused: %v\n", execErr)
		return 1
	}
	return printActResult(stdout, target, res, hasFlag(args, "--json"))
}

// governedRepointIntent builds the external_side_effect / easy / local ActionIntent for one config-key
// repoint, carrying the rollback_plan act's gate requires. It mirrors the intent shape internal/act's
// own tests pin, so the CLI writes through exactly the governed path — no bespoke governance.
func governedRepointIntent(workID, capability string, target envelope.ActionTarget, value string, now time.Time) envelope.ActionIntent {
	return envelope.ActionIntent{
		Header: envelope.Header{
			SchemaVersion: envelope.SVActionIntent,
			ID:            actIDFor("act_", workID, capability, target.Path, target.ID, value),
			CreatedAt:     now.UTC().Format(time.RFC3339),
			Producer:      envelope.Producer{Kind: "cli", Name: "compound"},
			IDs:           envelope.IDs{WorkID: workID},
			Authority:     envelope.Authority{SourceClass: "runtime", Status: "current"},
			Privacy:       "private",
			Risk:          "tier1",
			EvidenceRefs:  []string{},
			Uncertainty:   envelope.Uncertainty{Level: "known"},
		},
		Actor:         "operator",
		Capability:    capability,
		Action:        "repoint_config_key",
		Target:        target,
		MutationClass: "external_side_effect",
		Reversibility: "easy",
		BlastRadius:   "local",
		Preview:       fmt.Sprintf("repoint %s in %s to %q", target.ID, target.Path, value),
		RollbackPlan:  fmt.Sprintf("restore the captured pre-image of %s in %s", target.ID, target.Path),
	}
}

func printActPreview(stdout io.Writer, target envelope.ActionTarget, cur string, readErr error, proposed string, pd envelope.PolicyDecision, asJSON bool) int {
	allowed := policy.Allowed(pd)
	if asJSON {
		out := map[string]any{
			"mode": "dry-run", "target_id": target.ID, "target_path": target.Path,
			"current": cur, "proposed": proposed, "key_present": readErr == nil,
			"policy_outcome": pd.Outcome, "allowed": allowed,
		}
		if readErr != nil {
			out["read_error"] = readErr.Error()
		}
		if err := encodeJSON(stdout, out); err != nil {
			fmt.Fprintln(stdout, "error:", err)
			return 1
		}
		return 0
	}
	fmt.Fprintln(stdout, "# act repoint (dry-run — nothing written)")
	fmt.Fprintf(stdout, "- target: %s in %s\n", target.ID, target.Path)
	if readErr != nil {
		fmt.Fprintf(stdout, "- current: <absent> (%v)\n", readErr)
	} else {
		fmt.Fprintf(stdout, "- current: %q -> proposed: %q\n", cur, proposed)
	}
	if allowed {
		fmt.Fprintf(stdout, "- policy: %s (ALLOWED) — run again with --confirm --work W to perform the governed write\n", pd.Outcome)
	} else {
		fmt.Fprintf(stdout, "- policy: %s (BLOCKED) — no allowing PolicyDecision; add a standing grant for this capability/target first\n", pd.Outcome)
	}
	return 0
}

func printActResult(stdout io.Writer, target envelope.ActionTarget, res act.Result, asJSON bool) int {
	if asJSON {
		out := map[string]any{
			"mode": "confirmed", "target_id": target.ID, "target_path": target.Path,
			"receipt_id": res.Receipt.ID, "transition_id": res.Transition.ID, "move_type": res.Receipt.MoveType,
			"pre_image_ref": res.PreImageRef, "post_image_ref": res.PostImageRef,
			"before": string(res.PreImage), "after": string(res.PostImage),
		}
		if err := encodeJSON(stdout, out); err != nil {
			fmt.Fprintln(stdout, "error:", err)
			return 1
		}
		return 0
	}
	fmt.Fprintln(stdout, "# act repoint (confirmed — governed write applied)")
	fmt.Fprintf(stdout, "- target: %s in %s\n", target.ID, target.Path)
	fmt.Fprintf(stdout, "- change: %q -> %q\n", string(res.PreImage), string(res.PostImage))
	fmt.Fprintf(stdout, "- receipt=%s transition=%s move=%s\n", res.Receipt.ID, res.Transition.ID, res.Receipt.MoveType)
	fmt.Fprintf(stdout, "- pre-image=%s post-image=%s (a revert restores the pre-image through the same act seam)\n", res.PreImageRef, res.PostImageRef)
	return 0
}

func actIDFor(prefix string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return prefix + hex.EncodeToString(sum[:])[:12]
}

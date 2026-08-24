// sweep_stale_cli.go owns the `compound work sweep-stale` verb. It is the
// work-state-machine counterpart to `compound run-attempt sweep`: where the
// run-attempt sweep terminalizes stuck RunAttempts (and explicitly does NOT
// touch the work state machine), sweep-stale advances stuck WorkEnvelopes to
// failed/cancelled and explicitly does NOT touch RunAttempts. The two never
// run on the same byte; each owns its organ.
//
// Spec: docs_private/plan/packets/2026-08-24-work-sweep-stale.md
//
// DRY-RUN IS THE DEFAULT. `--yes` is the only mutation gate. The packet is
// explicit on this: a sweep-stale that accidentally abandoned a live envelope
// is exactly the fossilization we are trying to prevent — the verb exists to
// audit and operator-review the candidate set, not to silently reap it.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"compound/internal/work"
)

// runWorkSweepStaleCLI is the testable command edge for
// `compound work sweep-stale [--threshold D] [--dry-run|--yes] [--json]`.
// --dry-run is the default; --yes applies. --threshold defaults to
// work.DefaultSweepStaleThreshold (72h). The CLI always supplies the
// production probe (run_attempts + delegate bindings + land-lease); tests
// drive the store method directly with their own probe.
//
// Positional arguments are rejected: sweep-stale is a global operation, not a
// per-work command, and a stray work_id would invite silent misuse.
func runWorkSweepStaleCLI(args []string, store *work.Store, homePath string, now time.Time, stdout, stderr io.Writer) int {
	if hasUsageFlag(args) {
		fmt.Fprintln(stdout, workHelpText("sweep-stale"))
		return 0
	}
	// Argument validation. sweep-stale accepts no positional; --dry-run
	// and --yes are mutually exclusive; only --threshold and --json may
	// appear alongside them.
	for _, a := range args {
		switch a {
		case "--dry-run", "--yes", "--json":
		case "--threshold":
			// handled below (value flag follows)
		default:
			if !looksLikeValueOf(args, a, "--threshold") {
				fmt.Fprintf(stderr, "error: unexpected argument %q\n", a)
				fmt.Fprintln(stderr, workUsage("sweep-stale"))
				return 2
			}
		}
	}
	thresholdStr := ""
	dryRun := true
	jsonOut := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dry-run":
			dryRun = true
		case "--yes":
			dryRun = false
		case "--json":
			jsonOut = true
		case "--threshold":
			if i+1 >= len(args) {
				fmt.Fprintln(stderr, "error: --threshold requires a value")
				fmt.Fprintln(stderr, workUsage("sweep-stale"))
				return 2
			}
			i++
			thresholdStr = args[i]
		}
	}
	threshold := work.DefaultSweepStaleThreshold
	if thresholdStr != "" {
		parsed, err := time.ParseDuration(thresholdStr)
		if err != nil {
			fmt.Fprintf(stderr, "error: --threshold %q is not a valid duration: %v\n", thresholdStr, err)
			fmt.Fprintln(stderr, workUsage("sweep-stale"))
			return 2
		}
		if parsed <= 0 {
			fmt.Fprintf(stderr, "error: --threshold must be greater than zero, got %q\n", thresholdStr)
			fmt.Fprintln(stderr, workUsage("sweep-stale"))
			return 2
		}
		threshold = parsed
	}

	probe := work.NewDefaultSweepStaleProbe(homePath)
	var (
		rep work.SweepStaleReport
		err error
	)
	if dryRun {
		rep, err = store.SweepStalePlan(threshold, probe)
	} else {
		rep, err = store.SweepStaleApply(threshold, probe, now)
	}
	if err != nil {
		fmt.Fprintf(stderr, "error: work sweep-stale: %v\n", err)
		return 1
	}

	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if encodeErr := enc.Encode(rep); encodeErr != nil {
			fmt.Fprintf(stderr, "error: encode sweep-stale: %v\n", encodeErr)
			return 1
		}
		return 0
	}

	mode := "dry-run"
	if !dryRun {
		mode = "applied"
	}
	fmt.Fprintf(stdout, "sweep-stale %s threshold=%s scanned=%d safe=%d reclaimed=%d skipped=%d unreadable=%d\n",
		mode, threshold, rep.Scanned, rep.Safe, rep.Reclaimed, rep.Skipped, rep.Unreadable)
	if len(rep.Candidates) > 0 {
		fmt.Fprintln(stdout, "candidates:")
		for _, c := range rep.Candidates {
			status := "would-reclaim"
			if c.Reclaimed {
				status = "reclaimed"
			} else if c.ReclaimError != "" {
				status = "error:" + c.ReclaimError
			}
			fmt.Fprintf(stdout, "  %s state=%s -> %s age=%s %s\n",
				c.WorkID, c.State, c.TargetState, c.LastEventAge, status)
		}
	}
	if len(rep.UnreadableIDs) > 0 {
		fmt.Fprintln(stdout, "unreadable (probe failure, fail-closed):")
		for _, id := range rep.UnreadableIDs {
			fmt.Fprintf(stdout, "  %s\n", id)
		}
	}
	if !dryRun {
		// Distinguish the two outcomes the operator cares about: this is
		// either a clean apply (no candidates, or all candidates reclaimed)
		// or there is work to reconcile. Exiting 0 keeps the verb safe to
		// schedule; the operator reads stdout for the verdict.
		for _, c := range rep.Candidates {
			if c.ReclaimError != "" {
				return 1
			}
		}
	}
	return 0
}

// looksLikeValueOf reports whether `token` is the value immediately following
// `flagName` in args. Used to allow `--threshold 72h` to flow through the
// positional-arg gate without being mistaken for a stray value.
func looksLikeValueOf(args []string, token, flagName string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flagName && args[i+1] == token {
			return true
		}
	}
	return false
}

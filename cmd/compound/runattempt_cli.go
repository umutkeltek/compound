// runattempt_cli.go owns the `compound run-attempt ...` verb (cmdRunAttempt).
// C3: crash terminality — sweep stale pending/running RunAttempts to terminal
// so no attempt stays pending forever. RunAttempt is evidence, not enforcement:
// sweeping has NO effect on the work state machine.
//
// Subcommands:
//   - list [--non-terminal] [--home PATH]               list attempts (all, or just stuck)
//   - sweep [--threshold D] [--home PATH]               terminalize stale attempts (default 30m).
//     The CLI ALWAYS passes the work-owned
//     liveness probe (buildDelegateLivenessProbe)
//     so live delegated panes are heartbeated;
//     probe faults skip the candidate. The
//     store-level SweepStale (no probe) is
//     the compatibility test path and is NOT
//     surfaced as a CLI opt-out -- the
//     operator-visible default must never
//     abandon a healthy in-flight session.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"compound/internal/home"
	"compound/internal/landlease"
	"compound/internal/runattempt"
	"compound/internal/work"
)

func cmdRunAttempt(args []string) {
	if len(args) == 0 {
		printRunAttemptUsage()
		return
	}
	switch args[0] {
	case "list":
		cmdRunAttemptList(args[1:])
	case "sweep":
		cmdRunAttemptSweep(args[1:])
	case "--help", "-h", "help":
		printRunAttemptUsage()
	default:
		fmt.Fprintf(os.Stderr, "unknown run-attempt subcommand: %s\n\n", args[0])
		printRunAttemptUsage()
	}
}

func printRunAttemptUsage() {
	fmt.Fprintf(os.Stderr, `usage: compound run-attempt <subcommand>

subcommands:
  list [--non-terminal] [--home PATH]   list run attempts (all, or just pending/running)
  sweep [--threshold 30m] [--home PATH] terminalize stale pending/running attempts
                                        (default threshold: %s). Live delegated
                                        panes are protected via the work-owned
                                        liveness probe; probe faults skip.
`, runattempt.DefaultSweepThreshold)
}

func resolveRunAttemptHome(args []string) string {
	homePath, err := home.Resolve(flagValue(args, "--home"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "run-attempt: %v\n", err)
		os.Exit(1)
	}
	return homePath
}

func cmdRunAttemptList(args []string) {
	homePath := resolveRunAttemptHome(args)
	nonTerminal := hasFlag(args, "--non-terminal")
	jsonOut := hasFlag(args, "--json")
	store := runattempt.Open(homePath)

	var attempts []runattempt.RunAttempt
	var err error
	if nonTerminal {
		attempts, err = store.ListNonTerminal()
	} else {
		attempts, err = store.List()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "run-attempt list: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(attempts); err != nil {
			fmt.Fprintf(os.Stderr, "run-attempt list: encode: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if len(attempts) == 0 {
		fmt.Println("no run attempts found")
		return
	}
	for _, ra := range attempts {
		age := time.Since(ra.CreatedAt).Round(time.Second)
		origin := ra.Origin
		if origin == "" {
			origin = "unknown"
		}
		hb := ""
		if ra.LastHeartbeatAt != nil {
			hb = fmt.Sprintf(" hb=%s", time.Since(*ra.LastHeartbeatAt).Round(time.Second))
		}
		reason := ""
		if ra.SweepReason != "" {
			reason = " (" + ra.SweepReason + ")"
		}
		fmt.Printf("%s  status=%-9s origin=%s work=%s actor=%s harness=%s age=%s%s%s\n",
			ra.ID, ra.Status, origin, ra.WorkID, ra.Actor, ra.Harness, age, hb, reason)
	}
	fmt.Printf("\n%d attempt(s)\n", len(attempts))
}

func cmdRunAttemptSweep(args []string) {
	homePath := resolveRunAttemptHome(args)
	thresholdStr := flagValue(args, "--threshold")
	jsonOut := hasFlag(args, "--json")

	threshold := runattempt.DefaultSweepThreshold
	if thresholdStr != "" {
		parsed, err := time.ParseDuration(thresholdStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "run-attempt sweep: invalid threshold %q: %v\n", thresholdStr, err)
			os.Exit(1)
		}
		threshold = parsed
	}
	store := runattempt.Open(homePath)
	// The default sweep ALWAYS protects stale live delegates. The probe
	// is work-owned (RecoverBoundSession + tmux SessionStatus). Probe
	// errors skip rather than abandon. nil-probe byte identity with
	// SweepStale is preserved at the store level for the focused
	// store-level compatibility test (and for any direct API caller
	// that wants the pre-probe behaviour); it is NOT surfaced as a
	// CLI opt-out, because the operator-visible default must never
	// abandon a healthy in-flight session.
	probe := buildDelegateLivenessProbe(homePath)
	rep, err := store.SweepStaleWithProbe(threshold, time.Now().UTC(), probe)
	if err != nil {
		fmt.Fprintf(os.Stderr, "run-attempt sweep: %v\n", err)
		os.Exit(1)
	}
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintf(os.Stderr, "run-attempt sweep: encode: %v\n", err)
			os.Exit(1)
		}
		return
	}
	fmt.Printf("swept %d/%d stale attempt(s) (threshold %s, protected %d)\n",
		rep.Swept, rep.Scanned, threshold, rep.Protected)
	for _, id := range rep.SweptIDs {
		fmt.Printf("  swept: %s\n", id)
	}
	for _, id := range rep.SkippedIDs {
		fmt.Printf("  kept:  %s\n", id)
	}
}

// sweepStaleRunAttemptsBestEffort terminalizes RunAttempts whose bound
// pane is provably gone. It runs immediately before a new delegate
// spawn, so an operator who keeps delegating never accumulates ghosts.
//
// Why this call site exists at all: the sweep below was already correct
// and already protected live panes, but NOTHING invoked it unattended --
// no ticker, no goroutine, no launchd. Every delegate that died left its
// attempt reporting `running` forever, and `compound executors` /
// `compound attention` faithfully reported that lie. On 2026-07-26 that
// produced 55 phantom `running` sessions that had been dead for ten
// hours. Spawning is the natural moment to collect the previous
// generation's corpses: it is already a mutating command, the sweep is
// ~50ms against 1600+ attempts, and the liveness probe means a healthy
// in-flight session is never touched.
//
// Best-effort by design, and deliberately NOT fail-closed like pane
// capture: refusing to start new work because cleanup of unrelated old
// attempts failed would trade a small bookkeeping problem for a total
// outage. Failures are reported to stderr rather than swallowed, so a
// persistently broken sweep is visible instead of silent.
//
// This does not remove the need for a scheduled sweep -- an idle machine
// spawns nothing and so sweeps nothing. See
// tools/templates/com.example.compound-runattempt-sweep.plist.
func sweepStaleRunAttemptsBestEffort(homePath string, now time.Time, stderr io.Writer) int {
	return sweepStaleRunAttemptsBestEffortWithRunner(homePath, work.NewTmuxRunner(), now, stderr)
}

// sweepStaleRunAttemptsBestEffortWithRunner is the injectable seam, matching
// buildDelegateLivenessProbeWithRunner: tests drive a fake session runner so
// the live-pane protection path is exercised rather than asserted.
func sweepStaleRunAttemptsBestEffortWithRunner(homePath string, runner work.SessionRunner, now time.Time, stderr io.Writer) int {
	store := runattempt.Open(homePath)
	probe := buildDelegateLivenessProbeWithRunner(homePath, runner)
	rep, err := store.SweepStaleWithProbe(runattempt.DefaultSweepThreshold, now.UTC(), probe)
	if err != nil {
		fmt.Fprintf(stderr, "warning: pre-spawn run-attempt sweep failed: %v\n", err)
		return 0
	}
	if rep.Swept > 0 {
		fmt.Fprintf(stderr, "pre-spawn sweep: terminalized %d abandoned run attempt(s)\n", rep.Swept)
	}
	return rep.Swept
}

// buildDelegateLivenessProbe builds the work-owned liveness probe that
// `compound run-attempt sweep` always passes to SweepStaleWithProbe.
// The probe maps each stale candidate to its exact delegate binding
// (when one exists), probes SessionStatus, and returns live=true only
// when the bound pane is alive. A candidate with no delegate binding
// returns live=false (ordinary stale abandonment); a probe error
// returns err (the store's sweeper skips on err, so the attempt is
// retried on the next pass).
func buildDelegateLivenessProbe(homePath string) runattempt.LivenessProbe {
	return buildDelegateLivenessProbeWithRunner(homePath, work.NewTmuxRunner())
}

func buildDelegateLivenessProbeWithRunner(homePath string, runner work.SessionRunner) runattempt.LivenessProbe {
	wstore, werr := work.Open(homePath)
	if werr != nil {
		// Without work store, the default would NOT protect live panes
		// -- that defeats the safety contract. Return a probe that
		// errors on every candidate so the sweeper skips all of them
		// rather than abandoning any.
		return func(ra runattempt.RunAttempt) (bool, error) {
			return false, fmt.Errorf("work store unavailable: %w", werr)
		}
	}
	if runner == nil {
		return func(ra runattempt.RunAttempt) (bool, error) {
			return false, fmt.Errorf("delegate liveness runner unavailable")
		}
	}
	return func(ra runattempt.RunAttempt) (bool, error) {
		status, statusErr := landlease.Status(homePath, time.Now().UTC())
		if statusErr != nil {
			return false, statusErr
		}
		parts := strings.SplitN(status.Holder, ":", 5)
		if len(parts) == 5 && parts[0] == "proof" && parts[2] == ra.ID {
			pending, pendingErr := landlease.ExecutionPending(homePath, status.Holder)
			if pendingErr != nil {
				return false, pendingErr
			}
			if pending {
				return true, nil
			}
		}
		binding, _, rerr := wstore.RecoverBoundSession(ra.WorkID)
		if rerr != nil {
			return false, rerr
		}
		if binding == nil || binding.RunAttemptID != ra.ID {
			// No exact delegate binding for this ratt -- the candidate
			// is claim-reservation / observe-run, not a delegated
			// execution. Generic stale semantics apply (live=false
			// here means the candidate will be abandoned in the
			// caller).
			return false, nil
		}
		st, perr := runner.SessionStatus(binding.Session)
		if perr != nil {
			return false, perr
		}
		return st.Exists && !st.PaneDead, nil
	}
}

// runattempt_cli.go owns the `compound run-attempt ...` verb (cmdRunAttempt).
// C3: crash terminality — sweep stale pending/running RunAttempts to terminal
// so no attempt stays pending forever. RunAttempt is evidence, not enforcement:
// sweeping has NO effect on the work state machine.
//
// Subcommands:
//   - list [--non-terminal] [--home PATH]   list attempts (all, or just stuck)
//   - sweep [--threshold D] [--home PATH]   terminalize stale attempts (default 30m)
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"compound/internal/home"
	"compound/internal/runattempt"
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
  list [--non-terminal] [--home PATH]     list run attempts (all, or just pending/running)
  sweep [--threshold 30m] [--home PATH]   terminalize stale pending/running attempts
                                          (default threshold: %s)
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
		hb := ""
		if ra.LastHeartbeatAt != nil {
			hb = fmt.Sprintf(" hb=%s", time.Since(*ra.LastHeartbeatAt).Round(time.Second))
		}
		reason := ""
		if ra.SweepReason != "" {
			reason = " (" + ra.SweepReason + ")"
		}
		fmt.Printf("%s  status=%-9s work=%s actor=%s harness=%s age=%s%s%s\n",
			ra.ID, ra.Status, ra.WorkID, ra.Actor, ra.Harness, age, hb, reason)
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
	rep, err := store.SweepStale(threshold, time.Now().UTC())
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
	fmt.Printf("swept %d/%d stale attempt(s) (threshold %s)\n", rep.Swept, rep.Scanned, threshold)
	for _, id := range rep.SweptIDs {
		fmt.Printf("  swept: %s\n", id)
	}
	for _, id := range rep.SkippedIDs {
		fmt.Printf("  kept:  %s (fresh)\n", id)
	}
}

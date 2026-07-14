package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"compound/internal/home"
	"compound/internal/landlease"
)

// cmdLandLease is the operator/orchestrator door onto the land-lease — the single-holder, TTL-bounded,
// steal-with-reason advisory MUTEX over the ACT of landing to main (a flock-guarded JSON lease under
// COMPOUND_HOME). Concurrent executor lanes acquire it before their ff-merge so they serialize the
// landing window instead of colliding. It delegates to runLandLease, which returns an exit code (never
// calls os.Exit) so the refusal/timeout paths are exercised by tests.
func cmdLandLease(args []string) {
	if code := runLandLease(args, os.Stdout, os.Stderr); code != 0 {
		os.Exit(code)
	}
}

func landLeaseUsage() string {
	return "usage:\n" +
		"  compound land-lease acquire --holder H [--ttl S] [--wait] [--timeout S] [--home P] [--json]\n" +
		"    take (or refresh) the land-lease for holder H (default ttl 300s). Without --wait, a live lease\n" +
		"    held by a DIFFERENT holder exits non-zero with the current holder printed; --wait polls until the\n" +
		"    lease is free or --timeout (default 120s) elapses. A steal never happens here — only `steal` does.\n" +
		"  compound land-lease release --holder H [--home P] [--json]\n" +
		"    free the lease ONLY if H holds it; a non-holder release is refused (someone else's lease is never\n" +
		"    dropped), and a release with nothing held is an idempotent no-op\n" +
		"  compound land-lease status [--home P] [--json]\n" +
		"    show the current holder, acquired_at, expires_at, remaining seconds, and is_expired\n" +
		"  compound land-lease steal --holder H --reason R [--home P] [--json]\n" +
		"    transfer the lease to H and RECORD prior_holder + reason + stolen_at (auditable). A reason is\n" +
		"    REQUIRED — this is the only path that takes over a LIVE lease.\n" +
		"the land-lease is a COORDINATION HINT (it records who holds the right to land); it never mutates repo\n" +
		"state and the compound-land ceremony is the consumer that acts on it. No provider/network calls."
}

func runLandLease(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || hasUsageFlag(args) {
		fmt.Fprintln(stderr, landLeaseUsage())
		return 2
	}
	switch args[0] {
	case "acquire":
		return runLandLeaseAcquire(args[1:], stdout, stderr)
	case "release":
		return runLandLeaseRelease(args[1:], stdout, stderr)
	case "status":
		return runLandLeaseStatus(args[1:], stdout, stderr)
	case "steal":
		return runLandLeaseSteal(args[1:], stdout, stderr)
	default:
		fmt.Fprintln(stderr, landLeaseUsage())
		return 2
	}
}

// secondsFlag reads an integer-seconds flag, returning fallback when absent.
func secondsFlag(args []string, name string, fallback int) (time.Duration, error) {
	raw := flagValue(args, name)
	if raw == "" {
		return time.Duration(fallback) * time.Second, nil
	}
	n, err := intFlag(args, name)
	if err != nil {
		return 0, err
	}
	return time.Duration(n) * time.Second, nil
}

func runLandLeaseAcquire(args []string, stdout, stderr io.Writer) int {
	homePath, err := home.Resolve(flagValue(args, "--home"))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	holder := flagValue(args, "--holder")
	ttl, err := secondsFlag(args, "--ttl", landlease.DefaultTTLSeconds)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	var lease landlease.Lease
	if hasFlag(args, "--wait") {
		timeout, tErr := secondsFlag(args, "--timeout", 120)
		if tErr != nil {
			fmt.Fprintln(stderr, "error:", tErr)
			return 1
		}
		lease, err = landlease.AcquireWait(homePath, holder, ttl, landlease.RealClock{}, timeout, 2*time.Second)
	} else {
		lease, err = landlease.Acquire(homePath, holder, ttl, time.Now())
	}
	if err != nil {
		// A contention refusal prints the current holder (from status) so the caller knows who to wait
		// on or steal from; any error exits non-zero.
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}

	if hasFlag(args, "--json") {
		if err := encodeJSON(stdout, lease); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "acquired land-lease: holder=%s ttl=%ds expires=%s\n", lease.Holder, lease.TTLSeconds, lease.ExpiresAt)
	return 0
}

func runLandLeaseRelease(args []string, stdout, stderr io.Writer) int {
	homePath, err := home.Resolve(flagValue(args, "--home"))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	res, err := landlease.Release(homePath, flagValue(args, "--holder"), time.Now())
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if hasFlag(args, "--json") {
		if err := encodeJSON(stdout, res); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "%s (freed=%v)\n", res.Message, res.Freed)
	return 0
}

func runLandLeaseStatus(args []string, stdout, stderr io.Writer) int {
	homePath, err := home.Resolve(flagValue(args, "--home"))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	st, err := landlease.Status(homePath, time.Now())
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if hasFlag(args, "--json") {
		if err := encodeJSON(stdout, st); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		return 0
	}
	if st.Holder == "" {
		fmt.Fprintln(stdout, "land-lease: none held")
		return 0
	}
	fmt.Fprintf(stdout, "land-lease: holder=%s held=%v expired=%v remaining=%ds expires=%s\n",
		st.Holder, st.Held, st.IsExpired, st.RemainingSeconds, st.ExpiresAt)
	if st.PriorHolder != "" {
		fmt.Fprintf(stdout, "  (stolen from %s at %s: %s)\n", st.PriorHolder, st.StolenAt, st.StealReason)
	}
	return 0
}

func runLandLeaseSteal(args []string, stdout, stderr io.Writer) int {
	homePath, err := home.Resolve(flagValue(args, "--home"))
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	lease, err := landlease.Steal(homePath, flagValue(args, "--holder"), flagValue(args, "--reason"), time.Now())
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 1
	}
	if hasFlag(args, "--json") {
		if err := encodeJSON(stdout, lease); err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "stole land-lease: holder=%s prior=%s reason=%q expires=%s\n",
		lease.Holder, lease.PriorHolder, lease.StealReason, lease.ExpiresAt)
	return 0
}

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
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

// landLeaseContentionRefusal renders the acquire refusal when a DIFFERENT lane holds a live
// lease. The recovery is a ladder, not a single command, and the rungs are not
// interchangeable: waiting is correct when the holder is mid-ceremony, stealing is correct
// only when it is not — so the refusal names both and makes the steal's audit cost visible
// rather than presenting it as the obvious fix.
func landLeaseContentionRefusal(holder string, held *landlease.HeldError, waited bool, observed error) gateRefusal {
	me := firstNonEmptyRefusalValue(holder, "<holder>")
	// The trace quotes the observed error verbatim rather than re-describing it: the plain
	// and the --wait paths fail for different reasons (held now vs still held when the wait
	// window ran out) and only the real error distinguishes them.
	attempt := fmt.Sprintf("acquire for %s: %v", me, observed)
	if waited {
		attempt = fmt.Sprintf("acquire --wait for %s: %v", me, observed)
	}
	return gateRefusal{
		Command: "compound land-lease acquire",
		Gate:    "land_lease_contention",
		Refused: fmt.Sprintf("the land-lease is held by %q, so %q did not take it", held.Holder, me),
		Tried: []string{
			attempt,
			"transfer it anyway: not attempted — acquire never takes over a LIVE lease, only steal does",
		},
		DoThis: []string{
			"compound land-lease status   # the holder, remaining seconds, and any prior steal on the record",
			fmt.Sprintf("compound land-lease acquire --holder %s --wait --timeout 120   # block until the holder finishes or the window elapses", me),
			fmt.Sprintf("compound land-lease steal --holder %s --reason \"<why the holder cannot finish>\"   # records prior_holder + reason; only when the holder is NOT mid-ceremony", me),
		},
		Prevents: "two lanes landing to main inside the same window. The lease serializes the landing act; taking it from a live holder without a recorded reason turns a merge collision into one with no trail back to who decided to override whom.",
	}
}

// landLeaseHolderRefusal renders the release refusal for a caller who is not the holder.
// verb is the attempted operation so the same renderer can serve a future non-holder path
// without a second copy of this text.
func landLeaseHolderRefusal(verb, holder string, st landlease.StatusResult) gateRefusal {
	me := firstNonEmptyRefusalValue(holder, "<holder>")
	return gateRefusal{
		Command: "compound land-lease " + verb,
		Gate:    "land_lease_holder",
		Refused: fmt.Sprintf("%q does not hold the land-lease, so %s left %q's lease in place", me, verb, st.Holder),
		Tried: []string{
			fmt.Sprintf("--holder %s against the recorded holder %q: not the holder", me, st.Holder),
			fmt.Sprintf("current lease: holder=%s held=%v expired=%v remaining=%ds", st.Holder, st.Held, st.IsExpired, st.RemainingSeconds),
		},
		DoThis: []string{
			"compound land-lease status   # confirm who holds it and how long is left",
			fmt.Sprintf("compound land-lease release --holder %s   # the holder's own release is the ordinary path", firstNonEmptyRefusalValue(st.Holder, "<current-holder>")),
			fmt.Sprintf("compound land-lease steal --holder %s --reason \"<why the holder cannot finish>\"   # the auditable takeover, when the holder cannot release it themselves", me),
		},
		Prevents: "one lane silently dropping another lane's lease. A release that ignored the holder would let a lane that never landed anything clear the mutex out from under a lane mid-ceremony, with nothing recorded to say it happened.",
	}
}

// landLeaseStealAuditRefusal renders the steal refusal when no --reason was supplied. The
// missing reason is not a typo to correct: a steal without one is the same act as a steal
// with one, minus the only thing that makes it reviewable afterwards.
func landLeaseStealAuditRefusal(holder string, st landlease.StatusResult) gateRefusal {
	me := firstNonEmptyRefusalValue(holder, "<holder>")
	current := "nobody holds it right now"
	if st.Holder != "" {
		current = fmt.Sprintf("%q holds it (held=%v expired=%v remaining=%ds)", st.Holder, st.Held, st.IsExpired, st.RemainingSeconds)
	}
	return gateRefusal{
		Command: "compound land-lease steal",
		Gate:    "land_lease_steal_audit",
		Refused: fmt.Sprintf("no --reason was given, so the lease was not transferred to %q", me),
		Tried: []string{
			"--reason: not passed (or empty)",
			"current lease: " + current,
		},
		DoThis: []string{
			fmt.Sprintf("compound land-lease steal --holder %s --reason \"<why the current holder cannot finish>\"   # the reason lands on the record as prior_holder + reason + stolen_at", me),
			fmt.Sprintf("compound land-lease acquire --holder %s   # if the lease is free or already expired, this is the right verb — a steal is not needed", me),
			"compound land-lease status   # check whether the lease already aged out before overriding a live holder",
		},
		Prevents: "an unattributable takeover. steal is the ONLY path that overrides a live holder; without a recorded reason the audit trail cannot distinguish a justified override from a lane that simply did not wait.",
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
		// Contention is the ONE outcome here that is a gate rather than a fault: another lane
		// legitimately holds the right to land. It is typed (*landlease.HeldError, which
		// AcquireWait also wraps on timeout), so the refusal is selected by the error's kind
		// and never by matching its text. Anything else -- a bad ttl, an unreadable home -- is
		// a plain error and stays one.
		var held *landlease.HeldError
		if errors.As(err, &held) {
			landLeaseContentionRefusal(holder, held, hasFlag(args, "--wait"), err).writeTo(stderr)
			return 1
		}
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
	holder := flagValue(args, "--holder")
	res, err := landlease.Release(homePath, holder, time.Now())
	if err != nil {
		// Release refuses exactly one thing on purpose: dropping a lease the caller does not
		// hold. Which case fired is decided by re-reading the lease, not by matching Release's
		// error text -- a non-holder release and an unreadable home must not share a remedy.
		if st, sErr := landlease.Status(homePath, time.Now()); sErr == nil && st.Holder != "" && st.Holder != strings.TrimSpace(holder) {
			landLeaseHolderRefusal("release", holder, st).writeTo(stderr)
			return 1
		}
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
	holder := flagValue(args, "--holder")
	// The reason is the whole difference between a steal and a silent takeover: it is what
	// makes the transfer auditable afterwards. Checked here, before Steal, so the refusal can
	// name the alternative (acquire, for an uncontested lease) rather than repeat the rule.
	if strings.TrimSpace(flagValue(args, "--reason")) == "" && strings.TrimSpace(holder) != "" {
		st, _ := landlease.Status(homePath, time.Now())
		landLeaseStealAuditRefusal(holder, st).writeTo(stderr)
		return 1
	}
	lease, err := landlease.Steal(homePath, holder, flagValue(args, "--reason"), time.Now())
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

package executewire

import (
	"strings"
	"time"

	"compound/internal/work"
)

// resolveLaneLaunchTuple returns the {command, modelID, providerRoute} the
// lane's adapter would resolve at launch. The unattended execute seam calls
// this from both the preflight and launch hooks so the request handed to the
// readiness guard carries the resolved tuple — the guard's
// resolved-vs-expected comparison in
// internal/harness/registry.go:ReadOnlyLaunchReadiness then succeeds for a
// tuple-backed lane.
//
// For lanes the resolver treats as not tuple-backed (raw claude, codex), or
// when the resolver is not installed / returns an error, the helper returns
// ("", "", "", false): the seam falls through to passing the lane id, exactly
// as before. That matches the interactive delegate path's behavior when
// DelegateHarnessResolver is absent or rejects the request, and the
// readiness guard then surfaces the resolver-specific refusal with full
// context if the lane really is unready.
//
// The helper is read-only: it loads the Compound model-router authority but
// never mutates it, never records evidence, and never starts a subprocess.
//
// work_r6j_readiness_tuple_20260911 — the seam's two readiness call sites
// (PreflightViaActuator and LaunchViaActuator) used to fill
// {RequestedModelID, RequestedProviderRoute, Command} with the lane id and
// the empty string, so a tuple-backed Claude lane always failed the guard's
// "resolved tuple matches the request" comparison. The interactive delegate
// path already calls work.DelegateHarnessResolver; this helper is the same
// resolver reused read-only from the seam, with a fall-through for the
// non-tuple lanes the comparison does not gate.
func resolveLaneLaunchTuple(homePath, lane string, now time.Time) (command, modelID, providerRoute string, tupleBacked bool) {
	if strings.TrimSpace(lane) == "" || homePath == "" {
		return "", "", "", false
	}
	resolver := work.DelegateHarnessResolver
	if resolver == nil {
		return "", "", "", false
	}
	resolution, err := resolver(work.DelegateHarnessResolveRequest{
		HomePath: homePath,
		Harness:  lane,
		Now:      now,
	})
	if err != nil {
		// Fall through. The downstream readiness guard revalidates the same
		// tuple via the adapter's RevalidateGovernedRun, which surfaces the
		// resolver's error verbatim.
		return "", "", "", false
	}
	if !resolution.TupleBacked {
		return "", "", "", false
	}
	return strings.TrimSpace(resolution.Command),
		strings.TrimSpace(resolution.RequestedModelID),
		strings.TrimSpace(resolution.RequestedProviderRoute),
		true
}

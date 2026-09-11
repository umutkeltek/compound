package executewire

import (
	"strings"
	"time"

	"compound/internal/routeevidence"
	"compound/internal/work"
)

// resolveLaneLaunchTuple returns the {command, modelID, providerRoute,
// profileRevision, tupleBacked} the lane's adapter would resolve at launch.
// The unattended execute seam calls this from both the preflight and launch
// hooks so the request handed to the readiness guard carries the resolved
// tuple — the guard's resolved-vs-expected comparison in
// internal/harness/registry.go:ReadOnlyLaunchReadiness then succeeds for a
// tuple-backed lane.
//
// profileRevision is the Compound model-capabilities snapshot revision the
// resolver just consulted (sha256 of the manifest bytes). The launch wire
// uses it as the fallback Revision for posture evidence items the readiness
// guard produced WITHOUT a Revision — see work_r6m_seam_posture_20260911.
// Today the production `executable` evidence item in
// cmd/compound/work_delegate_readiness.go has Ref set and Revision empty;
// the wire fills the Revision from profileRevision before stamping the
// posture, so runattempt.SetExecutionPosture accepts the item. Items the
// wire CANNOT give a revision to (no resolver, no manifest) surface as
// `lane_unready:posture evidence <kind> has no revision` — the recognized
// refusal prefix the execute stage treats as a skip reason.
//
// For lanes the resolver treats as not tuple-backed (raw claude, codex), or
// when the resolver is not installed / returns an error, the helper returns
// ("", "", "", "", false): the seam falls through to passing the lane id,
// exactly as before. That matches the interactive delegate path's behavior
// when DelegateHarnessResolver is absent or rejects the request, and the
// readiness guard then surfaces the resolver-specific refusal with full
// context if the lane really is unready. The profileRevision field is left
// empty in that case — the wire's posture-evidence fallback degrades to
// refusing with `lane_unready:` rather than stamping a synthetic revision.
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
//
// work_r6m_seam_posture_20260911 — added the profileRevision return value
// so the launch wire can give a real revision to posture evidence items
// that the readiness guard produced without one.
func resolveLaneLaunchTuple(homePath, lane string, now time.Time) (command, modelID, providerRoute, profileRevision string, tupleBacked bool) {
	if strings.TrimSpace(lane) == "" || homePath == "" {
		return "", "", "", "", false
	}
	resolver := work.DelegateHarnessResolver
	if resolver == nil {
		return "", "", "", "", false
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
		return "", "", "", "", false
	}
	if !resolution.TupleBacked {
		return "", "", "", "", false
	}
	// Load the manifest snapshot revision directly: routeevidence is the
	// Compound authority owner and its LoadModelCapabilitiesSnapshot returns
	// the sha256 of the manifest bytes. That revision IS the lane tuple's
	// profile revision the wire stamps onto posture evidence items the
	// readiness guard produced without one (work_r6m_seam_posture_20260911).
	// An unreadable manifest here means we cannot derive a fallback revision
	// at all — the wire's posture-evidence pass surfaces `lane_unready:`
	// rather than stamping junk.
	profileRevision = ""
	if _, _, rev, snapshotErr := routeevidence.LoadModelCapabilitiesSnapshot(homePath); snapshotErr == nil {
		profileRevision = rev
	}
	return strings.TrimSpace(resolution.Command),
		strings.TrimSpace(resolution.RequestedModelID),
		strings.TrimSpace(resolution.RequestedProviderRoute),
		profileRevision,
		true
}

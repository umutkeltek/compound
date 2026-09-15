package executewire

import (
	"fmt"
	"strings"

	"compound/internal/envelope"
	"compound/internal/runattempt"
)

// buildSelectionSnapshotFromSaved resolves the launch wire's
// ExecutionSelectionSnapshot from the work envelope's saved selection,
// any explicit one-run override, the resolver-observed profile revision,
// and the harness-level inputs. source is "saved" unless overrideLaunchProfile
// is non-empty (which switches the snapshot to source: override and discards
// the saved revision).
//
// The snapshot reflects what the wire ASKED the adapter to run, plus the
// honest evidence about what the adapter COULD apply (effort control,
// session pinning). The attempt's observed identity (real model id from
// the provider receipt) stays on RunAttempt.ModelID / ProviderRoute and is
// attested only by route evidence -- the snapshot never claims observed
// identity, only what was asked.
//
// work_r7a_execution_selection_20260911.
func buildSelectionSnapshotFromSaved(
	wenv envelope.WorkEnvelope,
	overrideHarness string,
	overrideLaunchProfile string,
	resolvedProviderRoute string,
	resolvedModelID string,
	observedProfileRevision string,
	tupleProfileRevision string,
) (runattempt.ExecutionSelectionSnapshot, error) {
	if overrideLaunchProfile != "" {
		// Explicit one-run override. The wire treats the override as
		// authoritative for THIS run; the saved selection is untouched.
		// observedProfileRevision may be empty (a one-run override
		// can name a profile the resolver cannot resolve) -- the
		// validator accepts an empty revision, and the reader sees
		// source: override so it knows why.
		harnessName := strings.TrimSpace(overrideHarness)
		if harnessName == "" {
			harnessName = firstNonEmpty(envelopePtr(wenv.ExecutionSelection).Harness, "os-harness-entrypoints")
		}
		snap := runattempt.ExecutionSelectionSnapshot{
			Source:                "override",
			LaunchProfileID:       overrideLaunchProfile,
			LaunchProfileRevision: observedProfileRevision,
			Harness:               harnessName,
			ProviderRoute:         resolvedProviderRoute,
			RequestedModelID:      resolvedModelID,
			RequestedEffort:       envelopePtr(wenv.ExecutionSelection).RequestedEffort,
		}
		applyEffortEvidence(&snap)
		applyNativeSessionEvidence(&snap)
		if err := snap.Validate(); err != nil {
			return runattempt.ExecutionSelectionSnapshot{}, fmt.Errorf("build override snapshot: %w", err)
		}
		return snap, nil
	}
	sel := wenv.ExecutionSelection
	if sel == nil {
		// No saved selection: the wire falls through to the project
		// default. The snapshot records source: project_default so a
		// reader sees why no saved selection drove the launch.
		snap := runattempt.ExecutionSelectionSnapshot{
			Source:           "project_default",
			Harness:          firstNonEmpty(strings.TrimSpace(overrideHarness), "os-harness-entrypoints"),
			ProviderRoute:    resolvedProviderRoute,
			RequestedModelID: resolvedModelID,
		}
		applyEffortEvidence(&snap)
		applyNativeSessionEvidence(&snap)
		if err := snap.Validate(); err != nil {
			return runattempt.ExecutionSelectionSnapshot{}, fmt.Errorf("build project_default snapshot: %w", err)
		}
		return snap, nil
	}
	// Saved selection. Stamp the consumed revision; record drift when
	// the observed profile revision differs from the saved one.
	snap := runattempt.ExecutionSelectionSnapshot{
		Source:                "saved",
		SelectionRevision:     sel.Revision,
		LaunchProfileID:       sel.LaunchProfileID,
		LaunchProfileRevision: observedProfileRevision,
		Harness:               sel.Harness,
		ProviderRoute:         sel.ProviderRoute,
		RequestedModelID:      sel.RequestedModelID,
		RequestedEffort:       sel.RequestedEffort,
	}
	if overrideHarness != "" && overrideHarness != sel.Harness {
		// One-run harness override on a saved selection: the snapshot
		// records source: saved (the selection is the plan), and the
		// override is reflected by switching the harness on the snapshot
		// to the override's family. The envelope's saved selection is
		// untouched.
		snap.Harness = overrideHarness
	}
	if strings.TrimSpace(snap.LaunchProfileRevision) == "" {
		snap.LaunchProfileRevision = sel.LaunchProfileRevision
	}
	if tupleProfileRevision != "" && sel.LaunchProfileRevision != "" && tupleProfileRevision != sel.LaunchProfileRevision {
		snap.SelectionDrift = fmt.Sprintf("launch_profile_revision saved=%s launched=%s", sel.LaunchProfileRevision, tupleProfileRevision)
	}
	applyEffortEvidence(&snap)
	applyNativeSessionEvidence(&snap)
	if err := snap.Validate(); err != nil {
		return runattempt.ExecutionSelectionSnapshot{}, fmt.Errorf("build saved snapshot: %w", err)
	}
	return snap, nil
}

// BuildSelectionSnapshot resolves the per-run ExecutionSelectionSnapshot for
// a governed launch OUTSIDE the launch wire — today the public
// `work advance --execute` CLI path (and any future adapter-driven caller
// that has no lane tuple). Resolution precedence is the contract's:
//
//	1. resolvedOverride (an explicit one-run --launch-profile/--effort
//	   resolution) — source=override; the envelope's saved selection is
//	   not touched;
//	2. the envelope's saved selection — source=saved, stamped with its
//	   consumed revision;
//	3. otherwise the project default — source=project_default.
//
// The snapshot records only what this launch was ASKED to run plus the
// honest effort/session evidence; observed provider identity stays on the
// RunAttempt's own fields. The route/model asked for come from the
// resolution that produced the choice: the override's resolution when the
// override drives, the saved selection's own values when it drives, and
// nothing when the project default stands in (this caller has no lane
// tuple to resolve).
//
// work_r7a_execution_selection_20260911; public-CLI contract 2026-09-15.
func BuildSelectionSnapshot(
	wenv envelope.WorkEnvelope,
	overrideHarness string,
	resolvedOverride *envelope.ExecutionSelection,
) (runattempt.ExecutionSelectionSnapshot, error) {
	if resolvedOverride != nil && strings.TrimSpace(resolvedOverride.LaunchProfileID) != "" {
		snap := runattempt.ExecutionSelectionSnapshot{
			Source:                "override",
			LaunchProfileID:       strings.TrimSpace(resolvedOverride.LaunchProfileID),
			LaunchProfileRevision: strings.TrimSpace(resolvedOverride.LaunchProfileRevision),
			Harness:               firstNonEmpty(strings.TrimSpace(overrideHarness), strings.TrimSpace(resolvedOverride.Harness)),
			ProviderRoute:         strings.TrimSpace(resolvedOverride.ProviderRoute),
			RequestedModelID:      strings.TrimSpace(resolvedOverride.RequestedModelID),
			RequestedEffort:       strings.TrimSpace(resolvedOverride.RequestedEffort),
		}
		if snap.Harness == "" {
			snap.Harness = firstNonEmpty(envelopePtr(wenv.ExecutionSelection).Harness, "os-harness-entrypoints")
		}
		applyEffortEvidence(&snap)
		applyNativeSessionEvidence(&snap)
		if err := snap.Validate(); err != nil {
			return runattempt.ExecutionSelectionSnapshot{}, fmt.Errorf("build override snapshot: %w", err)
		}
		return snap, nil
	}
	sel := wenv.ExecutionSelection
	resolvedRoute, resolvedModel := "", ""
	if sel != nil {
		// The saved selection's own route/model ARE what the work asks for;
		// this caller has no lane tuple to resolve them from again.
		resolvedRoute, resolvedModel = sel.ProviderRoute, sel.RequestedModelID
	}
	return buildSelectionSnapshotFromSaved(
		wenv,
		overrideHarness,
		"",
		resolvedRoute,
		resolvedModel,
		"", // no observed profile revision on this path; the saved one stamps through
		"",
	)
}

// applyEffortEvidence stamps the honest evidence about whether the
// applied effort is known. Today the wire cannot attest applied effort
// for any production harness (the Claude wrapper carries effort as
// SB_LANE_REQUESTED_EFFORT env without argv), so the snapshot records
// RequestedEffort and leaves AppliedEffort empty with evidence
// "unobservable:wrapper carries effort as env only; no argv or settings
// pin attests the applied value". The validator refuses an AppliedEffort
// paired with unobservable evidence, so the snapshot cannot accidentally
// claim an applied value the wire never proved.
//
// work_r7a_execution_selection_20260911.
func applyEffortEvidence(snap *runattempt.ExecutionSelectionSnapshot) {
	switch snap.Harness {
	case "claude_code", "claude_minimax", "claude_mini", "claude_zai", "claude_qwen", "claude":
		if snap.RequestedEffort == "" {
			return
		}
		// Today: every claude_* wrapper carries effort as
		// SB_LANE_REQUESTED_EFFORT env. No argv, no settings file
		// pin. Record unobservable honestly so a reader sees the
		// requested value but never a claimed-applied value.
		snap.EffortEvidence = "unobservable:wrapper carries effort as env only (SB_LANE_REQUESTED_EFFORT); no argv or settings pin attests the applied value"
	case "codex":
		// codex adapter does not carry an effort control today;
		// record unsupported.
		snap.EffortEvidence = "unsupported:codex adapter does not expose effort control in v0.1"
		snap.RequestedEffort = ""
	default:
		// os-harness-entrypoints / unknown: no effort control.
		snap.EffortEvidence = "unsupported:harness " + snap.Harness + " has no effort control"
		snap.RequestedEffort = ""
	}
}

// applyNativeSessionEvidence stamps the honest evidence about whether a
// native session id is known. Today the production wire does not yet
// pin --session-id on the Claude adapter argv (the wire would need a
// RunInput field that the runner hands to the adapter, and that path
// is not implemented in v0.1), so the snapshot records
// unsupported:wire does not pin --session-id today. The validator
// refuses a NativeSessionID paired with unsupported evidence, so the
// snapshot cannot accidentally claim a pinned session it never proved.
//
// work_r7a_execution_selection_20260911.
func applyNativeSessionEvidence(snap *runattempt.ExecutionSelectionSnapshot) {
	if snap.NativeSessionEvidence != "" {
		return
	}
	switch snap.Harness {
	case "claude_code", "claude_minimax", "claude_mini", "claude_zai", "claude_qwen", "claude":
		snap.NativeSessionEvidence = "unsupported:wire does not pin --session-id on the argv in v0.1; pass --session-id directly via the wrapper for explicit pinning"
	default:
		snap.NativeSessionEvidence = "unsupported:harness " + snap.Harness + " has no native session concept"
	}
}

// envelopePtr returns *envelope.ExecutionSelection, treating a nil as a
// pointer-to-zero. Used to defensively read selection fields when the
// caller has not pre-checked nil.
func envelopePtr(s *envelope.ExecutionSelection) envelope.ExecutionSelection {
	if s == nil {
		return envelope.ExecutionSelection{}
	}
	return *s
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

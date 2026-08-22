package policy

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/envelope"
)

// The operator autonomy envelope.
//
// D3 says humans own irreversible decisions BY SETTING THE ENVELOPE, not by
// being paged each time. Standing grants were the first half of that; this is
// the half that makes the first half reach shell work.
//
// The failure it repairs: a shell intent carries no file path, so a path-scoped
// standing grant could never match it -- matchGrant missed with "target path
// empty" on every command. What survived was the class baseline, so an ordinary
// `bun install` or `python3 -c` inside a granted project reached the operator as
// a hold. On an interactive session a hold is a prompt; on an unattended lane it
// is a freeze, and a boundary that freezes the work it was meant to govern gets
// removed rather than obeyed. Adding one more verb to the read allowlist buys a
// day; the allowlist is not the shape of the problem.
//
// So the envelope is stated once, as an envelope: at L3 autonomy, inside a
// standing grant's target_path, a hold clears. The three things it does NOT do
// are what keep it a boundary rather than an off switch:
//
//  1. It never touches `deny`. Forbidden paths, invalid source lineage, sink
//     violations, and malicious-classifier verdicts are unaffected.
//  2. It never clears a floor reason outside autonomyEnvelopeClearableFloors --
//     an operator-gated shape, an unreadable command, a suspicious-classifier
//     verdict, or a missing schema/destination stays held.
//  3. It requires a grant that names a target_path, and requires the intent's
//     path to be inside it. No grant, no path, no envelope.
//
// What it deliberately DOES ignore is the grant's class/reversibility/blast
// fields. Requiring a grant to pre-enumerate every mutation class a project's
// build might produce is the same "enumerate the safe shapes" move the read
// allowlist already lost at. The operator authorizes a SCOPE; inside it, the
// class is a description of the work, not a second gate.
const (
	// AutonomyLevelFullProjectScope is the autonomy level at which the envelope
	// is in force. Lower levels leave every hold exactly where it was.
	AutonomyLevelFullProjectScope = "L3"

	// UnknownClassificationFloorReason is the fail-closed floor for a tool or
	// command shape the classifier does not recognize.
	UnknownClassificationFloorReason = "unknown tool/action classification fails closed"
)

// autonomyEnvelopeClearableFloors is the ONLY security-floor reason an envelope
// may clear. It is an allowlist, not a denylist, because a floor added later
// must be non-clearable until someone decides otherwise -- the safe default for
// a new floor is that the operator still sees it.
var autonomyEnvelopeClearableFloors = map[string]bool{
	UnknownClassificationFloorReason: true,
}

// applyAutonomyEnvelope clears a hold that the operator's standing envelope has
// already answered. It only ever lowers `hold`; every other outcome, and every
// non-clearable floor, passes through untouched.
func applyAutonomyEnvelope(pd envelope.PolicyDecision, ai envelope.ActionIntent, cfg Config, now time.Time) envelope.PolicyDecision {
	if pd.Outcome != "hold" {
		return pd
	}
	if !autonomyEnvelopeInForce(cfg) || autonomyEnvelopeBlocked(ai) {
		return pd
	}
	path := strings.TrimSpace(ai.Target.Path)
	if path == "" {
		return pd
	}
	// Restated here rather than trusted from evaluateBase: this function also
	// runs on a decision the security floor has already raised, and a hard
	// safety rule that only holds on one of two paths is not a hard safety rule.
	if _, forbidden := matchForbidden(cfg.ForbiddenPaths, path); forbidden {
		return pd
	}
	grant, ok := autonomyEnvelopeGrant(ai, cfg, now)
	if !ok {
		return pd
	}
	pd.Outcome = "allow_with_obligations"
	pd.Obligations = grantObligations(ai, cfg)
	pd.StandingGrantID = grant.ID
	pd.ExpiresAt = grant.ExpiresAt
	pd.Reasons = append(pd.Reasons, fmt.Sprintf(
		"autonomy envelope %s: hold auto-cleared inside %s:%s; classification=%s/%s",
		AutonomyLevelFullProjectScope, grantID(grant), grant.TargetPath, ai.Action, ai.MutationClass))
	if ref := strings.TrimSpace(cfg.OperatorAutonomyAuthorityRef); ref != "" {
		pd.EvidenceRefs = mergeStrings(pd.EvidenceRefs, ref)
	}
	pd.ID = idFor("pol_", ai.ID, pd.Outcome, "autonomy-envelope", ts(now))
	return pd
}

// autonomyEnvelopeInForce reports whether the operator has actually set an L3
// envelope AND named the authority that says so. An autonomy level with no
// authority ref is a config value nobody signed; it does not widen anything.
func autonomyEnvelopeInForce(cfg Config) bool {
	return strings.EqualFold(strings.TrimSpace(cfg.OperatorAutonomyLevel), AutonomyLevelFullProjectScope) &&
		strings.TrimSpace(cfg.OperatorAutonomyAuthorityRef) != ""
}

// autonomyEnvelopeBlocked reports whether this intent carries a floor the
// envelope must leave alone.
func autonomyEnvelopeBlocked(ai envelope.ActionIntent) bool {
	if strings.TrimSpace(ai.SecurityFloorOutcome) == "deny" {
		return true
	}
	if strings.TrimSpace(ai.Action) == OperatorGatedOperation {
		return true
	}
	for _, reason := range ai.SecurityReasons {
		if !autonomyEnvelopeClearableFloors[strings.TrimSpace(reason)] {
			return true
		}
	}
	return false
}

// autonomyEnvelopeGrant finds the standing grant whose SCOPE covers this intent.
// It matches on what the operator drew -- target_path, and capability or
// destination where the grant narrowed them -- and deliberately not on
// mutation class, reversibility, or blast radius, which are the very fields the
// envelope exists to stop gating on. The money caps stay enforced: a spend
// ceiling is a scope the operator drew, not a class description.
func autonomyEnvelopeGrant(ai envelope.ActionIntent, cfg Config, now time.Time) (StandingGrant, bool) {
	for _, g := range cfg.StandingGrants {
		if strings.TrimSpace(g.TargetPath) == "" {
			continue
		}
		if g.Capability != "" && g.Capability != ai.Capability {
			continue
		}
		if g.MutationClass != "" && g.MutationClass != ai.MutationClass {
			continue
		}
		if g.Destination != "" && strings.TrimSpace(g.Destination) != strings.TrimSpace(ai.Destination) {
			continue
		}
		if g.ExpiresAt != "" {
			if t, err := time.Parse(time.RFC3339, g.ExpiresAt); err == nil && now.After(t) {
				continue
			}
		}
		if ai.EstimatedCost.USD != nil && (g.MaxUSD == nil || *ai.EstimatedCost.USD > *g.MaxUSD) {
			continue
		}
		if ai.EstimatedCost.Tokens != nil && (g.MaxTokens == nil || *ai.EstimatedCost.Tokens > *g.MaxTokens) {
			continue
		}
		if _, ok := matchForbidden([]string{g.TargetPath}, filepath.ToSlash(ai.Target.Path)); !ok {
			continue
		}
		return g, true
	}
	return StandingGrant{}, false
}

// shellTargetPath gives a shell (or PTY) intent the one path it can honestly
// state: the directory it runs in. Non-shell domains keep their own target path
// -- a filesystem intent already names the file it writes, and overwriting that
// with a cwd would widen the very field forbidden-path matching depends on.
func shellTargetPath(classification ActionClassification, cwd string) string {
	if classification.Domain != ActionDomainShell {
		return ""
	}
	if cwd = strings.TrimSpace(cwd); cwd == "" {
		return ""
	}
	return filepath.Clean(cwd)
}

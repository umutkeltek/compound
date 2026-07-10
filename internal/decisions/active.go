package decisions

import (
	"compound/internal/mem"
	"compound/internal/policy"
)

// AllActiveDecisions returns accepted decision claims that are NOT superseded
// by any other current decision record's supersedes qualifier.
//
// This is the ONE read path every grant/freeze consumer uses (director's
// apply-grant resolution, work's freeze admission guard): it reads ONLY
// decision records folded through mem.Store — never markdown, worklog, or
// brain prose (grants-as-gates 2026-07-10, falsifier e). Undo is modeled as
// a superseding record (falsifier c): a NEW accepted record whose
// `supersedes` qualifier names the old decision id flips this projection's
// answer without deleting the old claim — it stays in AllCurrent() history,
// just excluded from this ACTIVE projection.
func AllActiveDecisions(claims []mem.Claim) []mem.Claim {
	superseded := map[string]bool{}
	for _, c := range claims {
		if c.ClaimType != "decision" {
			continue
		}
		lifecycle := LifecycleFromClaim(c)
		if lifecycle.Status != StatusAccepted {
			continue
		}
		for _, id := range lifecycle.Supersedes {
			if id != "" {
				superseded[id] = true
			}
		}
	}
	var out []mem.Claim
	for _, c := range claims {
		if c.ClaimType != "decision" {
			continue
		}
		l := LifecycleFromClaim(c)
		if l.Status != StatusAccepted {
			continue
		}
		if l.ID != "" && superseded[l.ID] {
			continue
		}
		out = append(out, c)
	}
	return out
}

// ActiveDecisions narrows the canonical active projection to one operational
// kind (for example KindGrant or KindFreeze).
func ActiveDecisions(claims []mem.Claim, kind string) []mem.Claim {
	var out []mem.Claim
	for _, c := range AllActiveDecisions(claims) {
		if LifecycleFromClaim(c).Kind == kind {
			out = append(out, c)
		}
	}
	return out
}

// AuthoritativeActiveDecisions is the stricter projection for decisions that
// can release autonomous work. It admits only claims minted by the dedicated
// policy-decision import door and lets only equally authoritative claims
// supersede them. Generic recalled meaning remains evidence even if it copies
// every visible lifecycle qualifier.
func AuthoritativeActiveDecisions(claims []mem.Claim, kind string) []mem.Claim {
	var authoritative []mem.Claim
	for _, c := range claims {
		if c.ClaimType == "decision" && mem.HasProtectedOperationalDecisionAuthority(c) && policy.HasOperatorProvenance(c.Qualifiers["reviewed_by"]) {
			authoritative = append(authoritative, c)
		}
	}
	var out []mem.Claim
	for _, c := range AllActiveDecisions(authoritative) {
		if LifecycleFromClaim(c).Kind == kind {
			out = append(out, c)
		}
	}
	return out
}

package decisions

import "compound/internal/mem"

// ActiveDecisions returns current decision claims of the given kind (e.g.
// KindGrant, KindFreeze) whose lifecycle status is accepted and that are NOT
// superseded by any other current decision record's supersedes qualifier.
//
// This is the ONE read path every grant/freeze consumer uses (director's
// apply-grant resolution, work's freeze admission guard): it reads ONLY
// decision records folded through mem.Store — never markdown, worklog, or
// brain prose (grants-as-gates 2026-07-10, falsifier e). Undo is modeled as
// a superseding record (falsifier c): a NEW accepted record whose
// `supersedes` qualifier names the old decision id flips ActiveDecisions'
// answer without deleting the old claim — it stays in AllCurrent() history,
// just excluded from this ACTIVE projection.
func ActiveDecisions(claims []mem.Claim, kind string) []mem.Claim {
	superseded := map[string]bool{}
	for _, c := range claims {
		if c.ClaimType != "decision" {
			continue
		}
		for _, id := range LifecycleFromClaim(c).Supersedes {
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
		if l.Kind != kind || l.Status != StatusAccepted {
			continue
		}
		if l.ID != "" && superseded[l.ID] {
			continue
		}
		out = append(out, c)
	}
	return out
}

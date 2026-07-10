package director

// Director-apply grant consumption (grants-as-gates, 2026-07-10): director
// --apply now requires an active operator grant decision record (kind=grant,
// status=accepted, scope=director-apply, reviewed_by present) before it
// files anything. This is W9-J6's first increment through the pre-built door
// in internal/work/auto_admit.go: AutoAdmitInput.GrantRef is the designed
// slot for exactly this evidence. The kill-switch inside AdmitAuto still
// wins — resolving a grant here only proves eligibility; it never bypasses
// the halt.
//
// The read path is decisions.ActiveDecisions over mem.Store claims ONLY —
// never markdown, worklog, or brain prose (falsifier e): a worklog line or a
// brain paragraph ratifying a grant binds nothing here.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"compound/internal/decisions"
	"compound/internal/mem"
)

// directorApplyGrantScope is the fixed scope director --apply resolves.
const directorApplyGrantScope = "director-apply"

// resolvedApplyGrant is the ACTIVE director-apply grant Propose(apply=true)
// resolves before filing anything.
type resolvedApplyGrant struct {
	DecisionID string
	ClaimID    string
	ReviewedBy string
	GrantRef   string
}

// resolveApplyGrant reads ONLY decision records (mem claims) for an active
// grant scoped to director-apply. No matching record => a named refusal;
// more than one => the most recently promoted wins (deterministic by claim
// seq), matching decisions' own "latest edit wins" semantics elsewhere.
func resolveApplyGrant(homePath string, now time.Time) (resolvedApplyGrant, error) {
	store, err := mem.Open(homePath)
	if err != nil {
		return resolvedApplyGrant{}, err
	}
	claims, err := store.AllCurrent()
	if err != nil {
		return resolvedApplyGrant{}, err
	}
	active := decisions.ActiveDecisions(claims, decisions.KindGrant)
	var candidates []mem.Claim
	for _, c := range active {
		l := decisions.LifecycleFromClaim(c)
		if l.Scope != directorApplyGrantScope {
			continue
		}
		if strings.TrimSpace(l.ReviewedBy) == "" {
			continue
		}
		candidates = append(candidates, c)
	}
	if len(candidates) == 0 {
		return resolvedApplyGrant{}, fmt.Errorf(
			"director --apply refused: no active grant record for scope %q "+
				"(record one: `compound decisions record --kind grant --scope %s --reviewed-by <operator>`)",
			directorApplyGrantScope, directorApplyGrantScope)
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Seq < candidates[j].Seq })
	latest := candidates[len(candidates)-1]
	l := decisions.LifecycleFromClaim(latest)
	return resolvedApplyGrant{
		DecisionID: l.ID,
		ClaimID:    latest.ID,
		ReviewedBy: l.ReviewedBy,
		GrantRef:   "grant:" + l.ID,
	}, nil
}

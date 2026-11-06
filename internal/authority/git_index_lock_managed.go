package authority

import (
	"context"
	"time"

	"compound/internal/authority/gitrepository"
)

const GitIndexLockAgentManaged = "agent_managed"

type ManagedGitIndexLockResult struct {
	PlanID       string             `json:"plan_id,omitempty"`
	ReviewID     string             `json:"review_id,omitempty"`
	Coordination string             `json:"coordination"`
	Result       GitIndexLockResult `json:"result"`
}

// RecoverStaleGitIndexLock owns preparation, machine-labelled review and apply.
// It does not assert that all writers are paused. It retains the exact stale,
// empty-lock checks, fresh policy/liveness checks, private preimages and receipt
// path. Native recoveries serialize at the primary Git directory. An unmanaged
// process replacing the source name in the final rename gap remains a bounded,
// explicitly accepted limitation of this coordination mode.
func RecoverStaleGitIndexLock(ctx context.Context, home, repo, workID, actor string, idle gitrepository.IndexLockLiveness, clock func() time.Time) (ManagedGitIndexLockResult, error) {
	result := ManagedGitIndexLockResult{Coordination: GitIndexLockAgentManaged}
	if clock == nil {
		clock = time.Now
	}
	plan, err := BuildGitIndexLockPlan(ctx, repo, workID, actor, idle, clock())
	if err != nil {
		return result, err
	}
	result.PlanID = plan.ID
	if err := RecordGitIndexLockPlan(home, plan, clock()); err != nil {
		return result, err
	}
	now := clock()
	review, err := ReviewGitIndexLockPlan(home, plan.ID, GitIndexLockWindow{
		Coordination: GitIndexLockAgentManaged,
		ReviewedBy:   actor,
		EvidenceRef:  agentIndexLockEvidence(plan),
		ExpiresAt:    now.Add(2 * time.Minute),
	}, now)
	if err != nil {
		return result, err
	}
	result.ReviewID = review.ID
	result.Result, err = ApplyGitIndexLockRecovery(ctx, home, plan.ID, review.ID, actor, idle, clock)
	return result, err
}

func agentIndexLockEvidence(plan GitIndexLockPlan) string {
	return "git-index-lock-observation:" + plan.ID + ":" + plan.Digest
}

package work

import (
	"compound/internal/envelope"
	"compound/internal/policy"
	"fmt"
	"strings"
	"time"

	"compound/internal/landlease"
)

// LandingResourceKey resolves the repository/ref from the durable delivery
// contract and revalidates it against the bound checkout. Callers cannot choose
// a fingerprint or substitute their current working directory.
func (s *Store) LandingResourceKey(workID string) (landlease.ResourceKey, error) {
	w, err := s.Get(workID)
	if err != nil {
		return landlease.ResourceKey{}, err
	}
	if w.GitDelivery == nil {
		return landlease.ResourceKey{}, fmt.Errorf("work %s has no git_delivery contract", workID)
	}
	if err := s.validateBoundGitDelivery(w); err != nil {
		return landlease.ResourceKey{}, err
	}
	return landlease.LandingResourceKey(w.GitDelivery.RepositoryFingerprint, w.GitDelivery.TargetLocalRef)
}

type LandingResourceMutation struct {
	Actor, Holder, Operation, Reason string
	TTL                              time.Duration
}
type LandingResourceMutationResult struct {
	Lease   landlease.Lease
	Release landlease.ReleaseResult
}

// MutateLandingResource binds the advisory mutation to current work authority.
// Waiting happens outside this method so a waiter never holds work admission.
func (s *Store) MutateLandingResource(workID string, in LandingResourceMutation, now time.Time) (LandingResourceMutationResult, error) {
	var result LandingResourceMutationResult
	err := s.withLifecycleMutation(func() error {
		if in.Operation != "acquire" && in.Operation != "release" && in.Operation != "steal" {
			return fmt.Errorf("unsupported landing lease operation %q", in.Operation)
		}
		if !holderNamesWork(in.Holder, workID) || strings.TrimSpace(in.Holder) == "" || (in.Operation == "steal" && strings.TrimSpace(in.Reason) == "") {
			return fmt.Errorf("holder must name exact work_id; steal also requires an explicit reason")
		}
		w, err := s.resourceMutationOwner(workID, in.Actor)
		if err != nil {
			return err
		}
		key, err := s.LandingResourceKey(workID)
		if err != nil {
			return err
		}
		resource, err := landlease.OpenResource(s.home, key)
		if err != nil {
			return err
		}
		action := s.resourceMutationAction(w, in.Actor, "landing."+in.Operation,
			fmt.Sprintf("repository=%s ref=%s holder=%q ttl=%s reason=%q", key.RepositoryFingerprint, key.TargetRef, in.Holder, in.TTL, in.Reason), w.GitDelivery.TargetWorktreeRoot, now)
		if in.Operation == "steal" {
			action.MutationClass = "security"
		}
		if err := s.authorizeResourceMutationLocked(w, action, now); err != nil {
			return err
		}
		switch in.Operation {
		case "acquire":
			result.Lease, err = resource.Acquire(in.Holder, in.TTL, now)
		case "release":
			result.Release, err = resource.Release(in.Holder, now)
		case "steal":
			result.Lease, err = resource.Steal(in.Holder, in.Reason, now)
		}
		return err
	})
	return result, err
}

func (s *Store) resourceMutationOwner(workID, actor string) (envelope.WorkEnvelope, error) {
	w, err := s.recoverPendingClaimBeforeMutation(workID)
	if err != nil {
		return w, err
	}
	if w.State != "claimed" && w.State != "running" && w.State != "verifying" {
		return w, fmt.Errorf("resource mutation requires claimed, running, or verifying work")
	}
	claim, err := s.readLease(workID)
	if err != nil {
		return w, err
	}
	if strings.TrimSpace(actor) == "" || claim.By != actor {
		return w, fmt.Errorf("actor %q does not hold work %s", actor, workID)
	}
	return w, w.Validate()
}

func (s *Store) resourceMutationAction(w envelope.WorkEnvelope, actor, operation, preview, path string, now time.Time) envelope.ActionIntent {
	return envelope.ActionIntent{
		Header: newHeader(envelope.SVActionIntent, idFor("act_", w.ID, actor, operation, preview, path, ts(now)), now, w.ID, w.IDs.ContextPackID, w.Privacy, w.Risk),
		Actor:  actor, Capability: "coordination.resources", Action: operation,
		Target:        envelope.ActionTarget{Kind: "work", ID: w.ID, Path: path},
		MutationClass: "local_reversible", Reversibility: "manual", BlastRadius: "local",
		Preview: preview, RollbackPlan: "retain coordination records; release the exact owned lease or reconcile the cutover under writer quiescence",
	}
}

func (s *Store) authorizeResourceMutationLocked(w envelope.WorkEnvelope, action envelope.ActionIntent, now time.Time) error {
	if err := action.Validate(); err != nil {
		return err
	}
	cfg, _, err := policy.LoadConfig(s.home)
	if err != nil {
		return err
	}
	for _, class := range []string{"build-implement", action.Capability} {
		halted, reason, err := policy.AutonomyHalted(s.home, class)
		if err != nil {
			return err
		}
		if halted {
			return fmt.Errorf("resource mutation halted: %s", reason)
		}
	}
	if err := s.writeEventJSON(action.ID, action); err != nil {
		return err
	}
	decision := policy.Evaluate(action, cfg, now)
	if decision.Outcome == "hold" {
		decision, _, err = policy.NewExactActionApprovalStore(s.home).ApplyMatchingLocked(action, cfg, decision, now)
		if err != nil {
			return err
		}
	}
	if !policy.Allowed(decision) {
		return fmt.Errorf("resource mutation action %s policy %s", action.ID, decision.Outcome)
	}
	_, err = s.recordActionLocked(w.ID, action, decision, now)
	return err
}

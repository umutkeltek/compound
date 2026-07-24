package work

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/home"
	"compound/internal/policy"
)

const (
	CollaborationSchemaVersion = "compound.work_collaboration_event.v0.1"
	CollaborationKindObserved  = "work.collaboration.observed"
)

// CollaborationInput is metadata for one content-free multiplayer observation.
// InlineBody exists only as a fail-closed API guard: callers must preserve the
// body through a governed artifact door and pass BodyRef instead.
type CollaborationInput struct {
	Actor           string
	IdempotencyKey  string
	SessionID       string
	RunAttemptID    string
	TargetActor     string
	CausalParentIDs []string
	BodyRef         string
	Visibility      string
	InlineBody      string
}

// CollaborationEvent is evidence attached to one WorkEnvelope lifecycle. It
// intentionally has no message, prompt, transcript, or inline body field.
type CollaborationEvent struct {
	SchemaVersion    string   `json:"schema_version"`
	ID               string   `json:"id"`
	Kind             string   `json:"kind"`
	CreatedAt        string   `json:"created_at"`
	WorkID           string   `json:"work_id"`
	Actor            string   `json:"actor"`
	IdempotencyKey   string   `json:"idempotency_key"`
	SessionID        string   `json:"session_id,omitempty"`
	RunAttemptID     string   `json:"run_attempt_id,omitempty"`
	TargetActor      string   `json:"target_actor,omitempty"`
	CausalParentIDs  []string `json:"causal_parent_ids,omitempty"`
	BodyRef          string   `json:"body_ref"`
	Visibility       string   `json:"visibility"`
	Privacy          string   `json:"privacy"`
	ActionIntentID   string   `json:"action_intent_id"`
	PolicyDecisionID string   `json:"policy_decision_id"`
}

type CollaborationResult struct {
	SchemaVersion string                `json:"schema_version"`
	Created       bool                  `json:"created"`
	Event         CollaborationEvent    `json:"event"`
	Work          envelope.WorkEnvelope `json:"work"`
}

var appendCollaborationLifecycle = func(s *Store, event Event) error {
	return s.appendEventControlled(event, nil, nil)
}

// RecordCollaboration records one governed, work-scoped, content-free
// collaboration observation. The idempotency key is scoped to the work; a
// retry recovers and finishes any missing lifecycle attachment.
func (s *Store) RecordCollaboration(workID string, in CollaborationInput, now time.Time) (CollaborationResult, error) {
	var result CollaborationResult
	err := s.withLifecycleMutation(func() error {
		var err error
		result, err = s.recordCollaborationLocked(workID, in, now)
		return err
	})
	return result, err
}

func (s *Store) recordCollaborationLocked(workID string, in CollaborationInput, now time.Time) (CollaborationResult, error) {
	workID = strings.TrimSpace(workID)
	if workID == "" {
		return CollaborationResult{}, fmt.Errorf("collaboration work_id is required")
	}
	in = normalizeCollaborationInput(in)
	if err := validateCollaborationInput(in); err != nil {
		return CollaborationResult{}, err
	}

	w, err := s.recoverPendingClaimBeforeMutation(workID)
	if err != nil {
		return CollaborationResult{}, err
	}
	if terminalStates[w.State] {
		return CollaborationResult{}, fmt.Errorf("work %s %s (terminal); cannot record collaboration", workID, w.State)
	}
	lease, err := s.readLease(workID)
	if err != nil {
		return CollaborationResult{}, fmt.Errorf("collaboration requires a claimed work lease: %w", err)
	}
	if strings.TrimSpace(lease.By) != in.Actor {
		return CollaborationResult{}, fmt.Errorf("cannot record collaboration for %s: lease held by %q, actor is %q", workID, lease.By, in.Actor)
	}
	artifactStore, err := home.NewArtifactStore(s.home)
	if err != nil {
		return CollaborationResult{}, fmt.Errorf("collaboration body_ref store: %w", err)
	}
	if _, err := artifactStore.Verify(in.BodyRef); err != nil {
		return CollaborationResult{}, fmt.Errorf("collaboration body_ref %s does not resolve to verified Compound custody: %w", in.BodyRef, err)
	}
	if in.RunAttemptID != "" {
		attempt, err := s.runAttempts().Get(in.RunAttemptID)
		if err != nil {
			return CollaborationResult{}, fmt.Errorf("collaboration run_attempt_id %s: %w", in.RunAttemptID, err)
		}
		if attempt.WorkID != workID {
			return CollaborationResult{}, fmt.Errorf("collaboration run attempt %s belongs to work %s, not %s", in.RunAttemptID, attempt.WorkID, workID)
		}
	}

	eventID := collaborationEventID(workID, in.IdempotencyKey)
	if err := s.validateCollaborationCausalParents(workID, eventID, in.CausalParentIDs); err != nil {
		return CollaborationResult{}, err
	}
	semanticDigest := collaborationSemanticDigest(in)
	actionID := collaborationActionID(eventID)
	action, err := s.loadOrBuildCollaborationAction(actionID, eventID, semanticDigest, w, in, now)
	if err != nil {
		return CollaborationResult{}, err
	}
	decisionID := collaborationPolicyID(eventID)
	decision, err := s.loadOrBuildCollaborationDecision(decisionID, action, now)
	if err != nil {
		return CollaborationResult{}, err
	}
	if !policy.Allowed(decision) {
		return CollaborationResult{}, fmt.Errorf("cannot record collaboration %s: policy %s", eventID, decision.Outcome)
	}

	if err := s.writeEventJSON(action.ID, &action); err != nil {
		return CollaborationResult{}, err
	}
	if err := s.writeEventJSON(decision.ID, &decision); err != nil {
		return CollaborationResult{}, err
	}

	collaboration := CollaborationEvent{
		SchemaVersion:    CollaborationSchemaVersion,
		ID:               eventID,
		Kind:             CollaborationKindObserved,
		CreatedAt:        action.CreatedAt,
		WorkID:           workID,
		Actor:            in.Actor,
		IdempotencyKey:   in.IdempotencyKey,
		SessionID:        in.SessionID,
		RunAttemptID:     in.RunAttemptID,
		TargetActor:      in.TargetActor,
		CausalParentIDs:  append([]string{}, in.CausalParentIDs...),
		BodyRef:          in.BodyRef,
		Visibility:       in.Visibility,
		Privacy:          w.Privacy,
		ActionIntentID:   action.ID,
		PolicyDecisionID: decision.ID,
	}
	if err := collaboration.Validate(); err != nil {
		return CollaborationResult{}, err
	}

	var stored CollaborationEvent
	switch err := s.readEventJSON(eventID, &stored); {
	case err == nil:
		if !collaborationEventsEquivalent(stored, collaboration) {
			return CollaborationResult{}, fmt.Errorf("collaboration idempotency key %q conflicts with existing event %s", in.IdempotencyKey, eventID)
		}
	case errors.Is(err, os.ErrNotExist):
		if err := s.writeEventJSON(eventID, &collaboration); err != nil {
			return CollaborationResult{}, err
		}
	default:
		return CollaborationResult{}, err
	}

	lifecycle, err := s.eventsFor(workID)
	if err != nil {
		return CollaborationResult{}, err
	}
	attachments := countAttached(lifecycle, eventID)
	if attachments > 1 {
		return CollaborationResult{}, fmt.Errorf("collaboration event %s is attached %d times", eventID, attachments)
	}
	if attachments == 1 {
		return CollaborationResult{
			SchemaVersion: CollaborationSchemaVersion,
			Created:       false,
			Event:         collaboration,
			Work:          w,
		}, nil
	}

	if !oneOf(action.ID, w.ActionIntentIDs) {
		w.ActionIntentIDs = append(w.ActionIntentIDs, action.ID)
		w.PolicyDecisionID = decision.ID
		if err := s.writeJSON(s.workPath(workID), &w); err != nil {
			return CollaborationResult{}, err
		}
	}
	if err := appendCollaborationLifecycle(s, Event{
		TS:       ts(now),
		WorkID:   workID,
		From:     w.State,
		To:       w.State,
		By:       in.Actor,
		Note:     "collaboration observation recorded",
		Attached: []string{action.ID, decision.ID, collaboration.ID},
	}); err != nil {
		return CollaborationResult{}, err
	}
	return CollaborationResult{
		SchemaVersion: CollaborationSchemaVersion,
		Created:       true,
		Event:         collaboration,
		Work:          w,
	}, nil
}

func normalizeCollaborationInput(in CollaborationInput) CollaborationInput {
	in.Actor = strings.TrimSpace(in.Actor)
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	in.SessionID = strings.TrimSpace(in.SessionID)
	in.RunAttemptID = strings.TrimSpace(in.RunAttemptID)
	in.TargetActor = strings.TrimSpace(in.TargetActor)
	in.BodyRef = strings.TrimSpace(in.BodyRef)
	in.Visibility = strings.TrimSpace(in.Visibility)
	if in.Visibility == "" {
		in.Visibility = "work"
	}
	for i := range in.CausalParentIDs {
		in.CausalParentIDs[i] = strings.TrimSpace(in.CausalParentIDs[i])
	}
	return in
}

func validateCollaborationInput(in CollaborationInput) error {
	if !validCollaborationToken(in.Actor) {
		return fmt.Errorf("collaboration actor must be a bounded identifier")
	}
	if !validCollaborationToken(in.IdempotencyKey) {
		return fmt.Errorf("collaboration idempotency_key must be a bounded identifier")
	}
	if strings.TrimSpace(in.InlineBody) != "" {
		return fmt.Errorf("inline collaboration content is forbidden; preserve it and pass --body-ref")
	}
	if in.SessionID != "" && !validCollaborationToken(in.SessionID) {
		return fmt.Errorf("collaboration session_id must be a bounded identifier")
	}
	if in.TargetActor != "" && !validCollaborationToken(in.TargetActor) {
		return fmt.Errorf("collaboration target_actor must be a bounded identifier")
	}
	if !validCollaborationBodyRef(in.BodyRef) {
		return fmt.Errorf("collaboration body_ref must be artifact:sha256:<64 lowercase hex>")
	}
	if in.Visibility != "work" {
		return fmt.Errorf("collaboration visibility must be %q in v1", "work")
	}
	return nil
}

func (e CollaborationEvent) Validate() error {
	if e.SchemaVersion != CollaborationSchemaVersion {
		return fmt.Errorf("collaboration schema_version = %q, want %q", e.SchemaVersion, CollaborationSchemaVersion)
	}
	if !strings.HasPrefix(e.ID, "wco_") {
		return fmt.Errorf("collaboration id %q must start with wco_", e.ID)
	}
	if e.Kind != CollaborationKindObserved {
		return fmt.Errorf("collaboration kind = %q, want %q", e.Kind, CollaborationKindObserved)
	}
	if _, err := time.Parse(time.RFC3339, e.CreatedAt); err != nil {
		return fmt.Errorf("collaboration created_at invalid: %w", err)
	}
	if strings.TrimSpace(e.WorkID) == "" || !validCollaborationToken(e.Actor) || !validCollaborationToken(e.IdempotencyKey) {
		return fmt.Errorf("collaboration work_id, actor, and idempotency_key must be bounded identifiers")
	}
	if e.SessionID != "" && !validCollaborationToken(e.SessionID) {
		return fmt.Errorf("collaboration session_id must be a bounded identifier")
	}
	if e.TargetActor != "" && !validCollaborationToken(e.TargetActor) {
		return fmt.Errorf("collaboration target_actor must be a bounded identifier")
	}
	if !validCollaborationBodyRef(e.BodyRef) {
		return fmt.Errorf("collaboration body_ref must be artifact:sha256:<64 lowercase hex>")
	}
	if e.Visibility != "work" {
		return fmt.Errorf("collaboration visibility must be %q", "work")
	}
	if strings.TrimSpace(e.Privacy) == "" {
		return fmt.Errorf("collaboration privacy is required")
	}
	if !strings.HasPrefix(e.ActionIntentID, "act_") || !strings.HasPrefix(e.PolicyDecisionID, "pol_") {
		return fmt.Errorf("collaboration action_intent_id and policy_decision_id are required")
	}
	return nil
}

func validCollaborationBodyRef(ref string) bool {
	const prefix = "artifact:sha256:"
	if !strings.HasPrefix(ref, prefix) {
		return false
	}
	digest := strings.TrimPrefix(ref, prefix)
	if len(digest) != 64 {
		return false
	}
	for _, r := range digest {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func validCollaborationToken(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for i, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			continue
		}
		if i > 0 && strings.ContainsRune("._:/@-", r) {
			continue
		}
		return false
	}
	return true
}

func collaborationEventID(workID, key string) string {
	sum := sha256.Sum256([]byte(workID + "\x00" + key))
	return "wco_" + hex.EncodeToString(sum[:])[:16]
}

func collaborationActionID(eventID string) string {
	sum := sha256.Sum256([]byte("work.collaboration.record\x00" + eventID))
	return "act_" + hex.EncodeToString(sum[:])[:16]
}

func collaborationPolicyID(eventID string) string {
	sum := sha256.Sum256([]byte("work.collaboration.policy\x00" + eventID))
	return "pol_" + hex.EncodeToString(sum[:])[:16]
}

func collaborationSemanticDigest(in CollaborationInput) string {
	body, _ := json.Marshal(struct {
		SessionID       string   `json:"session_id,omitempty"`
		RunAttemptID    string   `json:"run_attempt_id,omitempty"`
		TargetActor     string   `json:"target_actor,omitempty"`
		CausalParentIDs []string `json:"causal_parent_ids,omitempty"`
		BodyRef         string   `json:"body_ref"`
		Visibility      string   `json:"visibility"`
	}{
		SessionID:       in.SessionID,
		RunAttemptID:    in.RunAttemptID,
		TargetActor:     in.TargetActor,
		CausalParentIDs: in.CausalParentIDs,
		BodyRef:         in.BodyRef,
		Visibility:      in.Visibility,
	})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func collaborationEventsEquivalent(a, b CollaborationEvent) bool {
	left, leftErr := json.Marshal(a)
	right, rightErr := json.Marshal(b)
	return leftErr == nil && rightErr == nil && string(left) == string(right)
}

func (s *Store) validateCollaborationCausalParents(workID, eventID string, parentIDs []string) error {
	if len(parentIDs) == 0 {
		return nil
	}
	lifecycle, err := s.eventsFor(workID)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(parentIDs))
	for _, parentID := range parentIDs {
		if !strings.HasPrefix(parentID, "wco_") {
			return fmt.Errorf("collaboration causal parent %q must start with wco_", parentID)
		}
		if parentID == eventID {
			return fmt.Errorf("collaboration causal parent %s cannot reference itself", parentID)
		}
		if seen[parentID] {
			return fmt.Errorf("collaboration causal parent %s is duplicated", parentID)
		}
		seen[parentID] = true
		if countAttached(lifecycle, parentID) != 1 {
			return fmt.Errorf("collaboration causal parent %s is not attached exactly once to work %s", parentID, workID)
		}
		var parent CollaborationEvent
		if err := s.readEventJSON(parentID, &parent); err != nil {
			return fmt.Errorf("collaboration causal parent %s: %w", parentID, err)
		}
		if err := parent.Validate(); err != nil {
			return fmt.Errorf("collaboration causal parent %s invalid: %w", parentID, err)
		}
		if parent.WorkID != workID {
			return fmt.Errorf("collaboration causal parent %s belongs to work %s, not %s", parentID, parent.WorkID, workID)
		}
	}
	return nil
}

func (s *Store) loadOrBuildCollaborationAction(id, eventID, semanticDigest string, w envelope.WorkEnvelope, in CollaborationInput, now time.Time) (envelope.ActionIntent, error) {
	expected := func(createdAt time.Time) envelope.ActionIntent {
		return envelope.ActionIntent{
			Header:        newHeader(envelope.SVActionIntent, id, createdAt, w.ID, w.IDs.ContextPackID, w.Privacy, w.Risk),
			Actor:         in.Actor,
			Capability:    "work.collaboration.record",
			Action:        "record_collaboration_observation",
			Target:        envelope.ActionTarget{Kind: "work_collaboration", ID: eventID, Path: in.BodyRef},
			MutationClass: "local_reversible",
			Reversibility: "manual",
			BlastRadius:   "local",
			Preview:       "record content-free collaboration metadata sha256:" + semanticDigest,
			RollbackPlan:  "preserve the append-only evidence and supersede or retire it through the work lifecycle",
		}
	}

	var stored envelope.ActionIntent
	err := s.readEventJSON(id, &stored)
	if err == nil {
		createdAt, parseErr := time.Parse(time.RFC3339, stored.CreatedAt)
		if parseErr != nil {
			return envelope.ActionIntent{}, fmt.Errorf("stored collaboration action %s created_at: %w", id, parseErr)
		}
		want := expected(createdAt)
		if !reflect.DeepEqual(stored, want) {
			return envelope.ActionIntent{}, fmt.Errorf("collaboration action %s conflicts with idempotency key", id)
		}
		return stored, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return envelope.ActionIntent{}, err
	}
	action := expected(now)
	if err := action.Validate(); err != nil {
		return envelope.ActionIntent{}, err
	}
	return action, nil
}

func (s *Store) loadOrBuildCollaborationDecision(id string, action envelope.ActionIntent, now time.Time) (envelope.PolicyDecision, error) {
	var stored envelope.PolicyDecision
	err := s.readEventJSON(id, &stored)
	if err == nil {
		if err := stored.Validate(); err != nil {
			return envelope.PolicyDecision{}, err
		}
		if stored.ActionIntentID != action.ID {
			return envelope.PolicyDecision{}, fmt.Errorf("collaboration policy %s is for action %s, want %s", id, stored.ActionIntentID, action.ID)
		}
		return stored, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return envelope.PolicyDecision{}, err
	}
	cfg, _, err := policy.LoadConfig(s.home)
	if err != nil {
		return envelope.PolicyDecision{}, err
	}
	decision := policy.Evaluate(action, cfg, now)
	decision.ID = id
	if err := decision.Validate(); err != nil {
		return envelope.PolicyDecision{}, err
	}
	return decision, nil
}

func countAttached(events []Event, id string) int {
	count := 0
	for _, event := range events {
		for _, attached := range event.Attached {
			if attached == id {
				count++
			}
		}
	}
	return count
}

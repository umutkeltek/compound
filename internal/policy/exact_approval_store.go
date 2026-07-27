package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"compound/internal/envelope"
	"compound/internal/eventstore"
)

const (
	ExactActionApprovalPlanSchema = "compound.exact_action_approval_plan.v0.1"

	ExactApprovalStatusReady    = "ready"
	ExactApprovalStatusConsumed = "consumed"
	ExactApprovalStatusExpired  = "expired"
)

type ExactActionApprovalPlan struct {
	SchemaVersion  string        `json:"schema_version"`
	ID             string        `json:"id"`
	ActionIntentID string        `json:"action_intent_id"`
	Binding        ActionBinding `json:"binding"`
	BindingDigest  string        `json:"binding_digest"`
	CreatedAt      string        `json:"created_at"`
	ExpiresAt      string        `json:"expires_at"`
}

type ExactActionApprovalStatus struct {
	SchemaVersion string `json:"schema_version"`
	ID            string `json:"id"`
	Status        string `json:"status"`
	BindingDigest string `json:"binding_digest,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	ConsumedAt    string `json:"consumed_at,omitempty"`
}

type ExactActionApprovalStore struct {
	homePath string
}

func NewExactActionApprovalStore(homePath string) *ExactActionApprovalStore {
	return &ExactActionApprovalStore{homePath: strings.TrimSpace(homePath)}
}

func (s *ExactActionApprovalStore) Plan(
	actionIntentID string,
	cfg Config,
	ttl time.Duration,
	now time.Time,
) (ExactActionApprovalPlan, string, error) {
	if s == nil || s.homePath == "" {
		return ExactActionApprovalPlan{}, "", fmt.Errorf("exact action approval store requires compound home")
	}
	if ttl <= 0 || ttl > 15*time.Minute {
		return ExactActionApprovalPlan{}, "", fmt.Errorf("exact approval ttl must be greater than zero and at most 15m")
	}
	store, err := eventstore.Open(s.homePath)
	if err != nil {
		return ExactActionApprovalPlan{}, "", err
	}
	record, err := store.Read(strings.TrimSpace(actionIntentID))
	if err != nil {
		return ExactActionApprovalPlan{}, "", err
	}
	var intent envelope.ActionIntent
	if err := json.Unmarshal(record.Bytes, &intent); err != nil {
		return ExactActionApprovalPlan{}, "", fmt.Errorf("parse action intent: %w", err)
	}
	if err := intent.Validate(); err != nil {
		return ExactActionApprovalPlan{}, "", err
	}
	if strings.TrimSpace(intent.SecurityFloorOutcome) != "" {
		return ExactActionApprovalPlan{}, "", fmt.Errorf("action security floor is not approvable")
	}
	binding := ActionBindingForIntent(intent, cfg)
	digest := ActionBindingDigest(binding)
	expiresAt := now.UTC().Add(ttl)
	plan := ExactActionApprovalPlan{
		SchemaVersion:  ExactActionApprovalPlanSchema,
		ID:             "approval_plan_" + shortActionHash(intent.ID, digest, expiresAt.Format(time.RFC3339Nano)),
		ActionIntentID: intent.ID,
		Binding:        binding,
		BindingDigest:  digest,
		CreatedAt:      now.UTC().Format(time.RFC3339Nano),
		ExpiresAt:      expiresAt.Format(time.RFC3339Nano),
	}
	result, err := store.PutJSON(plan.ID, plan, "  ")
	if err != nil {
		return ExactActionApprovalPlan{}, "", err
	}
	return plan, result.Reference.String(), nil
}

func (s *ExactActionApprovalStore) Approve(
	planID, grantedBy string,
	cfg Config,
	now time.Time,
) (ExactActionApproval, string, error) {
	if s == nil || s.homePath == "" {
		return ExactActionApproval{}, "", fmt.Errorf("exact action approval store requires compound home")
	}
	store, err := eventstore.Open(s.homePath)
	if err != nil {
		return ExactActionApproval{}, "", err
	}
	record, err := store.Read(strings.TrimSpace(planID))
	if err != nil {
		return ExactActionApproval{}, "", err
	}
	var plan ExactActionApprovalPlan
	if err := json.Unmarshal(record.Bytes, &plan); err != nil {
		return ExactActionApproval{}, "", fmt.Errorf("parse exact approval plan: %w", err)
	}
	if plan.SchemaVersion != ExactActionApprovalPlanSchema ||
		plan.ID != strings.TrimSpace(planID) ||
		ActionBindingDigest(plan.Binding) != plan.BindingDigest {
		return ExactActionApproval{}, "", fmt.Errorf("invalid exact approval plan")
	}
	expiry, err := time.Parse(time.RFC3339Nano, plan.ExpiresAt)
	if err != nil || !now.Before(expiry) {
		return ExactActionApproval{}, "", fmt.Errorf("exact approval plan expired")
	}
	actionRecord, err := store.Read(plan.ActionIntentID)
	if err != nil {
		return ExactActionApproval{}, "", err
	}
	var intent envelope.ActionIntent
	if err := json.Unmarshal(actionRecord.Bytes, &intent); err != nil {
		return ExactActionApproval{}, "", fmt.Errorf("parse planned action intent: %w", err)
	}
	currentBinding := ActionBindingForIntent(intent, cfg)
	if ActionBindingDigest(currentBinding) != plan.BindingDigest {
		return ExactActionApproval{}, "", fmt.Errorf("exact approval plan binding changed")
	}
	approval, err := NewExactActionApproval(
		"approval_grant_"+shortActionHash(plan.ID, plan.BindingDigest, grantedBy),
		grantedBy,
		currentBinding,
		expiry,
	)
	if err != nil {
		return ExactActionApproval{}, "", err
	}
	approval.EvidenceRefs = mergeStrings(approval.EvidenceRefs, record.Reference.String(), actionRecord.Reference.String())
	ref, err := s.Save(approval)
	return approval, ref, err
}

func (s *ExactActionApprovalStore) Check(id string, now time.Time) (ExactActionApprovalStatus, error) {
	if s == nil || s.homePath == "" {
		return ExactActionApprovalStatus{}, fmt.Errorf("exact action approval store requires compound home")
	}
	store, err := eventstore.Open(s.homePath)
	if err != nil {
		return ExactActionApprovalStatus{}, err
	}
	record, err := store.Read(strings.TrimSpace(id))
	if err != nil {
		return ExactActionApprovalStatus{}, err
	}
	var approval ExactActionApproval
	if err := json.Unmarshal(record.Bytes, &approval); err != nil ||
		approval.SchemaVersion != ExactActionApprovalSchema {
		return ExactActionApprovalStatus{}, fmt.Errorf("%s is not an exact action approval", id)
	}
	status := ExactActionApprovalStatus{
		SchemaVersion: "compound.exact_action_approval_status.v0.1",
		ID:            approval.ID,
		Status:        ExactApprovalStatusReady,
		BindingDigest: approval.BindingDigest,
		ExpiresAt:     approval.ExpiresAt,
	}
	consumptionID := "approval_use_" + shortActionHash(approval.ID)
	if consumed, err := store.Read(consumptionID); err == nil {
		var row struct {
			ConsumedAt string `json:"consumed_at"`
		}
		if json.Unmarshal(consumed.Bytes, &row) == nil {
			status.Status = ExactApprovalStatusConsumed
			status.ConsumedAt = row.ConsumedAt
			return status, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ExactActionApprovalStatus{}, err
	}
	expiry, err := time.Parse(time.RFC3339Nano, approval.ExpiresAt)
	if err != nil {
		return ExactActionApprovalStatus{}, err
	}
	if !now.Before(expiry) {
		status.Status = ExactApprovalStatusExpired
	}
	return status, nil
}

func (s *ExactActionApprovalStore) Save(approval ExactActionApproval) (string, error) {
	if s == nil || s.homePath == "" {
		return "", fmt.Errorf("exact action approval store requires compound home")
	}
	if approval.SchemaVersion != ExactActionApprovalSchema ||
		!strings.HasPrefix(approval.ID, "approval_") ||
		approval.MaxUses != 1 ||
		!HasOperatorProvenance(approval.GrantedBy) ||
		strings.TrimSpace(approval.BindingDigest) == "" {
		return "", fmt.Errorf("invalid exact action approval")
	}
	if _, err := time.Parse(time.RFC3339Nano, approval.ExpiresAt); err != nil {
		return "", fmt.Errorf("invalid approval expiry: %w", err)
	}
	store, err := eventstore.Open(s.homePath)
	if err != nil {
		return "", err
	}
	result, err := store.PutJSON(approval.ID, approval, "  ")
	if err != nil {
		return "", err
	}
	return result.Reference.String(), nil
}

func (s *ExactActionApprovalStore) ApplyMatching(
	intent envelope.ActionIntent,
	cfg Config,
	base envelope.PolicyDecision,
	now time.Time,
) (envelope.PolicyDecision, bool, error) {
	if s == nil || s.homePath == "" {
		return base, false, fmt.Errorf("exact action approval store requires compound home")
	}
	binding := ActionBindingForIntent(intent, cfg)
	digest := ActionBindingDigest(binding)
	store, err := eventstore.Open(s.homePath)
	if err != nil {
		return base, false, err
	}
	entries, err := store.Scan(eventstore.ScanOptions{
		Prefixes:    []string{"approval_"},
		RegularOnly: true,
	})
	if err != nil {
		return base, false, err
	}
	for _, entry := range entries {
		record, err := entry.Read()
		if err != nil {
			return base, false, err
		}
		var approval ExactActionApproval
		if json.Unmarshal(record.Bytes, &approval) != nil ||
			approval.SchemaVersion != ExactActionApprovalSchema ||
			approval.BindingDigest != digest {
			continue
		}
		monitored := MonitoredAction{
			Intent:   intent,
			Decision: base,
			Binding:  binding,
		}
		decision, err := ApplyExactActionApproval(monitored, approval, eventApprovalConsumer{store: store}, now)
		if err == nil {
			decision.EvidenceRefs = mergeStrings(decision.EvidenceRefs, entry.Reference.String())
		}
		return decision, true, err
	}
	return base, false, nil
}

func ActionBindingForIntent(intent envelope.ActionIntent, cfg Config) ActionBinding {
	inputRefs := make([]string, 0, len(intent.InputSources))
	for _, source := range intent.InputSources {
		inputRefs = append(inputRefs, source.SourceID+"@"+source.ContentHash)
	}
	sort.Strings(inputRefs)
	domain := intent.Capability
	if before, _, ok := strings.Cut(intent.Capability, "."); ok {
		domain = before
	}
	return ActionBinding{
		SchemaVersion:      ActionBindingSchema,
		ActionIntentID:     intent.ID,
		Tool:               intent.Capability,
		Domain:             domain,
		Operation:          intent.Action,
		ArgumentsHash:      intent.ArgumentsHash,
		Destination:        intent.Destination,
		InputRefs:          inputRefs,
		ToolSchemaRevision: intent.SchemaRevision,
		PolicyRevision:     actionPolicyRevision(cfg),
	}
}

type eventApprovalConsumer struct {
	store *eventstore.Store
}

func (c eventApprovalConsumer) Consume(approvalID string, now time.Time) error {
	if c.store == nil {
		return fmt.Errorf("approval consumption store is required")
	}
	id := "approval_use_" + shortActionHash(approvalID)
	_, err := c.store.PutJSONExclusive(id, struct {
		SchemaVersion string `json:"schema_version"`
		ID            string `json:"id"`
		ApprovalID    string `json:"approval_id"`
		ConsumedAt    string `json:"consumed_at"`
	}{
		SchemaVersion: "compound.exact_action_approval_consumption.v0.1",
		ID:            id,
		ApprovalID:    approvalID,
		ConsumedAt:    now.UTC().Format(time.RFC3339Nano),
	}, "  ")
	if errors.Is(err, os.ErrExist) {
		return fmt.Errorf("approval %s already consumed", approvalID)
	}
	if err != nil {
		return fmt.Errorf("consume approval %s: %w", approvalID, err)
	}
	return nil
}

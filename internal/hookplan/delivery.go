package hookplan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"compound/internal/envelope"
	"compound/internal/eventstore"
	"compound/internal/policy"
	"compound/internal/stream"
	"compound/internal/work"
)

const (
	LifecyclePlanSchemaVersion       = "compound.lifecycle_plan.v0.1"
	DeliveryCardSchemaVersion        = "compound.coordination_delivery_card.v0.1"
	DeliveryStateSchemaVersion       = "compound.coordination_delivery_state.v0.2"
	DeliveryReportSchemaVersion      = "compound.coordination_delivery_report.v0.1"
	DeliveryMeasurementSchemaVersion = "compound.coordination_delivery_measurement.v0.1"
)

const (
	DeliveryTriggerInterrupt = "interrupt_tick"
	DeliveryTriggerBoundary  = "next_boundary"
)

type DeliveryBudget struct {
	MaxCards           int `json:"max_cards"`
	MaxBytes           int `json:"max_bytes"`
	MaxEstimatedTokens int `json:"max_estimated_tokens"`
	MaxItems           int `json:"max_items"`
}

var DefaultDeliveryBudget = DeliveryBudget{
	MaxCards:           1,
	MaxBytes:           2048,
	MaxEstimatedTokens: 240,
	MaxItems:           8,
}

type DeliveryClassBudget struct {
	MaxEstimatedTokensPerItem int `json:"max_estimated_tokens_per_item"`
	MaxEstimatedTokensPerCard int `json:"max_estimated_tokens_per_card"`
}

// DeliveryClassBudgetHypotheses are explicit initial operating hypotheses.
// Exact bytes remain measured; token counts use the documented byte/4 estimate.
var DeliveryClassBudgetHypotheses = map[string]DeliveryClassBudget{
	work.CollaborationDeliverySilentState:    {MaxEstimatedTokensPerItem: 0, MaxEstimatedTokensPerCard: 0},
	work.CollaborationDeliveryNextBoundary:   {MaxEstimatedTokensPerItem: 120, MaxEstimatedTokensPerCard: 240},
	work.CollaborationDeliveryInterrupt:      {MaxEstimatedTokensPerItem: 80, MaxEstimatedTokensPerCard: 80},
	work.CollaborationDeliveryHumanAttention: {MaxEstimatedTokensPerItem: 180, MaxEstimatedTokensPerCard: 180},
}

// DeliveryRecipient is the live context against which persisted event
// relevance is evaluated. Every coordinate is reference-only.
type DeliveryRecipient struct {
	Actor            string   `json:"actor,omitempty"`
	WorkID           string   `json:"work_id,omitempty"`
	RunAttemptID     string   `json:"run_attempt_id,omitempty"`
	OperatorStateRef string   `json:"operator_state_ref,omitempty"`
	ScopeRefs        []string `json:"scope_refs,omitempty"`
	DependencyRefs   []string `json:"dependency_refs,omitempty"`
	PolicyRefs       []string `json:"policy_refs,omitempty"`
	BaseRevisions    []string `json:"base_revisions,omitempty"`
	ResourceRefs     []string `json:"resource_refs,omitempty"`
}

type DeliveryRequest struct {
	HomePath            string
	Harness             string
	CursorKey           string
	Recipient           DeliveryRecipient
	Trigger             string
	BoundarySource      string
	Budget              *DeliveryBudget
	BeforeReportPersist func() error
	BeforeStatePersist  func() error
}

// LifecyclePlan is the semantic owner. Native hook presentation is compiled
// from it after relevance and boundary disposition are decided.
type LifecyclePlan struct {
	SchemaVersion  string               `json:"schema_version"`
	ID             string               `json:"id"`
	Revision       string               `json:"revision"`
	BoundarySource string               `json:"boundary_source"`
	Intentions     []LifecycleIntention `json:"intentions"`
}

type LifecycleIntention struct {
	EventID       string `json:"event_id"`
	DeliveryClass string `json:"delivery_class"`
	Disposition   string `json:"disposition"`
	Reason        string `json:"reason,omitempty"`
}

type DeliveryItem struct {
	EventID       string                       `json:"event_id"`
	DeliveryClass string                       `json:"delivery_class"`
	Delta         work.CollaborationStateDelta `json:"delta"`
	ArtifactRefs  []string                     `json:"artifact_refs"`
}

type DeliveryCard struct {
	SchemaVersion string         `json:"schema_version"`
	ID            string         `json:"id"`
	MeaningDigest string         `json:"meaning_digest"`
	RequiresHuman bool           `json:"requires_human,omitempty"`
	Items         []DeliveryItem `json:"items"`
}

type NativeDelivery struct {
	Harness       string       `json:"harness"`
	MeaningDigest string       `json:"meaning_digest"`
	Semantic      DeliveryCard `json:"semantic"`
	Context       string       `json:"context"`
}

type DeliveryReport struct {
	SchemaVersion            string                      `json:"schema_version"`
	ID                       string                      `json:"id"`
	ReportRef                string                      `json:"report_ref,omitempty"`
	Plan                     LifecyclePlan               `json:"lifecycle_plan"`
	Cards                    []DeliveryCard              `json:"cards"`
	Native                   *NativeDelivery             `json:"native,omitempty"`
	Cursor                   int64                       `json:"cursor"`
	PendingEvents            int                         `json:"pending_events"`
	IrrelevantEvents         int                         `json:"irrelevant_events"`
	SilentEvents             int                         `json:"silent_events"`
	DeliveredBytes           int                         `json:"delivered_bytes"`
	EstimatedDeliveredTokens int                         `json:"estimated_delivered_tokens"`
	DeclaredStateDeltaRefs   []string                    `json:"declared_state_delta_refs"`
	AcknowledgementMessages  int                         `json:"acknowledgement_messages"`
	FailedClosed             bool                        `json:"failed_closed,omitempty"`
	ExecutionHeld            bool                        `json:"execution_held,omitempty"`
	HoldEvidenceRefs         []string                    `json:"hold_evidence_refs,omitempty"`
	DegradedReason           string                      `json:"degraded_reason,omitempty"`
	ObservationBaseline      DeliveryObservationBaseline `json:"observation_baseline,omitempty"`
}

type DeliveryObservationBaseline struct {
	WorkID         string   `json:"work_id,omitempty"`
	WorkRevision   string   `json:"work_revision,omitempty"`
	ActionRefs     []string `json:"action_refs"`
	RunAttemptRefs []string `json:"run_attempt_refs"`
}

type DeliveryMeasurementReceipt struct {
	SchemaVersion          string   `json:"schema_version"`
	ID                     string   `json:"id"`
	DeliveryReportID       string   `json:"delivery_report_id"`
	CausedActionRefs       []string `json:"caused_action_refs"`
	ObservedActionRefs     []string `json:"observed_action_refs"`
	ObservedRunAttemptRefs []string `json:"observed_run_attempt_refs"`
	ObservedWorkRefs       []string `json:"observed_work_refs"`
}

type DeliveryPublisher func(DeliveryReport) error

type pendingSemanticState struct {
	EventRef      string `json:"event_ref"`
	StateRevision string `json:"state_revision"`
}

type deliveryState struct {
	SchemaVersion     string                          `json:"schema_version"`
	CursorKey         string                          `json:"cursor_key"`
	RecipientRevision string                          `json:"recipient_revision"`
	Cursor            int64                           `json:"cursor"`
	SemanticRevision  string                          `json:"semantic_revision"`
	Applied           map[string]string               `json:"applied"`
	Pending           map[string]pendingSemanticState `json:"pending"`
}

// DeliverCoordination scans the canonical governed replay, advances one
// recipient cursor, and emits at most one compact card.
func DeliverCoordination(request DeliveryRequest) (DeliveryReport, error) {
	return deliverCoordination(request, nil)
}

// DeliverCoordinationWithPublisher defers report/cursor publication until the
// caller durably publishes the native hook event that makes the response
// observable. A publisher failure leaves the semantic card retryable.
func DeliverCoordinationWithPublisher(request DeliveryRequest, publish DeliveryPublisher) (DeliveryReport, error) {
	if publish == nil {
		return DeliveryReport{}, fmt.Errorf("coordination delivery publisher is required")
	}
	return deliverCoordination(request, publish)
}

func deliverCoordination(request DeliveryRequest, publish DeliveryPublisher) (DeliveryReport, error) {
	request.HomePath = strings.TrimSpace(request.HomePath)
	request.Harness = normalizeDeliveryHarness(request.Harness)
	request.CursorKey = strings.TrimSpace(request.CursorKey)
	request.Trigger = strings.TrimSpace(request.Trigger)
	request.BoundarySource = strings.TrimSpace(request.BoundarySource)
	request.Recipient = normalizeDeliveryRecipient(request.Recipient)
	if request.HomePath == "" || request.Harness == "" || request.CursorKey == "" {
		return DeliveryReport{}, fmt.Errorf("coordination delivery requires home, harness, and cursor key")
	}
	if request.Trigger != DeliveryTriggerInterrupt && request.Trigger != DeliveryTriggerBoundary {
		return DeliveryReport{}, fmt.Errorf("coordination delivery trigger %q is invalid", request.Trigger)
	}
	if request.BoundarySource == "" {
		if request.Trigger == DeliveryTriggerInterrupt {
			request.BoundarySource = "pre_tool_use"
		} else {
			request.BoundarySource = "boundary"
		}
	}
	if request.Budget != nil {
		if request.Budget.MaxCards < 0 || request.Budget.MaxBytes < 0 ||
			request.Budget.MaxEstimatedTokens < 0 || request.Budget.MaxItems < 0 {
			return DeliveryReport{}, fmt.Errorf("coordination delivery budgets cannot be negative")
		}
	}
	recipientRevision := deliveryRecipientRevision(request.Recipient)

	var report DeliveryReport
	err := withDeliveryStateLock(request.HomePath, request.CursorKey, func(statePath string) error {
		state, err := readDeliveryState(statePath, request.CursorKey, recipientRevision)
		if err != nil {
			return err
		}
		frames, nextCursor, err := stream.ReadAvailable(request.HomePath, state.Cursor)
		if err != nil {
			return err
		}
		state.Cursor = nextCursor

		intentions := map[string]LifecycleIntention{}
		intentionOrder := []string{}
		setIntention := func(eventID, class, disposition, reason string) {
			if _, exists := intentions[eventID]; !exists {
				intentionOrder = append(intentionOrder, eventID)
			}
			intentions[eventID] = LifecycleIntention{
				EventID: eventID, DeliveryClass: class,
				Disposition: disposition, Reason: reason,
			}
		}

		for _, frame := range frames {
			if frame.Kind != stream.KindCollaborationEvent || frame.Collaboration == nil {
				continue
			}
			event := *frame.Collaboration
			if event.SchemaVersion == work.CollaborationLegacySchemaVersion {
				report.SilentEvents++
				setIntention(event.ID, work.CollaborationDeliverySilentState, "legacy_silent", "legacy event requires explicit migration or reclassification")
				continue
			}
			class := collaborationDeliveryClass(event)
			switch {
			case !deliveryRelevant(event, request.Recipient):
				report.IrrelevantEvents++
				setIntention(event.ID, class, "irrelevant", "no receiving work coordinate intersects")
			case class == work.CollaborationDeliverySilentState:
				report.SilentEvents++
				key := collaborationSemanticKey(event)
				state.Applied[key] = collaborationStateRevision(event)
				delete(state.Pending, key)
				setIntention(event.ID, class, "silent_state", "state recorded without context injection")
			default:
				key := collaborationSemanticKey(event)
				revision := collaborationStateRevision(event)
				if state.Applied[key] == revision {
					if previous, exists := state.Pending[key]; exists {
						setIntention(previous.EventRef, class, "coalesced", "newer event restores the already-delivered effective state")
						delete(state.Pending, key)
					}
					setIntention(event.ID, class, "unchanged", "effective state already delivered")
					continue
				}
				if previous, exists := state.Pending[key]; exists && previous.EventRef != event.ID {
					setIntention(previous.EventRef, class, "coalesced", "superseded by the latest effective state before delivery")
				}
				state.Pending[key] = pendingSemanticState{EventRef: event.ID, StateRevision: revision}
				setIntention(event.ID, class, "deferred", deliveryDeferredReason(class, request.Trigger))
			}
		}

		pendingKeys := sortedPendingKeys(state.Pending)
		pendingEvents := make([]work.CollaborationEvent, 0, len(pendingKeys))
		eventKeys := make(map[string]string, len(pendingKeys))
		for _, key := range pendingKeys {
			pending := state.Pending[key]
			event, err := loadCollaborationEvent(request.HomePath, pending.EventRef)
			if err != nil {
				return err
			}
			pendingEvents = append(pendingEvents, event)
			eventKeys[event.ID] = key
			if _, exists := intentions[event.ID]; !exists {
				setIntention(event.ID, collaborationDeliveryClass(event), "deferred", deliveryDeferredReason(collaborationDeliveryClass(event), request.Trigger))
			}
		}
		deliverable := make([]work.CollaborationEvent, 0, len(pendingEvents))
		for _, event := range pendingEvents {
			if deliveryClassReady(collaborationDeliveryClass(event), request.Trigger) {
				deliverable = append(deliverable, event)
			}
		}

		budget := DefaultDeliveryBudget
		if request.Budget != nil {
			budget = *request.Budget
		}
		if len(deliverable) > 0 && (budget.MaxCards < 1 || budget.MaxBytes < 1 || budget.MaxEstimatedTokens < 1 || budget.MaxItems < 1) {
			report.FailedClosed = true
			report.DegradedReason = "delivery budget cannot hold one card"
			for _, event := range deliverable {
				setIntention(event.ID, collaborationDeliveryClass(event), "budget_held", report.DegradedReason)
			}
			holdInterruptExecution(&report, deliverable)
		} else if len(deliverable) > 0 {
			var selected []work.CollaborationEvent
			var selectedCard DeliveryCard
			var selectedNative NativeDelivery
			for _, event := range deliverable {
				if len(selected) >= budget.MaxItems {
					break
				}
				candidate := append(append([]work.CollaborationEvent{}, selected...), event)
				card := makeDeliveryCard(candidate)
				native := compileNativeDelivery(request.Harness, card)
				bytes := len([]byte(native.Context))
				estimatedTokens := estimatedDeliveredTokenCount(native.Context)
				if bytes > budget.MaxBytes || estimatedTokens > budget.MaxEstimatedTokens ||
					!withinClassBudgetHypotheses(request.Harness, candidate, native) {
					break
				}
				selected = candidate
				selectedCard = card
				selectedNative = native
			}
			if len(selected) == 0 {
				report.FailedClosed = true
				report.DegradedReason = "smallest relevant coordination card exceeds byte or token budget"
				for _, event := range deliverable {
					setIntention(event.ID, collaborationDeliveryClass(event), "budget_held", report.DegradedReason)
				}
				holdInterruptExecution(&report, deliverable)
			} else {
				for _, event := range selected {
					key := eventKeys[event.ID]
					state.Applied[key] = collaborationStateRevision(event)
					delete(state.Pending, key)
					setIntention(event.ID, collaborationDeliveryClass(event), "delivered", "relevant at current semantic boundary")
					if event.Delta != nil && event.Delta.Before != event.Delta.After {
						report.DeclaredStateDeltaRefs = append(report.DeclaredStateDeltaRefs, event.ID)
					}
				}
				report.Cards = []DeliveryCard{selectedCard}
				report.Native = &selectedNative
				report.DeliveredBytes = len([]byte(selectedNative.Context))
				report.EstimatedDeliveredTokens = estimatedDeliveredTokenCount(selectedNative.Context)
				holdInterruptExecution(&report, selected)
				if len(selected) < len(deliverable) {
					report.DegradedReason = "bounded card delivered a prefix; remaining relevant events stay pending"
					held := deliverable[len(selected):]
					for _, event := range held {
						setIntention(event.ID, collaborationDeliveryClass(event), "budget_held", report.DegradedReason)
					}
					holdInterruptExecution(&report, held)
				}
			}
		}

		report.SchemaVersion = DeliveryReportSchemaVersion
		report.Cursor = state.Cursor
		report.PendingEvents = len(state.Pending)
		state.SemanticRevision = deliverySemanticRevision(state)
		report.Plan = finalizeLifecyclePlan(request.BoundarySource, intentionOrder, intentions)
		report.ObservationBaseline, err = observeRecipientState(request.HomePath, request.Recipient.WorkID)
		if err != nil {
			return err
		}
		report.ID = deliveryReportID(request, report)
		if len(report.Plan.Intentions) > 0 || report.PendingEvents > 0 ||
			report.Native != nil || report.FailedClosed {
			report.ReportRef = deliveryReportRef(report.ID)
		}
		if publish != nil {
			if err := publish(report); err != nil {
				return err
			}
		}
		if report.ReportRef != "" {
			if request.BeforeReportPersist != nil {
				if err := request.BeforeReportPersist(); err != nil {
					return err
				}
			}
			if err := writeDeliveryReport(request.HomePath, report); err != nil {
				return err
			}
		}
		if request.BeforeStatePersist != nil {
			if err := request.BeforeStatePersist(); err != nil {
				return err
			}
		}
		if err := writeDeliveryState(statePath, state); err != nil {
			return err
		}
		return nil
	})
	return report, err
}

func deliveryReportID(request DeliveryRequest, report DeliveryReport) string {
	payload, _ := json.Marshal(struct {
		CursorKey string `json:"cursor_key"`
		Harness   string `json:"harness"`
		Trigger   string `json:"trigger"`
		Cursor    int64  `json:"cursor"`
		Plan      string `json:"plan_revision"`
	}{
		CursorKey: request.CursorKey,
		Harness:   request.Harness,
		Trigger:   request.Trigger,
		Cursor:    report.Cursor,
		Plan:      report.Plan.Revision,
	})
	digest := sha256.Sum256(payload)
	return "drep_" + hex.EncodeToString(digest[:])[:16]
}

func writeDeliveryReport(homePath string, report DeliveryReport) error {
	dir := filepath.Join(homePath, "context", "coordination-delivery", "reports")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writePrivateJSONAtomic(filepath.Join(dir, report.ID+".json"), report)
}

func deliveryReportRef(id string) string {
	return filepath.Join("context", "coordination-delivery", "reports", id+".json")
}

func deliveryRelevant(event work.CollaborationEvent, recipient DeliveryRecipient) bool {
	affects := collaborationAffects(event)
	if event.TargetActor != "" && recipient.Actor != event.TargetActor {
		return false
	}
	if len(affects.Actors) > 0 && !containsRef(affects.Actors, recipient.Actor) {
		return false
	}
	switch collaborationDeliveryClass(event) {
	case work.CollaborationDeliveryInterrupt:
		return recipient.RunAttemptID != "" &&
			containsRef(affects.RunAttemptIDs, recipient.RunAttemptID) &&
			recipient.WorkID != "" &&
			(recipient.WorkID == event.WorkID || containsRef(affects.WorkIDs, recipient.WorkID))
	case work.CollaborationDeliveryHumanAttention:
		if recipient.OperatorStateRef != policy.OperatorStateAuthorityRef {
			return false
		}
	}
	if strings.TrimSpace(recipient.WorkID) != "" &&
		(recipient.WorkID == event.WorkID || containsRef(affects.WorkIDs, recipient.WorkID)) {
		return true
	}
	if recipient.RunAttemptID != "" && containsRef(affects.RunAttemptIDs, recipient.RunAttemptID) {
		return true
	}
	return intersects(recipient.ScopeRefs, affects.ScopeRefs) ||
		intersects(recipient.DependencyRefs, affects.DependencyRefs) ||
		intersects(recipient.PolicyRefs, affects.PolicyRefs) ||
		intersects(recipient.BaseRevisions, affects.BaseRevisions) ||
		intersects(recipient.ResourceRefs, affects.ResourceRefs)
}

func collaborationAffects(event work.CollaborationEvent) work.CollaborationAffects {
	if event.Affects == nil {
		return work.CollaborationAffects{}
	}
	return *event.Affects
}

func collaborationDeliveryClass(event work.CollaborationEvent) string {
	if strings.TrimSpace(event.DeliveryClass) == "" {
		return work.CollaborationDeliveryNextBoundary
	}
	return event.DeliveryClass
}

func collaborationDelta(event work.CollaborationEvent) work.CollaborationStateDelta {
	if event.Delta != nil {
		return *event.Delta
	}
	return work.CollaborationStateDelta{Kind: "observation", SubjectRef: event.WorkID, After: "observed"}
}

func collaborationSemanticKey(event work.CollaborationEvent) string {
	delta := collaborationDelta(event)
	return delta.Kind + "|" + delta.SubjectRef
}

func collaborationStateRevision(event work.CollaborationEvent) string {
	delta := collaborationDelta(event)
	effective := delta.After
	if effective == "" {
		effective = delta.Before
	}
	body, _ := json.Marshal(struct {
		Kind          string `json:"kind"`
		SubjectRef    string `json:"subject_ref"`
		Effective     string `json:"effective"`
		DeliveryClass string `json:"delivery_class"`
	}{
		Kind: delta.Kind, SubjectRef: delta.SubjectRef, Effective: effective,
		DeliveryClass: collaborationDeliveryClass(event),
	})
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func deliveryRecipientRevision(recipient DeliveryRecipient) string {
	body, _ := json.Marshal(recipient)
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func normalizeDeliveryRecipient(recipient DeliveryRecipient) DeliveryRecipient {
	recipient.Actor = strings.TrimSpace(recipient.Actor)
	recipient.WorkID = strings.TrimSpace(recipient.WorkID)
	recipient.RunAttemptID = strings.TrimSpace(recipient.RunAttemptID)
	recipient.OperatorStateRef = strings.TrimSpace(recipient.OperatorStateRef)
	recipient.ScopeRefs = normalizedDeliveryRefs(recipient.ScopeRefs)
	recipient.DependencyRefs = normalizedDeliveryRefs(recipient.DependencyRefs)
	recipient.PolicyRefs = normalizedDeliveryRefs(recipient.PolicyRefs)
	recipient.BaseRevisions = normalizedDeliveryRefs(recipient.BaseRevisions)
	recipient.ResourceRefs = normalizedDeliveryRefs(recipient.ResourceRefs)
	return recipient
}

func normalizedDeliveryRefs(refs []string) []string {
	out := make([]string, 0, len(refs))
	seen := map[string]bool{}
	for _, ref := range refs {
		ref = strings.TrimSpace(ref)
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	sort.Strings(out)
	return out
}

func deliverySemanticRevision(state deliveryState) string {
	body, _ := json.Marshal(struct {
		RecipientRevision string                          `json:"recipient_revision"`
		Applied           map[string]string               `json:"applied"`
		Pending           map[string]pendingSemanticState `json:"pending"`
	}{
		RecipientRevision: state.RecipientRevision,
		Applied:           state.Applied,
		Pending:           state.Pending,
	})
	digest := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(digest[:])
}

func sortedPendingKeys(pending map[string]pendingSemanticState) []string {
	keys := make([]string, 0, len(pending))
	for key := range pending {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func deliveryClassReady(class, trigger string) bool {
	if trigger == DeliveryTriggerBoundary {
		return class == work.CollaborationDeliveryNextBoundary
	}
	return class == work.CollaborationDeliveryInterrupt || class == work.CollaborationDeliveryHumanAttention
}

func deliveryDeferredReason(class, trigger string) string {
	if class == work.CollaborationDeliveryNextBoundary && trigger != DeliveryTriggerBoundary {
		return "waiting for next semantic boundary"
	}
	return "waiting for relevant delivery budget"
}

func makeDeliveryCard(events []work.CollaborationEvent) DeliveryCard {
	items := make([]DeliveryItem, 0, len(events))
	requiresHuman := false
	for _, event := range events {
		class := collaborationDeliveryClass(event)
		if class == work.CollaborationDeliveryHumanAttention {
			requiresHuman = true
		}
		items = append(items, DeliveryItem{
			EventID: event.ID, DeliveryClass: class, Delta: collaborationDelta(event),
			ArtifactRefs: []string{event.BodyRef},
		})
	}
	semantic := struct {
		RequiresHuman bool           `json:"requires_human,omitempty"`
		Items         []DeliveryItem `json:"items"`
	}{RequiresHuman: requiresHuman, Items: items}
	body, _ := json.Marshal(semantic)
	digest := sha256.Sum256(body)
	hexDigest := hex.EncodeToString(digest[:])
	return DeliveryCard{
		SchemaVersion: DeliveryCardSchemaVersion,
		ID:            "dcard_" + hexDigest[:16],
		MeaningDigest: "sha256:" + hexDigest,
		RequiresHuman: requiresHuman,
		Items:         items,
	}
}

func compileNativeDelivery(harnessName string, card DeliveryCard) NativeDelivery {
	var lines []string
	for _, item := range card.Items {
		delta := item.Delta
		change := delta.After
		if delta.Before != "" {
			change = delta.Before + " -> " + delta.After
		}
		lines = append(lines, fmt.Sprintf("- %s %s %s: %s event=%s artifact=%s",
			item.DeliveryClass, delta.Kind, delta.SubjectRef, change, item.EventID, item.ArtifactRefs[0]))
	}
	var context string
	switch normalizeDeliveryHarness(harnessName) {
	case "codex":
		context = "## Compound coordination\nmeaning=" + card.MeaningDigest + "\n" + strings.Join(lines, "\n")
	default:
		context = "<compound-coordination meaning=\"" + card.MeaningDigest + "\">\n" + strings.Join(lines, "\n") + "\n</compound-coordination>"
	}
	return NativeDelivery{
		Harness: normalizeDeliveryHarness(harnessName), MeaningDigest: card.MeaningDigest,
		Semantic: card, Context: context,
	}
}

func estimatedDeliveredTokenCount(context string) int {
	return (len([]byte(context)) + 3) / 4
}

func withinClassBudgetHypotheses(harnessName string, events []work.CollaborationEvent, native NativeDelivery) bool {
	cardEstimate := estimatedDeliveredTokenCount(native.Context)
	for _, event := range events {
		class := collaborationDeliveryClass(event)
		hypothesis, ok := DeliveryClassBudgetHypotheses[class]
		if !ok || hypothesis.MaxEstimatedTokensPerCard < cardEstimate {
			return false
		}
		itemNative := compileNativeDelivery(harnessName, makeDeliveryCard([]work.CollaborationEvent{event}))
		if estimatedDeliveredTokenCount(itemNative.Context) > hypothesis.MaxEstimatedTokensPerItem {
			return false
		}
	}
	return true
}

func holdInterruptExecution(report *DeliveryReport, events []work.CollaborationEvent) {
	if report == nil {
		return
	}
	for _, event := range events {
		if collaborationDeliveryClass(event) != work.CollaborationDeliveryInterrupt {
			continue
		}
		report.ExecutionHeld = true
		report.HoldEvidenceRefs = appendUniqueRef(report.HoldEvidenceRefs, event.ID)
		report.HoldEvidenceRefs = appendUniqueRef(report.HoldEvidenceRefs, event.BodyRef)
	}
}

func normalizeDeliveryHarness(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), "_", "-")
}

func intersects(left, right []string) bool {
	if len(left) == 0 || len(right) == 0 {
		return false
	}
	set := make(map[string]bool, len(left))
	for _, value := range left {
		set[strings.TrimSpace(value)] = true
	}
	for _, value := range right {
		if set[strings.TrimSpace(value)] {
			return true
		}
	}
	return false
}

func containsRef(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func appendUniqueRef(values []string, ref string) []string {
	if containsRef(values, ref) {
		return values
	}
	return append(values, ref)
}

func loadCollaborationEvent(homePath, ref string) (work.CollaborationEvent, error) {
	store, err := eventstore.Open(homePath)
	if err != nil {
		return work.CollaborationEvent{}, err
	}
	record, err := store.Read(ref)
	if err != nil {
		return work.CollaborationEvent{}, fmt.Errorf("coordination delivery event %s: %w", ref, err)
	}
	var event work.CollaborationEvent
	if err := json.Unmarshal(record.Bytes, &event); err != nil {
		return work.CollaborationEvent{}, fmt.Errorf("coordination delivery event %s decode: %w", ref, err)
	}
	if err := event.Validate(); err != nil {
		return work.CollaborationEvent{}, fmt.Errorf("coordination delivery event %s invalid: %w", ref, err)
	}
	workStore, err := work.Open(homePath)
	if err != nil {
		return work.CollaborationEvent{}, err
	}
	if err := workStore.ValidateCollaborationDeliveryEvidence(event); err != nil {
		return work.CollaborationEvent{}, fmt.Errorf("coordination delivery event %s ineligible: %w", ref, err)
	}
	return event, nil
}

func finalizeLifecyclePlan(boundarySource string, order []string, intentions map[string]LifecycleIntention) LifecyclePlan {
	items := make([]LifecycleIntention, 0, len(order))
	for _, eventID := range order {
		items = append(items, intentions[eventID])
	}
	body, _ := json.Marshal(struct {
		BoundarySource string               `json:"boundary_source"`
		Intentions     []LifecycleIntention `json:"intentions"`
	}{BoundarySource: boundarySource, Intentions: items})
	digest := sha256.Sum256(body)
	hexDigest := hex.EncodeToString(digest[:])
	return LifecyclePlan{
		SchemaVersion:  LifecyclePlanSchemaVersion,
		ID:             "lplan_" + hexDigest[:16],
		Revision:       "sha256:" + hexDigest,
		BoundarySource: boundarySource,
		Intentions:     items,
	}
}

func observeRecipientState(homePath, workID string) (DeliveryObservationBaseline, error) {
	baseline := DeliveryObservationBaseline{
		WorkID: strings.TrimSpace(workID), ActionRefs: []string{}, RunAttemptRefs: []string{},
	}
	if baseline.WorkID == "" {
		return baseline, nil
	}
	store, err := work.Open(homePath)
	if err != nil {
		return baseline, err
	}
	item, err := store.Get(baseline.WorkID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return baseline, nil
		}
		return baseline, err
	}
	body, err := json.Marshal(item)
	if err != nil {
		return baseline, err
	}
	digest := sha256.Sum256(body)
	baseline.WorkRevision = "sha256:" + hex.EncodeToString(digest[:])
	baseline.ActionRefs = append([]string{}, item.ActionIntentIDs...)
	baseline.RunAttemptRefs = append([]string{}, item.RunAttemptIDs...)
	sort.Strings(baseline.ActionRefs)
	sort.Strings(baseline.RunAttemptRefs)
	return baseline, nil
}

// MeasureDeliveryOutcome is the post-delivery causal measurement seam. It
// compares only owned observable work/action/run references against the
// delivery-time baseline; sender-declared state is not counted as caused action.
func MeasureDeliveryOutcome(homePath, deliveryReportID string) (DeliveryMeasurementReceipt, error) {
	deliveryReportID = strings.TrimSpace(deliveryReportID)
	if !strings.HasPrefix(deliveryReportID, "drep_") {
		return DeliveryMeasurementReceipt{}, fmt.Errorf("delivery report id is invalid")
	}
	body, err := os.ReadFile(filepath.Join(homePath, deliveryReportRef(deliveryReportID)))
	if err != nil {
		return DeliveryMeasurementReceipt{}, err
	}
	var report DeliveryReport
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&report); err != nil {
		return DeliveryMeasurementReceipt{}, err
	}
	if report.ID != deliveryReportID || report.SchemaVersion != DeliveryReportSchemaVersion {
		return DeliveryMeasurementReceipt{}, fmt.Errorf("delivery report identity is invalid")
	}
	current, err := observeRecipientState(homePath, report.ObservationBaseline.WorkID)
	if err != nil {
		return DeliveryMeasurementReceipt{}, err
	}
	observedActionRefs := refDifference(current.ActionRefs, report.ObservationBaseline.ActionRefs)
	causedActionRefs, err := causedActionRefs(homePath, observedActionRefs, report.ReportRef)
	if err != nil {
		return DeliveryMeasurementReceipt{}, err
	}
	receipt := DeliveryMeasurementReceipt{
		SchemaVersion:          DeliveryMeasurementSchemaVersion,
		DeliveryReportID:       deliveryReportID,
		CausedActionRefs:       causedActionRefs,
		ObservedActionRefs:     observedActionRefs,
		ObservedRunAttemptRefs: refDifference(current.RunAttemptRefs, report.ObservationBaseline.RunAttemptRefs),
		ObservedWorkRefs:       []string{},
	}
	if current.WorkRevision != "" && current.WorkRevision != report.ObservationBaseline.WorkRevision {
		receipt.ObservedWorkRefs = []string{current.WorkID + "@" + current.WorkRevision}
	}
	payload, _ := json.Marshal(struct {
		Report string   `json:"report"`
		Caused []string `json:"caused"`
		Action []string `json:"action"`
		Run    []string `json:"run"`
		Work   []string `json:"work"`
	}{
		deliveryReportID, receipt.CausedActionRefs, receipt.ObservedActionRefs,
		receipt.ObservedRunAttemptRefs, receipt.ObservedWorkRefs,
	})
	digest := sha256.Sum256(payload)
	receipt.ID = "dmeasure_" + hex.EncodeToString(digest[:])[:16]
	path := filepath.Join(homePath, "context", "coordination-delivery", "measurements", receipt.ID+".json")
	if err := writePrivateJSONAtomic(path, receipt); err != nil {
		return DeliveryMeasurementReceipt{}, err
	}
	return receipt, nil
}

func causedActionRefs(homePath string, observedActionRefs []string, reportRef string) ([]string, error) {
	if strings.TrimSpace(reportRef) == "" || len(observedActionRefs) == 0 {
		return []string{}, nil
	}
	store, err := eventstore.Open(homePath)
	if err != nil {
		return nil, err
	}
	caused := make([]string, 0, len(observedActionRefs))
	for _, ref := range observedActionRefs {
		record, err := store.Read(ref)
		if err != nil {
			return nil, fmt.Errorf("measure delivery action %s: %w", ref, err)
		}
		var action envelope.ActionIntent
		if err := json.Unmarshal(record.Bytes, &action); err != nil {
			return nil, fmt.Errorf("measure delivery action %s decode: %w", ref, err)
		}
		if err := action.Validate(); err != nil {
			return nil, fmt.Errorf("measure delivery action %s invalid: %w", ref, err)
		}
		if containsRef(action.EvidenceRefs, reportRef) {
			caused = append(caused, ref)
		}
	}
	sort.Strings(caused)
	return caused, nil
}

func refDifference(current, baseline []string) []string {
	seen := make(map[string]bool, len(baseline))
	for _, ref := range baseline {
		seen[ref] = true
	}
	out := make([]string, 0)
	for _, ref := range current {
		if !seen[ref] {
			out = append(out, ref)
		}
	}
	sort.Strings(out)
	return out
}

func withDeliveryStateLock(homePath, cursorKey string, fn func(statePath string) error) error {
	dir := filepath.Join(homePath, "context", "coordination-delivery", "cursors")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(cursorKey))
	stem := hex.EncodeToString(digest[:])
	lock, err := os.OpenFile(filepath.Join(dir, stem+".lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return fn(filepath.Join(dir, stem+".json"))
}

func readDeliveryState(path, cursorKey, recipientRevision string) (deliveryState, error) {
	state := deliveryState{
		SchemaVersion:     DeliveryStateSchemaVersion,
		CursorKey:         cursorKey,
		RecipientRevision: recipientRevision,
		Applied:           map[string]string{},
		Pending:           map[string]pendingSemanticState{},
	}
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return state, nil
	}
	if err != nil {
		return deliveryState{}, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return deliveryState{}, fmt.Errorf("coordination delivery state decode: %w", err)
	}
	if state.SchemaVersion != DeliveryStateSchemaVersion || state.CursorKey != cursorKey ||
		state.Cursor < 0 {
		return deliveryState{}, fmt.Errorf("coordination delivery state identity is invalid")
	}
	if state.Applied == nil || state.Pending == nil ||
		(state.SemanticRevision != "" && state.SemanticRevision != deliverySemanticRevision(state)) {
		return deliveryState{}, fmt.Errorf("coordination delivery semantic state is invalid")
	}
	for key, revision := range state.Applied {
		if !validSemanticStateEntry(key, revision) {
			return deliveryState{}, fmt.Errorf("coordination delivery applied semantic state is invalid")
		}
	}
	for key, pending := range state.Pending {
		if !validSemanticStateEntry(key, pending.StateRevision) || !strings.HasPrefix(pending.EventRef, "wco_") {
			return deliveryState{}, fmt.Errorf("coordination delivery pending semantic state is invalid")
		}
	}
	if state.RecipientRevision != recipientRevision {
		state.RecipientRevision = recipientRevision
		state.Cursor = 0
		state.Pending = map[string]pendingSemanticState{}
		state.SemanticRevision = ""
	}
	return state, nil
}

func writeDeliveryState(path string, state deliveryState) error {
	state.SchemaVersion = DeliveryStateSchemaVersion
	if state.Applied == nil {
		state.Applied = map[string]string{}
	}
	if state.Pending == nil {
		state.Pending = map[string]pendingSemanticState{}
	}
	state.SemanticRevision = deliverySemanticRevision(state)
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".delivery-state-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func validSemanticStateEntry(key, revision string) bool {
	parts := strings.SplitN(key, "|", 2)
	return len(parts) == 2 && parts[0] != "" && parts[1] != "" &&
		strings.HasPrefix(revision, "sha256:") && len(revision) == len("sha256:")+64
}

func writePrivateJSONAtomic(path string, value any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".private-json-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

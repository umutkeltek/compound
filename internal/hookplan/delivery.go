package hookplan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"compound/internal/eventstore"
	"compound/internal/home"
	"compound/internal/stream"
	"compound/internal/work"
)

const (
	LifecyclePlanSchemaVersion  = "compound.lifecycle_plan.v0.1"
	DeliveryCardSchemaVersion   = "compound.coordination_delivery_card.v0.1"
	DeliveryStateSchemaVersion  = "compound.coordination_delivery_state.v0.1"
	DeliveryReportSchemaVersion = "compound.coordination_delivery_report.v0.1"
)

const (
	DeliveryTriggerInterrupt = "interrupt_tick"
	DeliveryTriggerBoundary  = "next_boundary"
)

type DeliveryBudget struct {
	MaxCards  int `json:"max_cards"`
	MaxBytes  int `json:"max_bytes"`
	MaxTokens int `json:"max_tokens"`
	MaxItems  int `json:"max_items"`
}

var DefaultDeliveryBudget = DeliveryBudget{
	MaxCards:  1,
	MaxBytes:  2048,
	MaxTokens: 512,
	MaxItems:  8,
}

// DeliveryRecipient is the live context against which persisted event
// relevance is evaluated. Every coordinate is reference-only.
type DeliveryRecipient struct {
	Actor          string   `json:"actor,omitempty"`
	WorkID         string   `json:"work_id,omitempty"`
	ScopeRefs      []string `json:"scope_refs,omitempty"`
	DependencyRefs []string `json:"dependency_refs,omitempty"`
	PolicyRefs     []string `json:"policy_refs,omitempty"`
	BaseRevisions  []string `json:"base_revisions,omitempty"`
	ResourceRefs   []string `json:"resource_refs,omitempty"`
}

type DeliveryRequest struct {
	HomePath  string
	Harness   string
	CursorKey string
	Recipient DeliveryRecipient
	Trigger   string
	Budget    *DeliveryBudget
}

// LifecyclePlan is the semantic owner. Native hook presentation is compiled
// from it after relevance and boundary disposition are decided.
type LifecyclePlan struct {
	SchemaVersion string               `json:"schema_version"`
	ID            string               `json:"id"`
	Revision      string               `json:"revision"`
	Intentions    []LifecycleIntention `json:"intentions"`
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
	SchemaVersion           string          `json:"schema_version"`
	ID                      string          `json:"id"`
	ReportRef               string          `json:"report_ref,omitempty"`
	Plan                    LifecyclePlan   `json:"lifecycle_plan"`
	Cards                   []DeliveryCard  `json:"cards"`
	Native                  *NativeDelivery `json:"native,omitempty"`
	Cursor                  int64           `json:"cursor"`
	PendingEvents           int             `json:"pending_events"`
	IrrelevantEvents        int             `json:"irrelevant_events"`
	SilentEvents            int             `json:"silent_events"`
	DeliveredBytes          int             `json:"delivered_bytes"`
	DeliveredTokens         int             `json:"delivered_tokens"`
	ActionDeltaRefs         []string        `json:"action_delta_refs"`
	AcknowledgementMessages int             `json:"acknowledgement_messages"`
	FailedClosed            bool            `json:"failed_closed,omitempty"`
	DegradedReason          string          `json:"degraded_reason,omitempty"`
}

type deliveryState struct {
	SchemaVersion     string   `json:"schema_version"`
	CursorKey         string   `json:"cursor_key"`
	Cursor            int64    `json:"cursor"`
	PendingEventRefs  []string `json:"pending_event_refs"`
	TerminalEventRefs []string `json:"terminal_event_refs"`
}

// DeliverCoordination scans the canonical governed replay, advances one
// recipient cursor, and emits at most one compact card. State persistence
// happens before the caller receives presentation bytes.
func DeliverCoordination(request DeliveryRequest) (DeliveryReport, error) {
	request.HomePath = strings.TrimSpace(request.HomePath)
	request.Harness = normalizeDeliveryHarness(request.Harness)
	request.CursorKey = strings.TrimSpace(request.CursorKey)
	request.Trigger = strings.TrimSpace(request.Trigger)
	if request.HomePath == "" || request.Harness == "" || request.CursorKey == "" {
		return DeliveryReport{}, fmt.Errorf("coordination delivery requires home, harness, and cursor key")
	}
	if request.Trigger != DeliveryTriggerInterrupt && request.Trigger != DeliveryTriggerBoundary {
		return DeliveryReport{}, fmt.Errorf("coordination delivery trigger %q is invalid", request.Trigger)
	}
	if request.Budget != nil {
		if request.Budget.MaxCards < 0 || request.Budget.MaxBytes < 0 ||
			request.Budget.MaxTokens < 0 || request.Budget.MaxItems < 0 {
			return DeliveryReport{}, fmt.Errorf("coordination delivery budgets cannot be negative")
		}
	}

	var report DeliveryReport
	err := withDeliveryStateLock(request.HomePath, request.CursorKey, func(statePath string) error {
		state, err := readDeliveryState(statePath, request.CursorKey)
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
			if containsRef(state.PendingEventRefs, event.ID) || containsRef(state.TerminalEventRefs, event.ID) {
				continue
			}
			class := collaborationDeliveryClass(event)
			switch {
			case !deliveryRelevant(event, request.Recipient):
				report.IrrelevantEvents++
				state.TerminalEventRefs = appendUniqueRef(state.TerminalEventRefs, event.ID)
				setIntention(event.ID, class, "irrelevant", "no receiving work coordinate intersects")
			case class == work.CollaborationDeliverySilentState:
				report.SilentEvents++
				state.TerminalEventRefs = appendUniqueRef(state.TerminalEventRefs, event.ID)
				setIntention(event.ID, class, "silent_state", "state recorded without context injection")
			default:
				state.PendingEventRefs = appendUniqueRef(state.PendingEventRefs, event.ID)
				setIntention(event.ID, class, "deferred", deliveryDeferredReason(class, request.Trigger))
			}
		}

		pendingEvents := make([]work.CollaborationEvent, 0, len(state.PendingEventRefs))
		for _, ref := range state.PendingEventRefs {
			event, err := loadCollaborationEvent(request.HomePath, ref)
			if err != nil {
				return err
			}
			pendingEvents = append(pendingEvents, event)
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
		if len(deliverable) > 0 && (budget.MaxCards < 1 || budget.MaxBytes < 1 || budget.MaxTokens < 1 || budget.MaxItems < 1) {
			report.FailedClosed = true
			report.DegradedReason = "delivery budget cannot hold one card"
			for _, event := range deliverable {
				setIntention(event.ID, collaborationDeliveryClass(event), "budget_held", report.DegradedReason)
			}
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
				tokens := deliveredTokenCount(native.Context)
				if bytes > budget.MaxBytes || tokens > budget.MaxTokens {
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
			} else {
				selectedIDs := map[string]bool{}
				for _, event := range selected {
					selectedIDs[event.ID] = true
					state.TerminalEventRefs = appendUniqueRef(state.TerminalEventRefs, event.ID)
					setIntention(event.ID, collaborationDeliveryClass(event), "delivered", "relevant at current semantic boundary")
					if event.Delta != nil && event.Delta.Before != event.Delta.After {
						report.ActionDeltaRefs = append(report.ActionDeltaRefs, event.ID)
					}
				}
				remaining := state.PendingEventRefs[:0]
				for _, ref := range state.PendingEventRefs {
					if !selectedIDs[ref] {
						remaining = append(remaining, ref)
					}
				}
				state.PendingEventRefs = append([]string{}, remaining...)
				report.Cards = []DeliveryCard{selectedCard}
				report.Native = &selectedNative
				report.DeliveredBytes = len([]byte(selectedNative.Context))
				report.DeliveredTokens = deliveredTokenCount(selectedNative.Context)
				if len(selected) < len(deliverable) {
					report.DegradedReason = "bounded card delivered a prefix; remaining relevant events stay pending"
				}
			}
		}

		report.SchemaVersion = DeliveryReportSchemaVersion
		report.Cursor = state.Cursor
		report.PendingEvents = len(state.PendingEventRefs)
		report.Plan = finalizeLifecyclePlan(intentionOrder, intentions)
		report.ID = deliveryReportID(request, report)
		if len(report.Plan.Intentions) > 0 || report.PendingEvents > 0 ||
			report.Native != nil || report.FailedClosed {
			report.ReportRef = "hook-delivery-report:" + report.ID
			if err := writeDeliveryReport(request.HomePath, report); err != nil {
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
	dir := filepath.Join(homePath, "hook-delivery", "reports")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return writePrivateJSONAtomic(filepath.Join(dir, report.ID+".json"), report)
}

func RetrieveArtifactBody(homePath, ref string) ([]byte, error) {
	store, err := home.NewArtifactStore(homePath)
	if err != nil {
		return nil, err
	}
	return store.Get(ref)
}

func deliveryRelevant(event work.CollaborationEvent, recipient DeliveryRecipient) bool {
	if strings.TrimSpace(recipient.Actor) != "" &&
		(recipient.Actor == event.TargetActor || containsRef(collaborationAffects(event).Actors, recipient.Actor)) {
		return true
	}
	if strings.TrimSpace(recipient.WorkID) != "" &&
		(recipient.WorkID == event.WorkID || containsRef(collaborationAffects(event).WorkIDs, recipient.WorkID)) {
		return true
	}
	affects := collaborationAffects(event)
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

func deliveryClassReady(class, trigger string) bool {
	if trigger == DeliveryTriggerBoundary {
		return class != work.CollaborationDeliverySilentState
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

func deliveredTokenCount(context string) int {
	return (len([]byte(context)) + 3) / 4
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
	return event, nil
}

func finalizeLifecyclePlan(order []string, intentions map[string]LifecycleIntention) LifecyclePlan {
	items := make([]LifecycleIntention, 0, len(order))
	for _, eventID := range order {
		items = append(items, intentions[eventID])
	}
	body, _ := json.Marshal(items)
	digest := sha256.Sum256(body)
	hexDigest := hex.EncodeToString(digest[:])
	return LifecyclePlan{
		SchemaVersion: LifecyclePlanSchemaVersion,
		ID:            "lplan_" + hexDigest[:16],
		Revision:      "sha256:" + hexDigest,
		Intentions:    items,
	}
}

func withDeliveryStateLock(homePath, cursorKey string, fn func(statePath string) error) error {
	dir := filepath.Join(homePath, "hook-delivery")
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

func readDeliveryState(path, cursorKey string) (deliveryState, error) {
	state := deliveryState{
		SchemaVersion:     DeliveryStateSchemaVersion,
		CursorKey:         cursorKey,
		PendingEventRefs:  []string{},
		TerminalEventRefs: []string{},
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
	if state.SchemaVersion != DeliveryStateSchemaVersion || state.CursorKey != cursorKey || state.Cursor < 0 {
		return deliveryState{}, fmt.Errorf("coordination delivery state identity is invalid")
	}
	seen := map[string]bool{}
	for _, refs := range []struct {
		name   string
		values []string
	}{
		{name: "pending", values: state.PendingEventRefs},
		{name: "terminal", values: state.TerminalEventRefs},
	} {
		for _, ref := range refs.values {
			if !strings.HasPrefix(ref, "wco_") || seen[ref] {
				return deliveryState{}, fmt.Errorf("coordination delivery %s event refs are invalid", refs.name)
			}
			seen[ref] = true
		}
	}
	return state, nil
}

func writeDeliveryState(path string, state deliveryState) error {
	state.SchemaVersion = DeliveryStateSchemaVersion
	if state.PendingEventRefs == nil {
		state.PendingEventRefs = []string{}
	}
	if state.TerminalEventRefs == nil {
		state.TerminalEventRefs = []string{}
	}
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

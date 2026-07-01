package work

import (
	"sort"
	"strings"
	"time"

	"compound/internal/envelope"
)

// WorkbenchOptions controls the read-only workbench projection.
type WorkbenchOptions struct {
	RecentLimit int
}

// WorkbenchReport is a read model over WorkEnvelope, Receipt, and Transition
// state. It does not dispatch, claim, close, or repair work.
type WorkbenchReport struct {
	GeneratedAt string          `json:"generated_at"`
	Counts      WorkbenchCounts `json:"counts"`
	Open        []WorkbenchItem `json:"open"`
	Recent      []WorkbenchItem `json:"recent,omitempty"`
	Integrity   []string        `json:"integrity,omitempty"`
}

type WorkbenchCounts struct {
	Total             int            `json:"total"`
	Open              int            `json:"open"`
	Terminal          int            `json:"terminal"`
	NeedsClaim        int            `json:"needs_claim"`
	NeedsReview       int            `json:"needs_review"`
	InProgress        int            `json:"in_progress"`
	MissingReceipt    int            `json:"missing_receipt"`
	MissingTransition int            `json:"missing_transition"`
	ByState           map[string]int `json:"by_state"`
}

type WorkbenchItem struct {
	WorkID             string   `json:"work_id"`
	State              string   `json:"state"`
	Owner              string   `json:"owner,omitempty"`
	ClaimedBy          string   `json:"claimed_by,omitempty"`
	ClaimedAt          string   `json:"claimed_at,omitempty"`
	SuccessPredicate   string   `json:"success_predicate"`
	DesiredOutcome     string   `json:"desired_outcome,omitempty"`
	CanonicalSurface   string   `json:"canonical_surface,omitempty"`
	ContextPackID      string   `json:"context_pack_id,omitempty"`
	PolicyDecisionID   string   `json:"policy_decision_id,omitempty"`
	ReceiptID          string   `json:"receipt_id,omitempty"`
	TransitionID       string   `json:"transition_id,omitempty"`
	MoveType           string   `json:"move_type,omitempty"`
	Changed            bool     `json:"changed,omitempty"`
	ReviewTrigger      string   `json:"review_trigger,omitempty"`
	NextAction         string   `json:"next_action"`
	OpenReason         string   `json:"open_reason,omitempty"`
	EvidenceRefs       []string `json:"evidence_refs,omitempty"`
	ArtifactIDs        []string `json:"artifact_ids,omitempty"`
	VerificationIDs    []string `json:"verification_ids,omitempty"`
	MemoryCandidateIDs []string `json:"memory_candidate_ids,omitempty"`
}

func (s *Store) Workbench(now time.Time, opts WorkbenchOptions) (WorkbenchReport, error) {
	if opts.RecentLimit <= 0 {
		opts.RecentLimit = 5
	}
	report := WorkbenchReport{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		Counts:      WorkbenchCounts{ByState: map[string]int{}},
		Open:        []WorkbenchItem{},
	}
	works, err := s.List()
	if err != nil {
		return report, err
	}
	report.Counts.Total = len(works)
	for _, w := range works {
		events, err := s.eventsFor(w.ID)
		if err != nil {
			return report, err
		}
		item := s.workbenchItem(w, events, &report)
		report.Counts.ByState[w.State]++
		if terminalStates[w.State] {
			report.Counts.Terminal++
			if terminalProofRequired(w.State) && item.ReceiptID == "" {
				report.Counts.MissingReceipt++
			}
			if terminalProofRequired(w.State) && item.TransitionID == "" {
				report.Counts.MissingTransition++
			}
			report.Recent = append(report.Recent, item)
			continue
		}
		report.Counts.Open++
		switch w.State {
		case "ready", "admitted", "proposed":
			report.Counts.NeedsClaim++
		case "held", "blocked":
			report.Counts.NeedsReview++
		case "claimed", "running", "verifying":
			report.Counts.InProgress++
		}
		report.Open = append(report.Open, item)
	}
	sort.Slice(report.Open, func(i, j int) bool {
		pi, pj := statePriority(report.Open[i].State), statePriority(report.Open[j].State)
		if pi != pj {
			return pi < pj
		}
		return report.Open[i].WorkID < report.Open[j].WorkID
	})
	sort.Slice(report.Recent, func(i, j int) bool {
		return report.Recent[i].WorkID > report.Recent[j].WorkID
	})
	if len(report.Recent) > opts.RecentLimit {
		report.Recent = report.Recent[:opts.RecentLimit]
	}
	return report, nil
}

func (s *Store) workbenchItem(w envelope.WorkEnvelope, events []Event, report *WorkbenchReport) WorkbenchItem {
	item := WorkbenchItem{
		WorkID:             w.ID,
		State:              w.State,
		Owner:              w.Owner,
		SuccessPredicate:   w.SuccessPredicate,
		DesiredOutcome:     w.DesiredOutcome,
		CanonicalSurface:   w.CanonicalSurface,
		ContextPackID:      w.IDs.ContextPackID,
		PolicyDecisionID:   w.PolicyDecisionID,
		TransitionID:       w.IDs.TransitionID,
		NextAction:         nextWorkAction(w.State),
		OpenReason:         openReason(w.State),
		EvidenceRefs:       append([]string{}, w.EvidenceRefs...),
		ArtifactIDs:        append([]string{}, w.ArtifactIDs...),
		VerificationIDs:    append([]string{}, w.VerificationIDs...),
		MemoryCandidateIDs: append([]string{}, w.MemoryCandidateIDs...),
	}
	if lease, err := s.readLease(w.ID); err == nil {
		item.ClaimedBy = lease.By
		item.ClaimedAt = lease.ClaimAt
	}
	item.ReceiptID = lastAttachedWithPrefix(events, "rec_")
	if item.ReceiptID != "" {
		var rec envelope.Receipt
		if err := s.readJSON(s.envPath(item.ReceiptID), &rec); err != nil {
			report.Integrity = append(report.Integrity, w.ID+": receipt "+item.ReceiptID+" unreadable: "+err.Error())
		} else if err := rec.Validate(); err != nil {
			report.Integrity = append(report.Integrity, w.ID+": receipt "+item.ReceiptID+" invalid: "+err.Error())
		} else {
			item.MoveType = rec.MoveType
			item.Changed = rec.StateDelta.Changed
			item.ReviewTrigger = rec.Next.ReviewTrigger
		}
	}
	if item.TransitionID == "" {
		item.TransitionID = lastAttachedWithPrefix(events, "tr_")
	}
	if terminalStates[w.State] && item.TransitionID != "" {
		var tr envelope.Transition
		if err := s.readJSON(s.envPath(item.TransitionID), &tr); err != nil {
			report.Integrity = append(report.Integrity, w.ID+": transition "+item.TransitionID+" unreadable: "+err.Error())
		} else if err := tr.Validate(); err != nil {
			report.Integrity = append(report.Integrity, w.ID+": transition "+item.TransitionID+" invalid: "+err.Error())
		}
	}
	return item
}

func lastAttachedWithPrefix(events []Event, prefix string) string {
	for i := len(events) - 1; i >= 0; i-- {
		for j := len(events[i].Attached) - 1; j >= 0; j-- {
			if strings.HasPrefix(events[i].Attached[j], prefix) {
				return events[i].Attached[j]
			}
		}
	}
	return ""
}

func nextWorkAction(state string) string {
	switch state {
	case "proposed":
		return "admit or cancel after owner review"
	case "admitted":
		return "mark ready when context and policy are available"
	case "ready":
		return "claim before touching"
	case "claimed":
		return "advance running or release to ready"
	case "running":
		return "advance verifying, completed, blocked, held, or failed"
	case "verifying":
		return "close completed or failed, or return running"
	case "held":
		return "resolve policy/source-control hold, then ready or cancel"
	case "blocked":
		return "resolve blocker, then running, failed, or cancel"
	case "completed", "failed", "cancelled":
		return "terminal; mine receipt and transition if useful"
	default:
		return "inspect work state"
	}
}

func openReason(state string) string {
	switch state {
	case "proposed", "admitted", "ready":
		return "needs_claim"
	case "held", "blocked":
		return "needs_review"
	case "claimed", "running", "verifying":
		return "in_progress"
	default:
		return ""
	}
}

func terminalProofRequired(state string) bool {
	return state == "completed" || state == "failed"
}

func statePriority(state string) int {
	switch state {
	case "held":
		return 0
	case "blocked":
		return 1
	case "running", "verifying":
		return 2
	case "claimed":
		return 3
	case "ready":
		return 4
	case "admitted":
		return 5
	case "proposed":
		return 6
	default:
		return 9
	}
}

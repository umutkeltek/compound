package attention

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"compound/internal/reviewartifact"
)

// What came of an answer. A review record that carries a recorded answer
// (the review owner's resolution, or an owner's own decision on the card,
// such as a capability proposal's disposition) is listed in Report.Answered
// with what the answer was, when, the receipt it left, and, when the answer
// named work, that work's current state read from the work store. It is a
// join of ids the records already hold; nothing new is stored, and nothing
// here counts toward needs_you.

// WorkLookup reads one work item's current state. Nil Inputs.Work leaves
// every work state unknown.
type WorkLookup interface {
	WorkState(workID string) (state string, ok bool)
}

// Answer is one recorded answer and what came of it.
type Answer struct {
	Area   string `json:"area"`
	ItemID string `json:"item_id"`
	Path   string `json:"path"`
	// Question is the record's own question or summary.
	Question string `json:"question,omitempty"`
	// Decision is the answer as the owner recorded it (dismissed, parked,
	// open_work, defer, reject, ...).
	Decision   string `json:"decision"`
	DecidedAt  string `json:"decided_at,omitempty"`
	DecidedBy  string `json:"decided_by,omitempty"`
	ReceiptID  string `json:"receipt_id,omitempty"`
	DecisionID string `json:"decision_id,omitempty"`
	DeferUntil string `json:"defer_until,omitempty"`
	// WorkID is the work the answer created or named; WorkState is that
	// work's state now, "unknown" when the work cannot be read.
	WorkID    string `json:"work_id,omitempty"`
	WorkState string `json:"work_state,omitempty"`
	// StillWaiting is true when the item counts in his list again now (a
	// deferral whose date passed, for example).
	StillWaiting bool `json:"still_waiting"`
	// Outcome is one plain sentence: what he answered and what happened.
	Outcome string `json:"outcome"`
}

const workStateUnknown = "unknown"

// liveReviewItems indexes the review items counted in his list now.
func liveReviewItems(groups []Group) map[string]bool {
	out := map[string]bool{}
	for _, g := range groups {
		if g.Source != SourceReviews || g.Band != BandNeedsYou {
			continue
		}
		for _, id := range g.ItemIDs {
			out[strings.TrimSpace(g.Area)+"\x00"+strings.TrimSpace(id)] = true
		}
	}
	return out
}

func answersFromReviews(report reviewartifact.QueueReport, live map[string]bool, probe Probe, work WorkLookup) []Answer {
	if probe == nil {
		probe = osProbe{}
	}
	out := []Answer{}
	for _, item := range report.Items {
		answer, ok := answerOf(item, probe)
		if !ok {
			continue
		}
		answer.StillWaiting = live[strings.TrimSpace(item.Area)+"\x00"+strings.TrimSpace(item.ID)]
		if answer.WorkID != "" {
			answer.WorkState = workStateUnknown
			if work != nil {
				if state, found := work.WorkState(answer.WorkID); found && strings.TrimSpace(state) != "" {
					answer.WorkState = strings.TrimSpace(state)
				}
			}
		}
		answer.Outcome = answerOutcome(answer)
		out = append(out, answer)
	}
	// Newest answer first; an answer with no readable time goes last.
	sort.SliceStable(out, func(i, j int) bool {
		ti, okI := parseTime(out[i].DecidedAt)
		tj, okJ := parseTime(out[j].DecidedAt)
		if okI != okJ {
			return okI
		}
		if okI && !ti.Equal(tj) {
			return ti.After(tj)
		}
		if out[i].Area != out[j].Area {
			return out[i].Area < out[j].Area
		}
		return out[i].ItemID < out[j].ItemID
	})
	return out
}

// answerOf reads the recorded answer a record carries: the review owner's
// resolution first, else the decision the owner wrote onto its card.
func answerOf(item reviewartifact.QueueItem, probe Probe) (Answer, bool) {
	answer := Answer{Area: item.Area, ItemID: item.ID, Path: item.JSONPath, Question: firstNonEmpty(item.OperatorSummary, item.ContextLabel)}
	if item.DecisionCard != nil && strings.TrimSpace(item.DecisionCard.Claim) != "" {
		answer.Question = strings.TrimSpace(item.DecisionCard.Claim)
	}
	if r := item.Resolution; r != nil && strings.TrimSpace(r.Status) == "recorded" && strings.TrimSpace(r.Decision) != "" {
		answer.Decision = strings.TrimSpace(r.Decision)
		answer.DecidedAt = strings.TrimSpace(r.ResolvedAt)
		answer.DecidedBy = firstNonEmpty(r.ReviewedBy, r.Actor)
		answer.ReceiptID = strings.TrimSpace(r.ReceiptID)
		answer.DecisionID = strings.TrimSpace(r.ID)
		return answer, true
	}
	if item.DecisionCard == nil {
		return Answer{}, false
	}
	meta := item.DecisionCard.Meta
	decision := strings.TrimSpace(meta["decision"])
	if decision == "" || strings.TrimSpace(meta["decision_id"]) == "" {
		return Answer{}, false
	}
	answer.Decision = decision
	answer.DecisionID = strings.TrimSpace(meta["decision_id"])
	answer.DecidedBy = strings.TrimSpace(meta["decision_actor"])
	answer.DeferUntil = strings.TrimSpace(meta["defer_until"])
	answer.WorkID = strings.TrimSpace(meta["work_id"])
	answer.DecidedAt = decisionSidecarTime(item, answer.DecisionID, probe)
	return answer, true
}

// decisionSidecarTime reads when the owner wrote the decision: the
// `<record>.<decision>.decision.json` sidecar it keeps next to the record.
func decisionSidecarTime(item reviewartifact.QueueItem, decisionID string, probe Probe) string {
	latest := ""
	for _, path := range item.ExtraPaths {
		if !strings.HasSuffix(path, ".decision.json") {
			continue
		}
		data, err := probe.ReadFile(path, sidecarReadLimit)
		if err != nil {
			continue
		}
		var sidecar map[string]any
		if json.Unmarshal(data, &sidecar) != nil {
			continue
		}
		if id := stringField(sidecar, "id"); id != decisionID && stringField(sidecar, "disposition_id") != decisionID {
			continue
		}
		latest = newerTimestamp(latest, stringField(sidecar, "created_at"))
	}
	return latest
}

func answerOutcome(a Answer) string {
	on := ""
	if at, ok := parseTime(a.DecidedAt); ok {
		on = " on " + at.UTC().Format("2006-01-02")
	}
	var s string
	switch a.Decision {
	case "dismissed":
		s = "Dismissed" + on + ". Recording it ran nothing else."
	case "parked":
		s = "Parked" + on + "; it waits until someone picks it up again."
	case "reviewed":
		s = "Marked reviewed" + on + "."
	case "reject":
		s = "Rejected" + on + ". Recording it ran nothing else."
	case "defer":
		s = "Put off" + on
		if at, ok := parseTime(a.DeferUntil); ok {
			s += " until " + at.UTC().Format("2006-01-02")
		}
		s += "."
	case "open_work":
		s = "Turned into work" + on + "."
	default:
		s = fmt.Sprintf("Answered %q%s.", a.Decision, on)
	}
	switch {
	case a.WorkID != "" && a.WorkState == workStateUnknown:
		s += " That work cannot be read now."
	case a.WorkID != "":
		s += " That work is now " + a.WorkState + "."
	}
	if a.StillWaiting {
		s += " It is back in your list."
	}
	return s
}

package attention

import (
	"strings"
	"testing"
	"time"

	"compound/internal/reviewartifact"
)

type fakeWork map[string]string

func (f fakeWork) WorkState(id string) (string, bool) {
	state, ok := f[id]
	return state, ok
}

func answeredByItem(t *testing.T, report Report) map[string]map[string]any {
	t.Helper()
	out := map[string]map[string]any{}
	raw, _ := wireOf(t, report)["answered"].([]any)
	for _, entry := range raw {
		answer := entry.(map[string]any)
		out[answer["item_id"].(string)] = answer
	}
	return out
}

func answersFixture(t *testing.T) *reviewartifact.QueueReport {
	t.Helper()
	answeredAt := liveNow.Add(-30 * 24 * time.Hour)
	deferredAt := liveNow.Add(-40 * 24 * time.Hour)
	reviews := liveFixture(t,
		liveRecord{area: "lock-requests", id: "lock_a", status: "recorded", summary: "Clear the old lock?", created: answeredAt.Add(-time.Hour),
			resolved: &reviewartifact.ReviewResolution{ID: "rres_a", Status: "recorded", Decision: "dismissed", Actor: "operator", ResolvedAt: answeredAt.Format(time.RFC3339), ReceiptID: "rec_a"}},
		liveRecord{area: "skill-proposals", id: "clp_work", status: "recorded", band: reviewartifact.BandFiled, summary: "Update the skill?", claim: "Teach skill-a the new flag.", created: answeredAt.Add(-time.Hour),
			sidecars: map[string]any{"clp_work.cld_work.decision.json": map[string]any{"id": "cld_work", "decision": "open_work", "created_at": answeredAt.Format(time.RFC3339)}}},
		liveRecord{area: "skill-proposals", id: "clp_back", status: "review_required", summary: "Update the skill?", claim: "Retire skill-b.", created: deferredAt.Add(-time.Hour),
			sidecars: map[string]any{"clp_back.cld_back.decision.json": map[string]any{"id": "cld_back", "decision": "defer", "defer_until": liveNow.Add(-24 * time.Hour).Format(time.RFC3339), "created_at": deferredAt.Format(time.RFC3339)}}},
		liveRecord{area: "skill-proposals", id: "clp_open", status: "review_required", summary: "Update the skill?", claim: "Add skill-c.", created: deferredAt},
	)
	for i := range reviews.Items {
		item := &reviews.Items[i]
		switch item.ID {
		case "clp_work":
			item.DecisionCard.Meta = map[string]string{"decision": "open_work", "decision_id": "cld_work", "decision_actor": "operator-console", "work_id": "work_from_answer"}
		case "clp_back":
			item.DecisionCard.Meta = map[string]string{"decision": "defer", "decision_id": "cld_back", "defer_until": liveNow.Add(-24 * time.Hour).Format(time.RFC3339)}
		}
	}
	return reviews
}

func TestAnsweredShowsWhatCameOfEachRecordedAnswer(t *testing.T) {
	reviews := answersFixture(t)
	report := Build(Inputs{Reviews: reviews, Work: fakeWork{"work_from_answer": "cancelled"}}, liveNow)
	answers := answeredByItem(t, report)

	if len(answers) != 3 || answers["clp_open"] != nil {
		t.Fatalf("answered = %v, want the three records that carry a recorded answer and not the unanswered one", answers)
	}
	dismissed := answers["lock_a"]
	if dismissed["decision"] != "dismissed" || dismissed["receipt_id"] != "rec_a" || !strings.HasPrefix(dismissed["outcome"].(string), "Dismissed on "+liveNow.Add(-30*24*time.Hour).Format("2006-01-02")) {
		t.Fatalf("dismissed answer = %v", dismissed)
	}
	work := answers["clp_work"]
	if work["work_id"] != "work_from_answer" || work["work_state"] != "cancelled" || !strings.Contains(work["outcome"].(string), "That work is now cancelled.") {
		t.Fatalf("open-work answer = %v, want the work it created and that work's state now", work)
	}
	if work["decided_at"] != liveNow.Add(-30*24*time.Hour).Format(time.RFC3339) {
		t.Fatalf("open-work decided_at = %v, want the decision sidecar's time", work["decided_at"])
	}
	back := answers["clp_back"]
	if back["still_waiting"] != true || !strings.Contains(back["outcome"].(string), "It is back in your list.") {
		t.Fatalf("deferred answer = %v, want it marked back in his list", back)
	}
	// Newest answer first.
	order := wireOf(t, report)["answered"].([]any)
	if order[0].(map[string]any)["item_id"] != "lock_a" && order[0].(map[string]any)["item_id"] != "clp_work" {
		t.Fatalf("answered order starts with %v, want a newest answer first", order[0])
	}
	if order[len(order)-1].(map[string]any)["item_id"] != "clp_back" {
		t.Fatalf("answered order ends with %v, want the oldest answer last", order[len(order)-1])
	}
}

func TestAnsweredWorkStateIsUnknownWhenTheWorkCannotBeRead(t *testing.T) {
	report := Build(Inputs{Reviews: answersFixture(t)}, liveNow)
	work := answeredByItem(t, report)["clp_work"]
	if work["work_state"] != "unknown" || !strings.Contains(work["outcome"].(string), "cannot be read") {
		t.Fatalf("answer with unread work = %v, want unknown, never a guess", work)
	}
}

func TestAnsweredDoesNotChangeAnyCount(t *testing.T) {
	without := Build(Inputs{Reviews: answersFixture(t)}, liveNow)
	with := Build(Inputs{Reviews: answersFixture(t), Work: fakeWork{"work_from_answer": "done"}}, liveNow)
	if without.Counts != with.Counts || without.Totals.TotalRead != with.Totals.TotalRead || without.Totals.LiveDecisions != with.Totals.LiveDecisions {
		t.Fatalf("answers moved a count: %+v vs %+v", without.Counts, with.Counts)
	}
	if with.Counts.NeedsYou != 2 {
		t.Fatalf("needs_you = %d, want the two open proposals (one back from a deferral)", with.Counts.NeedsYou)
	}
}

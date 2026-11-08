package main

import (
	"testing"
	"time"

	"compound/internal/reviewartifact"
)

// Orient's "oldest waiting item" must skip every review the owner calls
// finished. Its local copy of the list lacked "completed",
// "candidate_recorded" and "authorization_recorded".
func TestReviewQueueAgedItemSkipsEveryTerminalStatus(t *testing.T) {
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	created := now.Add(-72 * time.Hour).Format(time.RFC3339)
	for _, status := range reviewartifact.TerminalStatuses() {
		item := reviewartifact.QueueItem{ID: "rev_" + status, Area: "test", Status: status, CreatedAt: created}
		if aged := reviewQueueAgedItem(item, now); aged != nil {
			t.Errorf("finished review (status %q) reported as waiting: %+v", status, aged)
		}
	}
	waiting := reviewartifact.QueueItem{ID: "rev_waiting", Area: "test", Status: "review_required", CreatedAt: created}
	if reviewQueueAgedItem(waiting, now) == nil {
		t.Fatal("a review that still needs a decision was not reported")
	}
}

// A candidate whose verdict orient treats as final must not also count as
// operator work. "merge_existing" was final in one predicate and actionable
// in the other.
func TestTerminalCandidateVerdictIsNeverActionable(t *testing.T) {
	for _, verdict := range []string{
		"promote", "promoted", "reject", "rejected", "park", "parked", "operationalize", "retire",
		"done", "recorded", "resolved", "accepted", "applied", "ok", "no_promote", "merge_existing",
	} {
		state, terminal := terminalCandidateVerdict(verdict)
		if !terminal {
			t.Errorf("verdict %q is not terminal; update this list with the predicate", verdict)
			continue
		}
		if candidateStateActionable(state) {
			t.Errorf("terminal verdict %q is counted as actionable", verdict)
		}
	}
}

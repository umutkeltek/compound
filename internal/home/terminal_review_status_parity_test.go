package home_test

import (
	"testing"

	"compound/internal/home"
	"compound/internal/reviewartifact"
)

// The archive keeps its own copy of the review terminal set because
// reviewartifact imports home. The copy must say "finished" for every word
// the owner does; a missing word makes archive skip finished artifacts
// ("completed" until 2026-08-18, "authorization_recorded" until 2026-11-08).
func TestTerminalReviewStatusMatchesOwner(t *testing.T) {
	for _, status := range reviewartifact.TerminalStatuses() {
		if !home.TerminalReviewStatus(status) {
			t.Errorf("archive does not treat owner terminal status %q as finished", status)
		}
	}
	for _, status := range []string{"", "review_required", "needs_review", "pending", "blocked", "held", "superseded"} {
		if home.TerminalReviewStatus(status) != reviewartifact.TerminalStatus(status) {
			t.Errorf("archive and owner disagree on %q", status)
		}
	}
}

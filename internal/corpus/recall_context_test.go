package corpus

import (
	"strings"
	"testing"
)

func TestRecallContextBudgetMarksIncompleteExcerpts(t *testing.T) {
	r := RecallReport{}
	for i := 0; i < 25; i++ {
		r.CurrentClaims = append(r.CurrentClaims, CurrentClaim{ObjectExcerpt: strings.Repeat("claim ", 200)})
	}
	for i := 0; i < 10; i++ {
		r.Evidence = append(r.Evidence, Evidence{QueryHit: QueryHit{Text: strings.Repeat("archive ", 1000), Neighbors: []QueryNeighbor{{Text: strings.Repeat("context ", 1000)}}}})
	}
	boundRecallContext(&r, []string{"archive"})
	total := 0
	for _, c := range r.CurrentClaims {
		total += len([]rune(c.ObjectExcerpt))
	}
	marked := false
	for _, e := range r.Evidence {
		total += len([]rune(e.Text))
		marked = marked || e.TextTruncated || e.ContextOmitted
		for _, n := range e.Neighbors {
			total += len([]rune(n.Text))
		}
	}
	if total > recallTextBudget || !marked {
		t.Fatalf("budget=%d truncated=%v", total, marked)
	}
}

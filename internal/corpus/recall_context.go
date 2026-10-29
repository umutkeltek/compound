package corpus

import (
	"strings"
	"unicode"

	"compound/internal/mem"
)

const recallTextBudget = 16000

// CurrentClaim is a bounded view of an adjudicated claim, not a rewritten Claim.
type CurrentClaim struct {
	ID               string   `json:"id"`
	Entity           string   `json:"entity"`
	Predicate        string   `json:"predicate"`
	ObjectExcerpt    string   `json:"object_excerpt"`
	ExcerptTruncated bool     `json:"excerpt_truncated,omitempty"`
	ValidFrom        string   `json:"valid_from,omitempty"`
	Confidence       float64  `json:"confidence"`
	EvidenceRefs     []string `json:"evidence_refs,omitempty"`
}

func questionTerms(query string) []string {
	stop := map[string]bool{}
	for _, word := range strings.Fields("a an the and or of to in on at for from with as by is are was were be been being do does did have has had can could would should will we our us i me my you your it its this that these those what which who when where why how about after before again please tell know remember recall comparing compared decide decided other") {
		stop[word] = true
	}
	words := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	out := []string{}
	seen := map[string]bool{}
	for _, word := range words {
		if len([]rune(word)) < 2 || stop[word] || seen[word] {
			continue
		}
		seen[word] = true
		out = append(out, word)
	}
	return out
}
func claimView(c mem.Claim, terms []string) CurrentClaim {
	excerpt, truncated := recallExcerpt(c.Object, 800, terms)
	return CurrentClaim{ID: c.ID, Entity: c.Entity, Predicate: c.Predicate, ObjectExcerpt: excerpt,
		ExcerptTruncated: truncated, ValidFrom: c.ValidFrom, Confidence: c.Confidence, EvidenceRefs: c.EvidenceRefs}
}
func recallExcerpt(text string, budget int, terms []string) (string, bool) {
	runes := []rune(text)
	if len(runes) <= budget {
		return text, false
	}
	if budget <= 0 {
		return "", len(runes) > 0
	}
	// Center a truncated excerpt on a specific query term when possible.
	start := 0
	lower := strings.ToLower(text)
	for _, term := range terms {
		if offset := strings.Index(lower, term); offset >= 0 {
			start = len([]rune(lower[:offset])) - budget/3
			if start < 0 {
				start = 0
			}
			break
		}
	}
	if start+budget > len(runes) {
		start = len(runes) - budget
	}
	return string(runes[start : start+budget]), true
}
func boundRecallContext(r *RecallReport, terms []string) {
	remaining := recallTextBudget / 3
	for i := range r.CurrentClaims {
		c := &r.CurrentClaims[i]
		text, truncated := recallExcerpt(c.ObjectExcerpt, remaining, terms)
		c.ObjectExcerpt = text
		c.ExcerptTruncated = c.ExcerptTruncated || truncated
		remaining -= len([]rune(text))
	}
	remaining += recallTextBudget - recallTextBudget/3
	if remaining < 0 {
		remaining = 0
	}
	for i := range r.Evidence {
		hit := &r.Evidence[i]
		// Reserve an anchor excerpt for every selected evidence item.
		slots := len(r.Evidence) - i
		anchorBudget := remaining
		if slots > 1 {
			anchorBudget = remaining / slots
		}
		hit.Text, hit.TextTruncated = recallExcerpt(hit.Text, anchorBudget, terms)
		remaining -= len([]rune(hit.Text))
		if hit.TextTruncated {
			hit.ContextOmitted = true
		}
		for n := range hit.Neighbors {
			neighbor := &hit.Neighbors[n]
			// Neighbor context cannot consume the anchor reservations for later hits.
			allowance := remaining
			if slots > 1 {
				allowance = remaining - (slots-1)*1000
				if allowance < 0 {
					allowance = 0
				}
			}
			text, truncated := recallExcerpt(neighbor.Text, allowance, terms)
			neighbor.Text = text
			remaining -= len([]rune(text))
			if truncated {
				hit.ContextOmitted = true
			}
		}
	}
}

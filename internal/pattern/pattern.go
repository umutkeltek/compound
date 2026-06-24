// Package pattern surfaces recurring through-lines — (entity, topic) the owner
// returns to across multiple sessions. These are HYPOTHESES with support, not
// canon (Oracle: "across N claims and M sessions the owner repeatedly..."). The
// semantic version (an LLM naming the through-line) is a later pass; this is the
// deterministic recurrence detector. See docs/memory-architecture.md.
package pattern

import (
	"fmt"
	"strings"
	"time"

	"github.com/umutkeltek/compound/internal/memory"
)

// minClaims is the recurrence floor — a single claim is not a pattern.
const minClaims = 2

// Build emits a Pattern hypothesis per (entity, topic) reached by >= minClaims
// claims, annotated with how many distinct sessions support it.
func Build(claims []memory.Claim) []memory.Pattern {
	type key struct{ entity, topic string }
	type agg struct {
		claims   []string
		sessions map[string]bool
	}
	m := map[key]*agg{}
	var order []key
	for _, c := range claims {
		if c.Status == memory.StatusRejected {
			continue
		}
		k := key{c.SubjectEntity, c.Topic()}
		a := m[k]
		if a == nil {
			a = &agg{sessions: map[string]bool{}}
			m[k] = a
			order = append(order, k)
		}
		a.claims = append(a.claims, c.ID)
		a.sessions[sessionOf(c.ID)] = true
	}

	now := time.Now().UTC()
	var out []memory.Pattern
	for _, k := range order {
		a := m[k]
		if len(a.claims) < minClaims {
			continue
		}
		out = append(out, memory.Pattern{
			ID:              "pat::" + k.entity + "::" + k.topic,
			ScopeEntitySlug: k.entity,
			Description:     fmt.Sprintf("Recurring: %d claims about %s/%s across %d session(s)", len(a.claims), k.entity, k.topic, len(a.sessions)),
			SupportClaims:   a.claims,
			Confidence:      0.5,
			Status:          "hypothesis",
			FirstSeen:       now,
			LastSeen:        now,
			LastVerified:    now,
		})
	}
	return out
}

// sessionOf extracts the source session from a distiller claim id (cl_<session>_<idx>).
func sessionOf(claimID string) string {
	s := strings.TrimPrefix(claimID, "cl_")
	if i := strings.LastIndexByte(s, '_'); i >= 0 {
		s = s[:i]
	}
	return s
}

// Package position folds Claims into Positions — the CURRENT durable stance per
// (entity, topic). "Current truth" is a Position, not "sort claims by date and
// pray". Derived from the ledger; rebuildable. See docs/memory-architecture.md.
package position

import (
	"time"

	"compound/internal/memory"
)

// Build groups live Claims by (entity, topic) and picks the active claim
// (promoted preferred, else latest candidate). No supersedes edges yet — real
// conflict detection is a later pass; the chain records history without asserting it.
func Build(claims []memory.Claim) []memory.Position {
	type key struct{ entity, topic string }
	groups := map[key][]memory.Claim{}
	var order []key
	for _, c := range claims {
		if c.Status == memory.StatusRejected || c.Status == memory.StatusRetired {
			continue
		}
		k := key{c.SubjectEntity, c.Topic()}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], c)
	}

	now := time.Now().UTC()
	out := make([]memory.Position, 0, len(order))
	for _, k := range order {
		cs := groups[k]
		active, status := pick(cs)
		chain := make([]string, len(cs))
		for i, c := range cs {
			chain[i] = c.ID
		}
		out = append(out, memory.Position{
			ID:            k.entity + "::" + k.topic,
			EntitySlug:    k.entity,
			TopicSlug:     k.topic,
			ActiveClaimID: active,
			Status:        status,
			ClaimChain:    chain,
			LastReviewed:  now,
		})
	}
	return out
}

// pick returns the active claim id and the position status. A promoted claim makes
// the position "active" (canon); only-candidates leaves it "unstable".
func pick(cs []memory.Claim) (active, status string) {
	for _, c := range cs {
		if c.Status == memory.StatusPromoted {
			active = c.ID // last promoted wins
		}
	}
	if active != "" {
		return active, "active"
	}
	for _, c := range cs {
		if c.Status == memory.StatusCandidate {
			active = c.ID
		}
	}
	return active, "unstable"
}

// Package reconcile is the alive-memory pass: it adjudicates claims that pile up
// on the same (entity, topic), marking superseded/duplicate ones so memory evolves
// instead of just accumulating. Inspired by mem0's ADD/UPDATE/DELETE/NONE memory
// manager and gbrain's superseded_by. The deterministic NONE (dedup) happens at
// ingest; this LLM pass handles UPDATE/DELETE → superseded + supersedes edges.
package reconcile

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/umutkeltek/compound/internal/distill"
	"github.com/umutkeltek/compound/internal/ledger"
	"github.com/umutkeltek/compound/internal/memory"
)

const reconcileSystem = `You are a memory reconciler. You are given CLAIMS the system currently holds about ONE entity/topic, each with an id and text. Decide their relationships, in the spirit of memory management (ADD/UPDATE/DELETE/NONE):
- SUPERSEDES: a later / more complete / contradicting claim makes an earlier one obsolete.
- DUPLICATE: two claims mean the same thing; keep the most complete, mark the other a duplicate.
Be conservative — only relate claims that genuinely supersede or duplicate; leave independent claims alone.
Output ONLY JSON (no prose, no code fence): {"supersedes": [["newer_id","older_id"], ...], "duplicates": [["keep_id","dup_id"], ...]}. Use ONLY the given ids. If nothing to reconcile, output {"supersedes":[],"duplicates":[]}.`

type decisionSet struct {
	Supersedes [][]string `json:"supersedes"`
	Duplicates [][]string `json:"duplicates"`
}

// Reconcile groups live claims by (entity, topic) and, per group with >=2 claims,
// asks the model which supersede/duplicate which, then applies it: the older claim
// is marked superseded (status + EffectiveTo) and a supersedes Edge is appended.
// Append-only — nothing is destroyed; superseded claims remain as history.
func Reconcile(ctx context.Context, l *ledger.Ledger, model, onlyEntity string) (superseded, dups int, err error) {
	claims, err := loadLive(l)
	if err != nil {
		return 0, 0, err
	}
	byID := make(map[string]memory.Claim, len(claims))
	groups := map[string][]memory.Claim{}
	var order []string
	for _, c := range claims {
		byID[c.ID] = c
		if onlyEntity != "" && c.SubjectEntity != onlyEntity {
			continue
		}
		k := c.SubjectEntity + "::" + c.Topic()
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], c)
	}

	now := time.Now().UTC()
	supersededIDs := map[string]bool{}
	apply := func(newerID, olderID string) {
		old, ok := byID[olderID]
		if !ok || supersededIDs[olderID] || old.Status == memory.StatusSuperseded {
			return
		}
		if _, ok := byID[newerID]; !ok {
			return
		}
		old.Status = memory.StatusSuperseded
		old.EffectiveTo = &now
		_ = l.Append("claims", old)
		_ = l.Append("edges", memory.Edge{
			FromID: newerID, FromType: "claim",
			EdgeType: memory.EdgeSupersedes,
			ToID:     olderID, ToType: "claim",
			EffectiveFrom: &now,
		})
		supersededIDs[olderID] = true
	}

	for _, k := range order {
		cs := groups[k]
		if len(cs) < 2 {
			continue
		}
		var b strings.Builder
		for _, c := range cs {
			fmt.Fprintf(&b, "- id=%s: %s\n", c.ID, c.Object)
		}
		out, e := distill.LLMComplete(ctx, model, reconcileSystem, b.String())
		if e != nil {
			return superseded, dups, e
		}
		var d decisionSet
		if parseJSON(out, &d) != nil {
			continue
		}
		for _, p := range d.Supersedes {
			if len(p) == 2 {
				apply(p[0], p[1])
				superseded++
			}
		}
		for _, p := range d.Duplicates {
			if len(p) == 2 {
				apply(p[0], p[1]) // a duplicate is superseded by the kept claim
				dups++
			}
		}
	}
	return superseded, dups, nil
}

func loadLive(l *ledger.Ledger) ([]memory.Claim, error) {
	byID := map[string]memory.Claim{}
	var order []string
	err := l.Each("claims", func(b []byte) error {
		var c memory.Claim
		if json.Unmarshal(b, &c) != nil {
			return nil
		}
		if _, ok := byID[c.ID]; !ok {
			order = append(order, c.ID)
		}
		byID[c.ID] = c
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]memory.Claim, 0, len(order))
	for _, id := range order {
		c := byID[id]
		if c.Status == memory.StatusSuperseded || c.Status == memory.StatusRejected || c.Status == memory.StatusRetired {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func parseJSON(s string, v any) error {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	i := strings.IndexByte(s, '{')
	j := strings.LastIndexByte(s, '}')
	if i < 0 || j < i {
		return fmt.Errorf("no JSON object")
	}
	return json.Unmarshal([]byte(s[i:j+1]), v)
}

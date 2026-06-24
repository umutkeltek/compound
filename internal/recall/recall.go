// Package recall is the query planner + promotion gate — the owned side of
// retrieval. It picks a mode, applies HARD filters before touching the engine,
// reads owned Claims from the ledger, and adds engine breadth. See
// docs/memory-architecture.md. Visibility is enforced here, never by the engine.
package recall

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/umutkeltek/compound/internal/engine"
	"github.com/umutkeltek/compound/internal/ledger"
	"github.com/umutkeltek/compound/internal/memory"
)

type Mode string

const (
	ModeEntity Mode = "entity_anchored" // precision: a known entity is named
	ModeHybrid Mode = "hybrid_corpus"   // breadth: exploratory
)

type HardFilters struct {
	Visibility []memory.Visibility  // nil = all; ambient excludes private/personal
	Statuses   []memory.ClaimStatus // which claim statuses may surface
}

type Plan struct {
	Mode        Mode
	Entity      string
	Query       string
	HardFilters HardFilters
}

// MakePlan chooses the mode (entity-anchored if the query names a registry
// entity) and sets hard filters. Ambient recall is conservative: promoted +
// operational/world only.
func MakePlan(query string, registry []memory.Entity, ambient bool) Plan {
	p := Plan{Query: query, Mode: ModeHybrid}
	lq := strings.ToLower(query)
	for _, e := range registry {
		if mentions(lq, strings.ToLower(e.Slug)) {
			p.Mode, p.Entity = ModeEntity, e.Slug
			break
		}
		for _, al := range e.Aliases {
			if mentions(lq, strings.ToLower(al)) {
				p.Mode, p.Entity = ModeEntity, e.Slug
				break
			}
		}
		if p.Mode == ModeEntity {
			break
		}
	}
	if ambient {
		p.HardFilters = HardFilters{
			Visibility: []memory.Visibility{memory.VisOperational, memory.VisWorld},
			Statuses:   []memory.ClaimStatus{memory.StatusPromoted},
		}
	} else {
		p.HardFilters = HardFilters{
			Statuses: []memory.ClaimStatus{memory.StatusPromoted, memory.StatusCandidate},
		}
	}
	return p
}

type Result struct {
	Plan   Plan
	Claims []memory.Claim // owned, from the ledger
	Hits   []engine.Hit   // breadth, from the rented engine
}

// Run executes a plan: owned Claims first (entity mode), then engine breadth.
func Run(l *ledger.Ledger, eng engine.MemoryEngine, p Plan) (*Result, error) {
	r := &Result{Plan: p}
	if p.Mode == ModeEntity {
		claims, err := LoadClaims(l)
		if err != nil {
			return nil, err
		}
		for _, c := range claims {
			if c.SubjectEntity != p.Entity {
				continue
			}
			if !statusOK(c.Status, p.HardFilters.Statuses) || !visOK(c.Visibility, p.HardFilters.Visibility) {
				continue
			}
			r.Claims = append(r.Claims, c)
		}
	}
	if eng != nil {
		if hits, err := eng.Query(p.Query, 8); err == nil {
			r.Hits = hits
		}
	}
	return r, nil
}

// LoadClaims returns the CURRENT claim per ID (last write wins — the ledger is
// append-only, so a promoted/superseded record overrides its candidate).
func LoadClaims(l *ledger.Ledger) ([]memory.Claim, error) {
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
		out = append(out, byID[id])
	}
	return out, nil
}

// Promote moves a candidate Claim to promoted — the gate that turns evidence into
// canon. It requires a basis and is the ONLY path to promoted status (the distiller
// can never self-canonize). Append-only: writes a new promoted record.
func Promote(l *ledger.Ledger, claimID string, basis memory.PromotionBasis) (memory.Claim, error) {
	claims, err := LoadClaims(l)
	if err != nil {
		return memory.Claim{}, err
	}
	for _, c := range claims {
		if c.ID != claimID {
			continue
		}
		if c.Status == memory.StatusPromoted {
			return c, fmt.Errorf("claim %s already promoted", claimID)
		}
		c.Status = memory.StatusPromoted
		c.PromotionBasis = basis
		now := time.Now().UTC()
		c.EffectiveFrom = &now
		if err := l.Append("claims", c); err != nil {
			return c, err
		}
		return c, nil
	}
	return memory.Claim{}, fmt.Errorf("claim %s not found", claimID)
}

func statusOK(s memory.ClaimStatus, allowed []memory.ClaimStatus) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if s == a {
			return true
		}
	}
	return false
}

func visOK(v memory.Visibility, allowed []memory.Visibility) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if v == a {
			return true
		}
	}
	return false
}

func mentions(text, term string) bool {
	if term == "" {
		return false
	}
	for idx := 0; ; {
		i := strings.Index(text[idx:], term)
		if i < 0 {
			return false
		}
		i += idx
		before := i == 0 || !isAlnum(text[i-1])
		end := i + len(term)
		after := end >= len(text) || !isAlnum(text[end])
		if before && after {
			return true
		}
		idx = i + 1
	}
}

func isAlnum(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9' || b == '-'
}

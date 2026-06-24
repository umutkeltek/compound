// Package scope solves deterministic session attribution: which project/topic a
// session is "about", without trusting cwd. It emits ScopeAssignments backed by
// evidence via a boring weighted scorer (not ML). See docs/memory-architecture.md.
package scope

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/umutkeltek/compound/internal/distill"
	"github.com/umutkeltek/compound/internal/memory"
)

const ResolverVersion = "scope-resolver-v0.2"

type Relation string

const (
	RelPrimary  Relation = "primary_about"
	RelMentions Relation = "mentions"
)

type Assignment struct {
	Entity   string   `json:"entity"`
	Relation Relation `json:"relation"`
	Score    int      `json:"score"`
	Signals  []string `json:"signals"`
}

type Packet struct {
	SessionID       string       `json:"session_id"`
	Status          string       `json:"status"` // primary_about | multi_scope | unresolved
	PrimaryScope    string       `json:"primary_scope,omitempty"`
	SecondaryScopes []string     `json:"secondary_scopes,omitempty"`
	Assignments     []Assignment `json:"assignments"`
	ResolverVersion string       `json:"resolver_version"`
}

// Scoring weights (Oracle's shape, adapted to content signals): explicit
// identifiers and human decision-turns outrank tool/agent mentions.
const (
	wIdentifier   = 90 // exact domain/repo/bundle/port match
	wDecisionTurn = 50 // alias inside an Umut decision-turn
	wUmutTurn     = 30 // alias in an Umut turn
	wAgentTurn    = 20 // alias in an agent turn
	wTitle        = 25 // alias in the session title (first Umut turn)
)

// SeedEntities is a starter registry. The Authority layer will own the real one;
// the resolver also merges any entities already in the ledger.
func SeedEntities() []memory.Entity {
	mk := func(slug string, t memory.EntityType, aliases, ids []string) memory.Entity {
		return memory.Entity{Slug: slug, Type: t, Aliases: aliases, Identifiers: ids, VisibilityDefault: memory.VisWorld}
	}
	return []memory.Entity{
		mk("compound", memory.EntProduct, []string{"compound"}, []string{"systems/compound", "github.com/umutkeltek/compound"}),
		mk("gbrain", memory.EntTool, []string{"gbrain"}, []string{"/users/umut/gbrain"}),
		mk("switchback", memory.EntProduct, []string{"switchback"}, []string{"18765", "18770", "18771"}),
		mk("samplegame", memory.EntProduct, []string{"samplegame", "sample game"}, nil),
		mk("exampleapp", memory.EntProduct, []string{"exampleapp", "exampletrack"}, []string{"example.app"}),
		mk("umut-os", memory.EntProject, []string{"umut-os"}, []string{"projects/operating-system"}),
		mk("infra-os", memory.EntProject, []string{"infra-os"}, nil),
		mk("hermes", memory.EntTool, []string{"hermes"}, nil),
		mk("openclaw", memory.EntTool, []string{"openclaw"}, nil),
		mk("oracle", memory.EntTool, []string{"oracle"}, nil),
		mk("mempalace", memory.EntTool, []string{"mempalace", "mem palace"}, []string{"github.com/mempalace"}),
		mk("sampleforecast", memory.EntProduct, []string{"sampleforecast"}, []string{"sampleforecast.com"}),
		mk("samplefin", memory.EntProduct, []string{"samplefin"}, nil),
	}
}

// mentions reports a word-boundary occurrence of term in (lowercased) text.
func mentions(text, term string) bool {
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
	return b >= 'a' && b <= 'z' || b >= '0' && b <= '9'
}

// Resolve attributes a session to entities deterministically. cwd is not consulted;
// scope is derived from content (identifiers + alias mentions weighted by where they
// appear). Unresolved is returned rather than guessing.
func Resolve(sessionPath string, registry []memory.Entity) (*Packet, error) {
	c, err := distill.Clean(sessionPath)
	if err != nil {
		return nil, err
	}
	pkt := &Packet{SessionID: base(sessionPath), ResolverVersion: ResolverVersion}

	// Score per signal TYPE with diminishing returns on repetition: a subject
	// mentioned in 40 decision-turns must outrank a passing one-time mention, but
	// without a ubiquitous token exploding linearly. weight * (1 + ln(count)).
	type sigAcc struct {
		weight int
		count  int
	}
	scores := map[string]map[string]*sigAcc{} // slug -> sigType -> acc
	add := func(slug, typ string, weight int) {
		m := scores[slug]
		if m == nil {
			m = map[string]*sigAcc{}
			scores[slug] = m
		}
		a := m[typ]
		if a == nil {
			a = &sigAcc{weight: weight}
			m[typ] = a
		}
		a.count++
	}

	title := ""
	if len(c.Turns) > 0 {
		title = strings.ToLower(c.Turns[0].Text)
	}

	for _, t := range c.Turns {
		lt := strings.ToLower(t.Text)
		dec := t.Role == distill.RoleUmut && distill.IsDecisionTurn(t)
		for _, e := range registry {
			for _, id := range e.Identifiers {
				if id != "" && strings.Contains(lt, id) {
					add(e.Slug, "identifier", wIdentifier)
				}
			}
			for _, al := range e.Aliases {
				if !mentions(lt, al) {
					continue
				}
				switch {
				case dec:
					add(e.Slug, "decision-turn", wDecisionTurn)
				case t.Role == distill.RoleUmut:
					add(e.Slug, "umut", wUmutTurn)
				default:
					add(e.Slug, "agent", wAgentTurn)
				}
			}
		}
	}
	for _, e := range registry {
		for _, al := range e.Aliases {
			if title != "" && mentions(title, al) {
				add(e.Slug, "title", wTitle)
			}
		}
	}

	strongSig := map[string]bool{}
	for slug, m := range scores {
		total := 0.0
		sig := make([]string, 0, len(m))
		for typ, a := range m {
			total += float64(a.weight) * (1 + math.Log(float64(a.count)))
			sig = append(sig, fmt.Sprintf("%s×%d", typ, a.count))
			if typ == "decision-turn" || typ == "identifier" {
				strongSig[slug] = true
			}
		}
		sort.Strings(sig)
		pkt.Assignments = append(pkt.Assignments, Assignment{Entity: slug, Score: int(total), Signals: sig})
	}
	sort.Slice(pkt.Assignments, func(i, j int) bool {
		return pkt.Assignments[i].Score > pkt.Assignments[j].Score
	})

	// Relative thresholds (robust to score rescaling): primary needs a clear lead
	// AND a strong (decision-turn or identifier) signal; else multi/unresolved.
	top := 0
	if len(pkt.Assignments) > 0 {
		top = pkt.Assignments[0].Score
	}
	switch {
	case len(pkt.Assignments) == 0:
		pkt.Status = "unresolved"
	case (len(pkt.Assignments) == 1 || float64(top) >= 1.4*float64(pkt.Assignments[1].Score)) && strongSig[pkt.Assignments[0].Entity]:
		pkt.Status = "primary_about"
		pkt.PrimaryScope = pkt.Assignments[0].Entity
		pkt.Assignments[0].Relation = RelPrimary
	default:
		strong := 0
		for _, a := range pkt.Assignments {
			if top > 0 && float64(a.Score) >= 0.4*float64(top) {
				strong++
			}
		}
		if strong >= 2 {
			pkt.Status = "multi_scope"
		} else {
			pkt.Status = "unresolved"
		}
	}
	for i := range pkt.Assignments {
		if pkt.Assignments[i].Relation == "" {
			pkt.Assignments[i].Relation = RelMentions
			if top > 0 && float64(pkt.Assignments[i].Score) >= 0.4*float64(top) {
				pkt.SecondaryScopes = append(pkt.SecondaryScopes, pkt.Assignments[i].Entity)
			}
		}
	}
	return pkt, nil
}

func base(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[i+1:]
	}
	return strings.TrimSuffix(p, ".jsonl")
}

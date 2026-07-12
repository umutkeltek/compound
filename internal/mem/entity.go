package mem

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// EntityKinds — the routing-backbone vocabulary (what an entity can be).
var EntityKinds = []string{"project", "product", "repo", "server", "person", "concept", "topic", "doctrine", "tool"}

// Entity is a canonical identity that survives path / host / name moves — the routing backbone the
// audit found missing in mem (where Claim.Entity was a bare string with no catalog/aliasing). All
// of an entity's aliases and identifiers resolve to ONE Slug, so a display name, a moved folder, and
// a repo URL all map to the same entity. This is the spine-native convergence of the Gen-1
// memory.Entity + scope's alias/identifier resolution (the session-content SCORING in scope.Resolve
// converges later on top of this registry).
type Entity struct {
	Slug        string   `json:"slug"` // canonical id
	Kind        string   `json:"kind"`
	Aliases     []string `json:"aliases,omitempty"`
	Identifiers []string `json:"identifiers,omitempty"` // stable ids: repo url, bundle id, domain, path prefix
	Parent      string   `json:"parent,omitempty"`
	Visibility  string   `json:"visibility,omitempty"`
	CreatedAt   string   `json:"created_at"`
}

// EntityResolution is the outcome of resolving a token to a canonical entity.
type EntityResolution struct {
	Slug   string `json:"slug,omitempty"`
	Status string `json:"status"`        // resolved | multi | unresolved
	Via    string `json:"via,omitempty"` // slug | identifier | alias | path
}

func (s *Store) entitiesLog() string { return s.home + "/ledger/entities.jsonl" }

// RegisterEntity appends an entity to the registry (append-only; last write wins per slug on load).
func (s *Store) RegisterEntity(e Entity, now time.Time) (Entity, error) {
	if strings.TrimSpace(e.Slug) == "" {
		return Entity{}, fmt.Errorf("entity needs a slug")
	}
	if !oneOf(e.Kind, EntityKinds) {
		return Entity{}, fmt.Errorf("entity kind must be one of %v, got %q", EntityKinds, e.Kind)
	}
	if e.Visibility == "" {
		e.Visibility = "operational"
	}
	e.CreatedAt = rfc3339(now)
	return e, appendJSONL(s.entitiesLog(), &e)
}

// Entities loads the registry (append-only, last-wins by slug, deterministic order).
func (s *Store) Entities() ([]Entity, error) {
	if s.entityLoader != nil {
		return s.entityLoader()
	}
	return s.loadEntities()
}

func (s *Store) loadEntities() ([]Entity, error) {
	byID := map[string]Entity{}
	var order []string
	err := readJSONL(s.entitiesLog(), func(line []byte) error {
		var e Entity
		if json.Unmarshal(line, &e) != nil || e.Slug == "" {
			return nil // skip a torn line rather than bricking the registry
		}
		if _, seen := byID[e.Slug]; !seen {
			order = append(order, e.Slug)
		}
		byID[e.Slug] = e
		return nil
	})
	if err != nil {
		return nil, err
	}
	out := make([]Entity, 0, len(order))
	for _, slug := range order {
		out = append(out, byID[slug])
	}
	return out, nil
}

// ResolveEntity maps a token (a slug, an identifier, an alias, or a path under a path-identifier)
// to a canonical entity slug — the path-independent lookup. Precedence slug > identifier > alias >
// path. If exactly one entity matches → resolved; if several distinct slugs match → multi; if none
// → unresolved (the kernel returns unresolved/multi rather than guessing).
func (s *Store) ResolveEntity(token string) (EntityResolution, error) {
	ents, err := s.Entities()
	if err != nil {
		return EntityResolution{}, err
	}
	return resolveEntity(ents, token), nil
}

type entityResolver struct {
	entities []Entity
	cache    map[string]EntityResolution
}

func newEntityResolver(entities []Entity) *entityResolver {
	return &entityResolver{entities: entities, cache: map[string]EntityResolution{}}
}

func (r *entityResolver) Resolve(token string) EntityResolution {
	if resolved, ok := r.cache[token]; ok {
		return resolved
	}
	resolved := resolveEntity(r.entities, token)
	r.cache[token] = resolved
	return resolved
}

func resolveEntity(ents []Entity, token string) EntityResolution {
	tok := strings.TrimSpace(token)
	low := strings.ToLower(tok)
	// rank: slug(3) > identifier(2) > alias(1) > path(0); keep the best tier each entity matches.
	best := map[string]int{} // slug -> tier
	via := map[string]string{}
	consider := func(slug string, tier int, how string) {
		if t, ok := best[slug]; !ok || tier > t {
			best[slug] = tier
			via[slug] = how
		}
	}
	for _, e := range ents {
		switch {
		case e.Slug == tok:
			consider(e.Slug, 3, "slug")
		}
		for _, id := range e.Identifiers {
			if id == tok {
				consider(e.Slug, 2, "identifier")
			} else if isPathPrefix(id, tok) {
				consider(e.Slug, 0, "path")
			}
		}
		for _, a := range e.Aliases {
			if strings.ToLower(a) == low {
				consider(e.Slug, 1, "alias")
			}
		}
	}
	if len(best) == 0 {
		return EntityResolution{Status: "unresolved"}
	}
	highest := -1
	for _, tier := range best {
		if tier > highest {
			highest = tier
		}
	}
	slugs := make([]string, 0, len(best))
	for slug, tier := range best {
		if tier == highest {
			slugs = append(slugs, slug)
		}
	}
	sort.Strings(slugs)
	if len(slugs) > 1 {
		// More than one distinct entity matched at the same strongest tier:
		// preserve uncertainty instead of letting lower-precedence matches
		// force ambiguity over an explicit id or identifier.
		return EntityResolution{Status: "multi", Slug: strings.Join(slugs, ",")}
	}
	return EntityResolution{Slug: slugs[0], Status: "resolved", Via: via[slugs[0]]}
}

// isPathPrefix reports whether tok is the path identifier id itself or a path nested under it —
// so a moved-but-registered path resolves to its entity. Only fires for path-shaped identifiers.
func isPathPrefix(id, tok string) bool {
	if !strings.Contains(id, "/") || id == "" {
		return false
	}
	return tok == id || strings.HasPrefix(tok, strings.TrimRight(id, "/")+"/")
}

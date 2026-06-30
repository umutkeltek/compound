package mem

import "testing"

func TestEntityResolveByAliasIdentifierPath(t *testing.T) {
	s := newStore(t)
	if _, err := s.RegisterEntity(Entity{
		Slug: "project:widget", Kind: "project",
		Aliases:     []string{"Widget", "the-widget"},
		Identifiers: []string{"github.com/acme/widget", "/srv/widget"},
	}, t0); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ token, wantSlug, wantStatus, wantVia string }{
		{"project:widget", "project:widget", "resolved", "slug"},
		{"github.com/acme/widget", "project:widget", "resolved", "identifier"},
		{"WIDGET", "project:widget", "resolved", "alias"},                 // case-insensitive alias
		{"/srv/widget/cmd/main.go", "project:widget", "resolved", "path"}, // a path nested under a path-identifier
		{"unknown-thing", "", "unresolved", ""},
	}
	for _, c := range cases {
		r, err := s.ResolveEntity(c.token)
		if err != nil {
			t.Fatal(err)
		}
		if r.Status != c.wantStatus || r.Slug != c.wantSlug || (c.wantVia != "" && r.Via != c.wantVia) {
			t.Errorf("resolve %q = %+v; want slug=%q status=%q via=%q", c.token, r, c.wantSlug, c.wantStatus, c.wantVia)
		}
	}
}

// Path-independence (the user's "project identity survives a folder move"): the same entity
// resolves by a STABLE identifier even after its path changes, and the new path resolves too.
func TestEntityResolveHighestPrecedenceWins(t *testing.T) {
	s := newStore(t)
	if _, err := s.RegisterEntity(Entity{Slug: "project:active", Kind: "project"}, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterEntity(Entity{Slug: "project:old", Kind: "project", Aliases: []string{"project:active"}}, t0); err != nil {
		t.Fatal(err)
	}
	if r, err := s.ResolveEntity("project:active"); err != nil || r.Status != "resolved" || r.Slug != "project:active" || r.Via != "slug" {
		t.Fatalf("slug must beat lower-tier alias matches globally; got %+v err=%v", r, err)
	}

	if _, err := s.RegisterEntity(Entity{Slug: "project:remote", Kind: "project", Identifiers: []string{"github.com/acme/active"}}, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RegisterEntity(Entity{Slug: "project:alias-only", Kind: "project", Aliases: []string{"github.com/acme/active"}}, t0); err != nil {
		t.Fatal(err)
	}
	if r, err := s.ResolveEntity("github.com/acme/active"); err != nil || r.Status != "resolved" || r.Slug != "project:remote" || r.Via != "identifier" {
		t.Fatalf("identifier must beat lower-tier alias matches globally; got %+v err=%v", r, err)
	}
}

func TestEntitySurvivesPathMove(t *testing.T) {
	s := newStore(t)
	s.RegisterEntity(Entity{Slug: "project:save", Kind: "project", Identifiers: []string{"bundle:com.acme.save"}}, t0)
	// re-register with a NEW path identifier (the folder moved) — last-wins by slug.
	s.RegisterEntity(Entity{Slug: "project:save", Kind: "project", Identifiers: []string{"bundle:com.acme.save", "/work/desktop/save"}}, t0)
	if r, _ := s.ResolveEntity("bundle:com.acme.save"); r.Slug != "project:save" {
		t.Errorf("a stable identifier must still resolve after a move; got %+v", r)
	}
	if r, _ := s.ResolveEntity("/work/desktop/save/x"); r.Status != "resolved" || r.Slug != "project:save" {
		t.Errorf("the new path should resolve to the same entity; got %+v", r)
	}
}

// Ambiguity is surfaced, not guessed: an alias shared by two entities resolves to multi.
func TestEntityAmbiguousResolvesMulti(t *testing.T) {
	s := newStore(t)
	s.RegisterEntity(Entity{Slug: "a", Kind: "concept", Aliases: []string{"shared"}}, t0)
	s.RegisterEntity(Entity{Slug: "b", Kind: "concept", Aliases: []string{"shared"}}, t0)
	if r, _ := s.ResolveEntity("shared"); r.Status != "multi" {
		t.Errorf("a shared alias must resolve to multi (no guessing); got %+v", r)
	}
}

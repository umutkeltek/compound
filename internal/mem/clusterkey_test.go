package mem

import (
	"os"
	"testing"
)

func TestClusterKeyIsPure(t *testing.T) {
	got := ClusterKey(" Project:API ", " Languages ", " Preference ")
	want := "project:api\x1flanguages\x1fpreference"
	if got != want {
		t.Fatalf("ClusterKey = %q, want %q", got, want)
	}
}

func TestStoreClusterKeyResolvesEntityWhenUnambiguous(t *testing.T) {
	s := newStore(t)
	if _, err := s.RegisterEntity(Entity{Slug: "project:api", Kind: "project", Aliases: []string{"API"}}, t0); err != nil {
		t.Fatal(err)
	}
	got, err := s.ClusterKey("api", "language", "decision")
	if err != nil {
		t.Fatal(err)
	}
	want := "project:api\x1flanguage\x1fdecision"
	if got != want {
		t.Fatalf("store ClusterKey = %q, want %q", got, want)
	}
}

func TestCaptureEchoReinforcesCurrentClaimWithoutNewCandidate(t *testing.T) {
	s := newStore(t)
	initial := cand("project:api", "runtime", "go", "")
	initial.Qualifiers = map[string]string{"domain": "language"}
	first, err := s.Capture([]Candidate{initial}, t0)
	if err != nil {
		t.Fatal(err)
	}
	promoted, err := s.Adjudicate(first[0].ID, "promote", "", t0)
	if err != nil {
		t.Fatal(err)
	}
	before := memoryCandidateFileCount(t, s)

	echo := cand("project:api", "implementation_language", " GO ", "")
	echo.Qualifiers = map[string]string{"domain": "language"}
	echo.SourceTraceRef = "trace_echo"
	got, err := s.Capture([]Candidate{echo}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("echo capture returned %d candidate(s), want 0", len(got))
	}
	if after := memoryCandidateFileCount(t, s); after != before {
		t.Fatalf("candidate files after echo = %d, want %d", after, before)
	}

	relations, err := s.loadEdges()
	if err != nil {
		t.Fatal(err)
	}
	var reinforces []Edge
	for _, edge := range relations {
		if edge.Kind == "reinforces" {
			reinforces = append(reinforces, edge)
		}
	}
	if len(reinforces) != 1 {
		t.Fatalf("reinforces edges = %+v, want exactly one", reinforces)
	}
	if reinforces[0].To != promoted.Claim.ID {
		t.Fatalf("reinforces target = %q, want %q", reinforces[0].To, promoted.Claim.ID)
	}
	if reinforces[0].From == "" || reinforces[0].From == first[0].ID {
		t.Fatalf("reinforces source should be new evidence id, got %+v", reinforces[0])
	}
}

func memoryCandidateFileCount(t *testing.T, s *Store) int {
	t.Helper()
	entries, err := os.ReadDir(s.candDir())
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

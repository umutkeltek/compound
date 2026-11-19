package mem

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestQueryBreadthExactGitSnapshot(t *testing.T) {
	const current = "0123456789abcdef0123456789abcdef01234567"
	const previous = "0123456789abcdef0123456789abcdef01234568"
	const absent = "0123456789abcdef0123456789abcdef01234569"
	const sha256 = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"
	s := newStore(t)
	add := func(entity, predicate, object string) Claim {
		t.Helper()
		candidates, err := s.Capture([]Candidate{cand(entity, predicate, object, "")}, t0)
		if err != nil || len(candidates) != 1 {
			t.Fatalf("capture %s.%s: %v, %v", entity, predicate, candidates, err)
		}
		result, err := promoteForTest(t, s, candidates[0].ID, "", t0)
		if err != nil || result.Claim == nil {
			t.Fatalf("promote %s.%s: %v, %v", entity, predicate, result, err)
		}
		return *result.Claim
	}
	add("repo:alder", "head", previous)
	want := add("repo:alder", "head", current)
	want256 := add("repo:birch", "head", sha256)
	bait := add("notes", "snapshot", "repo snapshot head and commit notes mention "+current+" and "+absent)
	add("repo:cedar", "head", "prefix-"+absent)
	add("repo:cedar", "summary", "repo snapshot head "+absent)

	eng := &LexicalEngine{}
	for _, tc := range []struct {
		name  string
		query string
		want  *Claim
	}{
		{"missing", "Does the repo snapshot record head " + absent + " exactly?", nil},
		{"superseded", "Which repository snapshot names commit " + previous + "?", nil},
		{"current", "Which source snapshot names head `" + strings.ToUpper(current) + "`?", &want},
		{"sha256", "Which Git snapshot names commit " + sha256 + "?", &want256},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.QueryBreadth(tc.query, 5, eng)
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == nil {
				if len(got) != 0 {
					t.Fatalf("expected no current heads, got %v", got)
				}
				return
			}
			if len(got) != 1 || got[0].ID != tc.want.ID || got[0].Seq != tc.want.Seq ||
				!reflect.DeepEqual(got[0].EvidenceRefs, tc.want.EvidenceRefs) {
				t.Fatalf("expected original current claim and evidence %v, got %v", tc.want, got)
			}
		})
	}

	for _, query := range []string{
		"snapshot notes",
		"compare repository snapshot commit " + current + " with " + sha256,
		"repo snapshot head " + current[:12],
	} {
		got, err := s.QueryBreadth(query, 10, eng)
		if err != nil || len(got) == 0 {
			t.Fatalf("general breadth query %q: got %v, err %v", query, got, err)
		}
	}

	query := "repo snapshot head " + current
	ledgerBefore, err := s.LedgerDigest()
	if err != nil {
		t.Fatal(err)
	}
	bad := &cannedHitEngine{name: "rented-index", hits: []Hit{{ClaimID: bait.ID}}}
	if _, err := s.QueryBreadth(query, 5, bad); !errors.Is(err, ErrEngineServedUnownedClaim) {
		t.Fatalf("expected refusal for an owned claim outside the literal gate, got %v", err)
	}
	ledgerAfter, err := s.LedgerDigest()
	if err != nil || ledgerBefore != ledgerAfter {
		t.Fatalf("lookup changed ledger: %s -> %s, %v", ledgerBefore, ledgerAfter, err)
	}
}

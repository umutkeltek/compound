package mem

import (
	"reflect"
	"testing"

	"compound/internal/candidates"
)

func candidateCustodyOrderStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"a", "a-", "z"} {
		candidate := Candidate{ID: id, Entity: "entity:" + id, Predicate: "order", Object: id, State: "proposed", Visibility: "candidate"}
		if err := candidates.WriteCandidate(store.home, candidates.Memory, id, candidate); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func TestListCandidatesLiteResumesAfterPrefixIDWithoutGap(t *testing.T) {
	store := candidateCustodyOrderStore(t)
	entries, corrupt, lastWalked, atEnd, err := store.ListCandidatesLite("a", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(corrupt) != 0 {
		t.Fatalf("corrupt = %v", corrupt)
	}
	if len(entries) != 1 || entries[0].ID != "a-" {
		t.Fatalf("resume after a returned %+v, want a- before z", entries)
	}
	if lastWalked != "a-" || atEnd {
		t.Fatalf("progress = last %q atEnd=%v, want a- with work remaining", lastWalked, atEnd)
	}
}

func TestListCandidatesLiteBoundedWalkPreservesCanonicalIDOrder(t *testing.T) {
	store := candidateCustodyOrderStore(t)
	var got []string
	cursor := ""
	for step := 0; step < 4; step++ {
		entries, corrupt, lastWalked, atEnd, err := store.ListCandidatesLite(cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(corrupt) != 0 {
			t.Fatalf("corrupt = %v", corrupt)
		}
		for _, entry := range entries {
			got = append(got, entry.ID)
		}
		if atEnd {
			break
		}
		if lastWalked == "" || lastWalked == cursor {
			t.Fatalf("cursor did not advance: before=%q after=%q", cursor, lastWalked)
		}
		cursor = lastWalked
	}
	if want := []string{"a", "a-", "z"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("bounded walk IDs = %v, want %v", got, want)
	}
}

func TestAllCandidatesReturnsDecodedCanonicalIDOrder(t *testing.T) {
	store := candidateCustodyOrderStore(t)
	all, err := store.AllCandidates()
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, 0, len(all))
	for _, candidate := range all {
		got = append(got, candidate.ID)
	}
	if want := []string{"a", "a-", "z"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("AllCandidates IDs = %v, want %v", got, want)
	}
}

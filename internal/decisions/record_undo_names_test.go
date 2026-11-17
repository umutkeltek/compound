package decisions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/mem"
)

func undoTestRecord(t *testing.T, store *mem.Store, home string, in RecordInput, at time.Time) error {
	t.Helper()
	in.ReviewedBy = "operator:test"
	in.Now = at
	_, err := Record(store, home, in)
	return err
}

func operatorFiles(t *testing.T, home string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, "decisions", "operator"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestRecordUndoOfUnknownNameIsRefusedAndWritesNothing(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	err = undoTestRecord(t, store, home, RecordInput{Kind: KindRuling, ID: "undo-typo", Title: "Undo a typo", UndoOf: "no-such-decision"}, time.Date(2026, 11, 17, 4, 0, 0, 0, time.UTC))
	if err == nil || !strings.Contains(err.Error(), "no-such-decision") {
		t.Fatalf("unknown --undo-of name must be refused and listed, got %v", err)
	}
	if files := operatorFiles(t, home); len(files) != 0 {
		t.Fatalf("refused record wrote files: %v", files)
	}
}

func TestRecordUndoOfCommaListOfKnownNamesRetiresBoth(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 17, 4, 0, 0, 0, time.UTC)
	for i, id := range []string{"rule-one", "rule-two", "rule-keep"} {
		if err := undoTestRecord(t, store, home, RecordInput{Kind: KindRuling, ID: id, Title: "Ruling " + id}, now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if err := undoTestRecord(t, store, home, RecordInput{Kind: KindRuling, ID: "undo-both", Title: "Undo two", UndoOf: " rule-one , decision://rule-two ,"}, now.Add(10*time.Minute)); err != nil {
		t.Fatalf("known names must be accepted: %v", err)
	}
	for _, id := range []string{"rule-one", "rule-two"} {
		if rec, err := LoadActive(store, id); err == nil {
			t.Fatalf("%s still active as %s", id, rec.Ref)
		}
	}
	if _, err := LoadActive(store, "rule-keep"); err != nil {
		t.Fatalf("unnamed record must stay active: %v", err)
	}
	// A retired record is still a known name (any lifecycle state).
	if err := undoTestRecord(t, store, home, RecordInput{Kind: KindRuling, ID: "undo-again", Title: "Undo a retired one", UndoOf: "rule-one"}, now.Add(11*time.Minute)); err != nil {
		t.Fatalf("a retired record is still a known name: %v", err)
	}
}

func TestRecordUndoOfOneKnownOneUnknownIsRefused(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 17, 4, 0, 0, 0, time.UTC)
	if err := undoTestRecord(t, store, home, RecordInput{Kind: KindRuling, ID: "rule-real", Title: "Real ruling"}, now); err != nil {
		t.Fatal(err)
	}
	before := operatorFiles(t, home)
	err = undoTestRecord(t, store, home, RecordInput{Kind: KindRuling, ID: "undo-mixed", Title: "Undo mixed", UndoOf: "rule-real, rule-typo"}, now.Add(time.Minute))
	if err == nil || !strings.Contains(err.Error(), "rule-typo") || strings.Contains(err.Error(), "rule-real,") {
		t.Fatalf("only the unknown name must be listed, got %v", err)
	}
	if after := operatorFiles(t, home); len(after) != len(before) {
		t.Fatalf("refused record wrote a file: before=%v after=%v", before, after)
	}
	if _, err := LoadActive(store, "rule-real"); err != nil {
		t.Fatalf("known name must not be retired by a refused record: %v", err)
	}
}

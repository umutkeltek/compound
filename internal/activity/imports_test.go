package activity

import (
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
	"time"
)

// TestFalsifier4And6_NoForbiddenImports is the static guard: internal/activity must NOT import the
// memory candidate machinery (mem/candidates — it mints ZERO auto-candidates) nor the event store
// (eventstore — segments land ONLY under observed/activity/, never in the event store). This is a
// by-construction invariant: an import here would be a silent architecture regression that no runtime
// test could reliably catch.
func TestFalsifier4And6_NoForbiddenImports(t *testing.T) {
	forbidden := map[string]string{
		"compound/internal/mem":        "activity mints zero auto memory candidates (F4)",
		"compound/internal/candidates": "activity mints zero auto memory candidates (F4)",
		"compound/internal/eventstore": "activity segments land only under observed/activity/, never the event store (F6)",
	}
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if perr != nil {
			t.Fatalf("parse %s: %v", name, perr)
		}
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if reason, bad := forbidden[path]; bad {
				t.Fatalf("%s imports forbidden package %q — %s", name, path, reason)
			}
		}
	}
}

// TestFalsifier4_DrainMintsZeroCandidates: after a successful drain on a temp home, the memory
// candidate inbox has ZERO entries. Activity exhaust never floods the inbox the ask-triage work drained.
func TestFalsifier4_DrainMintsZeroCandidates(t *testing.T) {
	h := newTestHome(t)
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	cfg := enableSensor(t, h, now)
	spool := writeSpool(t, drainEvents)

	rep, err := Drain(h, spool, cfg, "work_drain", now)
	if err != nil {
		t.Fatalf("drain err: %v", err)
	}
	if rep.SegmentLinesAdded == 0 {
		t.Fatal("precondition: drain should have ingested events")
	}
	// The candidate inbox must be empty (or absent) after a real drain.
	candDir := h + "/inbox/memory-candidates"
	entries, err := os.ReadDir(candDir)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read candidate inbox: %v", err)
	}
	count := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			count++
		}
	}
	if count != 0 {
		t.Fatalf("activity drain minted %d memory candidate(s); must be zero", count)
	}
}

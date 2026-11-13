package work

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The work view feeds claims, dispatch and the operator console, so a faster
// build must return the same projection. These tests build the fixture once and
// compare the production (fast) path with the reference path, which keeps the
// pre-optimization algorithm, byte for byte. Dropping, adding or reordering any
// row, count or warning in the fast path fails them. When the projection's
// meaning changes on purpose, change both paths in the same commit.

func TestWorkbenchFastPathMatchesReferenceProjection(t *testing.T) {
	t.Parallel()
	s := newEquivalenceStore(t)
	fx := buildWorkViewEquivalenceFixture(t, s, fixedNow)
	cases := []WorkbenchOptions{
		{},
		{RecentLimit: 1},
		{RecentLimit: 3},
		{RecentLimit: 8},
		{RecentLimit: 1000},
		{RecentLimit: 5, Actor: "codex"},
		{RecentLimit: 8, Actor: "claude"},
		{RecentLimit: 5, IncludeArchived: true},
	}
	for _, opts := range cases {
		name := fmt.Sprintf("recent=%d/actor=%s", opts.RecentLimit, opts.Actor)
		fast, err := s.workbench(fixedNow, opts, workbenchFastPath)
		if err != nil {
			t.Fatalf("%s fast: %v", name, err)
		}
		ref, err := s.workbench(fixedNow, opts, workbenchReferencePath)
		if err != nil {
			t.Fatalf("%s reference: %v", name, err)
		}
		assertSameJSON(t, name+" workbench", fast, ref)

		fastExecutors, err := s.ExecutorsProjection(fixedNow, ExecutorsProjectionOptions{Workbench: &fast})
		if err != nil {
			t.Fatalf("%s fast executors: %v", name, err)
		}
		refExecutors, err := s.ExecutorsProjection(fixedNow, ExecutorsProjectionOptions{Workbench: &ref})
		if err != nil {
			t.Fatalf("%s reference executors: %v", name, err)
		}
		assertSameJSON(t, name+" executors", fastExecutors, refExecutors)
	}

	// The fixture must stay rich enough to catch a dropped row: every run file
	// is visible at a wide limit, unreadable files keep their warnings, and a
	// kept row at the default limit really went through terminal resolution.
	wide, err := s.workbench(fixedNow, WorkbenchOptions{RecentLimit: 1000}, workbenchFastPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(wide.ContinueRuns) != fx.ContinueRuns || len(wide.DispatchStepRuns) != fx.DispatchRuns {
		t.Fatalf("wide view runs = %d continue / %d dispatch, want %d / %d", len(wide.ContinueRuns), len(wide.DispatchStepRuns), fx.ContinueRuns, fx.DispatchRuns)
	}
	if len(wide.Recent) != len(fx.Terminal) || len(wide.Open) != len(fx.Open) {
		t.Fatalf("wide view rows = %d recent / %d open, want %d / %d", len(wide.Recent), len(wide.Open), len(fx.Terminal), len(fx.Open))
	}
	for _, want := range []string{"continue run wconr_eqvbad.json unreadable", "continue run wconr_eqvtype.json unreadable", "dispatch step run wdsr_eqvbad.json unreadable"} {
		if !integrityHasPrefix(wide.Integrity, want) {
			t.Fatalf("integrity lost %q: %q", want, wide.Integrity)
		}
	}
	readsBefore := s.eventLogReadCount()
	narrow, err := s.workbench(fixedNow, WorkbenchOptions{}, workbenchFastPath)
	if err != nil {
		t.Fatal(err)
	}
	if reads := s.eventLogReadCount() - readsBefore; reads != 1 {
		t.Fatalf("fast build read the event log %d times, want 1 (the active-packet proof reuses it)", reads)
	}
	if narrow.ActivePacketProof == nil || narrow.ActivePacketProof.WorkID != fx.ArchivedPacket {
		t.Fatalf("active packet proof must select the newest, archived packet work %s: %+v", fx.ArchivedPacket, narrow.ActivePacketProof)
	}
	if !continueRunsHaveStatus(narrow.ContinueRuns, "resolved") || !continueRunsHaveStatus(narrow.ContinueRuns, "waiting") {
		t.Fatalf("default view must keep both resolved and waiting runs: %+v", narrow.ContinueRuns)
	}
}

// TestWorkbenchResolvesOnlyKeptRunFiles proves the cost cut: run files beyond
// the recent limit are still read (their warnings count) but never resolved, so
// their referenced WorkEnvelopes are never opened.
func TestWorkbenchResolvesOnlyKeptRunFiles(t *testing.T) {
	t.Parallel()
	s := newEquivalenceStore(t)
	fx := buildWorkViewEquivalenceFixture(t, s, fixedNow)
	for _, limit := range []int{1, 5, 8} {
		var fastResolved, refResolved int
		fast, fastWarnings := s.workbenchContinueRunsWith(limit, workbenchFastPath, func(summary ContinueRunSummary) ContinueRunSummary {
			fastResolved++
			return s.resolveContinueRunSummary(summary, nil)
		})
		ref, refWarnings := s.workbenchContinueRunsWith(limit, workbenchReferencePath, func(summary ContinueRunSummary) ContinueRunSummary {
			refResolved++
			return s.resolveContinueRunSummary(summary, nil)
		})
		if fastResolved != limit || refResolved != fx.ContinueRuns {
			t.Fatalf("limit %d: resolved fast=%d reference=%d, want %d and %d", limit, fastResolved, refResolved, limit, fx.ContinueRuns)
		}
		assertSameJSON(t, fmt.Sprintf("continue runs limit %d", limit), fast, ref)
		assertSameJSON(t, fmt.Sprintf("continue warnings limit %d", limit), fastWarnings, refWarnings)

		fastResolved, refResolved = 0, 0
		fastDispatch, fastDispatchWarnings := s.workbenchDispatchStepRunsWith(limit, workbenchFastPath, func(summary DispatchStepRunSummary) DispatchStepRunSummary {
			fastResolved++
			return s.resolveDispatchStepRunSummary(summary, nil)
		})
		refDispatch, refDispatchWarnings := s.workbenchDispatchStepRunsWith(limit, workbenchReferencePath, func(summary DispatchStepRunSummary) DispatchStepRunSummary {
			refResolved++
			return s.resolveDispatchStepRunSummary(summary, nil)
		})
		if fastResolved != limit || refResolved != fx.DispatchRuns {
			t.Fatalf("limit %d: dispatch resolved fast=%d reference=%d, want %d and %d", limit, fastResolved, refResolved, limit, fx.DispatchRuns)
		}
		assertSameJSON(t, fmt.Sprintf("dispatch runs limit %d", limit), fastDispatch, refDispatch)
		assertSameJSON(t, fmt.Sprintf("dispatch warnings limit %d", limit), fastDispatchWarnings, refDispatchWarnings)
	}
}

func assertSameJSON(t *testing.T, name string, got, want any) {
	t.Helper()
	gotJSON, err := json.MarshalIndent(got, "", " ")
	if err != nil {
		t.Fatalf("%s: marshal fast: %v", name, err)
	}
	wantJSON, err := json.MarshalIndent(want, "", " ")
	if err != nil {
		t.Fatalf("%s: marshal reference: %v", name, err)
	}
	if bytes.Equal(gotJSON, wantJSON) {
		return
	}
	gotLines, wantLines := strings.Split(string(gotJSON), "\n"), strings.Split(string(wantJSON), "\n")
	for i := 0; i < len(gotLines) || i < len(wantLines); i++ {
		var g, w string
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if g != w {
			t.Fatalf("%s: fast path differs from reference at line %d\nfast:      %s\nreference: %s", name, i+1, g, w)
		}
	}
}

func integrityHasPrefix(integrity []string, prefix string) bool {
	for _, line := range integrity {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

func continueRunsHaveStatus(runs []ContinueRunSummary, status string) bool {
	for _, run := range runs {
		if run.Status == status {
			return true
		}
	}
	return false
}

package main

import (
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// TestProofCheckSmokeInventoryGuard fails whenever a console/scripts/*smoke*.mjs file is
// neither wired into proofCheckSteps as a proof-check gate nor named in the manual-only
// allowlist below with a non-empty reason. A console smoke has gone silently unwired
// before (a new smoke lands, proof-check never learns about it, and it never runs
// anywhere) — this guard turns that into a loud, named test failure instead of quiet
// proof drift.
func TestProofCheckSmokeInventoryGuard(t *testing.T) {
	// manualOnlySmokes lists every *smoke*.mjs that is intentionally NOT wired into
	// proofCheckSteps, with the reason it cannot run in the portable proof-check path.
	// Every entry here must still exist on disk and must NOT also be wired as a step —
	// an allowlist row with no backing reason, or a stale row for a deleted file,
	// defeats the point of the guard.
	manualOnlySmokes := map[string]string{
		"work-mutation-trail-smoke.mjs": "requires COMPOUND_BIN (a freshly built compound binary); exercised manually / in strict-local batteries, not in the portable proof-check path",
		"work-legibility-smoke.mjs":     "static copy-model assertion (imports console WORK_COPY, no binary/home needed); run manually / in local batteries — wiring into the portable proof-check step list is out of the work-page-legibility packet's file scope (proof_check.go owned by a concurrent lane at packet time)",
		// Runs the real `home doctor` + `executors` doors to pin the two stale-lease
		// definitions; its proof_check.go registration is owned by a live concurrent
		// lane at land time. Wire in and DELETE this row in the registration change
		// (allowlisted-and-wired turns this guard red — cleanup is self-enforcing).
		"agents-consistency-smoke.mjs": "registration pending: proof_check.go owned by a live concurrent lane at land time; needs COMPOUND_BIN; wire in and DELETE this row in the registration change",
		// The two rows below are TEMPORARY: their proofCheckSteps registration is owned by a
		// concurrent live lane (proof_check.go was not editable when this guard landed). Wiring
		// them in WITHOUT deleting these rows turns this guard red (allowlisted-and-wired), so
		// cleanup is self-enforcing in the registration change.
		"projection-degradation-smoke.mjs": "registration pending: proof_check.go owned by a live concurrent lane at guard-landing time; wire in and DELETE this row in the registration change",
		"projection-doors-smoke.mjs":       "registration pending: proof_check.go owned by a live concurrent lane at guard-landing time; wire in and DELETE this row in the registration change",
		"stream-door-smoke.mjs":            "registration pending: proof_check.go owned by a live concurrent lane (work_cb8b0961355c) at land time; requires a freshly built COMPOUND_BIN with the `stream` verb; wire in and DELETE this row in the registration change",
	}

	repoRoot := inventoryGuardRepoRoot(t)

	steps, err := proofCheckSteps("")
	if err != nil {
		t.Fatalf("proofCheckSteps(\"\"): %v", err)
	}

	wired := map[string]bool{}
	for _, step := range steps {
		for _, arg := range step.Command {
			if strings.HasSuffix(arg, ".mjs") {
				wired[filepath.Base(arg)] = true
			}
		}
	}

	pattern := filepath.Join(repoRoot, "console", "scripts", "*smoke*.mjs")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("glob %s: %v", pattern, err)
	}
	if len(matches) == 0 {
		t.Fatalf("glob %s matched no files; repo root resolution is likely wrong", pattern)
	}

	onDisk := map[string]bool{}
	for _, m := range matches {
		onDisk[filepath.Base(m)] = true
	}

	var unclassified []string
	for name := range onDisk {
		if wired[name] {
			continue
		}
		if reason, allowlisted := manualOnlySmokes[name]; allowlisted && strings.TrimSpace(reason) != "" {
			continue
		}
		unclassified = append(unclassified, name)
	}
	sort.Strings(unclassified)
	if len(unclassified) > 0 {
		t.Errorf(
			"console/scripts smoke(s) neither wired into proofCheckSteps nor allowlisted with a reason: %s\n"+
				"remedy: wire each into proofCheckSteps (cmd/compound/proof_check.go) as a console-smoke step, "+
				"OR add it to manualOnlySmokes in this test with a non-empty reason",
			strings.Join(unclassified, ", "),
		)
	}

	var wiredAndAllowlisted []string
	for name := range manualOnlySmokes {
		if wired[name] {
			wiredAndAllowlisted = append(wiredAndAllowlisted, name)
		}
	}
	sort.Strings(wiredAndAllowlisted)
	if len(wiredAndAllowlisted) > 0 {
		t.Errorf(
			"console/scripts smoke(s) are both wired into proofCheckSteps and manual-only allowlisted (pick one): %s",
			strings.Join(wiredAndAllowlisted, ", "),
		)
	}

	var stale []string
	for name, reason := range manualOnlySmokes {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("manual-only allowlist entry %q has an empty reason", name)
		}
		if !onDisk[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf(
			"manual-only allowlist entry(ies) reference file(s) no longer on disk (stale allowlist row, remove): %s",
			strings.Join(stale, ", "),
		)
	}
}

// inventoryGuardRepoRoot resolves the repository root from this test file's own path so the
// guard works regardless of the working directory `go test` is invoked from.
func inventoryGuardRepoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed; cannot resolve repo root")
	}
	// this file lives at <repoRoot>/cmd/compound/proof_check_inventory_test.go
	return filepath.Dir(filepath.Dir(filepath.Dir(thisFile)))
}

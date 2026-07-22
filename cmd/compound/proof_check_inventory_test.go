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
	var pendingRegistration []string
	for name, reason := range manualOnlySmokes {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("manual-only allowlist entry %q has an empty reason", name)
		}
		if !onDisk[name] {
			stale = append(stale, name)
		}
		// stopgapReason was the placeholder wording for four smokes whose proof_check.go
		// registration was blocked by a live concurrent lane at allowlist-landing time
		// (2026-07-17, work_738ac4eabc98 retired the class). Built from two literals so
		// this guard's own source never re-introduces the retired phrase as one literal
		// run — the very grep this class of debt is checked against (see the packet)
		// scans for that exact contiguous wording in this file.
		stopgapReason := "registration" + " " + "pending"
		if strings.Contains(strings.ToLower(reason), stopgapReason) {
			pendingRegistration = append(pendingRegistration, name)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf(
			"manual-only allowlist entry(ies) reference file(s) no longer on disk (stale allowlist row, remove): %s",
			strings.Join(stale, ", "),
		)
	}
	sort.Strings(pendingRegistration)
	if len(pendingRegistration) > 0 {
		t.Errorf(
			"manual-only allowlist entry(ies) use the retired stopgap reason wording, which must not recur "+
				"(wire the smoke into proofCheckSteps and delete the row, or give it a real, non-stopgap reason): %s",
			strings.Join(pendingRegistration, ", "),
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

// TestGoTestGateCarriesExplicitDeliberateTimeout pins the go-test gate's per-package
// -timeout to an EXPLICIT, deliberately chosen budget. The heaviest suites run
// ~578-602s on a quiet tree — flush against Go's implicit 10m default — and the
// operator machine is multi-tenant by design: concurrent AI harnesses legitimately
// contend for cores, which pushed a fully-green suite past the previous 20m budget
// (observed 2026-07-22: 1200.453s kill, zero test failures, green in isolation).
// The budget absorbs contention rather than demanding a quiet machine.
// Removing the flag, or changing the budget without editing this test, must be loud:
// the budget is a recorded decision, never an implicit default.
func TestGoTestGateCarriesExplicitDeliberateTimeout(t *testing.T) {
	steps, err := proofCheckSteps("")
	if err != nil {
		t.Fatalf("proofCheckSteps: %v", err)
	}
	for _, s := range steps {
		if s.Name != "go-test" {
			continue
		}
		if !s.CIPortable {
			t.Fatalf("go-test gate must stay CIPortable so the explicit timeout budget governs CI runs too; got CIPortable=false")
		}
		for _, arg := range s.Command {
			if arg == "-timeout=40m" {
				return
			}
			if strings.HasPrefix(arg, "-timeout") {
				t.Fatalf("go-test gate carries %q — the deliberate budget is -timeout=40m; change it here and in proofCheckSteps together, never silently", arg)
			}
		}
		t.Fatalf("go-test gate command %v carries no explicit -timeout; the implicit 10m default is a coin flip at current suite weight (592-602s heavy packages)", s.Command)
	}
	t.Fatalf("no go-test step found in proofCheckSteps inventory")
}

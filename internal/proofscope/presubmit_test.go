package proofscope

import (
	"reflect"
	"testing"
)

// presubmitFixture is a CLI-shaped module: cmd/compound prod-imports
// internal/work, which prod-imports internal/landlease; internal/obs only
// test-imports internal/landlease; internal/policy and internal/solo are
// unrelated; internal/arch and internal/rr read the repository root; and a
// cmd/compound test names the landing skill by path.
func presubmitFixture() map[string]string {
	caller := func(pkg string) string {
		return "package " + pkg + "\nimport (\n\t\"runtime\"\n\t\"testing\"\n)\nfunc TestRoot(t *testing.T) { _, _, _, _ = runtime.Caller(0) }\n"
	}
	return map[string]string{
		"go.mod":                                    "module compound\n\ngo 1.23\n",
		"AGENTS.md":                                 "# agents\n",
		"docs/guide.md":                             "guide\n",
		"skills/compound-land/SKILL.md":             "# land\n",
		"cmd/compound/main.go":                      "package main\nimport (\n\t_ \"compound/internal/policy\"\n\t_ \"compound/internal/work\"\n)\nfunc main() {}\n",
		"cmd/compound/skill_test.go":                "package main\nimport (\n\t\"path/filepath\"\n\t\"testing\"\n)\nfunc TestCeremony(t *testing.T) { _ = filepath.Join(\"..\", \"..\", \"skills\", \"compound-land\", \"SKILL.md\") }\n",
		"internal/arch/arch.go":                     "package arch\n",
		"internal/arch/arch_test.go":                caller("arch"),
		"internal/rr/rr.go":                         "package rr\n",
		"internal/rr/rr_test.go":                    caller("rr"),
		"internal/landlease/lease.go":               "package landlease\n",
		"internal/work/work.go":                     "package work\nimport _ \"compound/internal/landlease\"\n",
		"internal/obs/obs.go":                       "package obs\n",
		"internal/obs/obs_test.go":                  "package obs\nimport (\n\t\"testing\"\n\t_ \"compound/internal/landlease\"\n)\nfunc TestObs(t *testing.T) {}\n",
		"internal/policy/policy.go":                 "package policy\n",
		"internal/solo/solo.go":                     "package solo\n",
		"internal/solo/solo_test.go":                "package solo\nimport \"testing\"\nfunc TestSolo(t *testing.T) {}\n",
		"internal/landlease/testdata/fixture.json":  "{}\n",
		"internal/plain/plain.go":                   "package plain\n",
		"internal/caller/caller.go":                 "package caller\nimport _ \"compound/internal/plain\"\n",
		"cmd/compound/plain_user.go":                "package main\nimport _ \"compound/internal/caller\"\n",
		"internal/policy/testdata/rules/rules.yaml": "rules: []\n",
	}
}

func TestPresubmitDerivesTheLandingRule(t *testing.T) {
	files := presubmitFixture()
	cli := PresubmitCLIPackage
	inventory := InventoryPackages(snapshot(files, false).Paths)
	if len(inventory) != 10 || inventory[0] != cli {
		t.Fatalf("inventory = %v; want the eight fixture packages", inventory)
	}
	readers := PresubmitRun{Packages: []string{cli}, Run: PresubmitCLIReaders}
	for _, tc := range []struct {
		name     string
		base     map[string]string
		changed  []string
		mode     Mode
		reason   string
		runs     []PresubmitRun
		deferred []string
	}{
		{
			name:     "docs, skills and AGENTS.md run arch plus the CLI doctrine readers only",
			changed:  []string{"AGENTS.md", "docs/guide.md", "skills/compound-land/SKILL.md"},
			mode:     Affected,
			reason:   "diff",
			runs:     []PresubmitRun{{Packages: pkgs("arch")}, readers},
			deferred: pkgs("rr"),
		},
		{
			name:     "a governance-core change keeps its reverse dependents and the whole CLI",
			changed:  []string{"internal/landlease/lease.go"},
			mode:     Affected,
			reason:   "diff",
			runs:     []PresubmitRun{{Packages: []string{cli, "compound/internal/arch", "compound/internal/landlease", "compound/internal/obs", "compound/internal/work"}}},
			deferred: pkgs("rr"),
		},
		{
			name:     "an ordinary change runs its own package and defers its reverse dependents",
			changed:  []string{"internal/plain/plain.go"},
			mode:     Affected,
			reason:   "diff",
			runs:     []PresubmitRun{{Packages: pkgs("arch", "plain")}, readers},
			deferred: []string{cli, "compound/internal/caller", "compound/internal/rr"},
		},
		{
			name:     "a change inside the CLI runs the whole CLI",
			changed:  []string{"cmd/compound/main.go"},
			mode:     Affected,
			reason:   "diff",
			runs:     []PresubmitRun{{Packages: []string{cli, "compound/internal/arch"}}},
			deferred: pkgs("rr"),
		},
		{
			name:     "a package nothing imports keeps the CLI restricted",
			changed:  []string{"internal/solo/solo.go"},
			mode:     Affected,
			reason:   "diff",
			runs:     []PresubmitRun{{Packages: pkgs("arch", "solo")}, readers},
			deferred: pkgs("rr"),
		},
		{
			name:    "go.mod runs the whole module",
			changed: []string{"go.mod"},
			mode:    Full,
			reason:  "module-files:go.mod",
			runs:    []PresubmitRun{{Packages: inventory}},
		},
		{
			name:    "go.sum runs the whole module",
			changed: []string{"go.sum"},
			mode:    Full,
			reason:  "module-files:go.sum",
			runs:    []PresubmitRun{{Packages: inventory}},
		},
		{
			name:    "the battery driver runs the whole module",
			changed: []string{"scripts/testbattery/main.go"},
			mode:    Full,
			reason:  "scope-owner:scripts/testbattery/main.go",
			runs:    []PresubmitRun{{Packages: inventory}},
		},
		{
			name:    "a root file runs the whole module",
			changed: []string{"install.sh"},
			mode:    Full,
			reason:  "root-file:install.sh",
			runs:    []PresubmitRun{{Packages: inventory}},
		},
		{
			name:    "a removed package runs the whole module",
			base:    with(files, map[string]string{"internal/gone/gone.go": "package gone\n"}),
			changed: []string{"internal/gone/gone.go"},
			mode:    Full,
			reason:  "package-removed:internal/gone",
			runs:    []PresubmitRun{{Packages: inventory}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := tc.base
			if base == nil {
				base = files
			}
			plan := Presubmit(derive(base, files, tc.changed...), inventory)
			if plan.Scope.Mode != tc.mode || plan.Scope.Reason != tc.reason {
				t.Fatalf("scope = %s/%s; want %s/%s", plan.Scope.Mode, plan.Scope.Reason, tc.mode, tc.reason)
			}
			if !reflect.DeepEqual(plan.Runs, tc.runs) {
				t.Fatalf("runs = %+v; want %+v", plan.Runs, tc.runs)
			}
			if !reflect.DeepEqual(plan.Deferred, tc.deferred) {
				t.Fatalf("deferred = %v; want %v", plan.Deferred, tc.deferred)
			}
		})
	}
}

// A test that names a changed path runs even when no import edge reaches it,
// but a path reference alone never lifts the CLI restriction: the doctrine
// readers stay the CLI's minutes-scale presubmit price.
func TestPresubmitPathReadersRunWithoutLiftingTheCLIRestriction(t *testing.T) {
	files := presubmitFixture()
	files["internal/policy/policy_test.go"] = "package policy\nimport (\n\t\"path/filepath\"\n\t\"testing\"\n)\nfunc TestRules(t *testing.T) { _ = filepath.Join(\"..\", \"..\", \"docs\") }\n"
	scope := derive(files, files, "skills/compound-land/SKILL.md")
	if !reflect.DeepEqual(scope.PathReaders, []string{PresubmitCLIPackage}) {
		t.Fatalf("path readers = %v; want the CLI skill reader", scope.PathReaders)
	}
	plan := Presubmit(scope, nil)
	want := []PresubmitRun{{Packages: pkgs("arch")}, {Packages: []string{PresubmitCLIPackage}, Run: PresubmitCLIReaders}}
	if !reflect.DeepEqual(plan.Runs, want) {
		t.Fatalf("runs = %+v; want %+v", plan.Runs, want)
	}

	scope = derive(files, files, "docs/guide.md")
	if !reflect.DeepEqual(scope.PathReaders, pkgs("policy")) || len(scope.Reached) != 0 {
		t.Fatalf("docs reader scope = %+v; want policy as the only path reader", scope)
	}
	plan = Presubmit(scope, nil)
	want = []PresubmitRun{{Packages: pkgs("arch", "policy")}, {Packages: []string{PresubmitCLIPackage}, Run: PresubmitCLIReaders}}
	if !reflect.DeepEqual(plan.Runs, want) {
		t.Fatalf("runs = %+v; want %+v", plan.Runs, want)
	}
}

// The certifying scope keeps every root reader; presubmit only defers them.
func TestPresubmitNeverNarrowsTheCertifyingScope(t *testing.T) {
	files := presubmitFixture()
	scope := derive(files, files, "internal/solo/solo.go")
	want := pkgs("arch", "rr", "solo")
	if !reflect.DeepEqual(scope.Packages, want) {
		t.Fatalf("certifying packages = %v; want %v", scope.Packages, want)
	}
	if !reflect.DeepEqual(scope.Reached, pkgs("solo")) {
		t.Fatalf("reached = %v; want solo", scope.Reached)
	}
}

// The governance core is an exact list: one file of a hub package stays
// ordinary while a proof or close file of the same package keeps the wide plan.
func TestGovernanceCoreMatchesTheDecisionOwnersOnly(t *testing.T) {
	for p, want := range map[string]bool{
		"internal/landlease/lease.go":         true,
		"internal/policy/testdata/rules.yaml": true,
		"internal/work/work.go":               true,
		"internal/work/proof_presubmit.go":    true,
		"internal/work/land_check.go":         true,
		"cmd/compound/proof_check_dev.go":     true,
		"internal/work/dispatch.go":           false,
		"internal/work/sub/proof_helper.go":   false,
		"internal/workbench/proof_view.go":    false,
		"cmd/compound/main.go":                false,
		"docs/architecture/runtime.md":        false,
	} {
		if got := firstGovernanceCorePath([]string{p}) != ""; got != want {
			t.Errorf("%s in governance core = %v; want %v", p, got, want)
		}
	}
}

// A ".." row in a traversal-rejection table names the parent of the test's own
// package. That once put the package in every plan under that parent. It stays
// certifying and is reported as deferred, but it does not run before the land.
func TestPresubmitDefersAParentDirectoryLiteral(t *testing.T) {
	files := presubmitFixture()
	files["internal/guard/guard.go"] = "package guard\n"
	files["internal/guard/guard_test.go"] = "package guard\nimport \"testing\"\nfunc TestRejects(t *testing.T) { _ = map[string]string{\"directory\": \"..\"} }\n"
	scope := derive(files, files, "internal/solo/solo.go")
	if !reflect.DeepEqual(scope.AncestorReaders, pkgs("guard")) || len(scope.PathReaders) != 0 {
		t.Fatalf("readers = ancestor %v path %v; want guard as the ancestor reader only", scope.AncestorReaders, scope.PathReaders)
	}
	if want := pkgs("arch", "guard", "rr", "solo"); !reflect.DeepEqual(scope.Packages, want) {
		t.Fatalf("certifying packages = %v; want %v", scope.Packages, want)
	}
	plan := Presubmit(scope, nil)
	want := []PresubmitRun{{Packages: pkgs("arch", "solo")}, {Packages: []string{PresubmitCLIPackage}, Run: PresubmitCLIReaders}}
	if !reflect.DeepEqual(plan.Runs, want) {
		t.Fatalf("runs = %+v; want %+v", plan.Runs, want)
	}
	if want := pkgs("guard", "rr"); !reflect.DeepEqual(plan.Deferred, want) {
		t.Fatalf("deferred = %v; want %v", plan.Deferred, want)
	}

	// A test that climbs out of its package to a changed file is still a reader.
	files["internal/guard/guard_test.go"] = "package guard\nimport (\n\t\"path/filepath\"\n\t\"testing\"\n)\nfunc TestReads(t *testing.T) { _ = filepath.Join(\"..\", \"solo\", \"solo.go\") }\n"
	scope = derive(files, files, "internal/solo/solo.go")
	if !reflect.DeepEqual(scope.PathReaders, pkgs("guard")) || len(scope.AncestorReaders) != 0 {
		t.Fatalf("readers = ancestor %v path %v; want guard as a path reader", scope.AncestorReaders, scope.PathReaders)
	}
}

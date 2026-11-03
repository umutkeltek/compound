package proofscope

import (
	"path"
	"slices"
	"sort"
	"strings"
)

// Presubmit is the pre-landing slice of a scope. It never replaces the
// canonical battery: a certifying run still uses Scope.Packages, which keeps
// every root reader.
//
// Rule (skills/compound-land/SKILL.md §1, operator ruling 2026-10-23):
//   - Full scope (go.mod/go.sum, scripts/testbattery, internal/proofscope, a
//     root file, a removed package, ...) runs every package of the Git
//     inventory at head, never a `./...` walk that would reach gitignored
//     directories.
//   - A changed path in the governance core also runs every package reached
//     from a changed package by import or test edges.
//   - Otherwise it runs the changed packages only, every package whose tests
//     name a changed path (a test naming only a parent of its own package does
//     not count), and internal/arch (every served-doctrine guard
//     lives there). The build and vet gates still compile every package, so
//     a broken caller stops the landing. Reverse dependents are deferred to
//     the post-land verifier and the canonical battery.
//   - cmd/compound imports nearly every package. It runs whole when it is in
//     the selected set by the rules above; otherwise only its doctrine readers
//     run, by name.
const (
	PresubmitCLIPackage      = modulePath + "/cmd/compound"
	PresubmitDoctrinePackage = modulePath + "/internal/arch"
	// PresubmitCLIReaders names the cmd/compound tests that read served
	// doctrine (skills, contracts, AGENTS.md).
	PresubmitCLIReaders = "Ceremony|ContractCompile|Orient|ProofCheck|Doctor"
)

// PresubmitRun is one `go test -short` invocation.
type PresubmitRun struct {
	Packages []string `json:"packages"`
	Run      string   `json:"run,omitempty"`
}

// PresubmitPlan is the go-test part of one presubmit.
type PresubmitPlan struct {
	Scope Scope          `json:"scope"`
	Runs  []PresubmitRun `json:"runs"`
	// Deferred lists root readers presubmit does not run; the canonical
	// battery after land covers them.
	Deferred []string `json:"deferred,omitempty"`
}

// InventoryPackages is the sorted import paths of every package directory in
// a Git tree listing, by the testbattery source-inventory rules.
func InventoryPackages(paths []string) []string {
	return importPaths(packageDirs(paths))
}

// Presubmit derives the go-test runs for a scope. inventory is the head
// package set a Full scope runs (InventoryPackages of the head tree).
func Presubmit(scope Scope, inventory []string) PresubmitPlan {
	plan := PresubmitPlan{Scope: scope}
	if scope.Mode != Affected {
		plan.Runs = []PresubmitRun{{Packages: append([]string(nil), inventory...)}}
		return plan
	}
	base := scope.Changed
	if scope.GovernanceCore != "" {
		base = scope.Reached
	}
	inBase := make(map[string]bool, len(base))
	for _, pkg := range base {
		inBase[pkg] = true
	}
	selected := make(map[string]bool)
	for _, pkg := range scope.RootReaders {
		if pkg == PresubmitDoctrinePackage {
			selected[pkg] = true // pinned reader, present at head
		}
	}
	for _, list := range [][]string{base, scope.PathReaders} {
		for _, pkg := range list {
			selected[pkg] = true
		}
	}
	cliWhole := inBase[PresubmitCLIPackage]
	delete(selected, PresubmitCLIPackage)
	affected := PresubmitRun{Packages: sortedKeys(selected)}
	if cliWhole {
		affected.Packages = append(affected.Packages, PresubmitCLIPackage)
		sort.Strings(affected.Packages)
	}
	plan.Runs = append(plan.Runs, affected)
	if !cliWhole {
		plan.Runs = append(plan.Runs, PresubmitRun{Packages: []string{PresubmitCLIPackage}, Run: PresubmitCLIReaders})
	}
	deferred := make(map[string]bool)
	for _, list := range [][]string{scope.RootReaders, scope.Reached, scope.AncestorReaders} {
		for _, pkg := range list {
			if !selected[pkg] && !(cliWhole && pkg == PresubmitCLIPackage) {
				deferred[pkg] = true
			}
		}
	}
	// A root reader's whole-CLI run was never promised here; only a reached CLI
	// that the rule above narrowed is a deferral to report.
	if !slices.Contains(scope.Reached, PresubmitCLIPackage) {
		delete(deferred, PresubmitCLIPackage)
	}
	plan.Deferred = sortedKeys(deferred)
	return plan
}

// governanceCorePaths decides whether a proof, landing, close or publication
// is legitimate. An entry ending in "/" matches its tree; an entry ending in
// "*" matches that name prefix in its own directory; otherwise the exact path.
var governanceCorePaths = []string{
	"cmd/compound/proof_check*",
	"internal/authority/",
	"internal/landlease/",
	"internal/policy/",
	"internal/work/canonical_*",
	"internal/work/close_*",
	"internal/work/differential_proof*",
	"internal/work/git_delivery_close*",
	"internal/work/land_check*",
	"internal/work/landing_resource*",
	"internal/work/lease_close*",
	"internal/work/private_release_tag*",
	"internal/work/private_source_publish*",
	"internal/work/proof_*",
	"internal/work/standing_proof*",
	"internal/work/terminal_close_proof*",
	"internal/work/work.go",
	"internal/work/worktree_retirement_source*",
}

// firstGovernanceCorePath returns the first of the sorted paths in the core.
func firstGovernanceCorePath(paths []string) string {
	for _, p := range paths {
		for _, entry := range governanceCorePaths {
			switch {
			case strings.HasSuffix(entry, "/"):
				if strings.HasPrefix(p, entry) {
					return p
				}
			case strings.HasSuffix(entry, "*"):
				prefix := strings.TrimSuffix(entry, "*")
				if path.Dir(p) == path.Dir(prefix+"x") && strings.HasPrefix(p, prefix) {
					return p
				}
			case p == entry:
				return p
			}
		}
	}
	return ""
}

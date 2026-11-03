package proofscope

import (
	"path"
	"sort"
	"strings"
)

// GovernanceCorePaths is the code that judges whether a proof, landing, close
// or publication is legitimate, plus the envelope spine, hook enforcement and
// the scope owners that decide what a presubmit runs. A diff that touches it
// keeps the full pre-land canonical battery (operator decision 2026-11-03):
// the post-land verifier verdict certifies every other change, but the judge of
// proofs does not certify its own change after the fact.
//
// An entry ending in "/" matches its whole subtree. An entry ending in "*"
// matches a file-name prefix directly in that directory, never a subpackage
// (internal/work/executewire is not core). Any other entry matches exactly.
var GovernanceCorePaths = []string{
	"cmd/compound/proof_check*",
	"internal/authority/",
	"internal/envelope/",
	"internal/hook/",
	"internal/landlease/",
	"internal/policy/",
	"internal/proofscope/",
	"internal/work/canonical_*",
	"internal/work/close_*",
	"internal/work/differential_proof*",
	"internal/work/git_delivery_close*",
	"internal/work/go_test_scope*",
	"internal/work/land_check*",
	"internal/work/landing_resource*",
	"internal/work/lease_close*",
	"internal/work/verifier_verdict*",
	"internal/work/postsubmit*",
	"internal/work/presubmit*",
	"internal/work/private_release_tag*",
	"internal/work/private_source_publish*",
	"internal/work/proof_*",
	"internal/work/standing_proof*",
	"internal/work/terminal_close_proof*",
	"internal/work/work.go",
	"internal/work/worktree_retirement_source*",
	"scripts/testbattery/",
}

// GovernanceCore returns the sorted changed paths that fall in
// GovernanceCorePaths. Empty means the verifier verdict alone certifies the change.
func GovernanceCore(changed []string) []string {
	var hits []string
	for _, p := range changed {
		p = strings.TrimSpace(p)
		if p != "" && IsGovernanceCore(p) {
			hits = append(hits, p)
		}
	}
	sort.Strings(hits)
	return hits
}

// IsGovernanceCore reports whether one repository-relative path is governance core.
func IsGovernanceCore(p string) bool {
	for _, entry := range GovernanceCorePaths {
		switch {
		case strings.HasSuffix(entry, "/"):
			if strings.HasPrefix(p, entry) {
				return true
			}
		case strings.HasSuffix(entry, "*"):
			prefix := strings.TrimSuffix(entry, "*")
			if path.Dir(p) == path.Dir(prefix+"x") && strings.HasPrefix(p, prefix) {
				return true
			}
		case p == entry:
			return true
		}
	}
	return false
}

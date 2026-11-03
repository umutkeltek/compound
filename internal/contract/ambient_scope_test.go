package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"compound/internal/candidates"
	"compound/internal/envelope"
	"compound/internal/mem"
)

// The ambient recall scope gate: a ledger fact reaches the session only when its scope is
// the cwd repository or the task names it. Before the gate, a fact about another repository
// rode in on one shared word ("upstream", "paywall") or on the semantic lane alone.

func promoteAmbientScopeClaims(t *testing.T, home string, cands []mem.Candidate) {
	t.Helper()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	for i := range cands {
		cands[i].Confidence = 0.9
		cands[i].Derivation = "deterministic"
		cands[i].PromotionBasis = candidates.BasisCuratedPage
		cands[i].SourceTraceRef = "ev_scope_" + cands[i].Entity
		cands[i].SnippetHash = "sha256:scope-" + cands[i].Entity + "-" + cands[i].Predicate
		cands[i].ObservationDate = "2026-06-30T00:00:00Z"
	}
	got, err := store.Capture(cands, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range got {
		if _, err := store.Adjudicate(c.ID, "promote", "test", fixedNow); err != nil {
			t.Fatal(err)
		}
	}
}

func ambientScopeRepo(t *testing.T, name string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// servedMemoryText is everything a reader of the pack sees as memory: the summary lines,
// the active refs, the folded positions and the included reasons.
func servedMemoryText(pack envelope.ContextPack) string {
	var b strings.Builder
	b.WriteString(pack.SummaryMD)
	for _, ref := range pack.ActiveMemory {
		b.WriteString("\n" + ref)
	}
	for _, p := range pack.ActivePositions {
		b.WriteString("\n" + p.Ref + " " + p.Line)
	}
	for _, inc := range pack.Included {
		b.WriteString("\n" + inc.Ref + " " + inc.Reason)
	}
	return b.String()
}

func hasExcludedRef(pack envelope.ContextPack, ref string) bool {
	for _, ex := range pack.Excluded {
		if ex.Ref == ref {
			return true
		}
	}
	return false
}

func TestAmbientRecallServesOnlyFactsScopedToCwdRepoOrTask(t *testing.T) {
	t.Setenv("COMPOUND_MEM_ENGINE_BIN", "")
	t.Setenv("COMPOUND_MEM_ENGINE_TIMEOUT", "")
	home := t.TempDir()
	promoteAmbientScopeClaims(t, home, []mem.Candidate{
		// cwd repository's own fact.
		{Entity: "repo:alpha-repo", Predicate: "upstream", Object: "origin/main", ClaimType: "status", Visibility: "operational"},
		// Another repository's fact sharing the task word "upstream".
		{Entity: "repo:zeta-repo", Predicate: "upstream", Object: "origin/trunk", ClaimType: "status", Visibility: "operational"},
		// Another project's docs, sharing "upstream" through the object.
		{Entity: "knowledge:project_docs:zeta-docs:docs/sync.md@abc123", Predicate: "sync_policy", Object: "zeta-docs mirrors upstream nightly", ClaimType: "architecture", Visibility: "contextual"},
		// Operator-wide fact sharing "upstream" through the object only.
		{Entity: "compound", Predicate: "session_distill_source", Object: "distill outputs are read from upstream worklists", ClaimType: "constraint", Visibility: "contextual"},
	})
	base := Input{Harness: "codex", Cwd: ambientScopeRepo(t, "alpha-repo"), RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute"}

	t.Run("cwd repo X, task about upstream: serves X, never Z", func(t *testing.T) {
		in := base
		in.Task = "check the upstream before the release"
		pack, err := PackWithCapabilities(in, DefaultPolicy(), home, fixedNow)
		if err != nil {
			t.Fatal(err)
		}
		served := servedMemoryText(pack)
		if !strings.Contains(pack.SummaryMD, "Compound memory (owned ledger): repo:alpha-repo.upstream = origin/main") {
			t.Fatalf("cwd repository fact not served; summary=%q", pack.SummaryMD)
		}
		for _, leak := range []string{"zeta-repo", "zeta-docs", "session_distill_source"} {
			if strings.Contains(served, leak) {
				t.Errorf("served %q in a session for alpha-repo:\n%s", leak, served)
			}
		}
		if !hasExcludedRef(pack, ambientScopeExclusionRef) {
			t.Errorf("scope gate fired without a visible exclusion; excluded=%+v", pack.Excluded)
		}
	})

	t.Run("task names Z: Z is served", func(t *testing.T) {
		in := base
		in.Task = "compare the upstream of zeta-repo"
		pack, err := PackWithCapabilities(in, DefaultPolicy(), home, fixedNow)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(pack.SummaryMD, "repo:zeta-repo.upstream") {
			t.Fatalf("a repository the task names must stay servable; summary=%q", pack.SummaryMD)
		}
	})

	t.Run("operator-wide fact needs its predicate in the task", func(t *testing.T) {
		in := base
		in.Task = "rebuild the session distill worklists from upstream"
		pack, err := PackWithCapabilities(in, DefaultPolicy(), home, fixedNow)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(pack.SummaryMD, "compound.session_distill_source") {
			t.Fatalf("operator-wide fact whose predicate the task uses was dropped; summary=%q", pack.SummaryMD)
		}
	})

	t.Run("nothing in scope: the block is omitted", func(t *testing.T) {
		in := base
		in.Cwd = ambientScopeRepo(t, "unrelated-repo")
		in.Task = "check the upstream before the release"
		pack, err := PackWithCapabilities(in, DefaultPolicy(), home, fixedNow)
		if err != nil {
			t.Fatal(err)
		}
		for _, block := range []string{"Compound memory (owned ledger)", "Compound positions (folded current view)"} {
			if strings.Contains(pack.SummaryMD, block) {
				t.Errorf("%s block served with no fact in scope:\n%s", block, pack.SummaryMD)
			}
		}
		if len(pack.ActiveMemory) != 0 || len(pack.ActivePositions) != 0 {
			t.Errorf("active memory/positions served with no fact in scope: memory=%v positions=%+v", pack.ActiveMemory, pack.ActivePositions)
		}
	})
}

// A product task for one app must not receive another app's facts or positions, even from a
// cwd that is not a product checkout (where the workspace product fence does not apply).
func TestProductTaskDoesNotReceiveAnotherProductsPositions(t *testing.T) {
	t.Setenv("COMPOUND_MEM_ENGINE_BIN", "")
	t.Setenv("COMPOUND_MEM_ENGINE_TIMEOUT", "")
	home := t.TempDir()
	promoteAmbientScopeClaims(t, home, []mem.Candidate{
		{Entity: "product:ledgerly", Predicate: "learned.1", Object: "paywall conversion rose after the price test", ClaimType: "status", Visibility: "operational"},
		{Entity: "product:tidewatch", Predicate: "learned.2", Object: "paywall conversion fell after onboarding change", ClaimType: "status", Visibility: "operational"},
	})
	in := Input{
		Harness: "codex", Cwd: ambientScopeRepo(t, "control-repo"),
		Task:     "what did we learn about Ledgerly paywall conversion",
		RepoKind: "control-plane", RepoKindVia: "flag", Mode: "execute",
	}
	pack, err := PackWithCapabilities(in, DefaultPolicy(), home, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	served := servedMemoryText(pack)
	if !strings.Contains(served, "product:ledgerly") {
		t.Fatalf("the named product's lesson was not served:\n%s", served)
	}
	if strings.Contains(served, "tidewatch") {
		t.Fatalf("another product's lesson was served on a Ledgerly task:\n%s", served)
	}
	for _, p := range pack.ActivePositions {
		if strings.Contains(p.Ref, "tidewatch") {
			t.Fatalf("another product's position served: %+v", p)
		}
	}
}

func TestAmbientFactScopeClassification(t *testing.T) {
	cases := []struct {
		entity   string
		wantKind ambientFactKind
		wantName string
	}{
		{"repo:sample-repo", ambientFactRepoScoped, "sample-repo"},
		{"source:sample-repo", ambientFactRepoScoped, "sample-repo"},
		{"product:sample_app", ambientFactRepoScoped, "sample_app"},
		{"knowledge:project_docs:sample-cli:docs/a.md@ff00", ambientFactRepoScoped, "sample-cli"},
		{"compound", ambientFactOperatorWide, "compound"},
		{"compound:closed-loop", ambientFactOperatorWide, "compound"},
		{"ContextPack", ambientFactConcept, "ContextPack"},
		{"infra:lan-control", ambientFactConcept, "lan-control"},
		{"knowledge:arxiv:2601.00001", ambientFactConcept, "arxiv:2601.00001"},
	}
	for _, c := range cases {
		kind, name := ambientFactScope(c.entity)
		if kind != c.wantKind || name != c.wantName {
			t.Errorf("ambientFactScope(%q) = (%d, %q), want (%d, %q)", c.entity, kind, name, c.wantKind, c.wantName)
		}
	}
	for remote, want := range map[string]string{
		"git@example.test:owner/sample-repo.git":  "sample-repo",
		"https://example.test/owner/sample-repo":  "sample-repo",
		"https://example.test/owner/sample-repo/": "sample-repo",
		"/srv/git/sample-repo.git":                "sample-repo",
	} {
		if got := remoteRepositoryName(remote); got != want {
			t.Errorf("remoteRepositoryName(%q) = %q, want %q", remote, got, want)
		}
	}
}

// The gate applies to facts that have a place (repository, product, project, the compound
// namespace). Concept facts have no place to mismatch and stay with the ranker. A predicate
// word never anchors a repository-scoped fact; the task naming it, a Work it cites, or an
// explicit topic scope does.
func TestAmbientScopeGateRules(t *testing.T) {
	scope := resolveAmbientScope(Input{Cwd: "/nonexistent/alpha-repo", Task: "check the branch for Ledgerly paywall under work_fixture_target"}, productRecallFence{})
	other := mem.Claim{Entity: "product:tidewatch", Predicate: "learned.2", ClaimType: "status"}
	named := mem.Claim{Entity: "product:ledgerly", Predicate: "learned.1", ClaimType: "status"}
	law := mem.Claim{Entity: "DecisionLifecycle", Predicate: "authority_boundary", ClaimType: "constraint"}
	otherBranch := mem.Claim{Entity: "repo:zeta-repo", Predicate: "current_branch", ClaimType: "status"}
	ownBranch := mem.Claim{Entity: "repo:alpha-repo", Predicate: "current_branch", ClaimType: "status"}
	citesWork := mem.Claim{Entity: "product:tidewatch", Predicate: "learned.3", Object: "result of work_fixture_target", ClaimType: "status"}
	opWide := mem.Claim{Entity: "compound", Predicate: "session_distill_source", ClaimType: "constraint"}
	opWideOnTask := mem.Claim{Entity: "compound", Predicate: "branch_policy", ClaimType: "constraint"}
	for _, c := range []struct {
		name        string
		claim       mem.Claim
		flat, probe bool
	}{
		{"another product", other, false, false},
		{"the named product", named, true, true},
		{"concept fact", law, true, true},
		{"another repo's branch on a task saying branch", otherBranch, false, false},
		{"the cwd repo's branch", ownBranch, true, true},
		{"another product's fact citing the addressed work", citesWork, true, true},
		{"operator-wide fact the task does not touch", opWide, false, true},
		{"operator-wide fact whose predicate the task uses", opWideOnTask, true, true},
	} {
		if got := scope.allows(c.claim, false); got != c.flat {
			t.Errorf("%s: flat allows = %v, want %v", c.name, got, c.flat)
		}
		if got := scope.allowsRepoScoped(c.claim, false); got != c.probe {
			t.Errorf("%s: probe allowsRepoScoped = %v, want %v", c.name, got, c.probe)
		}
	}
	if !scope.allows(other, true) || !scope.allowsRepoScoped(other, true) {
		t.Error("an explicit topic scope chosen by the request must anchor the facts inside it")
	}
}

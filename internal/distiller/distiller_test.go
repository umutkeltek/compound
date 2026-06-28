package distiller

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/mem"
)

var t0 = time.Date(2026, 6, 29, 12, 0, 0, 0, time.UTC)

func setup(t *testing.T) (string, *mem.Store) {
	t.Helper()
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	return home, store
}

// run uses FixturePolicy (auto-promote) so the dup/supersede/contradict MECHANICS tests below stay
// about the ledger, not the promotion-basis gate. The basis gate has its own tests (live mode).
func run(t *testing.T, home string, store *mem.Store, raw string) RunResult {
	t.Helper()
	return runWith(t, home, store, raw, FixturePolicy())
}

func runWith(t *testing.T, home string, store *mem.Store, raw string, p Policy) RunResult {
	t.Helper()
	res, err := Run(home, store, []byte(raw), p, ReferenceExtractor{}, t0)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return res
}

func verdicts(res RunResult) map[string]string {
	m := map[string]string{} // kind -> verdict (last wins; fine for distinct kinds)
	for _, d := range res.Decisions {
		m[d.Kind] = d.Verdict
	}
	return m
}

// The headline acceptance set on a fresh store: low-confidence parks, new memory promotes,
// a method upgrade parks (never auto-applied), a capability observation + eval case
// operationalize, and an ordinary skill candidate parks.
func TestRunVerdicts(t *testing.T) {
	home, store := setup(t)
	trace := `{"trace_ref":"trace_1","candidates":[
	  {"kind":"memory","entity":"project:api","predicate":"db","object":"postgres","confidence":0.9},
	  {"kind":"memory","entity":"project:api","predicate":"cache","object":"redis","confidence":0.3},
	  {"kind":"method_upgrade","subject":"distill.memory","statement":"ground dates","confidence":0.8},
	  {"kind":"capability_observation","subject":"skill:cro","statement":"missed_when","confidence":0.7},
	  {"kind":"eval_case","subject":"distill.skill","statement":"missed a skill","confidence":1.0},
	  {"kind":"skill","subject":"paywall-cro","statement":"useful lens","confidence":0.8}
	]}`
	res := run(t, home, store, trace)
	v := verdicts(res)
	want := map[string]string{
		KindMethodUpgrade: Park,           // a method change is never auto-applied
		KindCapabilityObs: Operationalize, // feeds the capability graph
		KindEvalCase:      Operationalize, // distiller's own corpus
		KindSkill:         Park,
	}
	for k, exp := range want {
		if v[k] != exp {
			t.Errorf("%s verdict = %q, want %q", k, v[k], exp)
		}
	}
	// the high-confidence memory promoted to a claim; the low-confidence one parked.
	var promoted, parked *AdjudicationDecision
	for i := range res.Decisions {
		d := &res.Decisions[i]
		if d.Kind == KindMemory && d.Verdict == Promote {
			promoted = d
		}
		if d.Kind == KindMemory && d.Verdict == Park {
			parked = d
		}
	}
	if promoted == nil || promoted.ClaimID == "" {
		t.Error("the 0.9 memory candidate should promote to a claim")
	}
	if parked == nil {
		t.Error("the 0.3 memory candidate should park (below the confidence floor)")
	}
}

// Same trace replayed ⇒ identical candidate ids (idempotent distillation).
func TestReplayStableIDs(t *testing.T) {
	raw := []byte(`{"trace_ref":"trace_x","candidates":[{"kind":"memory","entity":"e","predicate":"p","object":"o","confidence":0.9}]}`)
	a, _ := ReferenceExtractor{}.Extract(raw, DefaultPolicy())
	b, _ := ReferenceExtractor{}.Extract(raw, DefaultPolicy())
	if a.Candidates[0].ID != b.Candidates[0].ID {
		t.Errorf("replay must produce stable ids: %q != %q", a.Candidates[0].ID, b.Candidates[0].ID)
	}
	if a.Candidates[0].PolicyVersion != "default.v1" {
		t.Errorf("candidate must carry the policy version, got %q", a.Candidates[0].PolicyVersion)
	}
}

// Distill against canon: a duplicate memory is rejected; a changed value supersedes the prior claim.
func TestDuplicateAndSupersession(t *testing.T) {
	home, store := setup(t)
	run(t, home, store, `{"trace_ref":"t1","candidates":[{"kind":"memory","entity":"svc","predicate":"lang","object":"node","confidence":0.9}]}`)
	res := run(t, home, store, `{"trace_ref":"t2","candidates":[
	  {"kind":"memory","entity":"svc","predicate":"lang","object":"node","confidence":0.9},
	  {"kind":"memory","entity":"svc","predicate":"lang","object":"go","confidence":0.9}
	]}`)
	var dup, sup *AdjudicationDecision
	for i := range res.Decisions {
		d := &res.Decisions[i]
		if d.Verdict == Reject {
			dup = d
		}
		if d.Verdict == Promote {
			sup = d
		}
	}
	if dup == nil {
		t.Error("the duplicate 'node' memory should be rejected against prior canon")
	}
	if sup == nil || sup.ClaimID == "" {
		t.Error("the changed 'go' memory should promote (superseding)")
	}
	// the ledger now holds 'go' as current.
	cur, _ := store.AllCurrent()
	if len(cur) != 1 || cur[0].Object != "go" {
		t.Errorf("after supersession, current should be 'go'; got %v", cur)
	}
}

// No candidate enters ambient recall without adjudication: a parked skill candidate is a file in
// the inbox, NOT a claim — recall/AllCurrent never surfaces it.
func TestNoCandidateToAmbientWithoutAdjudication(t *testing.T) {
	home, store := setup(t)
	run(t, home, store, `{"trace_ref":"t","candidates":[{"kind":"skill","subject":"s","statement":"x","confidence":0.9}]}`)
	cur, _ := store.AllCurrent()
	if len(cur) != 0 {
		t.Errorf("a parked skill candidate must not become a claim; got %d claims", len(cur))
	}
	// it IS recorded as a candidate in the inbox (candidate-first), and as an adjudication.
	skillDir := candidates.InboxDir(candidates.Skill)
	files, _ := filepath.Glob(filepath.Join(home, skillDir, "*.json"))
	if len(files) != 1 {
		t.Errorf("the skill candidate should be written to %s; found %d", skillDir, len(files))
	}
	adj, _ := os.ReadFile(filepath.Join(home, "ledger", "adjudications.jsonl"))
	var d AdjudicationDecision
	if json.Unmarshal([]byte(firstLine(adj)), &d) != nil || d.Verdict != Park {
		t.Errorf("the skill candidate should have a parked adjudication decision; got %q", d.Verdict)
	}
}

// A weaker candidate conflicting with HIGHER-confidence current canon is a CONTRADICTION → it parks
// for operator review, never silently supersedes the stronger claim.
func TestContradictionParks(t *testing.T) {
	home, store := setup(t)
	run(t, home, store, `{"trace_ref":"t1","candidates":[{"kind":"memory","entity":"svc","predicate":"lang","object":"go","confidence":0.95}]}`)
	res := run(t, home, store, `{"trace_ref":"t2","candidates":[{"kind":"memory","entity":"svc","predicate":"lang","object":"rust","confidence":0.4}]}`)
	d := res.Decisions[0]
	if d.Verdict != Park || d.Relation != "contradicts" {
		t.Errorf("a 0.4 conflict vs 0.95 canon should PARK as a contradiction; got verdict=%q relation=%q", d.Verdict, d.Relation)
	}
	// canon unchanged — the weak contradiction did not supersede the strong claim.
	cur, _ := store.AllCurrent()
	if len(cur) != 1 || cur[0].Object != "go" {
		t.Errorf("canon should still be 'go'; got %v", cur)
	}
}

// Two candidates differing ONLY in Detail get distinct ids (no silent inbox collision).
func TestDetailDistinguishesIDs(t *testing.T) {
	raw := []byte(`{"trace_ref":"t","candidates":[
	  {"kind":"capability_observation","subject":"skill:x","statement":"ctx","detail":"missed_when","confidence":0.8},
	  {"kind":"capability_observation","subject":"skill:x","statement":"ctx","detail":"false_triggered_by","confidence":0.8}
	]}`)
	ext, err := ReferenceExtractor{}.Extract(raw, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if ext.Candidates[0].ID == ext.Candidates[1].ID {
		t.Errorf("candidates differing in Detail must get distinct ids; both = %q", ext.Candidates[0].ID)
	}
}

// An unknown / path-bearing kind is rejected — Kind keys a filesystem path and must stay typed.
func TestUnknownKindRejected(t *testing.T) {
	for _, k := range []string{"bogus", "../../evil"} {
		raw := []byte(`{"trace_ref":"t","candidates":[{"kind":"` + k + `","subject":"x","confidence":0.9}]}`)
		if _, err := (ReferenceExtractor{}).Extract(raw, DefaultPolicy()); err == nil {
			t.Errorf("kind %q should be rejected", k)
		}
	}
}

// In live mode (the DEFAULT), a high-confidence model-extracted memory candidate with NO promotion
// basis PARKS — "the model was confident" is not a basis. Canon stays empty.
func TestLiveModeParksMemoryWithoutBasis(t *testing.T) {
	home, store := setup(t)
	res := runWith(t, home, store,
		`{"trace_ref":"t","candidates":[{"kind":"memory","entity":"svc","predicate":"lang","object":"go","confidence":0.95}]}`,
		DefaultPolicy())
	if res.Decisions[0].Verdict != Park {
		t.Errorf("live-mode memory without a basis must PARK; got %q (%s)", res.Decisions[0].Verdict, res.Decisions[0].Basis)
	}
	if cur, _ := store.AllCurrent(); len(cur) != 0 {
		t.Errorf("nothing should be canon without a basis; got %d claims", len(cur))
	}
}

// With a valid promotion basis, the same candidate PROMOTES and the basis is persisted on the claim
// (the recallable "why" of canon — the first convergence increment of memory.PromotionBasis onto mem).
func TestLiveModePromotesWithBasisPersisted(t *testing.T) {
	home, store := setup(t)
	raw := `{"trace_ref":"t","candidates":[{"kind":"memory","entity":"svc","predicate":"lang","object":"go","confidence":0.95,"promotion_basis":"` + candidates.BasisExplicitConfirmation + `"}]}`
	res := runWith(t, home, store, raw, DefaultPolicy())
	d := res.Decisions[0]
	if d.Verdict != Promote || d.ClaimID == "" {
		t.Fatalf("a memory with an explicit_confirmation basis must promote; got %q claim=%q", d.Verdict, d.ClaimID)
	}
	if d.PromotionBasis != candidates.BasisExplicitConfirmation {
		t.Errorf("the decision should record the basis; got %q", d.PromotionBasis)
	}
	cur, _ := store.AllCurrent()
	if len(cur) != 1 || cur[0].PromotionBasis != candidates.BasisExplicitConfirmation {
		t.Errorf("the promoted claim must carry the basis; got %+v", cur)
	}
}

// An unknown basis string is treated as NO basis (parks) — only allowlisted bases promote.
func TestLiveModeInvalidBasisParks(t *testing.T) {
	home, store := setup(t)
	res := runWith(t, home, store,
		`{"trace_ref":"t","candidates":[{"kind":"memory","entity":"svc","predicate":"lang","object":"go","confidence":0.95,"promotion_basis":"vibes"}]}`,
		DefaultPolicy())
	if res.Decisions[0].Verdict != Park {
		t.Errorf("an unknown basis must be treated as no basis (park); got %q", res.Decisions[0].Verdict)
	}
}

func firstLine(b []byte) string {
	for i, c := range b {
		if c == '\n' {
			return string(b[:i])
		}
	}
	return string(b)
}

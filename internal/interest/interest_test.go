package interest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/eventstore"
	"compound/internal/finding"
	"compound/internal/home"
)

var t0 = time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)

type stepFixture struct{ name, status, tail string }

// putReport stores a synthetic proof report through the artifact store's own
// writer and returns its artifact ref.
func putReport(t *testing.T, homePath, repo, fingerprint string, steps ...stepFixture) string {
	t.Helper()
	rows := []map[string]any{{"name": "go-build", "status": "ok", "exit_code": 0, "duration_ms": 10, "proof_ref": "", "output_tail": ""}}
	for _, s := range steps {
		rows = append(rows, map[string]any{"name": s.name, "status": s.status, "exit_code": 1, "duration_ms": 20, "proof_ref": "", "output_tail": s.tail})
	}
	doc := map[string]any{
		"schema_version":  "compound.proof_check_report.v0.6",
		"profile_kind":    "canonical_full",
		"status":          "failed",
		"generated_at":    t0.Format(time.RFC3339),
		"repo":            repo,
		"source_snapshot": map[string]any{"repository_fingerprint": fingerprint},
		"steps":           rows,
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return putArtifact(t, homePath, b)
}

// putReportWithFailureLines stores a report whose one failing step carries the
// battery's own failure_lines extraction next to a tail that lost the FAIL line.
func putReportWithFailureLines(t *testing.T, homePath, fingerprint, step, tail string, failureLines []string) string {
	t.Helper()
	doc := map[string]any{
		"schema_version":  "compound.proof_check_report.v0.6",
		"profile_kind":    "canonical_full",
		"status":          "failed",
		"generated_at":    t0.Format(time.RFC3339),
		"source_snapshot": map[string]any{"repository_fingerprint": fingerprint},
		"steps": []map[string]any{
			{"name": "go-build", "status": "ok", "exit_code": 0, "duration_ms": 10, "proof_ref": "", "output_tail": ""},
			{"name": step, "status": "failed", "exit_code": 1, "duration_ms": 20, "proof_ref": "", "output_tail": tail, "failure_lines": failureLines},
		},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return putArtifact(t, homePath, b)
}

func putArtifact(t *testing.T, homePath string, body []byte) string {
	t.Helper()
	store, err := home.NewArtifactStore(homePath)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := store.Put(body)
	if err != nil {
		t.Fatal(err)
	}
	return ref
}

// putResult appends a synthetic work.proof_resource.result through the event
// store's own writer.
func putResult(t *testing.T, homePath, id, workID, status string, at time.Time, reportRef, fingerprint string) {
	t.Helper()
	store, err := eventstore.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	rec := map[string]any{
		"schema_version":     "compound.work_proof_result.v0.1",
		"id":                 id,
		"kind":               proofResultKind,
		"status":             status,
		"created_at":         at.Format(time.RFC3339Nano),
		"work_id":            workID,
		"actor":              "fixture-lane",
		"run_attempt_status": "failed",
	}
	if reportRef != "" {
		rec["report_artifact_ref"] = reportRef
		rec["report_status"] = status
	}
	if fingerprint != "" {
		rec["source_snapshot"] = map[string]any{"repository_fingerprint": fingerprint}
	}
	if _, err := store.PutJSON(id, rec, "  "); err != nil {
		t.Fatal(err)
	}
}

func goTestTail(name string) string {
	return "ok  \tcompound/internal/alpha\t1.2s\n--- FAIL: " + name + " (43.26s)\n    alpha_test.go:12: boom\nFAIL\nFAIL\tcompound/internal/hook\t424.9s\n"
}

// seedAlpha: TestAlpha fails in two works, one consumer is killed once, a passing
// result and an out-of-window failure must both be ignored.
func seedAlpha(t *testing.T, homePath string) {
	t.Helper()
	alpha := putReport(t, homePath, "/srv/fixture/example/checkout", "fp-alpha", stepFixture{"go-test", "failed", goTestTail("TestAlpha")})
	killed := putReport(t, homePath, "/srv/fixture/example/checkout", "fp-alpha", stepFixture{"consumer-proof", "failed", "starting consumer\nconsumer proof: signal: killed\n"})
	putResult(t, homePath, "pfrs_fixture_a1", "work_fixture_a", "failed", t0.Add(-3*time.Hour), alpha, "fp-alpha")
	putResult(t, homePath, "pfrs_fixture_a2", "work_fixture_b", "failed", t0.Add(-2*time.Hour), alpha, "fp-alpha")
	putResult(t, homePath, "pfrs_fixture_k1", "work_fixture_c", "failed", t0.Add(-1*time.Hour), killed, "fp-alpha")
	putResult(t, homePath, "pfrs_fixture_ok", "work_fixture_d", "verified", t0.Add(-1*time.Hour), alpha, "fp-alpha")
	putResult(t, homePath, "pfrs_fixture_old", "work_fixture_e", "failed", t0.Add(-40*24*time.Hour), alpha, "fp-alpha")
}

func mustBuild(t *testing.T, homePath string, now time.Time) Projection {
	t.Helper()
	proj, err := Build(homePath, BuildOptions{Now: now})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	return proj
}

func bySignature(t *testing.T, proj Projection, signature string) Signature {
	t.Helper()
	for _, s := range proj.Signatures {
		if s.Signature == signature {
			return s
		}
	}
	t.Fatalf("no signature %q in %+v", signature, proj.Signatures)
	return Signature{}
}

func hasWarning(proj Projection, source, code string) bool {
	for _, w := range proj.Warnings {
		if w.Source == source && w.Code == code {
			return true
		}
	}
	return false
}

func TestBuildClustersLearnableAndSingleton(t *testing.T) {
	h := t.TempDir()
	seedAlpha(t, h)
	proj := mustBuild(t, h, t0)

	want := Totals{FailedResults: 3, WithReport: 3, LearnableResults: 2, SingletonResults: 1}
	if proj.Totals != want {
		t.Fatalf("totals = %+v, want %+v", proj.Totals, want)
	}
	alpha := bySignature(t, proj, "fail:TestAlpha")
	if alpha.State != StateLearnable || alpha.Occurrences != 2 || alpha.AttentionRank != 1 || alpha.ResourceShape {
		t.Fatalf("TestAlpha cluster = %+v", alpha)
	}
	if alpha.ID != SignatureID("go-test", "fail:TestAlpha") || !reflect.DeepEqual(alpha.Tests, []string{"TestAlpha"}) {
		t.Fatalf("TestAlpha identity = %+v", alpha)
	}
	if !reflect.DeepEqual(alpha.WorkIDs, []string{"work_fixture_a", "work_fixture_b"}) {
		t.Fatalf("work ids = %v", alpha.WorkIDs)
	}
	if alpha.FirstSeen != t0.Add(-3*time.Hour).Format(time.RFC3339) || alpha.LastSeen != t0.Add(-2*time.Hour).Format(time.RFC3339) {
		t.Fatalf("seen range = %s..%s", alpha.FirstSeen, alpha.LastSeen)
	}
	killed := bySignature(t, proj, "resource:signal: killed")
	if killed.State != StateSingleton || !killed.ResourceShape || killed.Step != "consumer-proof" || killed.AttentionRank != 2 {
		t.Fatalf("killed cluster = %+v", killed)
	}
	if !hasWarning(proj, "projection", "no_previous") || len(proj.Progress.NewlyAssimilated) != 0 || len(proj.Progress.NewlyLearnable) != 0 {
		t.Fatalf("first build progress = %+v warnings=%+v", proj.Progress, proj.Warnings)
	}
}

// A resource-shaped cluster (kill, cancelled run) is the noise the paper calls
// boring; even with more occurrences it ranks after a regular learnable signature.
func TestResourceShapeRanksAfterRegularWithinState(t *testing.T) {
	h := t.TempDir()
	alpha := putReport(t, h, "", "fp-alpha", stepFixture{"go-test", "failed", goTestTail("TestAlpha")})
	killed := putReport(t, h, "", "fp-alpha", stepFixture{"consumer-proof", "failed", "consumer proof: signal: killed\n"})
	putResult(t, h, "pfrs_fixture_r1", "work_fixture_a", "failed", t0.Add(-5*time.Hour), alpha, "fp-alpha")
	putResult(t, h, "pfrs_fixture_r2", "work_fixture_b", "failed", t0.Add(-4*time.Hour), alpha, "fp-alpha")
	for i, w := range []string{"work_fixture_c", "work_fixture_d", "work_fixture_e"} {
		putResult(t, h, "pfrs_fixture_k"+string(rune('1'+i)), w, "failed", t0.Add(-time.Duration(i+1)*time.Hour), killed, "fp-alpha")
	}
	proj := mustBuild(t, h, t0)
	if a, k := bySignature(t, proj, "fail:TestAlpha"), bySignature(t, proj, "resource:signal: killed"); a.AttentionRank != 1 || k.AttentionRank != 2 || k.State != StateLearnable || k.Occurrences != 3 {
		t.Fatalf("ranks: alpha=%+v killed=%+v", a, k)
	}
}

func TestOneWorkRetriedSameDayStaysSingleton(t *testing.T) {
	h := t.TempDir()
	ref := putReport(t, h, "", "fp-alpha", stepFixture{"go-test", "failed", goTestTail("TestRetriedFixture")})
	putResult(t, h, "pfrs_fixture_r1", "work_fixture_a", "failed", t0.Add(-3*time.Hour), ref, "fp-alpha")
	putResult(t, h, "pfrs_fixture_r2", "work_fixture_a", "failed", t0.Add(-2*time.Hour), ref, "fp-alpha")
	if s := bySignature(t, mustBuild(t, h, t0), "fail:TestRetriedFixture"); s.State != StateSingleton || s.Occurrences != 2 {
		t.Fatalf("one work, one day = %+v; want singleton", s)
	}
}

// The derivative test: an explanation that names the signature moves it to
// assimilated and progress reports the observations it absorbed.
func TestFindingEvidenceAssimilatesAndProgressCountsAbsorbed(t *testing.T) {
	h := t.TempDir()
	seedAlpha(t, h)
	before := mustBuild(t, h, t0)
	if _, err := WriteProjection(h, before); err != nil {
		t.Fatal(err)
	}
	alpha := bySignature(t, before, "fail:TestAlpha")
	f, err := finding.Record(h, finding.Finding{
		Topic:        "proof battery note",
		Body:         "root cause recorded for this failure cluster",
		EvidenceRefs: []string{SignatureEvidencePrefix + alpha.ID, RepositoryEvidencePrefix + "fp-alpha"},
	}, t0)
	if err != nil {
		t.Fatal(err)
	}

	after := mustBuild(t, h, t0.Add(time.Hour))
	got := bySignature(t, after, "fail:TestAlpha")
	if got.State != StateAssimilated || !reflect.DeepEqual(got.ExplainedBy, []Explanation{{Ref: "finding:" + f.ID, Match: MatchExact}}) {
		t.Fatalf("after explanation = %+v", got)
	}
	if !reflect.DeepEqual(after.Progress.NewlyAssimilated, []Absorbed{{ID: alpha.ID, Occurrences: 2}}) {
		t.Fatalf("newly assimilated = %+v", after.Progress.NewlyAssimilated)
	}
	if after.Progress.PreviousComputedAt != before.ComputedAt || after.Progress.AssimilatedShareBefore != 0 {
		t.Fatalf("progress baseline = %+v", after.Progress)
	}
	if before.Totals.AssimilatedShare != 0 || after.Totals.AssimilatedShare != 0.6667 || after.Totals.AssimilatedResults != 2 {
		t.Fatalf("share %v -> %v (%+v)", before.Totals.AssimilatedShare, after.Totals.AssimilatedShare, after.Totals)
	}
	if got.AttentionRank != 2 || bySignature(t, after, "resource:signal: killed").AttentionRank != 1 {
		t.Fatalf("explained signature must rank after unexplained ones: %+v", after.Signatures)
	}
	if hasWarning(after, "projection", "no_previous") {
		t.Fatalf("second build still reports no previous projection: %+v", after.Warnings)
	}
	if line := Headline(after); !strings.Contains(line, "3 failed results in 30d; 66.7% assimilated; 0 learnable signatures") ||
		!strings.Contains(line, "progress since "+before.ComputedAt+": +1 assimilated (2 results absorbed)") {
		t.Fatalf("headline = %q", line)
	}
}

// A note that names a failing test is related evidence, never an explanation:
// "TestAlpha still fails. The root cause is unknown." diagnoses nothing, so it
// must not move the signature to assimilated or count as absorbed progress.
func TestFuzzyMentionIsRelatedNotAssimilated(t *testing.T) {
	h := t.TempDir()
	seedAlpha(t, h)
	if _, err := finding.Record(h, finding.Finding{Topic: "battery note", Body: "TestAlphaBeta is a different test"}, t0); err != nil {
		t.Fatal(err)
	}
	before := mustBuild(t, h, t0)
	if s := bySignature(t, before, "fail:TestAlpha"); s.State != StateLearnable || len(s.Related) != 0 {
		t.Fatalf("TestAlphaBeta must not relate to TestAlpha: %+v", s)
	}
	if _, err := WriteProjection(h, before); err != nil {
		t.Fatal(err)
	}
	f, err := finding.Record(h, finding.Finding{Topic: "battery note", Body: "TestAlpha still fails. The root cause is unknown."}, t0)
	if err != nil {
		t.Fatal(err)
	}
	after := mustBuild(t, h, t0.Add(time.Hour))
	s := bySignature(t, after, "fail:TestAlpha")
	if s.State != StateLearnable || len(s.ExplainedBy) != 0 || !reflect.DeepEqual(s.Related, []Explanation{{Ref: "finding:" + f.ID, Match: MatchFuzzy}}) {
		t.Fatalf("an unresolved mention must stay learnable with related evidence: %+v", s)
	}
	if len(after.Progress.NewlyAssimilated) != 0 || after.Totals.AssimilatedResults != 0 {
		t.Fatalf("a mention must not count as absorbed: progress=%+v totals=%+v", after.Progress, after.Totals)
	}
}

// An exact explanation covers only the repositories it names. A signature seen
// in two repositories is not assimilated by a finding that names none (scope
// unknown) or only one of them; naming both assimilates it.
func TestExactExplanationMustCoverEveryRepository(t *testing.T) {
	h := t.TempDir()
	alpha := putReport(t, h, "", "fp-alpha", stepFixture{"go-test", "failed", goTestTail("TestAlpha")})
	beta := putReport(t, h, "", "fp-beta", stepFixture{"go-test", "failed", goTestTail("TestAlpha")})
	putResult(t, h, "pfrs_fixture_s1", "work_fixture_a", "failed", t0.Add(-3*time.Hour), alpha, "fp-alpha")
	putResult(t, h, "pfrs_fixture_s2", "work_fixture_b", "failed", t0.Add(-2*time.Hour), beta, "fp-beta")
	id := SignatureID("go-test", "fail:TestAlpha")
	record := func(body string, refs ...string) string {
		t.Helper()
		f, err := finding.Record(h, finding.Finding{Topic: "proof battery note", Body: body, EvidenceRefs: append([]string{SignatureEvidencePrefix + id}, refs...)}, t0)
		if err != nil {
			t.Fatal(err)
		}
		return "finding:" + f.ID
	}
	unscoped := record("root cause seen on one checkout")
	alphaOnly := record("root cause on the alpha checkout", RepositoryEvidencePrefix+"fp-alpha")
	s := bySignature(t, mustBuild(t, h, t0), "fail:TestAlpha")
	wantRelated := []Explanation{{Ref: unscoped, Match: MatchExact, Scope: ScopeUnscoped}, {Ref: alphaOnly, Match: MatchExact, Scope: ScopeUncovered}}
	slices.SortFunc(wantRelated, func(a, b Explanation) int { return strings.Compare(a.Ref, b.Ref) })
	if s.State != StateLearnable || len(s.ExplainedBy) != 0 || !reflect.DeepEqual(s.Related, wantRelated) {
		t.Fatalf("partial scope must not assimilate: %+v", s)
	}
	both := record("root cause on both checkouts", RepositoryEvidencePrefix+"fp-alpha", RepositoryEvidencePrefix+"fp-beta")
	s = bySignature(t, mustBuild(t, h, t0), "fail:TestAlpha")
	if s.State != StateAssimilated || !reflect.DeepEqual(s.ExplainedBy, []Explanation{{Ref: both, Match: MatchExact}}) || len(s.Related) != 2 {
		t.Fatalf("covering scope must assimilate: %+v", s)
	}
}

// A repository:* claim covers every repository by declaration, including the
// ones the capped Repositories sample hides; repositories_total exposes the gap.
func TestRepositoryWildcardCoversBeyondTheCappedSample(t *testing.T) {
	h := t.TempDir()
	fps := []string{"fp-a", "fp-b", "fp-c", "fp-d", "fp-e", "fp-f", "fp-g"}
	for i, fp := range fps {
		ref := putReport(t, h, "", fp, stepFixture{"redact-public", "failed", "MEDIUM pii.phone.e164 40:58 12*****\n"})
		putResult(t, h, "pfrs_fixture_w"+string(rune('1'+i)), "work_fixture_"+fp, "failed", t0.Add(-time.Duration(i+1)*time.Hour), ref, fp)
	}
	sig := "MEDIUM pii.phone.e<n> <n>:<n> <n><mask>"
	id := SignatureID("redact-public", sig)
	s := bySignature(t, mustBuild(t, h, t0), sig)
	if s.RepositoriesTotal != 7 || len(s.Repositories) != 5 || s.State != StateLearnable {
		t.Fatalf("capped sample must still report the total: %+v", s)
	}
	// Naming only the five the sample shows is uncovered: two repositories are hidden.
	refs := []string{SignatureEvidencePrefix + id}
	for _, fp := range s.Repositories {
		refs = append(refs, RepositoryEvidencePrefix+fp)
	}
	if _, err := finding.Record(h, finding.Finding{Topic: "sample only", Body: "fixture digits trip the phone detector", EvidenceRefs: refs}, t0); err != nil {
		t.Fatal(err)
	}
	s = bySignature(t, mustBuild(t, h, t0), sig)
	if s.State != StateLearnable || len(s.ExplainedBy) != 0 || len(s.Related) != 1 || s.Related[0].Scope != ScopeUncovered {
		t.Fatalf("enumerating the capped sample must not cover hidden repositories: %+v", s)
	}
	star, err := finding.Record(h, finding.Finding{Topic: "machine cause", Body: "fixture digits trip the phone detector on every checkout", EvidenceRefs: []string{SignatureEvidencePrefix + id, RepositoryEvidencePrefix + RepositoryWildcard}}, t0)
	if err != nil {
		t.Fatal(err)
	}
	s = bySignature(t, mustBuild(t, h, t0), sig)
	if s.State != StateAssimilated || !reflect.DeepEqual(s.ExplainedBy, []Explanation{{Ref: "finding:" + star.ID, Match: MatchExact}}) {
		t.Fatalf("repository:* must cover every repository: %+v", s)
	}
}

// One work retrying the same failure 100 times in a day is a singleton; two
// independent works hitting a failure once each is learnable and ranks first.
func TestLearnableRanksBeforeHeavilyRetriedSingleton(t *testing.T) {
	h := t.TempDir()
	retried := putReport(t, h, "", "fp-alpha", stepFixture{"go-test", "failed", goTestTail("TestRetriedFixture")})
	for i := range 100 {
		putResult(t, h, fmt.Sprintf("pfrs_fixture_retry_%03d", i), "work_fixture_a", "failed", t0.Add(-time.Duration(i+1)*time.Minute), retried, "fp-alpha")
	}
	recurring := putReport(t, h, "", "fp-alpha", stepFixture{"go-test", "failed", goTestTail("TestRecurringFixture")})
	putResult(t, h, "pfrs_fixture_rec1", "work_fixture_b", "failed", t0.Add(-3*time.Hour), recurring, "fp-alpha")
	putResult(t, h, "pfrs_fixture_rec2", "work_fixture_c", "failed", t0.Add(-2*time.Hour), recurring, "fp-alpha")
	proj := mustBuild(t, h, t0)
	a, b := bySignature(t, proj, "fail:TestRetriedFixture"), bySignature(t, proj, "fail:TestRecurringFixture")
	if a.State != StateSingleton || a.Occurrences != 100 || b.State != StateLearnable || b.AttentionRank != 1 || a.AttentionRank != 2 {
		t.Fatalf("retried=%+v recurring=%+v; want learnable rank 1 before the 100-retry singleton", a, b)
	}
}

// A previous projection built under another schema assigned states by other
// rules, so diffing against it would report a fake derivative.
func TestSchemaChangeRestartsProgress(t *testing.T) {
	h := t.TempDir()
	seedAlpha(t, h)
	old := mustBuild(t, h, t0)
	old.SchemaVersion = "compound.interest.v0.1"
	for i := range old.Signatures {
		old.Signatures[i].State = StateAssimilated
	}
	if _, err := WriteProjection(h, old); err != nil {
		t.Fatal(err)
	}
	proj := mustBuild(t, h, t0.Add(time.Hour))
	if !hasWarning(proj, "projection", "schema_changed") || proj.Progress.PreviousComputedAt != "" || len(proj.Progress.NewlyLearnable) != 0 {
		t.Fatalf("progress across a schema change = %+v warnings=%+v", proj.Progress, proj.Warnings)
	}
}

// A negatives record says what was tried and failed; quoting the signature
// relates it to the failure but does not explain it.
func TestNegativesRecordIsRelatedEvidence(t *testing.T) {
	h := t.TempDir()
	line := "open fresh native report: openat result.junit.xml: no such file or directory"
	ref := putReport(t, h, "", "fp-alpha", stepFixture{"consumer-proof", "failed", "consumer starting\n" + line + "\n"})
	putResult(t, h, "pfrs_fixture_j1", "work_fixture_a", "failed", t0.Add(-time.Hour), ref, "fp-alpha")
	if s := bySignature(t, mustBuild(t, h, t0), line); s.State != StateSingleton {
		t.Fatalf("before negatives record = %+v", s)
	}
	doc := map[string]any{
		"id": "fp_fixture_junit", "kind": string(candidates.FailurePattern), "subject": "consumer-junit-missing",
		"statement": "consumer proof fails with " + line, "detail": "consumer-proof", "confidence": 0.8, "verdict": candidates.Park,
	}
	dir := filepath.Join(h, candidates.InboxDir(candidates.FailurePattern))
	b, _ := json.Marshal(doc)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fp_fixture_junit.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	s := bySignature(t, mustBuild(t, h, t0), line)
	if s.State != StateSingleton || len(s.ExplainedBy) != 0 || len(s.Related) != 1 || s.Related[0].Match != MatchFuzzy ||
		!strings.HasPrefix(s.Related[0].Ref, "candidate://") || !strings.HasSuffix(s.Related[0].Ref, "fp_fixture_junit") {
		t.Fatalf("negatives join = %+v", s)
	}
}

func TestSignatureRules(t *testing.T) {
	timeout := "ok  \tcompound/internal/act\t12.1s\npanic: test timed out after 40m0s\n\trunning tests:\n\t\tTestSlowFixture (40m0s)\n\ngoroutine 1 [running]:\nFAIL\tcompound/internal/work\t2406.3s\nFAIL\n"
	cases := []struct {
		name, tail, want string
		tests            []string
		resource         bool
	}{
		{"go test failures keep every top-level name", "--- FAIL: TestBeta (1.00s)\n    --- FAIL: TestBeta/case_one (0.00s)\n--- FAIL: TestGamma (2.00s)\nFAIL\n", "fail:TestBeta", []string{"TestBeta", "TestGamma"}, false},
		{"package timeout", timeout, "package-timeout:compound/internal/work", nil, true},
		{"slow package without panic", "FAIL\tcompound/internal/work\t1900.0s\n", "package-timeout:compound/internal/work", nil, true},
		{"fast package fail is not a timeout", "FAIL\tcompound/internal/hook\t424.9s\n", "FAIL compound/internal/hook <n>s", nil, false},
		{"context canceled", "step one\nrun: context canceled\n", "resource:context canceled", nil, true},
		{"marked line beats the summary", "scan done\nMEDIUM pii.phone.e164 40:58 12*****\nHIGH=0 MEDIUM=3 LOW=0 WARN=0\n", "MEDIUM pii.phone.e<n> <n>:<n> <n><mask>", nil, false},
		{"mask length does not split a cluster", "MEDIUM pii.phone.e164 12:4 12*******\n", "MEDIUM pii.phone.e<n> <n>:<n> <n><mask>", nil, false},
		{"short star runs stay", "** TEST FAILED **\n", "** TEST FAILED **", nil, false},
		{"last non-empty line", "phase one\nexit status 3\n\n", "exit status <n>", nil, false},
		{"tail cut off on a passing package is a cancelled run", "testbattery: phase 1/2 packages=108\nok  \tcompound/adapters/example-session\t5.389s", "resource:cancelled-run", nil, true},
		{"tail cut off on a phase header is a cancelled run", "testbattery: phase 1/3 packages=107\n", "resource:cancelled-run", nil, true},
		{"a failing package after the ok line is not cancelled", "ok  \tcompound/internal/a\t1.0s\nFAIL\tcompound/internal/hook\t424.9s\n", "FAIL compound/internal/hook <n>s", nil, false},
	}
	for _, tc := range cases {
		sig, tests, resource := signatureFromTail(tc.tail)
		if sig != tc.want || !reflect.DeepEqual(tests, tc.tests) || resource != tc.resource {
			t.Errorf("%s: got (%q, %v, %v), want (%q, %v, %v)", tc.name, sig, tests, resource, tc.want, tc.tests, tc.resource)
		}
	}
}

func TestFailureLinesRecoverTestNameWhenTailScrolledPast(t *testing.T) {
	h := t.TempDir()
	tail := "ok  \tcompound/internal/writeback\t7.6s\nFAIL\ntestbattery: phase 2/2 packages=1\nok  \tcompound/internal/release\t310.3s\ntestbattery: phase 1: exit status 1\nexit status 1\n"
	ref := putReportWithFailureLines(t, h, "fp-alpha", "go-test", tail, []string{"--- FAIL: TestScrolledAway (1.18s)", "FAIL\tcompound/cmd/compound\t1282.0s"})
	putResult(t, h, "pfrs_fixture_fl1", "work_fixture_a", "failed", t0.Add(-time.Hour), ref, "fp-alpha")
	s := bySignature(t, mustBuild(t, h, t0), "fail:TestScrolledAway")
	if s.Step != "go-test" || !reflect.DeepEqual(s.Tests, []string{"TestScrolledAway"}) || s.ResourceShape {
		t.Fatalf("failure_lines cluster = %+v", s)
	}
}

func TestPackageTimeoutClusterFromReport(t *testing.T) {
	h := t.TempDir()
	tail := "ok  \tcompound/internal/act\t12.1s\npanic: test timed out after 40m0s\nFAIL\tcompound/internal/work\t2406.3s\nFAIL\n"
	ref := putReport(t, h, "", "fp-alpha", stepFixture{"go-test", "failed", tail})
	putResult(t, h, "pfrs_fixture_t1", "work_fixture_a", "failed", t0.Add(-time.Hour), ref, "fp-alpha")
	if s := bySignature(t, mustBuild(t, h, t0), "package-timeout:compound/internal/work"); !s.ResourceShape || s.Step != "go-test" {
		t.Fatalf("timeout cluster = %+v", s)
	}
}

func TestNonJSONArtifactIsOneWarningAndCountedWithoutReport(t *testing.T) {
	h := t.TempDir()
	ref := putArtifact(t, h, []byte("this report is not json"))
	putResult(t, h, "pfrs_fixture_bad", "work_fixture_a", "failed", t0.Add(-time.Hour), ref, "fp-alpha")
	putResult(t, h, "pfrs_fixture_noreport", "work_fixture_b", "failed", t0.Add(-time.Hour), "", "")
	proj, err := Build(h, BuildOptions{Now: t0})
	if err != nil {
		t.Fatalf("a corrupt artifact must never fail the build: %v", err)
	}
	if want := (Totals{FailedResults: 2, WithoutReport: 2}); proj.Totals != want || len(proj.Signatures) != 0 {
		t.Fatalf("totals = %+v signatures=%d", proj.Totals, len(proj.Signatures))
	}
	artifactWarnings := 0
	for _, w := range proj.Warnings {
		if w.Source == "artifact" {
			artifactWarnings++
			if w.Code != "invalid_json" || !strings.Contains(w.Detail, "pfrs_fixture_bad") {
				t.Fatalf("artifact warning = %+v", w)
			}
		}
	}
	if artifactWarnings != 1 {
		t.Fatalf("want exactly one artifact warning, got %+v", proj.Warnings)
	}
}

// Public-safety falsifier: no absolute path, home path, hex digest or report repo
// path survives into the projection.
func TestProjectionNeverCarriesPathsOrDigests(t *testing.T) {
	h := t.TempDir()
	digest := strings.Repeat("0123456789abcdef", 3)[:40]
	repo := "/srv/fixture/example/checkout"
	line := "error: open /srv/fixture/example/run.log via ~/fixture/state: digest " + digest + " mismatch"
	ref := putReport(t, h, repo, "fp-report", stepFixture{"go-vet", "failed", "vet starting\n" + line + "\n"})
	putResult(t, h, "pfrs_fixture_p1", "work_fixture_a", "failed", t0.Add(-time.Hour), ref, "fp-record")
	proj := mustBuild(t, h, t0)
	if len(proj.Signatures) != 1 {
		t.Fatalf("signatures = %+v", proj.Signatures)
	}
	s := proj.Signatures[0]
	if s.Signature != "error: open <path> via <path> digest <hex> mismatch" {
		t.Fatalf("signature = %q", s.Signature)
	}
	if !reflect.DeepEqual(s.Repositories, []string{"fp-record"}) {
		t.Fatalf("repository identity must be the record fingerprint, got %v", s.Repositories)
	}
	path, err := WriteProjection(h, proj)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"/srv/", "~/", digest, repo, h} {
		if bytes.Contains(body, []byte(leak)) {
			t.Fatalf("projection leaks %q:\n%s", leak, body)
		}
	}
}

func TestRepositoryFallsBackToReportFingerprintNeverRepoPath(t *testing.T) {
	h := t.TempDir()
	ref := putReport(t, h, "/srv/fixture/example/checkout", "fp-report", stepFixture{"go-test", "failed", goTestTail("TestAlpha")})
	putResult(t, h, "pfrs_fixture_f1", "work_fixture_a", "failed", t0.Add(-time.Hour), ref, "")
	if s := bySignature(t, mustBuild(t, h, t0), "fail:TestAlpha"); !reflect.DeepEqual(s.Repositories, []string{"fp-report"}) {
		t.Fatalf("repositories = %v", s.Repositories)
	}
}

func TestBuildIsDeterministic(t *testing.T) {
	h := t.TempDir()
	seedAlpha(t, h)
	first := mustBuild(t, h, t0)
	if _, err := WriteProjection(h, first); err != nil {
		t.Fatal(err)
	}
	second := mustBuild(t, h, t0.Add(time.Minute))
	a, _ := json.Marshal(first.Signatures)
	b, _ := json.Marshal(second.Signatures)
	if !bytes.Equal(a, b) {
		t.Fatalf("signatures differ between builds:\n%s\n%s", a, b)
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"took 2406.3s in   step 12":                        "took <n>s in step <n>",
		"unix:///fixture/run/daemon.sock refused":          "unix:<path> refused",
		"from '/srv/fixture/a.tsx' failed":                 "from '<path>' failed",
		"relative compound/internal/work stays":            "relative compound/internal/work stays",
		"commit " + strings.Repeat("ab12", 10) + " is bad": "commit <hex> is bad",
		"word defaced keeps letters":                       "word defaced keeps letters",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Normalize(strings.Repeat("x", 400)); len([]rune(got)) != maxSignatureRunes {
		t.Fatalf("cap = %d runes", len([]rune(got)))
	}
}

func TestShortSignatureIsNeverFuzzilyExplained(t *testing.T) {
	h := t.TempDir()
	ref := putReport(t, h, "", "fp-alpha", stepFixture{"go-test", "failed", "FAIL\n"})
	putResult(t, h, "pfrs_fixture_s1", "work_fixture_a", "failed", t0.Add(-time.Hour), ref, "fp-alpha")
	if _, err := finding.Record(h, finding.Finding{Topic: "battery note", Body: "the battery printed FAIL again"}, t0); err != nil {
		t.Fatal(err)
	}
	if s := bySignature(t, mustBuild(t, h, t0), "FAIL"); s.State != StateSingleton || len(s.Related) != 0 {
		t.Fatalf("a bare FAIL signature must not match prose: %+v", s)
	}
}

func TestRunCLIUsageAndMissingProjection(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 || stderr.String() != Usage+"\n" {
		t.Fatalf("no args: code=%d stderr=%q", code, stderr.String())
	}
	for _, args := range [][]string{{"bogus"}, {"list", "--since", "1h"}, {"build", "--top", "3"}, {"list", "--state", "noisy"}, {"list", "--home"}} {
		stderr.Reset()
		if code := run(args, &stdout, &stderr); code != 2 {
			t.Fatalf("%v: code=%d, want 2", args, code)
		}
	}
	h := t.TempDir()
	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"list", "--home", h}, &stdout, &stderr); code != 1 || stdout.Len() != 0 ||
		strings.TrimSpace(stderr.String()) != "interest: no projection yet; run `compound interest build`" {
		t.Fatalf("list without projection: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunCLIBuildThenList(t *testing.T) {
	h := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"build", "--home", h, "--since", "720h"}, &stdout, &stderr); code != 0 {
		t.Fatalf("build: code=%d stderr=%q", code, stderr.String())
	}
	if want := "interest: 0 failed results in 30d; 0.0% assimilated; 0 learnable signatures; progress since none: +0 assimilated (0 results absorbed)\n"; !strings.HasPrefix(stdout.String(), want) {
		t.Fatalf("build output = %q", stdout.String())
	}
	stdout.Reset()
	if code := run([]string{"list", "--home", h, "--json", "--top", "5"}, &stdout, &stderr); code != 0 {
		t.Fatalf("list: code=%d stderr=%q", code, stderr.String())
	}
	var proj Projection
	if err := json.Unmarshal(stdout.Bytes(), &proj); err != nil || proj.SchemaVersion != SchemaVersion || proj.ProjectionRef != ProjectionRef {
		t.Fatalf("list --json = %v %q", err, stdout.String())
	}
}

func TestUnknownExplanationScopeNeverAssimilates(t *testing.T) {
	for _, tc := range []struct {
		name     string
		observed []string
		named    []string
	}{
		{"one repository without declared scope", []string{"fp-alpha", "fp-alpha"}, nil},
		{"unknown repositories without declared scope", []string{"", ""}, nil},
		{"unknown repositories with named scope", []string{"", ""}, []string{"fp-alpha"}},
		{"unknown repositories with wildcard scope", []string{"", ""}, []string{RepositoryWildcard}},
		{"mixed known and unknown repository", []string{"fp-alpha", ""}, []string{"fp-alpha"}},
		{"mixed known and unknown with wildcard", []string{"fp-alpha", ""}, []string{RepositoryWildcard}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := t.TempDir()
			for i, fp := range tc.observed {
				ref := putReport(t, h, "", fp, stepFixture{"go-test", "failed", goTestTail("TestScopedFixture")})
				putResult(t, h, fmt.Sprintf("pfrs_scope_%d", i), fmt.Sprintf("work_scope_%d", i), "failed", t0.Add(-time.Duration(i+1)*time.Hour), ref, fp)
			}
			before := mustBuild(t, h, t0)
			if _, err := WriteProjection(h, before); err != nil {
				t.Fatal(err)
			}
			id := SignatureID("go-test", "fail:TestScopedFixture")
			refs := []string{SignatureEvidencePrefix + id}
			for _, fp := range tc.named {
				refs = append(refs, RepositoryEvidencePrefix+fp)
			}
			f, err := finding.Record(h, finding.Finding{Topic: "scoped cause", Body: "diagnosis with scope still requiring evidence", EvidenceRefs: refs}, t0)
			if err != nil {
				t.Fatal(err)
			}
			after := mustBuild(t, h, t0.Add(time.Hour))
			s := bySignature(t, after, "fail:TestScopedFixture")
			want := []Explanation{{Ref: "finding:" + f.ID, Match: MatchExact, Scope: ScopeUnscoped}}
			if s.State != StateLearnable || len(s.ExplainedBy) != 0 || !reflect.DeepEqual(s.Related, want) {
				t.Fatalf("unknown scope must remain related: %+v", s)
			}
			if after.Totals.AssimilatedResults != 0 || len(after.Progress.NewlyAssimilated) != 0 {
				t.Fatalf("unknown scope counted as learning: totals=%+v progress=%+v", after.Totals, after.Progress)
			}
		})
	}
}

func TestExplicitSingleRepositoryExplanationAssimilates(t *testing.T) {
	h := t.TempDir()
	seedAlpha(t, h)
	id := SignatureID("go-test", "fail:TestAlpha")
	f, err := finding.Record(h, finding.Finding{Topic: "scoped cause", Body: "diagnosis on the named repository", EvidenceRefs: []string{SignatureEvidencePrefix + id, RepositoryEvidencePrefix + "fp-alpha"}}, t0)
	if err != nil {
		t.Fatal(err)
	}
	s := bySignature(t, mustBuild(t, h, t0), "fail:TestAlpha")
	if s.State != StateAssimilated || !reflect.DeepEqual(s.ExplainedBy, []Explanation{{Ref: "finding:" + f.ID, Match: MatchExact}}) {
		t.Fatalf("explicit matching scope rejected: %+v", s)
	}
}

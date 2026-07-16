package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"compound/internal/capgraph"
	"compound/internal/envelope"
	"compound/internal/finding"
	"compound/internal/hookplan"
)

// surfacingRowByHarness finds the matrix row whose aliased harness matches (claude|codex|…).
func surfacingRowByHarness(rows []surfacingRow, harness string) (surfacingRow, bool) {
	for _, r := range rows {
		if r.Harness == harness {
			return r, true
		}
	}
	return surfacingRow{}, false
}

func rowBelow(r surfacingRow, col string) bool {
	for _, c := range r.BelowExpectation {
		if c == col {
			return true
		}
	}
	return false
}

// writeSyntheticOrient drops a minimal recorded EffectiveContract for a harness into the home so
// lastOrientByHarness attributes an orient at ts. It carries exactly the fields the reader reads
// (schema_version/harness/created_at), which is the orient-produced evidence class the matrix uses.
func writeSyntheticOrient(t *testing.T, home, harness string, ts time.Time) {
	t.Helper()
	dir := filepath.Join(home, "contracts", "effective", ts.UTC().Format("2006-01-02"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir orient dir: %v", err)
	}
	body := fmt.Sprintf(`{"schema_version":%q,"harness":%q,"created_at":%q}`,
		envelope.SVEffectiveContract, harness, ts.UTC().Format(time.RFC3339))
	if err := os.WriteFile(filepath.Join(dir, "orient-"+harness+".json"), []byte(body+"\n"), 0o644); err != nil {
		t.Fatalf("write synthetic orient: %v", err)
	}
}

// F1 — the matrix mirrors today's real machine: claude guard ABSENT, codex guard DRIFT, both
// flagged below expectation. Built synthetically: install both harness hooks with one binary,
// delete the claude config (=> absent), then check with a different binary (=> codex drifts).
func TestDoctorSurfacing_F1_GuardAbsentAndDrift(t *testing.T) {
	home := t.TempDir()
	base := t.TempDir()
	binaryA := "/opt/compound-a/compound"
	binaryB := "/opt/compound-b/compound"

	if _, err := hookplan.InstallHooks(base, binaryA, "", nil, false); err != nil {
		t.Fatalf("install hooks: %v", err)
	}
	if err := os.Remove(filepath.Join(base, hookplan.ConfigRel("claude-code", ""))); err != nil {
		t.Fatalf("remove claude config: %v", err)
	}

	rep := runDoctorReport(t, home, base, binaryB)

	claude, ok := surfacingRowByHarness(rep.Surfacing, "claude")
	if !ok {
		t.Fatalf("no claude row in matrix: %+v", rep.Surfacing)
	}
	if claude.Guard != "absent" || !rowBelow(claude, "guard") {
		t.Fatalf("claude guard should be absent+below, got %q below=%v", claude.Guard, claude.BelowExpectation)
	}
	codex, ok := surfacingRowByHarness(rep.Surfacing, "codex")
	if !ok {
		t.Fatalf("no codex row in matrix: %+v", rep.Surfacing)
	}
	if codex.Guard != "drift" || !rowBelow(codex, "guard") {
		t.Fatalf("codex guard should be drift+below, got %q below=%v", codex.Guard, codex.BelowExpectation)
	}
	// The trailing "!" is a render of below-expectation; prove the text agrees.
	text := runDoctorText(t, home, base, binaryB)
	if !strings.Contains(text, "absent!") || !strings.Contains(text, "drift!") {
		t.Fatalf("text should mark absent!/drift!:\n%s", text)
	}
}

// F2 — a harness with no recorded orient shows orient:never; after a synthetic orient artifact is
// written for it, the same harness flips to orient:wired and drops "orient" from below-expectation.
// A foreign-schema file in the same tree must NOT count (depth probe on the schema filter).
func TestDoctorSurfacing_F2_OrientNeverThenWired(t *testing.T) {
	home := t.TempDir()
	base := t.TempDir()
	binary := "/opt/compound/compound"

	before := runDoctorReport(t, home, base, binary)
	codex, ok := surfacingRowByHarness(before.Surfacing, "codex")
	if !ok {
		t.Fatalf("no codex row: %+v", before.Surfacing)
	}
	if codex.Orient != "never" || !rowBelow(codex, "orient") {
		t.Fatalf("codex orient should be never+below, got %q below=%v", codex.Orient, codex.BelowExpectation)
	}

	// A non-EffectiveContract file must be ignored by the orient scan.
	foreignDir := filepath.Join(home, "contracts", "effective", "2026-07-14")
	if err := os.MkdirAll(foreignDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(foreignDir, "foreign.json"), []byte(`{"schema_version":"other","harness":"codex","created_at":"2026-07-14T10:00:00Z"}`), 0o644); err != nil {
		t.Fatalf("write foreign: %v", err)
	}
	stillNever := runDoctorReport(t, home, base, binary)
	if r, _ := surfacingRowByHarness(stillNever.Surfacing, "codex"); r.Orient != "never" {
		t.Fatalf("foreign-schema file must not count as orient, got %q", r.Orient)
	}

	// Now a real orient artifact for codex, three hours ago.
	writeSyntheticOrient(t, home, "codex", time.Now().Add(-3*time.Hour))
	after := runDoctorReport(t, home, base, binary)
	codex2, _ := surfacingRowByHarness(after.Surfacing, "codex")
	if codex2.Orient != "wired" {
		t.Fatalf("codex orient should be wired after synthetic orient, got %q", codex2.Orient)
	}
	if rowBelow(codex2, "orient") {
		t.Fatalf("wired orient must not be below expectation: %v", codex2.BelowExpectation)
	}
	if codex2.OrientAge == "" {
		t.Fatalf("wired orient should carry a coarse age")
	}
}

// F3 — a fresh, empty home (no capgraph, no hooks) still renders a non-empty matrix: fallback
// harness rows plus the explicit no-lanes note, exit 0, no crash.
func TestDoctorSurfacing_F3_FreshHomeFallbackRows(t *testing.T) {
	home := t.TempDir()
	base := t.TempDir() // empty base => both guards absent

	var out, errOut bytes.Buffer
	args := []string{"--home", home, "--base", base, "--binary", "/opt/compound/compound", "--json"}
	if code := runDoctor(args, &out, &errOut); code != 0 {
		t.Fatalf("fresh-home doctor must exit 0, code=%d stderr=%q", code, errOut.String())
	}
	var rep doctorReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("parse json: %v (%q)", err, out.String())
	}
	if len(rep.Surfacing) < 2 {
		t.Fatalf("fresh home must show fallback rows, got %d", len(rep.Surfacing))
	}
	if rep.SurfacingNote != "no agent lanes imported — showing hook harnesses only" {
		t.Fatalf("fresh home should carry the no-lanes note, got %q", rep.SurfacingNote)
	}
	if _, ok := surfacingRowByHarness(rep.Surfacing, "claude"); !ok {
		t.Fatalf("fallback rows must include claude: %+v", rep.Surfacing)
	}
	if _, ok := surfacingRowByHarness(rep.Surfacing, "codex"); !ok {
		t.Fatalf("fallback rows must include codex: %+v", rep.Surfacing)
	}
	if rep.SurfacingInjection.Scope != "global" || rep.SurfacingInjection.LastConsumed != "never" {
		t.Fatalf("fresh-home injection should be global/never, got %+v", rep.SurfacingInjection)
	}
}

// F4 — --json exposes the surfacing array with stable field names, and the text render agrees with
// the JSON (same guard tokens, same global injection numbers, same S2 peers caveat).
func TestDoctorSurfacing_F4_JSONAndTextAgree(t *testing.T) {
	home := t.TempDir()
	base := t.TempDir()
	binary := "/opt/compound/compound"

	var jsonOut, errOut bytes.Buffer
	jsonArgs := []string{"--home", home, "--base", base, "--binary", binary, "--json"}
	if code := runDoctor(jsonArgs, &jsonOut, &errOut); code != 0 {
		t.Fatalf("json doctor failed: %s", errOut.String())
	}
	raw := jsonOut.String()
	for _, field := range []string{`"surfacing"`, `"surfacing_injection"`, `"guard"`, `"orient"`, `"peers"`} {
		if !strings.Contains(raw, field) {
			t.Fatalf("json missing stable field %s:\n%s", field, raw)
		}
	}
	var rep doctorReport
	if err := json.Unmarshal(jsonOut.Bytes(), &rep); err != nil {
		t.Fatalf("parse json: %v", err)
	}

	text := runDoctorText(t, home, base, binary)
	if !strings.Contains(text, "## surfacing coverage") {
		t.Fatalf("text missing surfacing section:\n%s", text)
	}
	injLine := fmt.Sprintf("served %d, consumed %d", rep.SurfacingInjection.ServedEvents, rep.SurfacingInjection.ConsumedEvents)
	if !strings.Contains(text, injLine) {
		t.Fatalf("text injection numbers must match json (%s):\n%s", injLine, text)
	}
	if !strings.Contains(text, "global") {
		t.Fatalf("text must state the injection scope is global:\n%s", text)
	}
	// Slice C flips the peers cell from the static "absent (S2)" placeholder to an evidence read:
	// the S2 caveat is gone and the peers column is now a real state (absent on a fresh home).
	if strings.Contains(text, "(S2)") {
		t.Fatalf("static (S2) peers caveat must be gone:\n%s", text)
	}
	if !strings.Contains(text, "peers") {
		t.Fatalf("text must carry the peers column/note:\n%s", text)
	}
	// Each row's guard and peers tokens from JSON appear in the text.
	for _, r := range rep.Surfacing {
		if !strings.Contains(text, r.Guard) {
			t.Fatalf("guard token %q from json row missing in text:\n%s", r.Guard, text)
		}
		if !strings.Contains(text, r.Peers) {
			t.Fatalf("peers token %q from json row missing in text:\n%s", r.Peers, text)
		}
	}
}

// When agent lanes are imported into the capgraph, the matrix rows are the lane nodes (not the
// fallback bare harnesses) and the no-lanes note is absent — while the guard/orient columns still
// join by the lane's aliased harness. Covers the imported-lanes branch of buildSurfacingCoverage.
func TestDoctorSurfacing_LaneRowsFromCapgraph(t *testing.T) {
	home := t.TempDir()
	base := t.TempDir()
	binary := "/opt/compound/compound"

	store, err := capgraph.Open(home)
	if err != nil {
		t.Fatalf("open capgraph: %v", err)
	}
	for _, n := range []capgraph.Node{
		{ID: "agent-lane:codex--auto-build--gpt--balanced", Kind: "agent-lane", Name: "codex / auto/build / gpt / balanced", Weight: 0.25, EvidenceOnly: true},
		{ID: "agent-lane:claude-code--auto-build--claude--balanced", Kind: "agent-lane", Name: "claude-code / auto/build / claude / balanced", Weight: 0.25, EvidenceOnly: true},
	} {
		if err := store.Add(n); err != nil {
			t.Fatalf("seed lane %s: %v", n.ID, err)
		}
	}

	rep := runDoctorReport(t, home, base, binary)
	if rep.SurfacingNote != "" {
		t.Fatalf("imported lanes => no fallback note, got %q", rep.SurfacingNote)
	}
	if len(rep.Surfacing) != 2 {
		t.Fatalf("want 2 lane rows, got %d: %+v", len(rep.Surfacing), rep.Surfacing)
	}
	for _, r := range rep.Surfacing {
		if !strings.Contains(r.Lane, " / ") {
			t.Fatalf("row lane should be a lane name, got %q", r.Lane)
		}
	}
	if _, ok := surfacingRowByHarness(rep.Surfacing, "codex"); !ok {
		t.Fatalf("codex lane row missing: %+v", rep.Surfacing)
	}
	if _, ok := surfacingRowByHarness(rep.Surfacing, "claude"); !ok {
		t.Fatalf("claude lane row missing: %+v", rep.Surfacing)
	}
}

// runDoctorReport runs doctor with --json and returns the parsed report.
func runDoctorReport(t *testing.T, home, base, binary string) doctorReport {
	t.Helper()
	var out, errOut bytes.Buffer
	args := []string{"--home", home, "--base", base, "--binary", binary, "--json"}
	if code := runDoctor(args, &out, &errOut); code != 0 {
		t.Fatalf("doctor --json failed: code=%d stderr=%q", code, errOut.String())
	}
	var rep doctorReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("parse doctor json: %v (%q)", err, out.String())
	}
	return rep
}

// runDoctorText runs doctor in text mode and returns stdout.
func runDoctorText(t *testing.T, home, base, binary string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	args := []string{"--home", home, "--base", base, "--binary", binary}
	if code := runDoctor(args, &out, &errOut); code != 0 {
		t.Fatalf("doctor text failed: code=%d stderr=%q", code, errOut.String())
	}
	return out.String()
}

// compound doctor composes existing organ states into one read-only surface. This proves the
// composition end-to-end: an empty home reports hooks-absent + an install hint, lease-free, zero
// findings, and the fail-open placement default; recording a finding plus a superseding revision
// moves the live/superseded counts. It writes nothing.
func TestDoctorComposesOrganState(t *testing.T) {
	// Determinism: placement mode is overridable by env (policy.applyPlacementGuardEnv reads
	// COMPOUND_PLACEMENT_GUARD_MODE / PLACEMENT_GUARD_MODE). Clear both so this test always exercises
	// the built-in default (observe) regardless of the operator's ambient shell — the env-dependence
	// is what made this assertion flip between "block" and "observe" across environments.
	t.Setenv("COMPOUND_PLACEMENT_GUARD_MODE", "")
	t.Setenv("PLACEMENT_GUARD_MODE", "")

	home := t.TempDir()
	base := t.TempDir() // empty base => no harness hooks installed

	var out, errOut bytes.Buffer
	args := []string{"--home", home, "--base", base, "--binary", "/usr/local/bin/compound", "--json"}
	if code := runDoctor(args, &out, &errOut); code != 0 {
		t.Fatalf("doctor should succeed, code=%d stderr=%q", code, errOut.String())
	}
	var rep doctorReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("parse doctor json: %v (%q)", err, out.String())
	}
	if len(rep.Hooks) == 0 {
		t.Fatal("doctor should report per-harness hook status")
	}
	for _, h := range rep.Hooks {
		if h.Status != "absent" {
			t.Fatalf("empty base => hook %s should be absent, got %q", h.Harness, h.Status)
		}
	}
	if rep.HookHint == "" {
		t.Fatal("absent hooks should yield an install hint")
	}
	if rep.LeaseHeld {
		t.Fatal("no lease should be held")
	}
	// policy.DefaultConfig and an empty-home policy.LoadConfig use observe. The block assertion in
	// TestLoadConfigOverlaysDefaultPolicy follows an explicit policy.yaml fixture; it is not the
	// built-in default. Doctor must report the loaded mode and make fail-open staging explicit.
	if rep.PlacementMode != "observe" {
		t.Fatalf("empty-home placement mode should be observe, got %q", rep.PlacementMode)
	}
	if rep.PlacementNote == "" {
		t.Fatal("observe mode should carry an explicit fail-open warning")
	}
	if rep.FindingsTotal != 0 {
		t.Fatalf("no findings expected, got %d", rep.FindingsTotal)
	}

	now := time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	f1, err := finding.Record(home, finding.Finding{Topic: "deploy-runbook", Body: "v1 steps"}, now)
	if err != nil {
		t.Fatalf("record f1: %v", err)
	}
	if _, err := finding.Record(home, finding.Finding{Topic: "deploy-runbook", Body: "v2 steps", Supersedes: f1.ID}, now.Add(time.Minute)); err != nil {
		t.Fatalf("record f2: %v", err)
	}

	out.Reset()
	errOut.Reset()
	if code := runDoctor(args, &out, &errOut); code != 0 {
		t.Fatalf("doctor rerun failed: %s", errOut.String())
	}
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("parse doctor json (rerun): %v", err)
	}
	if rep.FindingsTotal != 2 || rep.FindingsLive != 1 || rep.FindingsSuperseded != 1 {
		t.Fatalf("findings counts wrong: total=%d live=%d superseded=%d", rep.FindingsTotal, rep.FindingsLive, rep.FindingsSuperseded)
	}
}

// TestFindingRecordPersistsEveryEvidenceFlag replays the multi --evidence CLI
// invocation that silently dropped all but the first ref (live repro
// find_ddcc04dc0641: 3 of 4 refs lost). It falsifies the single-valueFlag parse
// and proves the recorded finding carries -- and finding show reads back -- every
// ref that was passed.
func TestFindingRecordPersistsEveryEvidenceFlag(t *testing.T) {
	home := t.TempDir()
	now := time.Date(2026, 7, 16, 20, 0, 0, 0, time.UTC)

	args := []string{
		"--topic", "multi-ev",
		"--body", "four evidence refs passed at record time",
		"--evidence", "commit:aaa111",
		"--evidence", "check:bbb222",
		"--evidence", "land-check:ccc333",
		"--evidence", "live:ddd444",
	}

	want := []string{"commit:aaa111", "check:bbb222", "land-check:ccc333", "live:ddd444"}
	refs := findingEvidenceRefs(args)
	if !reflect.DeepEqual(refs, want) {
		t.Fatalf("findingEvidenceRefs parsed %v, want all four %v", refs, want)
	}

	rec, err := finding.Record(home, finding.Finding{
		Topic:        valueFlag(args, "--topic"),
		Body:         valueFlag(args, "--body"),
		EvidenceRefs: refs,
	}, now)
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	shown, err := finding.Show(home, rec.ID)
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if !reflect.DeepEqual(shown.EvidenceRefs, want) {
		t.Fatalf("finding show evidence = %v, want %v (only the first ref surviving is the original bug)", shown.EvidenceRefs, want)
	}
}

// TestFindingEvidenceRefsCommaAndRepeatBackCompat proves the comma-joined form
// still works, mixes with repeated flags, and drops empty/whitespace refs.
func TestFindingEvidenceRefsCommaAndRepeatBackCompat(t *testing.T) {
	got := findingEvidenceRefs([]string{
		"--evidence", "a,b",
		"--evidence", " c ",
		"--evidence", "",
		"--evidence", "d,,e",
	})
	want := []string{"a", "b", "c", "d", "e"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("comma/repeat/empty handling = %v, want %v", got, want)
	}
}

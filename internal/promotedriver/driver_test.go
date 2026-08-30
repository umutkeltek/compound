package promotedriver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/eventstore"
	"compound/internal/mem"
)

var testNow = time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)

// seedCandidate writes a candidate file directly into the memory inbox.
// The driver reads whatever the inbox holds; writing fixtures directly
// (as lanebeliefs and adapters also do) keeps each test in control of
// state, basis, and duplicate shape without Capture's echo-collapse.
func seedCandidate(t *testing.T, home, id string, c mem.Candidate) {
	t.Helper()
	c.ID = id // Capture sets the payload id before WriteCandidate; mirror it
	if c.State == "" {
		c.State = "proposed"
	}
	if c.Visibility == "" {
		c.Visibility = "operational"
	}
	if c.Derivation == "" {
		c.Derivation = "extracted"
	}
	if c.CreatedAt == "" {
		c.CreatedAt = "2026-08-25T00:00:00Z"
	}
	if err := candidates.WriteCandidate(home, candidates.Memory, id, &c); err != nil {
		t.Fatalf("seed candidate %s: %v", id, err)
	}
}

func candidateStates(t *testing.T, home string) map[string]string {
	t.Helper()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	cands, err := store.AllCandidates()
	if err != nil {
		t.Fatalf("all candidates: %v", err)
	}
	out := map[string]string{}
	for _, c := range cands {
		out[c.ID] = c.State
	}
	return out
}

// candidateStatesDirect decodes each inbox file's state field without
// opening the store — mem.Open scaffolds ledger/ and events/, which would
// contaminate the plan pass's byte-identical-home assertion.
func candidateStatesDirect(t *testing.T, home string) map[string]string {
	t.Helper()
	dir := filepath.Join(home, candidates.InboxDir(candidates.Memory))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read inbox: %v", err)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		var lite struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal(data, &lite); err != nil {
			t.Fatalf("decode %s: %v", e.Name(), err)
		}
		out[strings.TrimSuffix(e.Name(), ".json")] = lite.State
	}
	return out
}

func readReceipts(t *testing.T, home string) []Report {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, stateDirName, receiptFileName))
	if err != nil {
		t.Fatalf("read receipts: %v", err)
	}
	var out []Report
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var r Report
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("receipt line not JSON: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func passReceipts(t *testing.T, receipts []Report, passID string) []Report {
	t.Helper()
	var out []Report
	for _, receipt := range receipts {
		if receipt.PassID == passID {
			out = append(out, receipt)
		}
	}
	return out
}

func assertFrozenJournal(t *testing.T, receipts []Report, passID, terminalStage string) (Report, Report) {
	t.Helper()
	frames := passReceipts(t, receipts, passID)
	if len(frames) < 2 || frames[0].Stage != stagePlanned || frames[len(frames)-1].Stage != terminalStage {
		t.Fatalf("pass %s journal stages = %+v, want planned ... %s", passID, frames, terminalStage)
	}
	planned := frames[0]
	terminal := frames[len(frames)-1]
	if planned.Intent == nil {
		t.Fatalf("pass %s planned frame has no intent", passID)
	}
	if len(frames) != len(planned.Intent.Operations)+2 {
		t.Fatalf("pass %s frame count = %d, want plan + %d operations + terminal", passID, len(frames), len(planned.Intent.Operations))
	}
	for index, operation := range planned.Intent.Operations {
		frame := frames[index+1]
		if frame.Stage != stageOperationRecorded || len(frame.OperationResults) != 1 || frame.OperationResults[0].Key != operation.Key {
			t.Fatalf("pass %s operation frame %d = %+v, want key %s", passID, index, frame, operation.Key)
		}
	}
	if len(terminal.OperationResults) != len(planned.Intent.Operations) {
		t.Fatalf("pass %s terminal result count = %d, want %d", passID, len(terminal.OperationResults), len(planned.Intent.Operations))
	}
	return planned, terminal
}

// listHomeTree returns every path under home, relative, for footprint
// assertions. Order is deterministic (filepath.Walk).
func listHomeTree(t *testing.T, home string) []string {
	t.Helper()
	var out []string
	if err := filepath.Walk(home, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == home {
			return nil
		}
		rel, _ := filepath.Rel(home, path)
		out = append(out, rel)
		return nil
	}); err != nil {
		t.Fatalf("walk home: %v", err)
	}
	return out
}

func TestPlanPassLeavesHomeByteIdentical(t *testing.T) {
	home := t.TempDir()
	seedCandidate(t, home, "cand_dup1", mem.Candidate{Entity: "dup", Predicate: "value", Object: "same"})
	seedCandidate(t, home, "cand_dup2", mem.Candidate{Entity: "dup", Predicate: "value", Object: "same"})
	seedCandidate(t, home, "cand_basis", mem.Candidate{Entity: "basis", Predicate: "value", Object: "x", PromotionBasis: candidates.BasisRuntimeProof})
	seedCandidate(t, home, "cand_import", mem.Candidate{Entity: "imp", Predicate: "value", Object: "y", Qualifiers: map[string]string{"source": "knowledge_import"}})
	seedCandidate(t, home, "cand_plain", mem.Candidate{Entity: "plain", Predicate: "value", Object: "z"})
	before := listHomeTree(t, home)

	// Options{} IS the plan pass: the zero value must be the safe mode, so
	// a programmatic caller that constructs Options{} can never mutate.
	report, err := Run(home, Options{}, testNow)
	if err != nil {
		t.Fatalf("plan pass: %v", err)
	}
	if !report.DryRun {
		t.Fatal("plan report must carry dry_run")
	}
	if report.Stage != "" {
		t.Fatalf("plan pass journals nothing, got stage %q", report.Stage)
	}
	if report.Promote.Promoted != 1 {
		t.Fatalf("plan would-promote count = %d, want 1", report.Promote.Promoted)
	}
	if report.Promote.Promotions[0].Action != "would_promote" {
		t.Fatalf("promotion action = %q, want would_promote", report.Promote.Promotions[0].Action)
	}
	if report.Promote.Promotions[0].Basis != candidates.BasisRuntimeProof {
		t.Fatalf("receipt basis = %q, want %q", report.Promote.Promotions[0].Basis, candidates.BasisRuntimeProof)
	}
	if report.Dedupe.Groups != 1 || report.Dedupe.Collapsed != 1 {
		t.Fatalf("dedupe groups=%d collapsed=%d, want 1/1", report.Dedupe.Groups, report.Dedupe.Collapsed)
	}
	if report.Dedupe.Actions[0].Kept != "cand_dup1" {
		t.Fatalf("dedupe keeper = %q, want cand_dup1 (earliest created, smallest id)", report.Dedupe.Actions[0].Kept)
	}
	// Plan sweep plans every still-proposed candidate (the basis candidate
	// was not actually promoted) — except the basis candidate itself:
	// disposition defers it to the per-id promotion door.
	if got := report.Sweep.Adjudicated; got != 0 {
		t.Fatalf("plan sweep adjudicated = %d, want 0 actual mutations", got)
	}
	if got := len(report.Sweep.Actions); got != 4 {
		t.Fatalf("plan sweep actions = %d, want 4", got)
	}
	if report.Sweep.BasisDeferred != 1 {
		t.Fatalf("sweep basis-deferred = %d, want 1", report.Sweep.BasisDeferred)
	}
	if len(report.Sweep.VerdictCounts) != 0 || len(report.Sweep.ChangedIDs) != 0 {
		t.Fatalf("plan sweep claimed committed results: verdicts=%v changed=%v", report.Sweep.VerdictCounts, report.Sweep.ChangedIDs)
	}
	if report.Sweep.ReconciledPending != nil {
		t.Fatal("plan must not reconcile pending state")
	}

	states := candidateStatesDirect(t, home)
	for id, state := range states {
		if state != "proposed" {
			t.Fatalf("plan mutated %s -> %q", id, state)
		}
	}
	// The read-only contract, proven against the tree itself: a plan pass
	// creates no lock/, state/, events/, or ledger footprints and appends
	// no receipt. This is the acceptance the command-registry Read
	// admission for `memory promote-driver` (without --confirm) depends on.
	after := listHomeTree(t, home)
	if len(before) != len(after) {
		t.Fatalf("plan pass changed the home tree: before=%v after=%v", before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("plan pass changed the home tree at %q: before=%v after=%v", before[i], before, after)
		}
	}
}

func TestApplyPromotesBasisAndCollapsesDuplicates(t *testing.T) {
	home := t.TempDir()
	seedCandidate(t, home, "cand_dup1", mem.Candidate{Entity: "dup", Predicate: "value", Object: "same"})
	seedCandidate(t, home, "cand_dup2", mem.Candidate{Entity: "dup", Predicate: "value", Object: "same"})
	seedCandidate(t, home, "cand_basis", mem.Candidate{Entity: "basis", Predicate: "value", Object: "x", PromotionBasis: candidates.BasisRuntimeProof})
	seedCandidate(t, home, "cand_import", mem.Candidate{Entity: "imp", Predicate: "value", Object: "y", Qualifiers: map[string]string{"source": "knowledge_import"}})
	seedCandidate(t, home, "cand_plain", mem.Candidate{Entity: "plain", Predicate: "value", Object: "z"})

	report, err := Run(home, Options{Apply: true}, testNow)
	if err != nil {
		t.Fatalf("apply run: %v", err)
	}
	if report.DryRun {
		t.Fatal("apply pass must not carry dry_run")
	}
	if report.Stage != stageCommitted {
		t.Fatalf("apply pass stage = %q, want committed", report.Stage)
	}
	if report.Promote.Promoted != 1 {
		t.Fatalf("promoted = %d, want 1; errors=%v", report.Promote.Promoted, report.Promote.Errors)
	}
	if report.Promote.Promotions[0].ClaimID == "" {
		t.Fatal("promotion receipt must name the minted claim")
	}
	if report.Dedupe.Collapsed != 1 || report.Dedupe.Actions[0].Action != "collapsed" {
		t.Fatalf("dedupe collapsed=%d action=%q, want 1/collapsed", report.Dedupe.Collapsed, report.Dedupe.Actions[0].Action)
	}
	if !report.Sweep.WalkedAll {
		t.Fatalf("sweep must walk the whole small inbox; next_cursor=%q", report.Sweep.NextCursor)
	}
	if report.Sweep.ReconciledPending == nil || *report.Sweep.ReconciledPending != 0 {
		t.Fatalf("reconciled pending = %v, want 0", report.Sweep.ReconciledPending)
	}

	states := candidateStates(t, home)
	want := map[string]string{
		"cand_basis":  "promoted",
		"cand_dup1":   "parked",
		"cand_dup2":   "no_promote",
		"cand_import": "no_promote",
		"cand_plain":  "parked",
	}
	for id, state := range want {
		if states[id] != state {
			t.Fatalf("candidate %s state = %q, want %q", id, states[id], state)
		}
	}

	// The ledger carries exactly the one promoted claim.
	claims, err := os.ReadFile(filepath.Join(home, "ledger", "claims.jsonl"))
	if err != nil {
		t.Fatalf("read claims: %v", err)
	}
	if got := len(strings.Split(strings.TrimSpace(string(claims)), "\n")); got != 1 {
		t.Fatalf("claims.jsonl lines = %d, want 1", got)
	}

	// The intent journal writes the frozen plan before mutation, one exact
	// result frame per operation, then the terminal bijection.
	receipts := readReceipts(t, home)
	planned, _ := assertFrozenJournal(t, receipts, report.PassID, stageCommitted)
	firstPassID := planned.PassID

	// A second pass is a no-op drain: nothing proposed remains, and it
	// journals its own planned+committed pair with a distinct pass id.
	second, err := Run(home, Options{Apply: true}, testNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if second.Promote.Considered != 0 || second.Sweep.Adjudicated != 0 {
		t.Fatalf("second pass not idempotent: considered=%d adjudicated=%d", second.Promote.Considered, second.Sweep.Adjudicated)
	}
	receipts = readReceipts(t, home)
	assertFrozenJournal(t, receipts, second.PassID, stageCommitted)
	if second.PassID == firstPassID {
		t.Fatal("sequential passes must mint distinct pass ids")
	}
}

func TestPassIDsNeverCollideWithinOneSecond(t *testing.T) {
	// Second-resolution time + home hash collided on two sequential passes
	// inside one second. The journal keys receipts on the pass id, so two
	// sequential apply passes with the SAME now must mint distinct ids.
	home := t.TempDir()
	seedCandidate(t, home, "cand_a", mem.Candidate{Entity: "a", Predicate: "value", Object: "x"})
	first, err := Run(home, Options{Apply: true}, testNow)
	if err != nil {
		t.Fatalf("pass 1: %v", err)
	}
	second, err := Run(home, Options{Apply: true}, testNow)
	if err != nil {
		t.Fatalf("pass 2: %v", err)
	}
	if first.PassID == second.PassID {
		t.Fatalf("pass ids collided within one second: %s", first.PassID)
	}
}

func TestPromoteLimitDefersExtraBasisCandidates(t *testing.T) {
	home := t.TempDir()
	seedCandidate(t, home, "cand_b1", mem.Candidate{Entity: "b1", Predicate: "value", Object: "x", PromotionBasis: candidates.BasisRuntimeProof})
	seedCandidate(t, home, "cand_b2", mem.Candidate{Entity: "b2", Predicate: "value", Object: "y", PromotionBasis: candidates.BasisCuratedPage})

	report, err := Run(home, Options{Apply: true, PromoteLimit: 1}, testNow)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.Promote.Promoted != 1 {
		t.Fatalf("promoted = %d, want 1", report.Promote.Promoted)
	}
	if len(report.Promote.Deferred) != 1 || report.Promote.Deferred[0] != "cand_b2" {
		t.Fatalf("deferred = %v, want [cand_b2]", report.Promote.Deferred)
	}
	states := candidateStates(t, home)
	// b2 keeps its basis and stays proposed: disposition never forecloses
	// a promotion chance; the next pass promotes it.
	if states["cand_b1"] != "promoted" || states["cand_b2"] != "proposed" {
		t.Fatalf("states = %v, want b1 promoted / b2 still proposed", states)
	}
}

func TestExplicitConfirmationBasisNeverAutoPromotes(t *testing.T) {
	home := t.TempDir()
	seedCandidate(t, home, "cand_conf", mem.Candidate{Entity: "conf", Predicate: "value", Object: "x", PromotionBasis: candidates.BasisExplicitConfirmation})

	report, err := Run(home, Options{Apply: true}, testNow)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if report.Promote.Promoted != 0 {
		t.Fatal("explicit_confirmation must never auto-promote")
	}
	states := candidateStates(t, home)
	if states["cand_conf"] != "parked" {
		t.Fatalf("state = %q, want parked (operator material)", states["cand_conf"])
	}
}

func TestLockedApplyPassDoesNothing(t *testing.T) {
	home := t.TempDir()
	seedCandidate(t, home, "cand_a", mem.Candidate{Entity: "a", Predicate: "value", Object: "x"})

	if err := os.MkdirAll(filepath.Join(home, "lock"), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(filepath.Join(home, lockFileName), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("pre-lock: %v", err)
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)

	report, err := Run(home, Options{Apply: true}, testNow)
	if err != nil {
		t.Fatalf("locked run: %v", err)
	}
	if !report.Locked {
		t.Fatal("report must carry locked")
	}
	states := candidateStates(t, home)
	if states["cand_a"] != "proposed" {
		t.Fatalf("locked pass mutated: %q", states["cand_a"])
	}
	if _, err := os.Stat(filepath.Join(home, stateDirName, receiptFileName)); !os.IsNotExist(err) {
		t.Fatal("locked pass must not append a receipt")
	}
}

func TestApplyWalkResumesAndSweepNeverForeclosesBasis(t *testing.T) {
	home := t.TempDir()
	ids := []string{"cand_b1", "cand_b2", "cand_b3", "cand_b4", "cand_b5"}
	// Five basis candidates, one per walk window of 2. A walk-from-start
	// with this budget would only ever see the first two; the resume cursor
	// must tile the inbox so every candidate gets its promotion chance.
	for _, id := range ids {
		seedCandidate(t, home, id, mem.Candidate{Entity: id, Predicate: "value", Object: id, PromotionBasis: candidates.BasisRuntimeProof})
	}

	// Both walks tile at the same budget so their cursors stay aligned:
	// the promote phase sees each window before the sweep does.
	opts := Options{Apply: true, WalkScanLimit: 2, SweepScanLimit: 2}
	total := 0
	for pass := 0; pass < 3; pass++ {
		report, err := Run(home, opts, testNow.Add(time.Duration(pass)*time.Minute))
		if err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		total += report.Promote.Promoted
		if report.Promote.Scanned != 2 {
			t.Fatalf("pass %d scanned = %d, want 2 (cursor pins before the deferred basis candidate)", pass, report.Promote.Scanned)
		}
		if report.Dedupe.Skipped == "" {
			t.Fatal("dedupe must not run on budget-bounded walks")
		}
	}
	if total != 3 {
		t.Fatalf("promoted over 3 passes = %d, want 3 (limit 1/pass)", total)
	}
	// The doctrine under test: the sweep's disposition must NOT park the
	// basis candidates the promotion walk deferred or has not reached yet.
	// They stay proposed until their window comes around.
	states := candidateStates(t, home)
	if states["cand_b4"] != "proposed" || states["cand_b5"] != "proposed" {
		t.Fatalf("deferred basis candidates foreclosed: b4=%q b5=%q, want proposed", states["cand_b4"], states["cand_b5"])
	}
	// Two more cycles pick the deferred pair up.
	for pass := 3; pass < 5; pass++ {
		if _, err := Run(home, opts, testNow.Add(time.Duration(pass)*time.Minute)); err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
	}
	states = candidateStates(t, home)
	for _, id := range ids {
		if states[id] != "promoted" {
			t.Fatalf("%s state = %q, want promoted", id, states[id])
		}
	}
}

func TestSweepCursorResumesAcrossPasses(t *testing.T) {
	home := t.TempDir()
	for _, id := range []string{"cand_01", "cand_02", "cand_03", "cand_04", "cand_05"} {
		seedCandidate(t, home, id, mem.Candidate{Entity: id, Predicate: "value", Object: id})
	}

	opts := Options{Apply: true, SweepScanLimit: 2}
	first, err := Run(home, opts, testNow)
	if err != nil {
		t.Fatalf("pass 1: %v", err)
	}
	if first.Sweep.WalkedAll {
		t.Fatal("pass 1 must stop at the scan cap")
	}
	if first.Sweep.Scanned != 2 || first.Sweep.NextCursor == "" {
		t.Fatalf("pass 1 scanned=%d cursor=%q, want 2 and a resume point", first.Sweep.Scanned, first.Sweep.NextCursor)
	}
	if len(first.Sweep.ChangedIDs) != 2 || len(first.Sweep.Actions) != 2 {
		t.Fatalf("sweep operation receipt = changed %v actions %+v, want two exact operations", first.Sweep.ChangedIDs, first.Sweep.Actions)
	}
	if first.Sweep.ReconciledPending != nil {
		t.Fatal("partial walk must not reconcile")
	}
	if _, err := os.Stat(filepath.Join(home, stateDirName, cursorFileName)); err != nil {
		t.Fatalf("cursor must persist between passes: %v", err)
	}

	second, err := Run(home, opts, testNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("pass 2: %v", err)
	}
	if second.Sweep.Scanned != 2 || second.Sweep.WalkedAll {
		t.Fatalf("pass 2 scanned=%d walkedAll=%v, want 2/false", second.Sweep.Scanned, second.Sweep.WalkedAll)
	}

	third, err := Run(home, opts, testNow.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("pass 3: %v", err)
	}
	if !third.Sweep.WalkedAll || third.Sweep.ReconciledPending == nil || *third.Sweep.ReconciledPending != 0 {
		t.Fatalf("pass 3 must finish and reconcile: walkedAll=%v pending=%v", third.Sweep.WalkedAll, third.Sweep.ReconciledPending)
	}
	if _, err := os.Stat(filepath.Join(home, stateDirName, cursorFileName)); !os.IsNotExist(err) {
		t.Fatal("cursor must clear once the walk completes")
	}
	states := candidateStates(t, home)
	for id, state := range states {
		if state != "parked" {
			t.Fatalf("%s state = %q, want parked", id, state)
		}
	}
}

func TestPromoteWriteCapPinsCursorBeforeDeferredBasisCandidate(t *testing.T) {
	home := t.TempDir()
	seedCandidate(t, home, "cand_01", mem.Candidate{Entity: "basis-1", Predicate: "value", Object: "one", PromotionBasis: candidates.BasisRuntimeProof})
	seedCandidate(t, home, "cand_02", mem.Candidate{Entity: "basis-2", Predicate: "value", Object: "two", PromotionBasis: candidates.BasisRuntimeProof})
	for _, id := range []string{"cand_03", "cand_04", "cand_05"} {
		seedCandidate(t, home, id, mem.Candidate{Entity: id, Predicate: "value", Object: id})
	}

	opts := Options{Apply: true, PromoteLimit: 1, WalkScanLimit: 3, SweepScanLimit: 10}
	first, err := Run(home, opts, testNow)
	if err != nil {
		t.Fatalf("pass 1: %v", err)
	}
	if first.Promote.Promoted != 1 {
		t.Fatalf("pass 1 promoted = %d, want 1", first.Promote.Promoted)
	}
	if first.Promote.NextCursor != "cand_01.json" {
		t.Fatalf("pass 1 cursor = %q, want cand_01.json before the first unexecuted actionable candidate", first.Promote.NextCursor)
	}

	second, err := Run(home, opts, testNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("pass 2: %v", err)
	}
	if len(second.Promote.Promotions) != 1 || second.Promote.Promotions[0].CandidateID != "cand_02" {
		t.Fatalf("pass 2 promotions = %+v, want deferred cand_02 without a full-cycle gap", second.Promote.Promotions)
	}
	if got := candidateStates(t, home)["cand_02"]; got != "promoted" {
		t.Fatalf("cand_02 state = %q, want promoted", got)
	}
}

func TestDriverPersistsVersionedFilenameCursorsAndReadsLegacyAfterID(t *testing.T) {
	t.Run("writes versioned cursor", func(t *testing.T) {
		home := t.TempDir()
		for _, id := range []string{"cand_01", "cand_02", "cand_03"} {
			seedCandidate(t, home, id, mem.Candidate{Entity: id, Predicate: "value", Object: id})
		}
		if _, err := Run(home, Options{Apply: true, WalkScanLimit: 1, SweepScanLimit: 1}, testNow); err != nil {
			t.Fatalf("apply pass: %v", err)
		}
		for _, name := range []string{promoteCursorFileName, cursorFileName} {
			data, err := os.ReadFile(filepath.Join(stateDir(home), name))
			if err != nil {
				t.Fatalf("read %s: %v", name, err)
			}
			var state map[string]json.RawMessage
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatalf("decode %s: %v", name, err)
			}
			if _, exists := state["after_id"]; exists {
				t.Fatalf("%s retained legacy after_id: %s", name, data)
			}
			rawCursor, exists := state["after_cursor"]
			if !exists {
				t.Fatalf("%s missing after_cursor: %s", name, data)
			}
			var cursor candidates.EntryCursor
			if err := json.Unmarshal(rawCursor, &cursor); err != nil {
				t.Fatalf("decode %s cursor: %v", name, err)
			}
			if cursor != "cand_01.json" {
				t.Fatalf("%s cursor = %q, want cand_01.json", name, cursor)
			}
		}
	})

	t.Run("reads legacy after_id", func(t *testing.T) {
		home := t.TempDir()
		for _, id := range []string{"cand_01", "cand_02", "cand_03"} {
			seedCandidate(t, home, id, mem.Candidate{Entity: id, Predicate: "value", Object: id})
		}
		if err := os.MkdirAll(stateDir(home), 0o755); err != nil {
			t.Fatal(err)
		}
		legacy := []byte(`{"after_id":"cand_02","updated_at":"2026-08-25T00:00:00Z"}`)
		for _, name := range []string{promoteCursorFileName, cursorFileName} {
			if err := os.WriteFile(filepath.Join(stateDir(home), name), legacy, 0o644); err != nil {
				t.Fatal(err)
			}
		}

		report, err := Run(home, Options{WalkScanLimit: 1, SweepScanLimit: 1}, testNow)
		if err != nil {
			t.Fatalf("plan pass: %v", err)
		}
		if report.Promote.ResumedFrom != "cand_02.json" {
			t.Fatalf("promote resumed_from = %q, want migrated filename cursor", report.Promote.ResumedFrom)
		}
		if len(report.Sweep.ChangedIDs) != 0 || len(report.Sweep.Actions) != 1 || report.Sweep.Actions[0].CandidateID != "cand_03" {
			t.Fatalf("legacy sweep resume changed=%v actions=%v, want one cand_03 plan and no mutation", report.Sweep.ChangedIDs, report.Sweep.Actions)
		}
	})
}

func TestResolveStoredCursorMigratesLegacyIDWithTokenPrefix(t *testing.T) {
	got, err := resolveStoredCursor("", "filename-v1:abc")
	if err != nil {
		t.Fatalf("migrate legacy cursor with reserved token prefix: %v", err)
	}
	if want := candidates.EntryCursor("filename-v1:abc.json"); got != want {
		t.Fatalf("migrated cursor = %q, want %q", got, want)
	}
}

// TestSweepCursorAdvancesPastCorruptPageTail: a bounded page ending on an
// unreadable candidate file must advance the cursor PAST the corrupt entry.
// The old cursor keyed on the last DECODED candidate re-read the corrupt
// file every pass; the continuation point is the last directory entry
// walked, valid or corrupt.
func TestSweepCursorAdvancesPastCorruptPageTail(t *testing.T) {
	home := t.TempDir()
	previousLaneBeliefs := runLaneBeliefs
	runLaneBeliefs = func(string, Options, time.Time) LaneBeliefsPhaseReport { return LaneBeliefsPhaseReport{} }
	t.Cleanup(func() { runLaneBeliefs = previousLaneBeliefs })
	seedCandidate(t, home, "cand_01", mem.Candidate{Entity: "a", Predicate: "value", Object: "x"})
	seedCandidate(t, home, "cand_02", mem.Candidate{Entity: "b", Predicate: "value", Object: "y"})
	// cand_90 sorts third and is unreadable: with a budget of 3 the page
	// ends exactly on it, and cand_91 waits beyond. The old cursor keyed on
	// the last DECODED candidate set NextCursor=cand_02 and re-read the
	// corrupt file every pass.
	if err := os.WriteFile(filepath.Join(home, candidates.InboxDir(candidates.Memory), "cand_90.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedCandidate(t, home, "cand_91", mem.Candidate{Entity: "c", Predicate: "value", Object: "z"})

	report, err := Run(home, Options{Apply: true, SweepScanLimit: 3}, testNow)
	if err == nil || report.Stage != stagePartial {
		t.Fatalf("pass 1 must terminalize partial while preserving progress: stage=%q err=%v", report.Stage, err)
	}
	if report.Sweep.WalkedAll {
		t.Fatal("pass 1 must stop at the scan cap")
	}
	if report.Sweep.NextCursor != "cand_90.json" {
		t.Fatalf("cursor = %q, want cand_90.json (the corrupt page tail is progress too)", report.Sweep.NextCursor)
	}
	// The resume pass reads exactly the one entry past the corrupt tail, but
	// the full reconciliation must still fail closed on the retained corrupt
	// entry rather than certifying a clean completed pass.
	second, err := Run(home, Options{Apply: true, SweepScanLimit: 3}, testNow.Add(time.Minute))
	if err == nil || second.Stage != stageRecoveryRequired || !strings.Contains(err.Error(), "cand_90.json") {
		t.Fatalf("pass 2 must remain recoverable on retained corruption: stage=%q err=%v", second.Stage, err)
	}
	if !second.Sweep.WalkedAll || second.Sweep.Scanned != 1 {
		t.Fatalf("pass 2 scanned=%d walkedAll=%v, want 1/true (no re-read of the corrupt tail)", second.Sweep.Scanned, second.Sweep.WalkedAll)
	}
	if len(second.Sweep.CorruptIDs) != 0 {
		t.Fatalf("pass 2 must not re-report the corrupt entry: %v", second.Sweep.CorruptIDs)
	}
	if err := os.Remove(filepath.Join(home, candidates.InboxDir(candidates.Memory), "cand_90.json")); err != nil {
		t.Fatal(err)
	}
	recovered, err := Run(home, Options{Apply: true, SweepScanLimit: 3}, testNow.Add(2*time.Minute))
	if err != nil || recovered.Stage != stageCommitted || recovered.PassID != second.PassID {
		t.Fatalf("repaired corruption must finalize the same pass: id=%s/%s stage=%q err=%v", recovered.PassID, second.PassID, recovered.Stage, err)
	}
}

// TestCursorPersistenceFailureFailsTheApplyPass: a sweep that mutated
// candidates but cannot persist its continuation point must SAY SO — the
// error fails the pass and the journal records stage=failed. Swallowing it
// would let the next pass resume inconsistently with no durable trace.
func TestCursorPersistenceFailureFailsTheApplyPass(t *testing.T) {
	home := t.TempDir()
	for _, id := range []string{"cand_01", "cand_02", "cand_03"} {
		seedCandidate(t, home, id, mem.Candidate{Entity: id, Predicate: "value", Object: id})
	}
	// Deterministic cursor-persistence failure at the exact point the apply
	// pass needs its continuation point; the journal stays writable so the
	// failure is still durable.
	sweepCursorWriter = func(string, sweepCursor) error { return os.ErrPermission }
	t.Cleanup(func() { sweepCursorWriter = nil })

	report, err := Run(home, Options{Apply: true, SweepScanLimit: 2}, testNow)
	if err == nil {
		t.Fatalf("pass must fail when the sweep cursor cannot persist; report=%+v", report)
	}
	if report.Stage != stageRecoveryRequired || !strings.Contains(err.Error(), "finalize sweep cursor") {
		t.Fatalf("error = %v, want the cursor persistence failure named", err)
	}
	receipts := readReceipts(t, home)
	_, recovery := assertFrozenJournal(t, receipts, report.PassID, stageRecoveryRequired)
	if !strings.Contains(recovery.Error, "finalize sweep cursor") {
		t.Fatalf("recovery receipt must carry the cursor error, got %q", recovery.Error)
	}
}

// TestPromoteCursorPersistenceFailureFailsTheApplyPass mirrors the sweep
// contract: the promote cursor is forward progress across a huge inbox, so a
// pass that cannot persist it must NOT stamp stage=committed — the next pass
// would re-walk the same prefix and starve later candidates while the
// journal claims success. Pre-fix this error was only appended to the
// phase's Errors and the pass committed anyway.
func TestPromoteCursorPersistenceFailureFailsTheApplyPass(t *testing.T) {
	home := t.TempDir()
	for _, id := range []string{"cand_pc1", "cand_pc2", "cand_pc3"} {
		seedCandidate(t, home, id, mem.Candidate{Entity: id, Predicate: "value", Object: id})
	}
	promoteCursorWriter = func(string, promoteCursor) error { return os.ErrPermission }
	t.Cleanup(func() { promoteCursorWriter = nil })

	report, err := Run(home, Options{Apply: true, WalkScanLimit: 2}, testNow)
	if err == nil {
		t.Fatalf("pass must fail when the promote cursor cannot persist; report=%+v", report)
	}
	if report.Stage != stageRecoveryRequired || !strings.Contains(err.Error(), "finalize promote cursor") {
		t.Fatalf("error = %v, want the cursor persistence failure named", err)
	}
	receipts := readReceipts(t, home)
	_, recovery := assertFrozenJournal(t, receipts, report.PassID, stageRecoveryRequired)
	if !strings.Contains(recovery.Error, "finalize promote cursor") {
		t.Fatalf("recovery receipt must carry the cursor error, got %q", recovery.Error)
	}
}

// TestCorruptImportHashesFailThePass: a corrupt import-hashes ledger is not
// "no hashes". The pre-fix reader swallowed the unmarshal error, so a
// scheduled pass silently re-imported everything and overwrote the evidence
// on its next write. Now the pass fails and names the file and the repair
// path.
func TestCorruptImportHashesFailThePass(t *testing.T) {
	home := t.TempDir()
	registry := filepath.Join(home, "registry.yaml")
	if err := os.WriteFile(registry, []byte("kind: product\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stateDir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateDir(home), hashFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Dry-run must also refuse to bless a plan over untrustworthy state.
	_, err := Run(home, Options{RegistryPaths: []string{registry}}, testNow)
	if err == nil {
		t.Fatal("dry-run must fail over a corrupt import-hash ledger")
	}
	if !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("error must name the corruption: %v", err)
	}
	// The corrupt ledger must survive untouched for deliberate repair.
	data, readErr := os.ReadFile(filepath.Join(stateDir(home), hashFileName))
	if readErr != nil || string(data) != "{not json" {
		t.Fatalf("corrupt ledger must not be silently rewritten: %q %v", data, readErr)
	}
}

func TestRegistryApplyRefusesExperimentalMutationBeforeJournaling(t *testing.T) {
	home := t.TempDir()
	registry := filepath.Join(home, "registry.yaml")
	if err := os.WriteFile(registry, []byte("kind: product\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := Run(home, Options{Apply: true, RegistryPaths: []string{registry}}, testNow)
	if err == nil || report.Stage != "" || !strings.Contains(err.Error(), "experimental") {
		t.Fatalf("registry apply must refuse before pass admission: stage=%q err=%v", report.Stage, err)
	}
	if _, statErr := os.Stat(stateDir(home)); !os.IsNotExist(statErr) {
		t.Fatalf("registry refusal created driver state: %v", statErr)
	}
}

func TestPlannedReceiptCarriesExactCandidateIntent(t *testing.T) {
	home := t.TempDir()
	seedCandidate(t, home, "cand_basis", mem.Candidate{
		Entity: "a", Predicate: "value", Object: "x", PromotionBasis: candidates.BasisRuntimeProof,
	})
	seedCandidate(t, home, "cand_plain", mem.Candidate{Entity: "b", Predicate: "value", Object: "y"})

	report, err := Run(home, Options{Apply: true, PromoteLimit: 1}, testNow)
	if err != nil {
		t.Fatalf("pass: %v", err)
	}
	receipts := readReceipts(t, home)
	planned, _ := assertFrozenJournal(t, receipts, report.PassID, stageCommitted)
	if strings.Join(planned.Intent.CandidateIDs, ",") != "cand_basis,cand_plain" {
		t.Fatalf("intent candidate window = %v", planned.Intent.CandidateIDs)
	}
	if len(planned.Intent.Operations) != 2 {
		t.Fatalf("intent operations = %+v, want promote + disposition", planned.Intent.Operations)
	}
	if report.Intent == nil || report.Intent.Operations[0].Key == "" {
		t.Fatalf("terminal report lost planned intent: %+v", report.Intent)
	}
}

func TestSupportedApplySkipsExperimentalLaneBeliefMutation(t *testing.T) {
	home := t.TempDir()
	previous := runLaneBeliefs
	called := false
	runLaneBeliefs = func(string, Options, time.Time) LaneBeliefsPhaseReport {
		called = true
		return LaneBeliefsPhaseReport{Error: "fold: injected failure"}
	}
	t.Cleanup(func() { runLaneBeliefs = previous })

	report, err := Run(home, Options{Apply: true}, testNow)
	if err != nil || report.Stage != stageCommitted {
		t.Fatalf("candidate-only apply must commit: stage=%q err=%v report=%+v", report.Stage, err, report)
	}
	if called {
		t.Fatal("supported apply called the experimental lane-belief mutator")
	}
	if !strings.Contains(report.LaneBeliefs.Skipped, "experimental") {
		t.Fatalf("lane-belief exclusion not reported: %+v", report.LaneBeliefs)
	}
	receipts := readReceipts(t, home)
	assertFrozenJournal(t, receipts, report.PassID, stageCommitted)
}

func TestTerminalReceiptFailureWritesRecoveryMarker(t *testing.T) {
	home := t.TempDir()
	previous := receiptAppender
	receiptAppender = func(homePath string, report Report) error {
		if report.Stage != stagePlanned {
			return os.ErrPermission
		}
		return appendReceipt(homePath, report)
	}
	t.Cleanup(func() { receiptAppender = previous })

	report, err := Run(home, Options{Apply: true}, testNow)
	if err == nil || report.Stage != stageRecoveryRequired {
		t.Fatalf("terminal receipt failure must require recovery: stage=%q err=%v", report.Stage, err)
	}
	if _, statErr := os.Stat(filepath.Join(stateDir(home), "recovery-"+report.PassID+".json")); statErr != nil {
		t.Fatalf("recovery marker missing: %v", statErr)
	}
}

func TestSuccessorTerminalizesPlannedOnlyPassBeforeMutation(t *testing.T) {
	home := t.TempDir()
	orphan := Report{
		SchemaVersion: ReportSchemaVersion,
		PassID:        "pass_orphan",
		Home:          home,
		Stage:         stagePlanned,
		StartedAt:     rfc3339(testNow.Add(-time.Minute)),
		Intent:        &PassIntent{CandidateIDs: []string{}, Operations: []PassIntentOperation{}},
	}
	if err := appendReceipt(home, orphan); err != nil {
		t.Fatal(err)
	}

	report, err := Run(home, Options{Apply: true}, testNow)
	if err != nil {
		t.Fatalf("successor: %v", err)
	}
	if strings.Join(report.ReconciledPassIDs, ",") != orphan.PassID {
		t.Fatalf("reconciled passes = %v, want %s", report.ReconciledPassIDs, orphan.PassID)
	}
	receipts := readReceipts(t, home)
	if len(receipts) != 4 || receipts[1].PassID != orphan.PassID || receipts[1].Stage != stageRecoveryRequired {
		t.Fatalf("orphan was not terminalized before successor: %+v", receipts)
	}
}

func TestSuccessorReconcilesLegacyRawCursorReceipt(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(stateDir(home), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"schema_version":"compound.promotedriver.report.v0.2","pass_id":"pass_legacy","stage":"planned","intent":{"promote_cursor_before":"cand_01","sweep_cursor_before":"cand_02","candidate_ids":[],"corrupt_entries":["cand_bad"],"operations":[]},"promote":{"resumed_from":"cand_01","next_cursor":"cand_02","corrupt_ids":["cand_bad"]},"sweep":{"next_cursor":"cand_02","corrupt_ids":["cand_bad"]}}`
	if err := os.WriteFile(filepath.Join(stateDir(home), receiptFileName), []byte(legacy+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := Run(home, Options{Apply: true}, testNow)
	if err != nil {
		t.Fatalf("successor must reconcile historical raw receipt cursors: %v", err)
	}
	if got := strings.Join(report.ReconciledPassIDs, ","); got != "pass_legacy" {
		t.Fatalf("reconciled = %q, want pass_legacy", got)
	}
	if report.SchemaVersion == "compound.promotedriver.report.v0.2" {
		t.Fatalf("new receipt reused legacy raw-cursor schema %q", report.SchemaVersion)
	}
	data, err := os.ReadFile(filepath.Join(stateDir(home), receiptFileName))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 2 {
		t.Fatalf("receipt lines = %d, want legacy plus recovery", len(lines))
	}
	var recovery Report
	if err := json.Unmarshal([]byte(lines[1]), &recovery); err != nil {
		t.Fatalf("decode migrated recovery receipt: %v", err)
	}
	if recovery.SchemaVersion != ReportSchemaVersion {
		t.Fatalf("recovery schema = %q, want %q", recovery.SchemaVersion, ReportSchemaVersion)
	}
	if recovery.Intent == nil || recovery.Intent.PromoteCursorBefore != "cand_01.json" || recovery.Intent.SweepCursorBefore != "cand_02.json" {
		t.Fatalf("recovery intent cursors = %+v, want cand_01.json/cand_02.json", recovery.Intent)
	}
	if !slices.Equal(recovery.Intent.CorruptEntries, []candidates.EntryCursor{"cand_bad.json"}) {
		t.Fatalf("recovery corrupt entries = %q, want cand_bad.json", recovery.Intent.CorruptEntries)
	}
}

func TestCurrentReceiptSchemaRejectsRawCursorJSON(t *testing.T) {
	raw := []byte(`{"schema_version":"compound.promotedriver.report.v0.3","pass_id":"pass_raw","stage":"planned","intent":{"promote_cursor_before":"cand_01","candidate_ids":[],"operations":[]}}`)
	if _, err := decodeReceiptLine(raw); err == nil {
		t.Fatal("current receipt schema accepted an unversioned raw cursor")
	}
}

func TestFrozenCandidatePlanRefusesChangedTargetDigest(t *testing.T) {
	home := t.TempDir()
	seedCandidate(t, home, "cand_basis", mem.Candidate{
		Entity: "basis", Predicate: "value", Object: "before", PromotionBasis: candidates.BasisRuntimeProof,
	})

	previous := receiptAppender
	mutated := false
	receiptAppender = func(homePath string, report Report) error {
		if err := appendReceipt(homePath, report); err != nil {
			return err
		}
		if report.Stage == stagePlanned && !mutated {
			mutated = true
			seedCandidate(t, home, "cand_basis", mem.Candidate{
				Entity: "basis", Predicate: "value", Object: "after", PromotionBasis: candidates.BasisRuntimeProof,
			})
		}
		return nil
	}
	t.Cleanup(func() { receiptAppender = previous })

	report, err := Run(home, Options{Apply: true}, testNow)
	if err == nil || !strings.Contains(err.Error(), "stale_precondition") {
		t.Fatalf("changed planned target must fail stale without mutation: stage=%q err=%v", report.Stage, err)
	}
	var current mem.Candidate
	if err := candidates.ReadCandidate(home, candidates.Memory, "cand_basis", &current); err != nil {
		t.Fatal(err)
	}
	if current.Object != "after" || current.State != "proposed" {
		t.Fatalf("changed target was overwritten: object=%q state=%q", current.Object, current.State)
	}
	if claims, readErr := os.ReadFile(filepath.Join(home, "ledger", "claims.jsonl")); readErr == nil && len(strings.TrimSpace(string(claims))) != 0 {
		t.Fatalf("stale target appended a claim: %s", claims)
	} else if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatal(readErr)
	}
}

func TestFrozenDedupePlanBindsKeeperAndDuplicate(t *testing.T) {
	for _, changed := range []string{"keeper", "duplicate"} {
		t.Run(changed, func(t *testing.T) {
			home := t.TempDir()
			seedCandidate(t, home, "cand_keep", mem.Candidate{Entity: "dup", Predicate: "value", Object: "same", CreatedAt: "2026-08-25T00:00:00Z"})
			seedCandidate(t, home, "cand_dup", mem.Candidate{Entity: "dup", Predicate: "value", Object: "same", CreatedAt: "2026-08-25T00:01:00Z"})

			previous := receiptAppender
			mutated := false
			receiptAppender = func(homePath string, report Report) error {
				if err := appendReceipt(homePath, report); err != nil {
					return err
				}
				if report.Stage != stagePlanned || mutated {
					return nil
				}
				mutated = true
				if changed == "keeper" {
					seedCandidate(t, home, "cand_keep", mem.Candidate{Entity: "dup", Predicate: "value", Object: "changed", CreatedAt: "2026-08-25T00:00:00Z"})
				} else {
					seedCandidate(t, home, "cand_dup", mem.Candidate{Entity: "dup", Predicate: "value", Object: "same", Tags: []string{"changed"}, CreatedAt: "2026-08-25T00:01:00Z"})
				}
				return nil
			}
			t.Cleanup(func() { receiptAppender = previous })

			_, _ = Run(home, Options{Apply: true}, testNow)
			var duplicate mem.Candidate
			if err := candidates.ReadCandidate(home, candidates.Memory, "cand_dup", &duplicate); err != nil {
				t.Fatal(err)
			}
			if duplicate.State != "proposed" {
				t.Fatalf("changed %s let frozen dedupe mutate duplicate to %q", changed, duplicate.State)
			}
		})
	}
}

func TestFrozenPassDoesNotActOnCandidateAddedAfterPlanning(t *testing.T) {
	home := t.TempDir()
	seedCandidate(t, home, "cand_original", mem.Candidate{Entity: "original", Predicate: "value", Object: "x"})

	previous := receiptAppender
	added := false
	receiptAppender = func(homePath string, report Report) error {
		if err := appendReceipt(homePath, report); err != nil {
			return err
		}
		if report.Stage == stagePlanned && !added {
			added = true
			seedCandidate(t, home, "cand_late", mem.Candidate{
				Entity: "late", Predicate: "value", Object: "y", PromotionBasis: candidates.BasisRuntimeProof,
			})
		}
		return nil
	}
	t.Cleanup(func() { receiptAppender = previous })

	if _, err := Run(home, Options{Apply: true}, testNow); err != nil {
		t.Fatalf("frozen original plan: %v", err)
	}
	var late mem.Candidate
	if err := candidates.ReadCandidate(home, candidates.Memory, "cand_late", &late); err != nil {
		t.Fatal(err)
	}
	if late.State != "proposed" {
		t.Fatalf("post-plan candidate was executed: state=%q", late.State)
	}
}

func TestFrozenPassRecoveryReusesPassIDAndOperationList(t *testing.T) {
	home := t.TempDir()
	seedCandidate(t, home, "cand_basis", mem.Candidate{
		Entity: "basis", Predicate: "value", Object: "x", PromotionBasis: candidates.BasisRuntimeProof,
	})

	previous := receiptAppender
	receiptAppender = func(homePath string, report Report) error {
		if report.Stage != stagePlanned {
			return os.ErrPermission
		}
		return appendReceipt(homePath, report)
	}
	first, err := Run(home, Options{Apply: true}, testNow)
	receiptAppender = previous
	if err == nil || first.Stage != stageRecoveryRequired {
		t.Fatalf("first pass must require recovery after result-journal failure: stage=%q err=%v", first.Stage, err)
	}

	second, err := Run(home, Options{Apply: true}, testNow.Add(time.Minute))
	if err != nil {
		t.Fatalf("resume exact pass: %v", err)
	}
	if second.PassID != first.PassID {
		t.Fatalf("recovery minted new pass %q; want same %q", second.PassID, first.PassID)
	}
	if second.Intent == nil || first.Intent == nil || len(second.Intent.Operations) != len(first.Intent.Operations) {
		t.Fatalf("recovery changed operation vector: first=%+v second=%+v", first.Intent, second.Intent)
	}
	if len(second.OperationResults) != 1 ||
		second.OperationResults[0].Outcome != mem.FrozenAdjudicationAlreadyApplied ||
		second.OperationResults[0].ClaimID == "" ||
		second.OperationResults[0].TransitionID == "" {
		t.Fatalf("recovery did not receipt the exact already-applied promotion: %+v", second.OperationResults)
	}
	claims, err := os.ReadFile(filepath.Join(home, "ledger", "claims.jsonl"))
	if err != nil || len(strings.Split(strings.TrimSpace(string(claims)), "\n")) != 1 {
		t.Fatalf("recovery claim rows = %q, err=%v; want exactly one", claims, err)
	}
	events, err := eventstore.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := events.Scan(eventstore.ScanOptions{Prefixes: []string{"tr_"}})
	if err != nil || len(entries) != 1 || entries[0].ID != second.OperationResults[0].TransitionID {
		t.Fatalf("recovery transition entries = %+v, err=%v; want exact terminal result transition", entries, err)
	}
}

func TestFrozenPromotionRefusesCurrentClaimDriftAfterPlanning(t *testing.T) {
	home := t.TempDir()
	seedCandidate(t, home, "cand_target", mem.Candidate{
		Entity: "subject", Predicate: "value", Object: "same", PromotionBasis: candidates.BasisRuntimeProof,
	})

	previous := receiptAppender
	inserted := false
	receiptAppender = func(homePath string, report Report) error {
		if err := appendReceipt(homePath, report); err != nil {
			return err
		}
		if report.Stage != stagePlanned || inserted {
			return nil
		}
		inserted = true
		seedCandidate(t, home, "cand_racer", mem.Candidate{
			Entity: "subject", Predicate: "value", Object: "same", PromotionBasis: candidates.BasisRuntimeProof,
		})
		store, err := mem.Open(home)
		if err != nil {
			return err
		}
		_, err = store.Adjudicate("cand_racer", candidates.Promote, "work_concurrent_claim", testNow.Add(time.Second))
		return err
	}
	t.Cleanup(func() { receiptAppender = previous })

	report, err := Run(home, Options{Apply: true}, testNow)
	if err == nil || report.Stage != stageFailed || !strings.Contains(err.Error(), "stale_precondition") {
		t.Fatalf("claim-corpus drift must fail stale: stage=%q err=%v report=%+v", report.Stage, err, report)
	}
	var target mem.Candidate
	if err := candidates.ReadCandidate(home, candidates.Memory, "cand_target", &target); err != nil {
		t.Fatal(err)
	}
	if target.State != "proposed" {
		t.Fatalf("claim-corpus drift mutated the frozen target to %q", target.State)
	}
}

func TestTerminalOperationResultsBijectFrozenOperations(t *testing.T) {
	home := t.TempDir()
	seedCandidate(t, home, "cand_plain", mem.Candidate{Entity: "plain", Predicate: "value", Object: "x"})
	if _, err := Run(home, Options{Apply: true}, testNow); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(stateDir(home), receiptFileName))
	if err != nil {
		t.Fatal(err)
	}
	var planned, terminal map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var frame map[string]any
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatal(err)
		}
		switch frame["stage"] {
		case stagePlanned:
			planned = frame
		case stageCommitted:
			terminal = frame
		}
	}
	if planned == nil || terminal == nil {
		t.Fatalf("missing planned/terminal frames: %s", data)
	}
	intent := planned["intent"].(map[string]any)
	operations := intent["operations"].([]any)
	results, ok := terminal["operation_results"].([]any)
	if !ok || len(results) != len(operations) {
		t.Fatalf("terminal result count = %d/%t, want %d frozen operations; terminal=%v", len(results), ok, len(operations), terminal)
	}
	wantKeys := map[string]bool{}
	for _, raw := range operations {
		wantKeys[raw.(map[string]any)["key"].(string)] = true
	}
	for _, raw := range results {
		key := raw.(map[string]any)["key"].(string)
		if !wantKeys[key] {
			t.Fatalf("terminal result key %q is outside frozen plan", key)
		}
		delete(wantKeys, key)
	}
	if len(wantKeys) != 0 {
		t.Fatalf("frozen operations missing terminal results: %v", wantKeys)
	}
}

func TestLoadFrozenJournalRejectsTerminalResultDrift(t *testing.T) {
	tests := map[string]func(*Report){
		"missing result": func(terminal *Report) {
			terminal.OperationResults = nil
		},
		"conflicting result": func(terminal *Report) {
			terminal.OperationResults[0].AfterDigest = "sha256:" + strings.Repeat("0", 64)
		},
		"intent drift": func(terminal *Report) {
			copyOfIntent := *terminal.Intent
			copyOfIntent.CandidateIDs = append(copyOfIntent.CandidateIDs, "cand_unplanned")
			terminal.Intent = &copyOfIntent
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			seedCandidate(t, home, "cand_basis", mem.Candidate{
				Entity: "basis", Predicate: "value", Object: "x", PromotionBasis: candidates.BasisRuntimeProof,
			})
			if _, err := Run(home, Options{Apply: true}, testNow); err != nil {
				t.Fatal(err)
			}
			receipts := readReceipts(t, home)
			mutate(&receipts[len(receipts)-1])
			lines := make([]string, 0, len(receipts))
			for _, receipt := range receipts {
				body, err := json.Marshal(receipt)
				if err != nil {
					t.Fatal(err)
				}
				lines = append(lines, string(body))
			}
			path := filepath.Join(stateDir(home), receiptFileName)
			if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}

			report, err := Run(home, Options{Apply: true}, testNow.Add(time.Minute))
			if err == nil || report.Stage != "" || !strings.Contains(err.Error(), "terminal") {
				t.Fatalf("drifted terminal must refuse before a new pass: stage=%q err=%v", report.Stage, err)
			}
		})
	}
}

func TestLoadFrozenJournalRejectsOperationFrameAfterTerminal(t *testing.T) {
	home := t.TempDir()
	seedCandidate(t, home, "cand_plain", mem.Candidate{Entity: "plain", Predicate: "value", Object: "x"})
	if _, err := Run(home, Options{Apply: true}, testNow); err != nil {
		t.Fatal(err)
	}
	receipts := readReceipts(t, home)
	if len(receipts) < 3 || receipts[1].Stage != stageOperationRecorded {
		t.Fatalf("journal lacks reusable operation frame: %+v", receipts)
	}
	if err := appendReceipt(home, receipts[1]); err != nil {
		t.Fatal(err)
	}

	report, err := Run(home, Options{Apply: true}, testNow.Add(time.Minute))
	if err == nil || report.Stage != "" || !strings.Contains(err.Error(), "follows a terminal frame") {
		t.Fatalf("post-terminal operation frame must refuse before a new pass: stage=%q err=%v", report.Stage, err)
	}
}

func TestLoadFrozenJournalRejectsOperationFrameIntentDrift(t *testing.T) {
	home := t.TempDir()
	seedCandidate(t, home, "cand_plain", mem.Candidate{Entity: "plain", Predicate: "value", Object: "x"})
	if _, err := Run(home, Options{Apply: true}, testNow); err != nil {
		t.Fatal(err)
	}
	receipts := readReceipts(t, home)
	copyOfIntent := *receipts[1].Intent
	copyOfIntent.CandidateIDs = append(copyOfIntent.CandidateIDs, "cand_unplanned")
	receipts[1].Intent = &copyOfIntent
	lines := make([]string, 0, len(receipts))
	for _, receipt := range receipts {
		body, err := json.Marshal(receipt)
		if err != nil {
			t.Fatal(err)
		}
		lines = append(lines, string(body))
	}
	path := filepath.Join(stateDir(home), receiptFileName)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := Run(home, Options{Apply: true}, testNow.Add(time.Minute))
	if err == nil || report.Stage != "" || !strings.Contains(err.Error(), "operation frame intent") {
		t.Fatalf("drifted operation-frame intent must refuse before a new pass: stage=%q err=%v", report.Stage, err)
	}
}

func TestCorruptCursorFailsBeforePlannedReceipt(t *testing.T) {
	for _, name := range []string{promoteCursorFileName, cursorFileName} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			if err := os.MkdirAll(stateDir(home), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(stateDir(home), name), []byte("{not json"), 0o644); err != nil {
				t.Fatal(err)
			}
			report, err := Run(home, Options{Apply: true}, testNow)
			if err == nil || report.Stage != "" || !strings.Contains(err.Error(), "corrupt") {
				t.Fatalf("corrupt cursor must refuse before journaling: stage=%q err=%v", report.Stage, err)
			}
			if _, statErr := os.Stat(filepath.Join(stateDir(home), receiptFileName)); !os.IsNotExist(statErr) {
				t.Fatalf("corrupt cursor wrote planned receipt: %v", statErr)
			}
		})
	}
}

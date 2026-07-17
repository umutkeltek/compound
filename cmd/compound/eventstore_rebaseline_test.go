package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
	"compound/internal/policy"
)

// errFakeRestore forces the rollback restore to fail so F2 can exercise the recovery-finding path.
var errFakeRestore = errors.New("forced restore failure (test)")

// seedGrantedRebaselineHome builds a completed-migration home whose hook-events.ndjson
// legitimately grew after completion (so a rebaseline has real work to do), imports a standing
// grant that ALLOWS the rebaseline capability scoped to the sidecar, and returns the home + sidecar
// paths. The returned home is exactly the state the three falsifiers below drive.
func seedGrantedRebaselineHome(t *testing.T) (homePath, sidecarPath string) {
	t.Helper()
	homePath = t.TempDir()
	seedLooseCLIRecord(t, homePath, "rec_rb_seed", "x")
	streamPath := filepath.Join(homePath, "events", "hook-events.ndjson")
	if err := os.MkdirAll(filepath.Dir(streamPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(streamPath, []byte(`{"id":"pre"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, code := runEventStoreCLI(t, "migrate", "--apply", "--home", homePath); code != 0 {
		t.Fatalf("apply exit=%d", code)
	}

	// Legitimate growth AFTER completion — the sidecar now under-reports the live stream.
	f, err := os.OpenFile(streamPath, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"id":"grew"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	sidecarPath = filepath.Join(homePath, "events", "migration", "stream-baseline.json")
	grantSrc := filepath.Join(homePath, "rebaseline-grant.yaml")
	raw := "generated_at: \"2026-07-16T12:00:00Z\"\n" +
		"grants:\n" +
		"  - grant_id: grant_rebaseline\n" +
		"    capabilities: [\"" + eventStoreRebaselineCapability + "\"]\n" +
		"    target_scope: \"" + sidecarPath + "\"\n" +
		"    mutation_class: external_side_effect\n" +
		"    reversibility_ceiling: reversible\n" +
		"    blast_radius: local\n"
	if err := os.WriteFile(grantSrc, []byte(raw), 0o644); err != nil {
		t.Fatalf("write grant source: %v", err)
	}
	if _, err := policy.ImportStandingGrants(homePath, grantSrc, time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("import standing grants: %v", err)
	}
	return homePath, sidecarPath
}

// makeSegmentsReadOnly wedges the canonical event store's WRITE path (the post-mutation
// receipt/transition record) by removing traverse/write permission from every segments file and dir,
// while leaving the sidecar dir (events/migration) writable so RebaselineStreams still succeeds.
// This is the real, fs-level teeth for the record-after-sidecar-write failure — no test seam.
func makeSegmentsReadOnly(t *testing.T, homePath string) {
	t.Helper()
	segRoot := filepath.Join(homePath, "events", "segments")
	if _, err := os.Stat(segRoot); err != nil {
		t.Fatalf("expected a segments dir after migrate --apply: %v", err)
	}
	var paths []string
	if err := filepath.WalkDir(segRoot, func(p string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, p)
		return nil
	}); err != nil {
		t.Fatalf("walk %s: %v", segRoot, err)
	}
	// Restore writability BEFORE t.TempDir's RemoveAll runs (t.Cleanup is LIFO; this was
	// registered after TempDir's cleanup, so it runs first). Parents first so each child stays
	// reachable while we re-open the tree.
	t.Cleanup(func() {
		for _, p := range paths {
			_ = os.Chmod(p, 0o755)
		}
	})
	// Remove perms deepest-first so a parent stays traversable while we lock its children.
	for i := len(paths) - 1; i >= 0; i-- {
		if err := os.Chmod(paths[i], 0o444); err != nil {
			t.Fatalf("chmod ro %s: %v", paths[i], err)
		}
	}
}

func sha256Hex(t *testing.T, data []byte) string {
	t.Helper()
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func containsRef(refs []string, want string) bool {
	for _, r := range refs {
		if r == want {
			return true
		}
	}
	return false
}

// FALSIFIER F1 (durable proof or rollback): a record failure AFTER a successful RebaselineStreams
// must restore the sidecar byte-identical to its prior bytes and exit nonzero — never leave a
// mutated trust sidecar without durable proof. RED at base (base leaves the sidecar mutated and
// prints "rebaseline written but receipt could not be recorded"); GREEN with the rollback.
func TestEventStoreRebaselineRollsBackOnRecordFailure(t *testing.T) {
	homePath, sidecarPath := seedGrantedRebaselineHome(t)

	before, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("expected the auto-written sidecar after apply: %v", err)
	}

	// Wedge the post-mutation record: segments become unwritable, sidecar dir stays writable.
	makeSegmentsReadOnly(t, homePath)

	out, errStr, code := runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_rollback", "--home", homePath)
	if code == 0 {
		t.Fatalf("expected nonzero exit when the receipt cannot be recorded; stdout=%q stderr=%q", out, errStr)
	}
	if !strings.Contains(errStr, "rolled back") {
		t.Fatalf("expected the error to say the baseline was rolled back, got stderr=%q", errStr)
	}

	after, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("sidecar missing after rollback (should be restored byte-identical): %v", err)
	}
	if string(before) != string(after) {
		t.Fatalf("sidecar left mutated after a record failure; want byte-identical rollback.\nbefore=%q\nafter =%q", before, after)
	}
}

// FALSIFIER F1b (absent-prior variant): when there was NO sidecar before the attempt, a record
// failure must restore ABSENCE (remove the sidecar the mutation created), not leave a mutated one.
func TestEventStoreRebaselineRollsBackToAbsenceWhenNoPriorSidecar(t *testing.T) {
	homePath, sidecarPath := seedGrantedRebaselineHome(t)

	// Remove the auto-written sidecar so the prior state is genuine absence.
	if err := os.Remove(sidecarPath); err != nil {
		t.Fatalf("remove sidecar to simulate absence: %v", err)
	}

	makeSegmentsReadOnly(t, homePath)

	out, errStr, code := runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_absent", "--home", homePath)
	if code == 0 {
		t.Fatalf("expected nonzero exit; stdout=%q stderr=%q", out, errStr)
	}
	if !strings.Contains(errStr, "rolled back") {
		t.Fatalf("expected a rolled-back message, got stderr=%q", errStr)
	}
	if _, err := os.Stat(sidecarPath); !os.IsNotExist(err) {
		t.Fatalf("sidecar should be absent again after rollback (prior state was absent); stat err=%v", err)
	}
}

// FALSIFIER F2 (recovery finding when restore fails): if the record fails AND the rollback also
// fails, a durable recovery finding is landed and the error names it — the manual-recovery case is
// never silent. The restore is wedged with the restoreSidecarFn seam; the record is wedged with real
// fs perms.
func TestEventStoreRebaselineRecoveryFindingWhenRestoreFails(t *testing.T) {
	homePath, sidecarPath := seedGrantedRebaselineHome(t)

	makeSegmentsReadOnly(t, homePath)

	// Force the rollback itself to fail, so we reach the recovery-finding branch.
	orig := restoreSidecarFn
	restoreSidecarFn = func(string, []byte, bool) error {
		return errFakeRestore
	}
	t.Cleanup(func() { restoreSidecarFn = orig })

	out, errStr, code := runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_recovery", "--home", homePath)
	if code == 0 {
		t.Fatalf("expected nonzero exit; stdout=%q stderr=%q", out, errStr)
	}
	if !strings.Contains(errStr, "recovery finding") || !strings.Contains(errStr, "MANUAL RECOVERY REQUIRED") {
		t.Fatalf("expected an error naming the recovery finding and manual recovery, got stderr=%q", errStr)
	}

	// The finding is durable on disk under observed/findings.
	matches, err := filepath.Glob(filepath.Join(homePath, "observed", "findings", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatalf("expected a durable recovery finding under observed/findings; none found")
	}
	var found bool
	for _, m := range matches {
		body, err := os.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), sidecarPath) && strings.Contains(string(body), "manual recovery required") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no recovery finding named the sidecar path %q and manual recovery", sidecarPath)
	}
}

// FALSIFIER F3 (happy-path proof enrichment): a granted rebaseline records a receipt + transition
// whose EvidenceRefs carry the before/after sidecar sha256 refs (reconstructable from proof alone)
// and whose receipt Verification is non-empty. RED at base (refs are empty []string{}); GREEN with
// the enrichment.
func TestEventStoreRebaselineHappyPathCarriesBaselineRefs(t *testing.T) {
	homePath, sidecarPath := seedGrantedRebaselineHome(t)

	priorBytes, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("read prior sidecar: %v", err)
	}

	out, errStr, code := runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_refs", "--home", homePath, "--json")
	if code != 0 {
		t.Fatalf("granted confirm should succeed, got code=%d stderr=%q\n%s", code, errStr, out)
	}
	var result struct {
		ReceiptID    string `json:"receipt_id"`
		TransitionID string `json:"transition_id"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("parse rebaseline output json: %v (%q)", err, out)
	}

	afterBytes, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("read post sidecar: %v", err)
	}
	wantBefore := "eventstore-baseline-before:sha256:" + sha256Hex(t, priorBytes)
	wantAfter := "eventstore-baseline-after:sha256:" + sha256Hex(t, afterBytes)

	var receipt envelope.Receipt
	readTestEventJSON(t, homePath, result.ReceiptID, &receipt)
	if !containsRef(receipt.EvidenceRefs, wantBefore) {
		t.Fatalf("receipt EvidenceRefs missing before ref %q: %v", wantBefore, receipt.EvidenceRefs)
	}
	if !containsRef(receipt.EvidenceRefs, wantAfter) {
		t.Fatalf("receipt EvidenceRefs missing after ref %q: %v", wantAfter, receipt.EvidenceRefs)
	}
	if len(receipt.StateDelta.Verification) == 0 {
		t.Fatalf("receipt state_delta.verification is empty; want a falsifier/test ref")
	}

	var transition envelope.Transition
	readTestEventJSON(t, homePath, result.TransitionID, &transition)
	if !containsRef(transition.EvidenceRefs, wantBefore) || !containsRef(transition.EvidenceRefs, wantAfter) {
		t.Fatalf("transition EvidenceRefs missing before/after refs: %v", transition.EvidenceRefs)
	}
}

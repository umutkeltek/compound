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
	"compound/internal/eventstore"
	"compound/internal/finding"
	"compound/internal/home"
	"compound/internal/policy"
)

// errFakeRestore forces the rollback restore to fail so F2 can exercise the recovery-finding path.
var errFakeRestore = errors.New("forced restore failure (test)")
var errFakeReceiptRecord = errors.New("forced receipt record failure before put (test)")

func failReceiptRecordsBeforePut(t *testing.T) {
	t.Helper()
	original := recordEventstoreEnvelopeFn
	recordEventstoreEnvelopeFn = func(store *eventstore.Store, value envelope.Envelope) error {
		if _, ok := value.(*envelope.Receipt); ok {
			return errFakeReceiptRecord
		}
		return original(store, value)
	}
	t.Cleanup(func() { recordEventstoreEnvelopeFn = original })
}

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

	// Fail before Put so absence is provable and compensation is safe.
	failReceiptRecordsBeforePut(t)

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

	failReceiptRecordsBeforePut(t)

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
// never silent. The pre-Put record seam makes Receipt absence provable; rollbackRebaselineFn then
// wedges the compensating CAS restore so the recovery path must preserve both exact images.
func TestEventStoreRebaselineRecoveryFindingWhenRestoreFails(t *testing.T) {
	homePath, sidecarPath := seedGrantedRebaselineHome(t)
	prior, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatal(err)
	}

	failReceiptRecordsBeforePut(t)

	// Force the rollback itself to fail, so we reach the recovery-finding branch.
	orig := rollbackRebaselineFn
	rollbackRebaselineFn = func(*eventstore.RebaselineTransaction, []byte, []byte, bool) error {
		return errFakeRestore
	}
	t.Cleanup(func() { rollbackRebaselineFn = orig })

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
	var found *finding.Finding
	for _, m := range matches {
		body, err := os.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), sidecarPath) && strings.Contains(string(body), "manual recovery required") {
			var parsed finding.Finding
			if err := json.Unmarshal(body, &parsed); err != nil {
				t.Fatal(err)
			}
			found = &parsed
		}
	}
	if found == nil {
		t.Fatalf("no recovery finding named the sidecar path %q and manual recovery", sidecarPath)
	}
	if len(found.EvidenceRefs) != 2 {
		t.Fatalf("recovery finding refs=%v, want pre + actual", found.EvidenceRefs)
	}
	actual, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := home.NewArtifactStore(homePath)
	if err != nil {
		t.Fatal(err)
	}
	preArtifact, err := artifacts.Get(found.EvidenceRefs[0])
	if err != nil || string(preArtifact) != string(prior) {
		t.Fatalf("recovery preimage artifact mismatch: err=%v got=%q want=%q", err, preArtifact, prior)
	}
	actualArtifact, err := artifacts.Get(found.EvidenceRefs[1])
	if err != nil || string(actualArtifact) != string(actual) {
		t.Fatalf("recovery actual artifact mismatch: err=%v got=%q want=%q", err, actualArtifact, actual)
	}
	if _, err := os.Stat(filepath.Join(homePath, "events", "migration", "rebaseline-pending.json")); err != nil {
		t.Fatalf("recovery marker must remain: %v", err)
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
		BeforeRef    string `json:"before_ref"`
		AfterRef     string `json:"after_ref"`
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
	if len(receipt.ProofRefs) != 2 || len(receipt.StateDelta.Artifacts) != 2 {
		t.Fatalf("receipt canonical artifact proof missing: proof_refs=%v artifacts=%v", receipt.ProofRefs, receipt.StateDelta.Artifacts)
	}
	for _, artifactRef := range receipt.ProofRefs {
		if !containsRef(receipt.EvidenceRefs, artifactRef) {
			t.Fatalf("receipt EvidenceRefs missing artifact %q: %v", artifactRef, receipt.EvidenceRefs)
		}
	}
	if result.BeforeRef != wantBefore || result.AfterRef != wantAfter {
		t.Fatalf("output refs before=%q after=%q, want %q %q", result.BeforeRef, result.AfterRef, wantBefore, wantAfter)
	}

	var transition envelope.Transition
	readTestEventJSON(t, homePath, result.TransitionID, &transition)
	if !containsRef(transition.EvidenceRefs, wantBefore) || !containsRef(transition.EvidenceRefs, wantAfter) {
		t.Fatalf("transition EvidenceRefs missing before/after refs: %v", transition.EvidenceRefs)
	}
	if len(transition.BeforeRefs) != 1 || len(transition.AfterRefs) != 1 || transition.BeforeRefs[0] != receipt.ProofRefs[0] || transition.AfterRefs[0] != receipt.ProofRefs[1] {
		t.Fatalf("transition artifact refs do not match receipt proof: before=%v after=%v proof=%v", transition.BeforeRefs, transition.AfterRefs, receipt.ProofRefs)
	}
	for _, artifactRef := range receipt.ProofRefs {
		if !containsRef(transition.EvidenceRefs, artifactRef) {
			t.Fatalf("transition EvidenceRefs missing artifact %q: %v", artifactRef, transition.EvidenceRefs)
		}
	}
}

func TestEventStoreRebaselineTransitionFailureRollsForwardExactProofOnRetry(t *testing.T) {
	homePath, sidecarPath := seedGrantedRebaselineHome(t)
	original := recordEventstoreEnvelopeFn
	failed := false
	recordEventstoreEnvelopeFn = func(store *eventstore.Store, value envelope.Envelope) error {
		if _, ok := value.(*envelope.Transition); ok && !failed {
			failed = true
			return errors.New("forced transition write failure")
		}
		return original(store, value)
	}
	t.Cleanup(func() { recordEventstoreEnvelopeFn = original })

	out, errStr, code := runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_transition_retry", "--home", homePath, "--json")
	if code == 0 || !strings.Contains(errStr, "finalization pending") {
		t.Fatalf("first call code=%d out=%q err=%q, want pending", code, out, errStr)
	}
	pendingPath := filepath.Join(homePath, "events", "migration", "rebaseline-pending.json")
	body, err := os.ReadFile(pendingPath)
	if err != nil {
		t.Fatalf("pending marker missing: %v", err)
	}
	var pending eventstore.RebaselinePending
	if err := json.Unmarshal(body, &pending); err != nil {
		t.Fatal(err)
	}
	var receipt envelope.Receipt
	readTestEventJSON(t, homePath, pending.Receipt.ID, &receipt)
	if receipt.Outcome != "completed" {
		t.Fatalf("durable receipt not truthful: %+v", receipt)
	}
	if exists, err := eventstore.Open(homePath); err != nil {
		t.Fatal(err)
	} else if ok, _ := exists.Exists(pending.Transition.ID); ok {
		t.Fatalf("transition unexpectedly existed before retry")
	}
	postBeforeRetry, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatal(err)
	}

	recordEventstoreEnvelopeFn = original
	out, errStr, code = runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_should_not_replace_original", "--home", homePath, "--json")
	if code != 0 {
		t.Fatalf("retry code=%d out=%q err=%q", code, out, errStr)
	}
	var result struct {
		ReceiptID    string `json:"receipt_id"`
		TransitionID string `json:"transition_id"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.ReceiptID != pending.Receipt.ID || result.TransitionID != pending.Transition.ID {
		t.Fatalf("retry invented proof ids: got %s %s want %s %s", result.ReceiptID, result.TransitionID, pending.Receipt.ID, pending.Transition.ID)
	}
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Fatalf("pending marker not cleared after exact roll-forward: %v", err)
	}
	postAfterRetry, err := os.ReadFile(sidecarPath)
	if err != nil || string(postAfterRetry) != string(postBeforeRetry) {
		t.Fatalf("retry performed a second mutation: err=%v before=%q after=%q", err, postBeforeRetry, postAfterRetry)
	}
}

func TestEventStoreRebaselineRecordErrorAfterDurableReceiptDoesNotRollback(t *testing.T) {
	homePath, sidecarPath := seedGrantedRebaselineHome(t)
	before, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatal(err)
	}
	original := recordEventstoreEnvelopeFn
	recordEventstoreEnvelopeFn = func(store *eventstore.Store, value envelope.Envelope) error {
		if _, ok := value.(*envelope.Receipt); ok {
			if err := original(store, value); err != nil {
				return err
			}
			return errors.New("injected readback error after durable receipt")
		}
		return original(store, value)
	}
	t.Cleanup(func() { recordEventstoreEnvelopeFn = original })
	out, errStr, code := runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_durable_receipt", "--home", homePath, "--json")
	if code != 0 {
		t.Fatalf("durable receipt should roll forward transition: code=%d out=%q err=%q", code, out, errStr)
	}
	after, err := os.ReadFile(sidecarPath)
	if err != nil || string(after) == string(before) {
		t.Fatalf("post-state was rolled back despite durable receipt: err=%v", err)
	}
}

func TestEventStoreRebaselineSidecarConflictKeepsJournalAndArtifactsActualState(t *testing.T) {
	homePath, _ := seedGrantedRebaselineHome(t)
	foreign := []byte("foreign concurrent baseline bytes\n")
	original := writeRebaselineSidecarFn
	writeRebaselineSidecarFn = func(tx *eventstore.RebaselineTransaction, _ []byte, _ bool, _ []byte) error {
		if err := os.WriteFile(tx.SidecarPath(), foreign, 0o644); err != nil {
			return err
		}
		return eventstore.ErrRebaselineStateConflict
	}
	t.Cleanup(func() { writeRebaselineSidecarFn = original })

	_, errStr, code := runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_sidecar_conflict", "--home", homePath)
	if code == 0 || !strings.Contains(errStr, "recovery finding") {
		t.Fatalf("conflict code=%d err=%q", code, errStr)
	}
	if _, err := os.Stat(filepath.Join(homePath, "events", "migration", "rebaseline-pending.json")); err != nil {
		t.Fatalf("conflict cleared recovery journal: %v", err)
	}
	matches, err := filepath.Glob(filepath.Join(homePath, "observed", "findings", "*.json"))
	if err != nil || len(matches) == 0 {
		t.Fatalf("recovery finding missing: matches=%v err=%v", matches, err)
	}
	body, err := os.ReadFile(matches[len(matches)-1])
	if err != nil {
		t.Fatal(err)
	}
	var found finding.Finding
	if err := json.Unmarshal(body, &found); err != nil {
		t.Fatal(err)
	}
	if len(found.EvidenceRefs) != 2 {
		t.Fatalf("finding refs=%v, want pre + actual", found.EvidenceRefs)
	}
	artifacts, err := home.NewArtifactStore(homePath)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := artifacts.Get(found.EvidenceRefs[1])
	if err != nil || string(actual) != string(foreign) {
		t.Fatalf("actual-state artifact got=%q err=%v want=%q", actual, err, foreign)
	}
}

func TestEventStoreRebaselineValidatesBothEnvelopesBeforeJournalOrMutation(t *testing.T) {
	for _, kind := range []string{"receipt", "transition"} {
		t.Run(kind, func(t *testing.T) {
			homePath, sidecarPath := seedGrantedRebaselineHome(t)
			before, err := os.ReadFile(sidecarPath)
			if err != nil {
				t.Fatal(err)
			}
			original := buildRebaselineEnvelopesFn
			buildRebaselineEnvelopesFn = func(ai envelope.ActionIntent, before, after map[string]int64, beforeRef, afterRef, preRef, postRef string, now time.Time) (envelope.Receipt, envelope.Transition) {
				receipt, transition := original(ai, before, after, beforeRef, afterRef, preRef, postRef, now)
				if kind == "receipt" {
					receipt.SchemaVersion = "invalid"
				} else {
					transition.SchemaVersion = "invalid"
				}
				return receipt, transition
			}
			t.Cleanup(func() { buildRebaselineEnvelopesFn = original })
			_, errStr, code := runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_invalid_"+kind, "--home", homePath)
			if code == 0 || !strings.Contains(errStr, "validate rebaseline "+kind) {
				t.Fatalf("code=%d err=%q", code, errStr)
			}
			after, err := os.ReadFile(sidecarPath)
			if err != nil || string(after) != string(before) {
				t.Fatalf("invalid %s mutated sidecar", kind)
			}
			if _, err := os.Stat(filepath.Join(homePath, "events", "migration", "rebaseline-pending.json")); !os.IsNotExist(err) {
				t.Fatalf("invalid %s left pending marker: %v", kind, err)
			}
		})
	}
}

func TestEventStoreRebaselineAuthorityFaceIsLiveAndConfirmReevaluatesPolicy(t *testing.T) {
	homePath, sidecarPath := seedGrantedRebaselineHome(t)
	out, errStr, code := runEventStoreCLI(t, "rebaseline", "--home", homePath, "--json")
	if code != 0 {
		t.Fatalf("dry run code=%d err=%q", code, errStr)
	}
	var preview struct {
		Allowed         bool                 `json:"allowed"`
		PolicySource    string               `json:"policy_source"`
		PolicyAuthority policy.AuthorityFace `json:"policy_authority"`
	}
	if err := json.Unmarshal([]byte(out), &preview); err != nil {
		t.Fatal(err)
	}
	if !preview.Allowed || !preview.PolicyAuthority.Current || !preview.PolicyAuthority.Allowed || preview.PolicySource == "" {
		t.Fatalf("authority face not live: %+v", preview)
	}
	if err := os.Remove(policy.StandingGrantsPath(homePath)); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatal(err)
	}
	_, errStr, code = runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_fresh_policy", "--home", homePath)
	if code == 0 || !strings.Contains(errStr, "authority refused") {
		t.Fatalf("confirm did not freshly refuse changed policy: code=%d err=%q", code, errStr)
	}
	after, err := os.ReadFile(sidecarPath)
	if err != nil || string(after) != string(before) {
		t.Fatalf("refused confirm mutated sidecar")
	}
}

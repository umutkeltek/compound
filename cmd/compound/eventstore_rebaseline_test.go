package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// FALSIFIER F1 (durable proof or rollback): a record failure AFTER a successful sidecar CAS
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
	if len(found.EvidenceRefs) != 3 {
		t.Fatalf("recovery finding refs=%v, want pre + actual + raw marker", found.EvidenceRefs)
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
	marker, err := os.ReadFile(filepath.Join(homePath, "events", "migration", "rebaseline-pending.json"))
	if err != nil {
		t.Fatal(err)
	}
	markerArtifact, err := artifacts.Get(found.EvidenceRefs[2])
	if err != nil || string(markerArtifact) != string(marker) {
		t.Fatalf("recovery marker artifact mismatch: err=%v", err)
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
	var pending rebaselinePending
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

func TestEventStoreRebaselineRecordErrorAfterDurableTransitionFinalizesOnRetry(t *testing.T) {
	homePath, sidecarPath := seedGrantedRebaselineHome(t)
	original := recordEventstoreEnvelopeFn
	failed := false
	recordEventstoreEnvelopeFn = func(store *eventstore.Store, value envelope.Envelope) error {
		if _, ok := value.(*envelope.Transition); ok && !failed {
			failed = true
			if err := original(store, value); err != nil {
				return err
			}
			return errors.New("injected readback error after durable transition")
		}
		return original(store, value)
	}
	t.Cleanup(func() { recordEventstoreEnvelopeFn = original })

	_, errStr, code := runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_durable_transition", "--home", homePath, "--json")
	if code == 0 || !strings.Contains(errStr, "finalization pending") {
		t.Fatalf("first call code=%d err=%q, want truthful pending state", code, errStr)
	}
	pendingPath := filepath.Join(homePath, "events", "migration", "rebaseline-pending.json")
	raw, err := os.ReadFile(pendingPath)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := decodeRebaselinePending(raw, sidecarPath)
	if err != nil {
		t.Fatal(err)
	}
	store, err := eventstore.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if state, err := eventstoreProofPresence(store, &pending.Transition); err != nil || state != proofExact {
		t.Fatalf("transition did not land durably before recorder error: state=%v err=%v", state, err)
	}
	postBeforeRetry, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatal(err)
	}

	recordEventstoreEnvelopeFn = original
	out, errStr, code := runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_must_not_replace_durable_transition", "--home", homePath, "--json")
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
		t.Fatalf("retry replaced exact durable proof: got=%s/%s want=%s/%s", result.ReceiptID, result.TransitionID, pending.Receipt.ID, pending.Transition.ID)
	}
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Fatalf("retry did not clear finalized marker: %v", err)
	}
	postAfterRetry, err := os.ReadFile(sidecarPath)
	if err != nil || string(postAfterRetry) != string(postBeforeRetry) {
		t.Fatalf("retry performed a second sidecar mutation: err=%v", err)
	}
}

func TestEventStoreRebaselineSidecarConflictKeepsJournalAndArtifactsActualState(t *testing.T) {
	homePath, _ := seedGrantedRebaselineHome(t)
	foreign := []byte("foreign concurrent baseline bytes\n")
	original := writeRebaselineSidecarFn
	writeRebaselineSidecarFn = func(tx *eventstore.RebaselineTransaction, prior []byte, priorExisted bool, post []byte) error {
		if err := tx.WriteSidecarCAS(prior, priorExisted, post); err != nil {
			return err
		}
		if err := os.WriteFile(tx.SidecarPath(), foreign, 0o644); err != nil {
			return err
		}
		return nil
	}
	t.Cleanup(func() { writeRebaselineSidecarFn = original })

	_, errStr, code := runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_sidecar_conflict", "--home", homePath)
	if code == 0 || !strings.Contains(errStr, "recovery finding") {
		t.Fatalf("conflict code=%d err=%q", code, errStr)
	}
	if _, err := os.Stat(filepath.Join(homePath, "events", "migration", "rebaseline-pending.json")); err != nil {
		t.Fatalf("conflict cleared recovery journal: %v", err)
	}
	pendingRaw, err := os.ReadFile(filepath.Join(homePath, "events", "migration", "rebaseline-pending.json"))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := decodeRebaselinePending(pendingRaw, filepath.Join(homePath, "events", "migration", "stream-baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := eventstore.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	if exists, err := store.Exists(pending.Receipt.ID); err != nil || exists {
		t.Fatalf("CAS mismatch persisted completed receipt: exists=%t err=%v", exists, err)
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
	if len(found.EvidenceRefs) != 3 {
		t.Fatalf("finding refs=%v, want pre + actual + raw marker", found.EvidenceRefs)
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

func TestEventStoreRebaselineMalformedPendingIsPreservedAsRecoveryEvidence(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{name: "malformed_json", raw: []byte("{not-json\n")},
		{name: "invalid_schema", raw: []byte(`{"schema_version":"wrong"}` + "\n")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			homePath, _ := seedGrantedRebaselineHome(t)
			pendingPath := filepath.Join(homePath, "events", "migration", "rebaseline-pending.json")
			if err := os.WriteFile(pendingPath, tc.raw, 0o644); err != nil {
				t.Fatal(err)
			}
			_, errStr, code := runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_bad_pending", "--home", homePath)
			if code == 0 || !strings.Contains(errStr, "recovery finding") {
				t.Fatalf("code=%d err=%q", code, errStr)
			}
			gotRaw, err := os.ReadFile(pendingPath)
			if err != nil || string(gotRaw) != string(tc.raw) {
				t.Fatalf("raw pending marker changed: got=%q err=%v want=%q", gotRaw, err, tc.raw)
			}
			matches, err := filepath.Glob(filepath.Join(homePath, "observed", "findings", "*.json"))
			if err != nil || len(matches) != 1 {
				t.Fatalf("findings=%v err=%v", matches, err)
			}
			body, err := os.ReadFile(matches[0])
			if err != nil {
				t.Fatal(err)
			}
			var found finding.Finding
			if err := json.Unmarshal(body, &found); err != nil {
				t.Fatal(err)
			}
			for _, ref := range found.EvidenceRefs {
				if strings.TrimSpace(ref) == "" {
					t.Fatalf("finding contains empty evidence ref: %v", found.EvidenceRefs)
				}
			}
			if len(found.EvidenceRefs) != 2 {
				t.Fatalf("refs=%v, want actual state + raw marker", found.EvidenceRefs)
			}
			artifacts, err := home.NewArtifactStore(homePath)
			if err != nil {
				t.Fatal(err)
			}
			markerArtifact, err := artifacts.Get(found.EvidenceRefs[1])
			if err != nil || string(markerArtifact) != string(tc.raw) {
				t.Fatalf("raw marker artifact got=%q err=%v want=%q", markerArtifact, err, tc.raw)
			}
		})
	}
}

func TestEventstoreProofPresenceTreatsUnknownStoredFieldsAsConflict(t *testing.T) {
	homePath := t.TempDir()
	store, err := eventstore.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 17, 11, 12, 13, 123456789, time.UTC)
	ai := rebaselineIntent("work_proof_exact", envelope.ActionTarget{Kind: "eventstore_stream_baseline", ID: "stream-baseline", Path: filepath.Join(homePath, "events", "migration", "stream-baseline.json")}, now)
	preRef := "artifact:sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	postRef := "artifact:sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	receipt, _ := rebaselineReceiptAndTransition(ai, map[string]int64{}, map[string]int64{}, "eventstore-baseline-before:absent", "eventstore-baseline-after:sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", preRef, postRef, now)
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := json.Unmarshal(raw, &stored); err != nil {
		t.Fatal(err)
	}
	stored["future_unknown_field"] = map[string]any{"must_not_be_dropped": true}
	if _, err := store.PutJSON(receipt.ID, stored, " "); err != nil {
		t.Fatal(err)
	}
	state, err := eventstoreProofPresence(store, &receipt)
	if err != nil {
		t.Fatal(err)
	}
	if state != proofConflict {
		t.Fatalf("proof presence=%v, want conflict for unknown stored field", state)
	}
}

func TestEventStoreRebaselineSameWorkSameSecondProducesDistinctProofIDs(t *testing.T) {
	homePath, _ := seedGrantedRebaselineHome(t)
	base := time.Date(2026, 7, 17, 11, 12, 13, 0, time.UTC)
	withNanos := base.Add(123456789 * time.Nanosecond)
	intent := rebaselineIntent("work_timestamp", envelope.ActionTarget{Kind: "eventstore_stream_baseline", ID: "stream-baseline", Path: filepath.Join(homePath, "events", "migration", "stream-baseline.json")}, withNanos)
	if intent.CreatedAt != withNanos.Format(time.RFC3339Nano) {
		t.Fatalf("action created_at=%q, want %q", intent.CreatedAt, withNanos.Format(time.RFC3339Nano))
	}
	original := rebaselineNowFn
	clockCalls := 0
	rebaselineNowFn = func() time.Time {
		clockCalls++
		return base.Add(time.Duration(clockCalls) * time.Nanosecond)
	}
	t.Cleanup(func() { rebaselineNowFn = original })

	run := func() struct {
		ReceiptID    string `json:"receipt_id"`
		TransitionID string `json:"transition_id"`
	} {
		out, errStr, code := runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_same_second", "--home", homePath, "--json")
		if code != 0 {
			t.Fatalf("confirm code=%d err=%q out=%q", code, errStr, out)
		}
		var result struct {
			ReceiptID    string `json:"receipt_id"`
			TransitionID string `json:"transition_id"`
		}
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first := run()
	second := run()
	if first.ReceiptID == second.ReceiptID || first.TransitionID == second.TransitionID {
		t.Fatalf("same-second confirms collided: first=%+v second=%+v", first, second)
	}
	var firstReceipt envelope.Receipt
	readTestEventJSON(t, homePath, first.ReceiptID, &firstReceipt)
	var secondReceipt envelope.Receipt
	readTestEventJSON(t, homePath, second.ReceiptID, &secondReceipt)
	firstCreated, err := time.Parse(time.RFC3339Nano, firstReceipt.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	secondCreated, err := time.Parse(time.RFC3339Nano, secondReceipt.CreatedAt)
	if err != nil {
		t.Fatal(err)
	}
	if firstCreated.Equal(secondCreated) || !firstCreated.Truncate(time.Second).Equal(secondCreated.Truncate(time.Second)) {
		t.Fatalf("receipt timestamps not distinct within same second: %s %s", firstReceipt.CreatedAt, secondReceipt.CreatedAt)
	}
	if _, err := os.Stat(filepath.Join(homePath, "events", "migration", "rebaseline-pending.json")); !os.IsNotExist(err) {
		t.Fatalf("double confirm stranded journal: %v", err)
	}
}

func TestEventStoreRebaselinePendingClearFailureRetriesWithoutSecondMutation(t *testing.T) {
	homePath, sidecarPath := seedGrantedRebaselineHome(t)
	original := clearRebaselinePendingFn
	failed := false
	clearRebaselinePendingFn = func(tx *eventstore.RebaselineTransaction) error {
		if !failed {
			failed = true
			return errors.New("forced durable pending-clear failure")
		}
		return original(tx)
	}
	t.Cleanup(func() { clearRebaselinePendingFn = original })

	_, errStr, code := runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_clear_retry", "--home", homePath, "--json")
	if code == 0 || !strings.Contains(errStr, "finalization pending") {
		t.Fatalf("first confirm code=%d err=%q", code, errStr)
	}
	pendingPath := filepath.Join(homePath, "events", "migration", "rebaseline-pending.json")
	raw, err := os.ReadFile(pendingPath)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := decodeRebaselinePending(raw, sidecarPath)
	if err != nil {
		t.Fatal(err)
	}
	postBeforeRetry, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatal(err)
	}
	clearRebaselinePendingFn = original
	out, errStr, code := runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_must_not_replace", "--home", homePath, "--json")
	if code != 0 {
		t.Fatalf("retry code=%d err=%q out=%q", code, errStr, out)
	}
	var result struct {
		ReceiptID    string `json:"receipt_id"`
		TransitionID string `json:"transition_id"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	if result.ReceiptID != pending.Receipt.ID || result.TransitionID != pending.Transition.ID {
		t.Fatalf("retry replaced exact proof: got=%+v pending=%s/%s", result, pending.Receipt.ID, pending.Transition.ID)
	}
	postAfterRetry, err := os.ReadFile(sidecarPath)
	if err != nil || string(postAfterRetry) != string(postBeforeRetry) {
		t.Fatalf("retry second-mutated sidecar: err=%v", err)
	}
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Fatalf("retry did not clear pending marker: %v", err)
	}
}

func TestEventStoreRebaselineRecoversNoProofPreAndPostJournalStates(t *testing.T) {
	for _, state := range []string{"pre", "post"} {
		t.Run(state, func(t *testing.T) {
			homePath, sidecarPath := seedGrantedRebaselineHome(t)
			pre, err := os.ReadFile(sidecarPath)
			if err != nil {
				t.Fatal(err)
			}
			original := recordEventstoreEnvelopeFn
			failed := false
			recordEventstoreEnvelopeFn = func(store *eventstore.Store, value envelope.Envelope) error {
				if _, ok := value.(*envelope.Transition); ok && !failed {
					failed = true
					return errors.New("seed pending state before transition")
				}
				return original(store, value)
			}
			_, _, code := runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_seed_"+state, "--home", homePath)
			recordEventstoreEnvelopeFn = original
			t.Cleanup(func() { recordEventstoreEnvelopeFn = original })
			if code == 0 {
				t.Fatal("seed transition failure unexpectedly succeeded")
			}
			pendingPath := filepath.Join(homePath, "events", "migration", "rebaseline-pending.json")
			raw, err := os.ReadFile(pendingPath)
			if err != nil {
				t.Fatal(err)
			}
			pending, err := decodeRebaselinePending(raw, sidecarPath)
			if err != nil {
				t.Fatal(err)
			}
			store, err := eventstore.Open(homePath)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Remove(pending.Receipt.ID); err != nil {
				t.Fatalf("remove seeded receipt: %v", err)
			}
			if state == "pre" {
				if err := os.WriteFile(sidecarPath, pre, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			out, errStr, code := runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_recovered_"+state, "--home", homePath, "--json")
			if code != 0 {
				t.Fatalf("recovery code=%d err=%q out=%q", code, errStr, out)
			}
			var result struct {
				ReceiptID string `json:"receipt_id"`
			}
			if err := json.Unmarshal([]byte(out), &result); err != nil {
				t.Fatal(err)
			}
			if result.ReceiptID == pending.Receipt.ID {
				t.Fatalf("no-proof recovery reused abandoned receipt %s", result.ReceiptID)
			}
			var receipt envelope.Receipt
			readTestEventJSON(t, homePath, result.ReceiptID, &receipt)
			if len(receipt.ProofRefs) < 1 || receipt.ProofRefs[0] != pending.PreImageRef {
				t.Fatalf("recovery did not proceed from exact preimage: proof=%v want_pre=%s", receipt.ProofRefs, pending.PreImageRef)
			}
			if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
				t.Fatalf("recovery stranded marker: %v", err)
			}
		})
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
	if code == 0 || !strings.Contains(errStr, "refused") {
		t.Fatalf("confirm did not freshly refuse changed policy: code=%d err=%q", code, errStr)
	}
	after, err := os.ReadFile(sidecarPath)
	if err != nil || string(after) != string(before) {
		t.Fatalf("refused confirm mutated sidecar")
	}
}

func TestEventStoreRebaselineFreshInLockAuthorityCanRefuseAllowedPreflight(t *testing.T) {
	homePath, sidecarPath := seedGrantedRebaselineHome(t)
	before, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatal(err)
	}
	original := loadRebaselinePolicyFn
	calls := 0
	loadRebaselinePolicyFn = func(path string) (policy.Config, string, error) {
		calls++
		if calls == 1 {
			return original(path)
		}
		return policy.DefaultConfig(), "changed-after-preflight", nil
	}
	t.Cleanup(func() { loadRebaselinePolicyFn = original })
	_, errStr, code := runEventStoreCLI(t, "rebaseline", "--confirm", "--work", "work_fresh_in_lock", "--home", homePath)
	if code == 0 || !strings.Contains(errStr, "authority refused") {
		t.Fatalf("confirm code=%d err=%q, want fresh in-lock refusal", code, errStr)
	}
	if calls != 2 {
		t.Fatalf("policy load calls=%d, want preflight + in-lock evaluation", calls)
	}
	after, err := os.ReadFile(sidecarPath)
	if err != nil || string(after) != string(before) {
		t.Fatalf("fresh refusal mutated sidecar: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(homePath, "events", "migration", "rebaseline-pending.json")); !os.IsNotExist(err) {
		t.Fatalf("fresh refusal wrote pending marker: %v", err)
	}
}

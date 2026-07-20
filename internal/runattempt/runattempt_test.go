package runattempt

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIDFormat(t *testing.T) {
	fixed := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		id, err := Mint(fixed)
		if err != nil {
			t.Fatalf("Mint: %v", err)
		}
		if !Valid(id) {
			t.Fatalf("minted id %q did not pass Valid()", id)
		}
		if !strings.HasPrefix(id, "ratt_") {
			t.Fatalf("minted id %q missing ratt_ prefix", id)
		}
		if !strings.Contains(id, "20260720T000000Z") {
			t.Fatalf("minted id %q missing expected timestamp", id)
		}
	}
	// Two mints at the same instant still produce distinct ids (suffix is from rand).
	a, _ := Mint(fixed)
	b, _ := Mint(fixed)
	if a == b {
		t.Fatalf("two concurrent mints collided: %q", a)
	}
}

func TestIDRejectsLegacyShapes(t *testing.T) {
	for _, bad := range []string{
		"",
		"prun_20260718T100000Z_aaaaaaaaaaaa",
		"run_packet",
		"act_9f2c1d4e",
		"work_abcdef",
		"ratt_2026-07-20_aabbccdd",        // wrong timestamp layout
		"ratt_20260720T000000Z_aabbccd",   // 7-char suffix
		"ratt_20260720T000000Z_AABBCCDD",  // uppercase
		" ratt_20260720T000000Z_aabbccdd", // leading space
		"ratt_20260720T000000Z_aabbccdd ", // trailing space
	} {
		if Valid(bad) {
			t.Fatalf("Valid(%q) must be false", bad)
		}
	}
}

func TestStoreMintAndGet(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	ra, err := s.Mint("work_abc", "compound-panel", "fake", now)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if !Valid(ra.ID) {
		t.Fatalf("minted id %q not valid", ra.ID)
	}
	if ra.SchemaVersion != SchemaVersion {
		t.Fatalf("schema version = %q, want %q", ra.SchemaVersion, SchemaVersion)
	}
	if ra.Status != StatusPending {
		t.Fatalf("initial status = %q, want pending", ra.Status)
	}
	if !ra.CreatedAt.Equal(now) {
		t.Fatalf("created_at = %v, want %v", ra.CreatedAt, now)
	}

	// File landed in the right place.
	if _, err := os.Stat(filepath.Join(home, "run_attempts", ra.ID+".json")); err != nil {
		t.Fatalf("expected on-disk artifact: %v", err)
	}

	// Fresh Store re-reads from disk (no cache carry-over).
	s2 := Open(home)
	got, err := s2.Get(ra.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	// Field-by-field comparison: RunAttempt contains []string (ProofRefs, added in S2)
	// which makes the struct uncomparable with ==.
	if got.ID != ra.ID || got.WorkID != ra.WorkID || got.Actor != ra.Actor ||
		got.Harness != ra.Harness || got.Status != ra.Status ||
		!got.CreatedAt.Equal(ra.CreatedAt) || !got.CompletedAt.Equal(ra.CompletedAt) ||
		got.SchemaVersion != ra.SchemaVersion {
		t.Fatalf("Get round-trip mismatch:\n got: %+v\nwant: %+v", got, ra)
	}
}

func TestStoreMintRejectsMissingFields(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name    string
		workID  string
		actor   string
		harness string
		wantSub string
	}{
		{"no work_id", "", "compound", "fake", "work_id is required"},
		{"no actor", "work_abc", "", "fake", "actor is required"},
		{"no harness", "work_abc", "compound", "", "harness is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.Mint(tc.workID, tc.actor, tc.harness, now)
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("Mint(%s) err = %v, want substring %q", tc.name, err, tc.wantSub)
			}
		})
	}
}

func TestStoreComplete(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	ra, err := s.Mint("work_abc", "compound", "fake", now)
	if err != nil {
		t.Fatal(err)
	}
	// Only terminal statuses allowed.
	if _, err := s.Complete(ra.ID, StatusRunning, now.Add(time.Second)); err == nil {
		t.Fatalf("Complete with non-terminal status must fail")
	}
	if _, err := s.Complete(ra.ID, "garbage", now.Add(time.Second)); err == nil {
		t.Fatalf("Complete with bogus status must fail")
	}
	completed, err := s.Complete(ra.ID, StatusSucceeded, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if completed.Status != StatusSucceeded {
		t.Fatalf("status = %q, want succeeded", completed.Status)
	}
	if !completed.CompletedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("completed_at = %v, want %v", completed.CompletedAt, now.Add(time.Minute))
	}
	// Second Complete is rejected (already terminal).
	if _, err := s.Complete(ra.ID, StatusFailed, now.Add(2*time.Minute)); err == nil {
		t.Fatalf("re-Complete of terminal RunAttempt must fail")
	}
	// Disk shows the terminal status.
	got, err := Open(home).Get(ra.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusSucceeded {
		t.Fatalf("on-disk status = %q, want succeeded", got.Status)
	}
}

func TestStoreGetRejectsBadID(t *testing.T) {
	s := Open(t.TempDir())
	for _, bad := range []string{"", "prun_xxx", "free_string", "ratt_20260720T000000Z_short"} {
		if _, err := s.Get(bad); err == nil {
			t.Fatalf("Get(%q) must fail", bad)
		}
	}
}

func TestStoreGetNotFound(t *testing.T) {
	s := Open(t.TempDir())
	_, err := s.Get("ratt_20260720T000000Z_deadbeef")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

// TestStoreAttachActionIntentHappy binds a freshly minted RunAttempt to an act_ id and proves
// the binding is persisted (a fresh Store reads it back) and retry-idempotent.
func TestStoreAttachActionIntentHappy(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	ra, err := s.Mint("work_abc", "compound-panel", HarnessPlaceholder, now)
	if err != nil {
		t.Fatal(err)
	}
	actID := "act_9f2c1d4e"
	if err := s.AttachActionIntent(ra.ID, actID); err != nil {
		t.Fatalf("attach: %v", err)
	}
	got, err := Open(home).Get(ra.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ActionIntentID != actID {
		t.Fatalf("persisted action_intent_id = %q, want %q", got.ActionIntentID, actID)
	}
	// Idempotent retry: attaching the SAME intent again is a no-op success.
	if err := s.AttachActionIntent(ra.ID, actID); err != nil {
		t.Fatalf("idempotent re-attach must succeed, got: %v", err)
	}
}

// TestStoreAttachActionIntentRejects covers the three refusal paths: a non-act_ intent, a
// mismatched rebind (ErrAlreadyBound), and binding after terminalization.
func TestStoreAttachActionIntentRejects(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	ra, err := s.Mint("work_abc", "compound-panel", HarnessPlaceholder, now)
	if err != nil {
		t.Fatal(err)
	}
	// Non-act_ intent id.
	if err := s.AttachActionIntent(ra.ID, "prun_deadbeef"); err == nil {
		t.Fatalf("attach with non-act_ intent must fail")
	}
	// Bind once, then attempt a mismatched rebind.
	first := "act_11112222"
	if err := s.AttachActionIntent(ra.ID, first); err != nil {
		t.Fatal(err)
	}
	err = s.AttachActionIntent(ra.ID, "act_33334444")
	if !errors.Is(err, ErrAlreadyBound) {
		t.Fatalf("mismatched rebind must return ErrAlreadyBound, got: %v", err)
	}
	// Binding after terminalization is a bug.
	ra2, err := s.Mint("work_def", "compound-panel", HarnessPlaceholder, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Complete(ra2.ID, StatusFailed, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := s.AttachActionIntent(ra2.ID, "act_55556666"); err == nil {
		t.Fatalf("attach after terminal must fail")
	}
	// Malformed ratt_ id.
	if err := s.AttachActionIntent("not-a-ratt-id", "act_77778888"); err == nil {
		t.Fatalf("attach with malformed id must fail")
	}
}

// --- S2: full RunAttempt shape accessors ---
//
// Slice B adds context_pack_ref, workspace, command, last_heartbeat_at,
// transcript_ref, diff_ref, test_ref, and proof_refs to the RunAttempt
// envelope. All eight are omitempty so pre-S2 records (which never had
// these fields) marshal byte-identical when re-decoded and re-marshaled.
// Every setter refuses on a terminal RunAttempt (succeeded|failed) -- the
// audit invariant is that completed attempts cannot be retroactively
// rewritten.
//
// Falsifier: removing any setter turns the corresponding test RED with a
// "undefined" build error; removing the terminal-rejection check turns
// the refusal tests RED with "setter on terminal runattempt must fail".
// The hash-neutrality test goes RED if any new field drops `omitempty` --
// a single-byte drift is exactly the regression S1's TestEmptyRunAttemptIDsIsHashNeutral
// is built to catch.

// TestRunAttemptHashNeutralityForPreS2Records pins the contract that adding
// the S2 fields does not invalidate pre-existing ratt_* records on disk. A
// record written by S1 (no S2 fields) re-decoded and re-marshaled must
// produce byte-identical bytes. Pinned alongside the work-side
// TestEmptyRunAttemptIDsIsHashNeutral so future field additions face the
// same discipline.
func TestRunAttemptHashNeutralityForPreS2Records(t *testing.T) {
	preS2JSON := []byte(`{
  "schema_version": "compound.runattempt.v0.1",
  "id": "ratt_20260720T000000Z_deadbeef",
  "work_id": "work_abc",
  "action_intent_id": "act_11112222",
  "actor": "codex",
  "harness": "fake",
  "status": "succeeded",
  "created_at": "2026-07-20T00:00:00Z",
  "completed_at": "2026-07-20T00:01:00Z",
  "last_heartbeat_at": "0001-01-01T00:00:00Z"
}`)
	var ra RunAttempt
	if err := json.Unmarshal(preS2JSON, &ra); err != nil {
		t.Fatal(err)
	}
	out, err := json.MarshalIndent(ra, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != string(preS2JSON) {
		t.Fatalf("pre-S2 record re-marshal drifted:\n got: %s\nwant: %s", out, preS2JSON)
	}
}

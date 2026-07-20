package runattempt

import (
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

// --- C1: abandoned terminal status ---

func TestCompleteAcceptsAbandoned(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	ra, err := s.Mint("work_abc", "compound", "fake", now)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := s.Complete(ra.ID, StatusAbandoned, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Complete(abandoned): %v", err)
	}
	if completed.Status != StatusAbandoned {
		t.Fatalf("status = %q, want abandoned", completed.Status)
	}
	if !completed.CompletedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("completed_at = %v, want %v", completed.CompletedAt, now.Add(time.Minute))
	}
	// Re-complete is rejected.
	if _, err := s.Complete(ra.ID, StatusFailed, now.Add(2*time.Minute)); err == nil {
		t.Fatalf("re-Complete of abandoned RunAttempt must fail")
	}
	// Disk shows the terminal status.
	got, err := Open(home).Get(ra.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusAbandoned {
		t.Fatalf("on-disk status = %q, want abandoned", got.Status)
	}
}

func TestSettersRefuseOnAbandoned(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	ra, err := s.Mint("work_abc", "compound", "fake", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Complete(ra.ID, StatusAbandoned, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	setters := []struct {
		name string
		fn   func() error
	}{
		{"context_pack", func() error { _, e := s.SetContextPack(ra.ID, "x"); return e }},
		{"workspace", func() error { _, e := s.SetWorkspace(ra.ID, "/x"); return e }},
		{"command", func() error { _, e := s.SetCommand(ra.ID, "x"); return e }},
		{"heartbeat", func() error { _, e := s.Heartbeat(ra.ID, now.Add(2*time.Minute)); return e }},
		{"transcript", func() error { _, e := s.SetTranscript(ra.ID, "x"); return e }},
		{"diff", func() error { _, e := s.SetDiff(ra.ID, "x"); return e }},
		{"test", func() error { _, e := s.SetTest(ra.ID, "x"); return e }},
		{"proof_refs", func() error { _, e := s.SetProofRefs(ra.ID, []string{"x"}); return e }},
		{"attach_action_intent", func() error { return s.AttachActionIntent(ra.ID, "act_12345678") }},
	}
	for _, tc := range setters {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.fn(); err == nil {
				t.Fatalf("setter on abandoned RunAttempt must fail")
			}
		})
	}
}

func TestIsTerminal(t *testing.T) {
	for _, tc := range []struct {
		status string
		want   bool
	}{
		{StatusPending, false},
		{StatusRunning, false},
		{StatusSucceeded, true},
		{StatusFailed, true},
		{StatusAbandoned, true},
		{"garbage", false},
		{"", false},
	} {
		if got := IsTerminal(tc.status); got != tc.want {
			t.Errorf("IsTerminal(%q) = %v, want %v", tc.status, got, tc.want)
		}
	}
}

// --- C2: deterministic mint ---

func TestMintDeterministicSameSeedSameID(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	a := MintDeterministic("codex-job-abc123", now)
	b := MintDeterministic("codex-job-abc123", now)
	if a != b {
		t.Fatalf("same seed produced different ids: %q vs %q", a, b)
	}
	if !Valid(a) {
		t.Fatalf("deterministic id %q did not pass Valid()", a)
	}
}

func TestMintDeterministicDifferentSeedDifferentID(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	a := MintDeterministic("codex-job-abc123", now)
	b := MintDeterministic("codex-job-def456", now)
	if a == b {
		t.Fatalf("different seeds produced same id: %q", a)
	}
}

func TestMintWithIDIdempotent(t *testing.T) {
	home := t.TempDir()
	s := Open(home)
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	id := MintDeterministic("job-xyz", now)
	first, err := s.MintWithID(id, "work_abc", "codex", "codex", now)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != id {
		t.Fatalf("first mint id = %q, want %q", first.ID, id)
	}
	// Second call with same id returns the existing record.
	second, err := s.MintWithID(id, "work_abc", "codex", "codex", now)
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || second.WorkID != first.WorkID || second.Status != first.Status {
		t.Fatalf("idempotent mint returned different record: %+v vs %+v", second, first)
	}
	// Only one file on disk.
	entries, err := os.ReadDir(filepath.Join(home, "run_attempts"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("run_attempts has %d files, want 1", count)
	}
}

func TestMintWithIDRejectsInvalidID(t *testing.T) {
	s := Open(t.TempDir())
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	if _, err := s.MintWithID("bad-id", "work_abc", "codex", "codex", now); err == nil {
		t.Fatal("MintWithID with invalid id must fail")
	}
}

package mem

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
)

// TestCaptureToolOutputRoundTripObservedNeverLedger proves the C3 contract at the mem seam:
// a large tool body is content-addressed into a rebuildable blob (never the ledger) and minted
// as exactly ONE observed-class candidate carrying a compact pointer, retrievable byte-exact,
// idempotent, and never a Claim (so never ambient / never an index_hit).
func TestCaptureToolOutputRoundTripObservedNeverLedger(t *testing.T) {
	home := t.TempDir()
	s, err := Open(home)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	now := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)

	body := strings.Repeat("ABCDEFGHIJ", 30000) // ~300 KB, far above the context budget
	sum := sha256.Sum256([]byte(body))
	wantCID := "sha256:" + hex.EncodeToString(sum[:])

	before, err := s.LedgerDigest()
	if err != nil {
		t.Fatalf("ledger digest before: %v", err)
	}

	cid, pointer, err := s.CaptureToolOutput("Bash", "work_1177cb550af3", body, now)
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	if cid != wantCID {
		t.Fatalf("cid = %q, want %q", cid, wantCID)
	}

	// The pointer is the ONLY thing that flows into context: bounded, and body-free.
	if len(pointer) > 256 || strings.Contains(pointer, body[:64]) {
		t.Fatalf("pointer must be bounded and body-free, got len=%d %q", len(pointer), pointer)
	}
	if !strings.Contains(pointer, cid) {
		t.Fatalf("pointer must carry the cid, got %q", pointer)
	}

	// Blob under observed/, NEVER under ledger/.
	blob := filepath.Join(home, "observed", "tool-output", cid)
	if _, err := os.Stat(blob); err != nil {
		t.Fatalf("blob not written at %s: %v", blob, err)
	}
	if strings.HasPrefix(blob, filepath.Join(home, "ledger")+string(filepath.Separator)) {
		t.Fatalf("blob path must not live under ledger/: %s", blob)
	}

	// Round-trip the exact bytes.
	got, err := s.ReadToolOutput(cid)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != body {
		t.Fatalf("round-trip mismatch: got %d bytes, want %d", len(got), len(body))
	}

	// Ledger byte-identical: capture added ZERO ledger bytes.
	after, err := s.LedgerDigest()
	if err != nil {
		t.Fatalf("ledger digest after: %v", err)
	}
	if before != after {
		t.Fatalf("ledger digest changed: before=%s after=%s", before, after)
	}

	// Minted candidate is observed-class, proposed, no promotion basis.
	id := idFor("cand_", "tool-output", cid)
	var c Candidate
	if err := candidates.ReadCandidate(home, candidates.Memory, id, &c); err != nil {
		t.Fatalf("read candidate: %v", err)
	}
	if c.Derivation != DerivationObserved {
		t.Errorf("derivation = %q, want %q", c.Derivation, DerivationObserved)
	}
	if c.State != "proposed" {
		t.Errorf("state = %q, want proposed", c.State)
	}
	if c.PromotionBasis != "" {
		t.Errorf("promotion_basis = %q, want empty (never auto-promotes)", c.PromotionBasis)
	}
	if c.ProvenanceLane != "Bash" {
		t.Errorf("provenance_lane = %q, want Bash", c.ProvenanceLane)
	}
	if c.SourceTraceRef != "tool-output:"+cid {
		t.Errorf("source_trace_ref = %q, want tool-output:%s", c.SourceTraceRef, cid)
	}
	if c.SnippetHash == "" {
		t.Error("snippet_hash must be set (bounded head snippet hash)")
	}
	raw, _ := os.ReadFile(filepath.Join(home, "inbox", "memory-candidates", id+".json"))
	if strings.Contains(string(raw), body[:200]) {
		t.Error("candidate record leaked the tool-output body")
	}

	// Never ambient: the body is NOT a Claim, so it can never enter recall or resolve as index_hit.
	cur, err := s.AllCurrent()
	if err != nil {
		t.Fatalf("all current: %v", err)
	}
	if len(cur) != 0 {
		t.Fatalf("capture must not create claims; got %d current claims", len(cur))
	}
	ledgerClaims, err := s.LedgerClaims()
	if err != nil {
		t.Fatalf("ledger claims: %v", err)
	}
	if len(ledgerClaims) != 0 {
		t.Fatalf("capture must not append ledger claims; got %d", len(ledgerClaims))
	}

	// Idempotent: an in-flight adjudication verdict is never reset by re-surfacing the same body.
	c.State = "parked"
	if err := candidates.WriteCandidate(home, candidates.Memory, id, &c); err != nil {
		t.Fatalf("rewrite candidate: %v", err)
	}
	cid2, _, err := s.CaptureToolOutput("Bash", "work_1177cb550af3", body, now)
	if err != nil {
		t.Fatalf("recapture: %v", err)
	}
	if cid2 != cid {
		t.Fatalf("idempotent cid mismatch: %q vs %q", cid2, cid)
	}
	var c2 Candidate
	if err := candidates.ReadCandidate(home, candidates.Memory, id, &c2); err != nil {
		t.Fatalf("read candidate after recapture: %v", err)
	}
	if c2.State != "parked" {
		t.Errorf("recapture reset adjudication verdict: state = %q, want parked (skip-if-exists)", c2.State)
	}
	entries, _ := os.ReadDir(filepath.Join(home, "inbox", "memory-candidates"))
	n := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("want exactly one memory candidate, got %d", n)
	}
}

// TestReadToolOutputMissingIsExplicitError proves a missing or malformed cid is an EXPLICIT
// error, never a silent nil, and that a path-shaped cid can never escape the blob dir.
func TestReadToolOutputMissingIsExplicitError(t *testing.T) {
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	missing := "sha256:" + strings.Repeat("0", 64)
	if _, err := s.ReadToolOutput(missing); err == nil {
		t.Fatal("missing cid must be an explicit error, never a silent nil")
	}
	if _, err := s.ReadToolOutput("../../etc/passwd"); err == nil {
		t.Fatal("malformed/path-shaped cid must be rejected before any read")
	}
}

package mem

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compound/internal/candidates"
)

// toolOutputDir is the rebuildable, content-addressed home for externalized tool-output bodies:
// COMPOUND_HOME/observed/tool-output. It is a REBUILDABLE derivative directory (like indexes /
// projections), deliberately OUTSIDE COMPOUND_HOME/ledger — externalizing a large tool body must
// never add a ledger byte, which is what keeps EngineDoctor's ledger-immutability invariant intact
// by construction.
//
// FOLLOW-UP (not built here, by design): this directory has no size cap or GC. A retention/GC pass
// (evict blobs whose candidate was rejected/expired, or bound total bytes) is a documented
// follow-up; capture is deliberately append-only-idempotent for now.
const toolOutputDir = "observed/tool-output"

// toolOutputSnippetHeadBytes bounds the head snippet whose hash grounds the minted candidate —
// evidence of the body's head, never the raw body.
const toolOutputSnippetHeadBytes = 512

// ToolOutputCID is the content id for a tool-output body: "sha256:"+hex(sha256(body)). Same body
// always yields the same cid (the capture is idempotent on it).
func ToolOutputCID(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// CaptureToolOutput externalizes a LARGE tool-output body (captured at the PostToolUse seam) into a
// content-addressed, rebuildable blob and mints exactly ONE observed-class memory candidate carrying
// only a compact pointer. It writes ONLY through two rebuildable doors — the blob dir and the
// candidate inbox — and NEVER a ledger append, so LedgerDigest stays byte-identical (EngineDoctor
// green by construction). The body never becomes a Claim, so it can never enter ambient recall or
// resolve as an index_hit; it is retrievable only via ReadToolOutput / `compound memory show-output`.
//
// It is idempotent: cid is content-addressed on the body (same body → same cid → same blob path), and
// the candidate id is content-addressed on the cid, so re-capturing an identical body rewrites the
// identical blob and SKIPS the candidate if one already exists — never resetting an in-flight
// adjudication verdict (the same skip-if-exists contract as captureObservedFact).
//
// The returned pointer is the ONLY thing that flows back into the context window; it is bounded (cid +
// tool + byte count) and never carries the body.
func (s *Store) CaptureToolOutput(tool, workID, body string, now time.Time) (cid, pointer string, err error) {
	raw := []byte(body)
	cid = ToolOutputCID(raw)

	dir := filepath.Join(s.home, toolOutputDir)
	if err = os.MkdirAll(dir, 0o755); err != nil {
		return "", "", err
	}
	if err = os.WriteFile(filepath.Join(dir, cid), raw, 0o644); err != nil {
		return "", "", err
	}
	pointer = fmt.Sprintf("tool-output:%s tool=%s bytes=%d", cid, tool, len(raw))

	id := idFor("cand_", "tool-output", cid)
	var existing Candidate
	if rerr := candidates.ReadCandidate(s.home, candidates.Memory, id, &existing); rerr == nil {
		return cid, pointer, nil // skip-if-exists: never reset an adjudication verdict
	} else if !os.IsNotExist(rerr) {
		return "", "", rerr
	}

	head := raw
	if len(head) > toolOutputSnippetHeadBytes {
		head = head[:toolOutputSnippetHeadBytes]
	}
	c := Candidate{
		ID:             id,
		Entity:         tool,
		Predicate:      "produced_tool_output",
		Object:         cid,
		Derivation:     DerivationObserved,
		ProvenanceLane: tool,
		SourceTraceRef: "tool-output:" + cid,
		PromotionBasis: "", // no basis — an observed tool-output pointer can never auto-promote
		State:          "proposed",
		SnippetHash:    shortHash(string(head)),
		CreatedAt:      rfc3339(now),
	}
	if strings.TrimSpace(workID) != "" {
		c.EvidenceRefs = []string{"work:" + workID}
	}
	if err = candidates.WriteCandidate(s.home, candidates.Memory, id, &c); err != nil {
		return "", "", err
	}
	return cid, pointer, nil
}

// ReadToolOutput returns the exact original bytes for a captured tool-output cid. A missing cid is an
// EXPLICIT error (never a silent nil): the content-addressed body either rebuilds byte-identically or
// the caller is told it is gone. The cid is validated to the strict "sha256:<64 hex>" shape before any
// read, so a path-shaped value can never escape the blob dir.
func (s *Store) ReadToolOutput(cid string) ([]byte, error) {
	if err := validToolOutputCID(cid); err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(s.home, toolOutputDir, cid))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("tool output %s not found (rebuildable blob missing)", cid)
		}
		return nil, err
	}
	return b, nil
}

// validToolOutputCID enforces the exact content-id shape ("sha256:" + 64 lowercase hex chars). It is
// both a correctness guard and a path-traversal guard: the cid keys a filename under the blob dir, so
// a crafted value ("../…", a separator) must never reach os.ReadFile.
func validToolOutputCID(cid string) error {
	const prefix = "sha256:"
	if !strings.HasPrefix(cid, prefix) {
		return fmt.Errorf("tool output cid must be %q-prefixed, got %q", prefix, cid)
	}
	hexPart := cid[len(prefix):]
	if len(hexPart) != 64 {
		return fmt.Errorf("tool output cid must be sha256:<64 hex>, got %q", cid)
	}
	for _, r := range hexPart {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return fmt.Errorf("tool output cid has a non-hex character: %q", cid)
		}
	}
	return nil
}

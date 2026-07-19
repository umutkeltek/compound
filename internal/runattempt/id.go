// Package runattempt is the primitive the WorkEnvelope RunAttemptIDs slot was reserved for.
// A RunAttempt is the concrete record of one harness attempt against one work_id -- distinct
// from the legacy `AdvanceInput.RunID` chain (panel prun_*, observation run_*) which recorded
// what happened in prose alongside ActionIntent ids. RunAttempts are first-class objects:
// they carry their own id (`ratt_<UTC>_<hex>`), store, status, and lifecycle. S1 deliberately
// omits context_pack_ref / workspace / command / heartbeat / transcript_ref / diff_ref /
// test_ref / proof_refs -- those land when a real harness adapter produces them, not before.
// See docs/architecture/runtime.md for the WorkEnvelope schema and the v0.1 mix this slice
// retires (the v0.1 comment on ActionIntentIDs).
package runattempt

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"time"
)

// SchemaVersion is the on-disk schema tag for RunAttempt artifacts.
const SchemaVersion = "compound.runattempt.v0.1"

// Prefix is the only accepted RunAttempt id prefix. Tightened in envelope.validateRunAttemptIDs.
const Prefix = "ratt_"

// idPattern locks the shape so legacy ids (prun_*, run_*, free strings) cannot masquerade as
// RunAttempts -- this is the same trap the v0.1 ActionIntentIDs mix fell into and the one
// envelope-side rule that prevents a repeat.
var idPattern = regexp.MustCompile(`^ratt_\d{8}T\d{6}Z_[0-9a-f]{8}$`)

// Valid reports whether s is a syntactically well-formed RunAttempt id.
func Valid(s string) bool {
	return idPattern.MatchString(s)
}

// Mint returns a fresh RunAttempt id of the form `ratt_<UTC>_<8 hex>` derived from now. The
// hex suffix is sha256(rand.Reader output)[0:4] so concurrent mints never collide and the id
// is reproducible from its prefix alone (the timestamp is the only time input).
func Mint(now time.Time) (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("runattempt mint: read random: %w", err)
	}
	sum := sha256.Sum256(raw[:])
	suffix := hex.EncodeToString(sum[:])[:8]
	return fmt.Sprintf("ratt_%s_%s", now.UTC().Format("20060102T150405Z"), suffix), nil
}

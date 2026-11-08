package decisions

import (
	"path/filepath"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/mem"
)

// A status word that names no known state must never read as accepted: an
// accepted decision is in force, so the unknown word would turn a revoked or
// paused decision into an active one.
func TestCanonicalStatusUnknownWordIsNotAccepted(t *testing.T) {
	cases := map[string]string{
		"":                       StatusAccepted, // a page with no status line keeps the old default
		"accepted":               StatusAccepted,
		"Accepted (2026-06-24)":  StatusAccepted,
		"active":                 StatusAccepted,
		"ratified":               StatusAccepted,
		"approved":               StatusAccepted,
		"proposed":               StatusProposed,
		"draft":                  StatusProposed,
		"superseded":             StatusSuperseded,
		"deprecated":             StatusDeprecated,
		"retired":                StatusDeprecated,
		"rejected":               StatusRejected,
		"revoked":                StatusDeprecated,
		"withdrawn":              StatusDeprecated,
		"rescinded":              StatusDeprecated,
		"inactive":               StatusDeprecated,
		"not accepted":           StatusProposed,
		"paused":                 StatusProposed,
		"on hold":                StatusProposed,
		"candidate":              StatusProposed,
		"mystery":                StatusProposed,
		"proposed | accepted":    StatusProposed,
		"  Revoked by operator ": StatusDeprecated,
	}
	for in, want := range cases {
		if got := CanonicalStatus(in); got != want {
			t.Errorf("CanonicalStatus(%q) = %q, want %q", in, got, want)
		}
	}
}

// End to end: a policy decision whose source says `revoked` imports parked,
// not as an accepted, promoted decision.
func TestImportPolicyDecisionRevokedStatusIsNotActive(t *testing.T) {
	home := t.TempDir()
	store, err := mem.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "decisions", "DEC-0099.md")
	writeFile(t, path, `---
id: DEC-0099
title: Route every proof through the remote verifier
status: revoked
domains: [architecture]
falsifier: A landing closes on a laptop battery.
review_trigger: Re-check after the next verifier outage.
owner: compound
scope: public-core
---

# Route every proof through the remote verifier

Revoked by the operator.
`)
	report, err := ImportPolicyDecision(store, path, time.Date(2026, 11, 8, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if report.Lifecycle.Status == StatusAccepted {
		t.Fatalf("revoked decision imported as accepted: %+v", report.Lifecycle)
	}
	if report.Verdict == candidates.Promote || report.ClaimID != "" {
		t.Fatalf("revoked decision was promoted: %+v", report)
	}
	claims, err := store.AllCurrent()
	if err != nil {
		t.Fatal(err)
	}
	if active := AllActiveDecisions(claims); len(active) != 0 {
		t.Fatalf("revoked decision is active: %+v", active)
	}
}

package decisions

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestCanonicalStatusRequiresExplicitAffirmative(t *testing.T) {
	for _, status := range []string{
		"not approved", "not active", "acceptedd", "unapproved", "not accepted",
		"accepted pending review", "approved if tests pass", "conditionally adopted",
		"not ratified", "not adopted", "not in force", "not in-force", "not in_force",
		"accepted (not yet effective)", "approved (if confirmed)", "accepted (pending review)",
		"accepted (2026-06-24) if confirmed", "accepted (2026-02-30)",
		"accepted (2026-6-24)", "accepted ()", "accepted (2026-06-24) (2026-06-25)",
		"preapproved", "reactive", "approval", "acceptance", "ratification", "adoption",
	} {
		t.Run(status, func(t *testing.T) {
			if got := CanonicalStatus(status); got != StatusProposed {
				t.Fatalf("ambiguous status %q = %q, want proposed", status, got)
			}
		})
	}
	for _, status := range []string{"", "accepted", " ACCEPTED ", "approved", "ratified", "adopted", "active", "in force", "in-force", "in_force", "Accepted (2026-06-24)"} {
		if got := CanonicalStatus(status); got != StatusAccepted {
			t.Errorf("legacy %q = %q, want accepted", status, got)
		}
	}
	if got := CanonicalStatus("inactive"); got != StatusDeprecated {
		t.Errorf("inactive = %q, want deprecated", got)
	}
}

// Exercise the import and served projection together: an ambiguous successor
// must neither become active nor hide the valid predecessor it names.
func TestOrdinaryStatusImportCannotActivateOrSupersede(t *testing.T) {
	statuses := []struct {
		raw      string
		accepted bool
	}{
		{"not approved", false}, {"not active", false}, {"acceptedd", false},
		{"not accepted", false}, {"unapproved", false}, {"inactive", false},
		{"accepted pending review", false}, {"accepted (not yet effective)", false},
		{"approved (if confirmed)", false}, {"accepted (2026-02-30)", false},
		{"", true}, {"accepted", true}, {"active", true}, {"approved", true},
		{"ratified", true}, {"adopted", true}, {"in force", true},
		{"in-force", true}, {"in_force", true}, {"Accepted (2026-06-24)", true},
	}
	for _, kind := range []string{KindPolicy, KindDoctrine} {
		for _, tc := range statuses {
			t.Run(kind+"/"+tc.raw, func(t *testing.T) {
				store, err := mem.Open(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				dir := t.TempDir()
				now := time.Now().UTC()
				source := func(id, status, supersedes string) (string, string) {
					body := fmt.Sprintf("---\nid: %s\ntitle: %s\nkind: %s\nstatus: %q\nfalsifier: A contrary observation.\nreview_trigger: A new observation.\nsupersedes: [%s]\n---\n\n# %s\n", id, id, kind, status, supersedes, id)
					path := filepath.Join(dir, id+".md")
					writeFile(t, path, body)
					return path, body
				}
				oldPath, oldBody := source("valid-predecessor", "accepted", "")
				oldReport, err := ImportPolicyDecision(store, oldPath, now)
				if err != nil {
					t.Fatal(err)
				}
				if oldReport.Verdict != candidates.Promote {
					t.Fatalf("predecessor not promoted: %+v", oldReport)
				}
				newPath, newBody := source("proposed-successor", tc.raw, "valid-predecessor")
				report, err := ImportPolicyDecision(store, newPath, now.Add(time.Second))
				if err != nil {
					t.Fatal(err)
				}
				if tc.accepted {
					if report.Verdict != candidates.Promote || report.ClaimID == "" {
						t.Errorf("legacy successor not promoted: %+v", report)
					}
				} else if report.Verdict != candidates.Park || report.ClaimID != "" || report.Lifecycle.Status == StatusAccepted {
					t.Errorf("ambiguous successor promoted: %+v", report)
				}
				claims, err := store.AllCurrent()
				if err != nil {
					t.Fatal(err)
				}
				wantID := "valid-predecessor"
				if tc.accepted {
					wantID = "proposed-successor"
				}
				active := ActiveByRef(claims)
				if len(active) != 1 || active[RefPrefix+wantID].ID == "" {
					t.Errorf("active projection = %+v, want only %s", active, wantID)
				}
				if _, _, ok := ResolveActive(claims, wantID); !ok {
					t.Errorf("served lookup cannot resolve %s", wantID)
				}
				for path, want := range map[string]string{oldPath: oldBody, newPath: newBody} {
					got, err := os.ReadFile(path)
					if err != nil || string(got) != want {
						t.Errorf("source changed: %s, err=%v", path, err)
					}
				}
				evidence, err := store.Evidence()
				if err != nil || len(evidence) != 2 {
					t.Errorf("import evidence lost: count=%d, err=%v", len(evidence), err)
				}
			})
		}
	}
}

// Ordinary aliases and the legacy missing-status default must never widen the
// separate strict import gate for operational decisions.
func TestOrdinaryStatusCompatibilityDoesNotWidenOperationalAuthority(t *testing.T) {
	for _, kind := range []string{KindGrant, KindFreeze, KindRuling} {
		for _, status := range []string{"", "approved", "active", "ratified", "adopted", "in force", "in-force", "in_force", "Accepted (2026-06-24)", "not approved", "not active", "acceptedd"} {
			t.Run(kind+"/"+status, func(t *testing.T) {
				store, err := mem.Open(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "operational.md")
				writeFile(t, path, fmt.Sprintf("---\nid: strict-operation\ntitle: Strict operation\nkind: %s\nstatus: %q\nreviewed_by: operator:test\nfalsifier: Contrary evidence.\nreview_trigger: New evidence.\n---\n", kind, status))
				if _, err := ImportPolicyDecision(store, path, time.Now()); err == nil || !strings.Contains(err.Error(), "explicit canonical status") {
					t.Fatalf("strict gate changed: %v", err)
				}
				claims, err := store.AllCurrentOperationalDecisions()
				if err != nil || len(claims) != 0 {
					t.Fatalf("unexpected authority: %+v, %v", claims, err)
				}
			})
		}
	}
}

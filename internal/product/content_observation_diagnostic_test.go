package product

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"compound/internal/candidates"
	"compound/internal/home"
)

func TestContentObservationRejectedResponseDiagnosticCustody(t *testing.T) {
	for _, tc := range []struct{ mode, wantError string }{
		{"wrong-schedule", "observation schedule identity changed"},
		{"wrong-request", "not bound to the original request"},
		{"malformed", "invalid typed transport observation"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			h, before, now, dir := recoveryScheduledFixture(t)
			original, err := candidates.ReadRecord(h, candidates.ContentDraft, before.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "mode"), []byte(tc.mode), 0600); err != nil {
				t.Fatal(err)
			}
			out, err := ExecuteContentPost(h, before.ID, "fixture", "", time.Time{}, false, true, now)
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("conflicting schedule observation: %v", err)
			}
			after, err := candidates.ReadRecord(h, candidates.ContentDraft, before.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(original.Body, after.Body) {
				t.Fatal("diagnostic custody changed the owned draft")
			}
			body, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			var result map[string]any
			if err := json.Unmarshal(body, &result); err != nil {
				t.Fatal(err)
			}
			ref, _ := result["diagnostic_observation_ref"].(string)
			if ref == "" {
				t.Fatal("rejected complete observation has no diagnostic evidence reference")
			}
			store, err := home.NewArtifactStore(h)
			if err != nil {
				t.Fatal(err)
			}
			got, err := store.Get(ref)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(dir, "response"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatal("diagnostic artifact changed the raw rejected response bytes")
			}
			if out.PostImageRef != before.Outbound.ResponseRef || out.Draft == nil || out.Draft.Outbound.ResponseRef != before.Outbound.ResponseRef || out.Draft.Outbound.ReceiptID != before.Outbound.ReceiptID {
				t.Fatal("diagnostic evidence replaced accepted scheduling evidence")
			}
			if _, _, err := BuildContentPostTransportRequest(h, before.ID, "fixture", now.Add(2*time.Minute), now); err == nil || !strings.Contains(err.Error(), "already owns outbound attempt") {
				t.Fatalf("rejected observation lost no-resend ownership: %v", err)
			}
			calls, err := os.ReadFile(filepath.Join(dir, "calls"))
			if err != nil || string(calls) != "schedule\nobserve\n" {
				t.Fatalf("transport calls %q: %v", calls, err)
			}

		})
	}
}

func TestContentObservationPureUnknownPreservesScheduledAttempt(t *testing.T) {
	h, before, now, dir := recoveryScheduledFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "mode"), []byte("pure-unknown"), 0600); err != nil {
		t.Fatal(err)
	}
	out, err := ExecuteContentPost(h, before.ID, "fixture", "", time.Time{}, false, true, now)
	if err != nil {
		t.Fatalf("fully bound pure UNKNOWN should remain nonterminal: %v", err)
	}
	if out.Mode != "reconciliation_required" || out.Draft == nil || out.DiagnosticObservationRef == "" {
		t.Fatalf("pure UNKNOWN result: %+v", out)
	}
	wantBody, err := json.Marshal(before)
	if err != nil {
		t.Fatal(err)
	}
	var want ContentDraft
	if err := json.Unmarshal(wantBody, &want); err != nil {
		t.Fatal(err)
	}
	want.Outbound.Status = "reconciliation_required"
	want.Outbound.ResponseRef = out.DiagnosticObservationRef
	if !reflect.DeepEqual(*out.Draft, want) {
		t.Fatal("pure UNKNOWN changed original identity, schedule, receipt, or draft state")
	}
	after, err := candidates.ReadRecord(h, candidates.ContentDraft, before.ID)
	if err != nil {
		t.Fatal(err)
	}
	var stored ContentDraft
	if err := json.Unmarshal(after.Body, &stored); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(stored, want) {
		t.Fatal("owned draft differs from nonterminal UNKNOWN result")
	}
	store, err := home.NewArtifactStore(h)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(out.DiagnosticObservationRef)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "response"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("pure UNKNOWN response was rewritten or filled with invented IDs")
	}
	if _, _, err := BuildContentPostTransportRequest(h, before.ID, "fixture", now.Add(2*time.Minute), now); err == nil || !strings.Contains(err.Error(), "already owns outbound attempt") {
		t.Fatalf("pure UNKNOWN lost no-resend ownership: %v", err)
	}
	calls, err := os.ReadFile(filepath.Join(dir, "calls"))
	if err != nil || string(calls) != "schedule\nobserve\n" {
		t.Fatalf("transport calls %q: %v", calls, err)
	}
}

func TestContentObservationPureUnknownRefusesIdentityOrBindingClaims(t *testing.T) {
	cases := []struct {
		name   string
		fields map[string]any
	}{
		{"schedule-id", map[string]any{"schedule_id": "other-schedule"}},
		{"scheduled-at", map[string]any{"scheduled_at": "present"}},
		{"remote-id", map[string]any{"remote_id": "other-publication"}},
		{"published-at", map[string]any{"published_at": "present"}},
		{"url", map[string]any{"url": "https://social.example/unverified"}},
		{"space-schedule-id", map[string]any{"schedule_id": " "}},
		{"space-scheduled-at", map[string]any{"scheduled_at": " "}},
		{"space-remote-id", map[string]any{"remote_id": " "}},
		{"space-published-at", map[string]any{"published_at": " "}},
		{"space-url", map[string]any{"url": " "}},
		{"schedule-object", map[string]any{"object_type": "schedule"}},
		{"processing", map[string]any{"status": "processing"}},
		{"terminal-error", map[string]any{"status": "error"}},
		{"schema", map[string]any{"schema_version": "other"}},
		{"operation", map[string]any{"operation": "schedule"}},
		{"attempt", map[string]any{"attempt_id": "other-attempt"}},
		{"idempotency", map[string]any{"idempotency_key": "other-attempt"}},
		{"request", map[string]any{"request_digest": "sha256:other"}},
		{"package", map[string]any{"package_digest": "sha256:other"}},
		{"account", map[string]any{"account_id": "other-account"}},
		{"integration", map[string]any{"integration_id": "other-integration"}},
		{"transport", map[string]any{"transport": "other"}},
		{"binding", map[string]any{"binding": map[string]any{"executable_digest": "sha256:other"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, before, now, dir := recoveryScheduledFixture(t)
			original, err := candidates.ReadRecord(h, candidates.ContentDraft, before.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "mode"), []byte("pure-unknown"), 0600); err != nil {
				t.Fatal(err)
			}
			fields, err := json.Marshal(tc.fields)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "unknown-fields"), fields, 0600); err != nil {
				t.Fatal(err)
			}
			out, err := ExecuteContentPost(h, before.ID, "fixture", "", time.Time{}, false, true, now)
			if err == nil {
				t.Fatal("identity-bearing or unbound UNKNOWN bypassed existing validation")
			}
			after, err := candidates.ReadRecord(h, candidates.ContentDraft, before.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(original.Body, after.Body) {
				t.Fatal("refused UNKNOWN changed the original draft")
			}
			if out.DiagnosticObservationRef == "" || out.PostImageRef != before.Outbound.ResponseRef {
				t.Fatal("refusal lost diagnostic evidence or replaced accepted evidence")
			}
			store, err := home.NewArtifactStore(h)
			if err != nil {
				t.Fatal(err)
			}
			raw, err := store.Get(out.DiagnosticObservationRef)
			if err != nil {
				t.Fatal(err)
			}
			want, err := os.ReadFile(filepath.Join(dir, "response"))
			if err != nil || !bytes.Equal(raw, want) {
				t.Fatalf("refused UNKNOWN diagnostic bytes: %v", err)
			}
			if _, _, err := BuildContentPostTransportRequest(h, before.ID, "fixture", now.Add(2*time.Minute), now); err == nil {
				t.Fatal("refused UNKNOWN enabled another outbound attempt")
			}
			calls, err := os.ReadFile(filepath.Join(dir, "calls"))
			if err != nil || string(calls) != "schedule\nobserve\n" {
				t.Fatalf("transport calls %q: %v", calls, err)
			}
		})
	}
}

func TestContentObservationPureUnknownCannotDemoteTerminalAttempt(t *testing.T) {
	for _, terminal := range []string{"posted", "failed"} {
		t.Run(terminal, func(t *testing.T) {
			h, before, now, dir := recoveryScheduledFixture(t)
			if terminal == "failed" {
				if err := os.WriteFile(filepath.Join(dir, "mode"), []byte("pure-unknown"), 0600); err != nil {
					t.Fatal(err)
				}
				fields := []byte("{\"status\":\"error\",\"schedule_id\":\"object-synth\"}")
				if err := os.WriteFile(filepath.Join(dir, "unknown-fields"), fields, 0600); err != nil {
					t.Fatal(err)
				}
			}
			accepted, err := ExecuteContentPost(h, before.ID, "fixture", "", time.Time{}, false, true, now)
			if err != nil || accepted.Mode != terminal {
				t.Fatalf("prepare terminal %s: %+v %v", terminal, accepted, err)
			}
			original, err := candidates.ReadRecord(h, candidates.ContentDraft, before.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "mode"), []byte("pure-unknown"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(dir, "unknown-fields")); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			out, err := ExecuteContentPost(h, before.ID, "fixture", "", time.Time{}, false, true, now)
			if err != nil || out.Mode != terminal || out.DiagnosticObservationRef != "" {
				t.Fatalf("terminal replay: %+v %v", out, err)
			}
			after, err := candidates.ReadRecord(h, candidates.ContentDraft, before.ID)
			if err != nil || !bytes.Equal(original.Body, after.Body) {
				t.Fatalf("pure UNKNOWN demoted terminal attempt: %v", err)
			}
			calls, err := os.ReadFile(filepath.Join(dir, "calls"))
			if err != nil || string(calls) != "schedule\nobserve\n" {
				t.Fatalf("terminal replay made another transport call %q: %v", calls, err)
			}
		})
	}
}

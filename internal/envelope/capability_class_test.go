package envelope

import (
	"encoding/json"
	"strings"
	"testing"
)

// legacyReceiptCanonicalJSON is a pre-capability_class Receipt in its exact canonical marshal form
// (frozen from the base codec). Decoding and re-encoding it must reproduce these bytes exactly: the
// additive field is omitempty, so a receipt that never declared a class stays byte-stable.
const legacyReceiptCanonicalJSON = `{"schema_version":"compound.receipt.v0.1","id":"rec_legacy_shape","created_at":"2026-06-28T00:00:00Z","producer":{"kind":"cli","name":"compound","version":""},"ids":{},"scope":{},"authority":{"source_class":"runtime","status":"current"},"privacy":"private","risk":"tier1","evidence_refs":[],"uncertainty":{"level":"known"},"outcome":"completed","move_type":"outward_delta","state_delta":{"changed":true,"summary":"","files_changed":null,"artifacts":null,"verification":null},"cost":{"model_calls":null,"tokens":null,"usd_actual":null,"usd_estimated":null,"human_minutes":null},"skills":{"used":null,"missed":null,"candidate":null},"memory":{"candidates":null,"promoted":null,"parked":null},"next":{"suggested_work":null},"proof_refs":["check:proof"]}`

// receiptWithCapabilityClass splices a capability_class key into the legacy wire shape, so the
// test speaks the persisted JSON a fold reads rather than a Go field a stale codec cannot see.
func receiptWithCapabilityClass(class string) string {
	b, _ := json.Marshal(class)
	return strings.Replace(legacyReceiptCanonicalJSON, `"proof_refs":`, `"capability_class":`+string(b)+`,"proof_refs":`, 1)
}

// A receipt declaring capability_class keeps it across decode → encode → RoundTrip, and validates.
// A legacy receipt without the key re-encodes byte-identically and never grows the key. Reds
// against a Receipt codec that drops the declared class (the fold would then bucket every attempt
// as "unknown" no matter what the close declared).
func TestReceiptCapabilityClassRoundTripsAndLegacyStaysByteStable(t *testing.T) {
	var rec Receipt
	if err := json.Unmarshal([]byte(receiptWithCapabilityClass("build-implement")), &rec); err != nil {
		t.Fatalf("decode receipt with capability_class: %v", err)
	}
	if err := rec.Validate(); err != nil {
		t.Fatalf("receipt with a well-formed capability_class must validate: %v", err)
	}
	out, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"capability_class":"build-implement"`) {
		t.Fatalf("capability_class lost on re-encode:\n%s", out)
	}
	if err := RoundTrip(&rec); err != nil {
		t.Fatalf("receipt-with-capability_class round-trip: %v", err)
	}

	var legacy Receipt
	if err := json.Unmarshal([]byte(legacyReceiptCanonicalJSON), &legacy); err != nil {
		t.Fatalf("decode legacy receipt: %v", err)
	}
	if err := legacy.Validate(); err != nil {
		t.Fatalf("legacy receipt without capability_class must remain valid: %v", err)
	}
	legacyOut, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if string(legacyOut) != legacyReceiptCanonicalJSON {
		t.Fatalf("legacy receipt is not byte-stable after round-trip:\n got %s\nwant %s", legacyOut, legacyReceiptCanonicalJSON)
	}
}

// Envelope validation checks capability_class SHAPE only (a lowercase [a-z0-9-] slug of at most 64
// chars); which classes exist is the close door's call. Reds against a Receipt that accepts prose,
// paths, or mixed case as a routing class.
func TestReceiptCapabilityClassShapeIsValidated(t *testing.T) {
	valid := []string{"build-implement", "judge-critique", "certify", "a", "x1", strings.Repeat("a", 64)}
	for _, class := range valid {
		var rec Receipt
		if err := json.Unmarshal([]byte(receiptWithCapabilityClass(class)), &rec); err != nil {
			t.Fatalf("decode %q: %v", class, err)
		}
		if err := rec.Validate(); err != nil {
			t.Errorf("capability_class %q should validate: %v", class, err)
		}
	}
	invalid := []string{"Build-Implement", "build implement", "build_implement", "build.implement", "lane/x", " build-implement", strings.Repeat("a", 65)}
	for _, class := range invalid {
		var rec Receipt
		if err := json.Unmarshal([]byte(receiptWithCapabilityClass(class)), &rec); err != nil {
			t.Fatalf("decode %q: %v", class, err)
		}
		err := rec.Validate()
		if err == nil || !strings.Contains(err.Error(), "capability_class") {
			t.Errorf("capability_class %q should be rejected naming the field, got %v", class, err)
		}
	}
}

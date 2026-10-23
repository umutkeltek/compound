package work

import (
	"strings"
	"testing"

	"compound/internal/eventstore"
)

func capabilityClassWorkToVerifying(t *testing.T, s *Store, predicate string) string {
	t.Helper()
	w, err := s.Create(CreateInput{Owner: "lane-beliefs", SuccessPredicate: predicate}, fixedNow)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.Claim(w.ID, "claude_code", fixedNow); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "running", Actor: "claude_code"}, fixedNow); err != nil {
		t.Fatalf("advance running: %v", err)
	}
	if _, err := s.Advance(w.ID, AdvanceInput{To: "verifying", Actor: "claude_code"}, fixedNow); err != nil {
		t.Fatalf("advance verifying: %v", err)
	}
	return w.ID
}

func persistedReceiptBytes(t *testing.T, s *Store, receiptID string) string {
	t.Helper()
	es, err := eventstore.Open(s.home)
	if err != nil {
		t.Fatal(err)
	}
	record, err := es.Read(receiptID)
	if err != nil {
		t.Fatalf("read persisted receipt %s: %v", receiptID, err)
	}
	return string(record.Bytes)
}

// A close that declares CapabilityClass writes it on the emitted AND persisted Receipt; a close
// that declares none writes no capability_class key at all (no guessing from predicate text).
// Reds against a close door that drops the declared class before the receipt is sealed.
func TestCloseWritesDeclaredCapabilityClassOnReceipt(t *testing.T) {
	s := newStore(t)
	declared := capabilityClassWorkToVerifying(t, s, "close declares its capability class")
	res, err := s.Close(declared, CloseInput{Outcome: "completed", MoveType: "audit_only", Actor: "claude_code", CapabilityClass: "build-implement"}, fixedNow)
	if err != nil {
		t.Fatalf("close with capability class: %v", err)
	}
	if res.Receipt.CapabilityClass != "build-implement" {
		t.Fatalf("receipt capability_class = %q, want build-implement", res.Receipt.CapabilityClass)
	}
	if raw := persistedReceiptBytes(t, s, res.Receipt.ID); !strings.Contains(raw, `"capability_class":"build-implement"`) {
		t.Fatalf("persisted receipt lost capability_class:\n%s", raw)
	}

	undeclared := capabilityClassWorkToVerifying(t, s, "build and implement the feature")
	res, err = s.Close(undeclared, CloseInput{Outcome: "completed", MoveType: "audit_only", Actor: "claude_code", Summary: "build and implement the feature"}, fixedNow)
	if err != nil {
		t.Fatalf("close without capability class: %v", err)
	}
	if res.Receipt.CapabilityClass != "" {
		t.Fatalf("undeclared close guessed capability_class %q from its text", res.Receipt.CapabilityClass)
	}
	if raw := persistedReceiptBytes(t, s, res.Receipt.ID); strings.Contains(raw, "capability_class") {
		t.Fatalf("undeclared close persisted a capability_class key:\n%s", raw)
	}
}

// A malformed class is refused by receipt validation before any close byte is written: the work
// stays verifying. Reds against a close door that seals prose as a routing class.
func TestCloseRefusesMalformedCapabilityClassBeforePersisting(t *testing.T) {
	s := newStore(t)
	id := capabilityClassWorkToVerifying(t, s, "malformed class is refused")
	_, err := s.Close(id, CloseInput{Outcome: "completed", MoveType: "audit_only", Actor: "claude_code", CapabilityClass: "Build Implement"}, fixedNow)
	if err == nil || !strings.Contains(err.Error(), "capability_class") {
		t.Fatalf("malformed capability class should refuse the close naming the field, got %v", err)
	}
	w, err := s.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	if w.State != "verifying" {
		t.Fatalf("refused close moved work to %q, want verifying", w.State)
	}
}

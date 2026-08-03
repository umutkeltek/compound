package main

import (
	"strings"
	"testing"
	"time"
)

// gated-write-refuses-without-grant: an external_side_effect store export must
// be refused by the default policy (no standing grant), never silently allowed.
func TestStoreGovernedGateRefusesWithoutGrant(t *testing.T) {
	homePath := t.TempDir()
	now := time.Now()
	ai := storeGovernedIntent("work_testgate", "migrate_apply", "postgres://u:****@db.example.invalid/compound", "preview", now)
	evaluation, err := gateStoreGovernedWrite(homePath, ai, now)
	if err == nil {
		t.Fatalf("expected refusal without a standing grant, got allowed (outcome=%s)", evaluation.Decision.Outcome)
	}
	if !strings.Contains(err.Error(), "refused") {
		t.Fatalf("refusal should name itself: %v", err)
	}
}

// The governed receipt+transition pair must validate against the envelope
// schema — a proof that cannot be recorded is a governance hole.
func TestStoreGovernedProofPairValidates(t *testing.T) {
	now := time.Now()
	ai := storeGovernedIntent("work_testgate", "sync_apply", "postgres://u:****@db.example.invalid/compound", "preview", now)
	receipt, transition := storeGovernedReceiptAndTransition(ai, "store sync --confirm: 3 claims, 2 edges → postgres", now)
	if err := receipt.Validate(); err != nil {
		t.Fatalf("receipt must validate: %v", err)
	}
	if err := transition.Validate(); err != nil {
		t.Fatalf("transition must validate: %v", err)
	}
	if receipt.IDs.WorkID != ai.IDs.WorkID || transition.IDs.WorkID != ai.IDs.WorkID {
		t.Fatalf("proof pair must hang off the intent's work_id")
	}
	if len(transition.ActionRefs) != 1 || transition.ActionRefs[0] != ai.ID {
		t.Fatalf("transition must bind the action intent id")
	}
}

package mem

import "testing"

// RetiredDecisionIDs groups only the caller's members: naming one import
// retires the shared canonical id, and a claim that is not a member never
// links a named id to another record.
func TestRetiredDecisionIDsGroupsOnlyMembers(t *testing.T) {
	legacy := Claim{ID: "DEC-0003", ClaimType: "decision", Qualifiers: map[string]string{"kind": "policy"}}
	newer := Claim{ID: "claim_new", ClaimType: "decision", Qualifiers: map[string]string{"decision_id": "DEC-0003"}}
	named := DecisionSupersedesNames([]Claim{stampedDecision("claim_r", "r", "claim_new")}, nil)
	retired := RetiredDecisionIDs([]Claim{legacy, newer}, named)
	if !retired["DEC-0003"] || len(retired) != 1 {
		t.Fatalf("retired = %v, want the shared canonical id DEC-0003", retired)
	}
	if got := RetiredDecisionIDs([]Claim{legacy}, named); len(got) != 0 {
		t.Fatalf("legacy import alone is not named (claim_new is not a member): %v", got)
	}
}

// DecisionSupersedesNames drops empty entries and a bare prefix, strips the
// decision:// prefix, and reads only decision claims the predicate accepts.
func TestDecisionSupersedesNamesNormalizesEntries(t *testing.T) {
	claims := []Claim{
		stampedDecision("claim_r", "r", " a , decision://b,decision://, ,c"),
		{ID: "claim_mem", ClaimType: "memory", Qualifiers: map[string]string{"supersedes": "d"}},
		stampedDecision("claim_skip", "skip", "e"),
	}
	named := DecisionSupersedesNames(claims, func(c Claim) bool { return c.ID != "claim_skip" })
	if len(named) != 3 || !named["a"] || !named["b"] || !named["c"] {
		t.Fatalf("named = %v, want a, b, c", named)
	}
}

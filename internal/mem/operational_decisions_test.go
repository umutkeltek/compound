package mem

import "testing"

func stampedDecision(claimID, decisionID string, supersedes string) Claim {
	q := map[string]string{
		"kind":                                "ruling",
		"lifecycle_status":                    "accepted",
		"reviewed_by":                         "operator:test",
		OperationalDecisionAuthorityQualifier: OperationalDecisionAuthorityValue,
	}
	if decisionID != "" {
		q["decision_id"] = decisionID
	}
	if supersedes != "" {
		q["supersedes"] = supersedes
	}
	return Claim{ID: claimID, ClaimType: "decision", Object: claimID, Qualifiers: q}
}

func activeIDs(claims []Claim) map[string]bool {
	out := map[string]bool{}
	for _, c := range AuthoritativeActiveOperationalDecisions(claims) {
		out[c.ID] = true
	}
	return out
}

// Job L1: an authoritative record that names an operational decision by its
// claim id or by decision:// ref retires it; before, only the decision_id
// matched, so a ruling retired by claim id still authorized ask laws and
// receipted actions.
func TestAuthoritativeOperationalDecisionsRetireByClaimIDOrRef(t *testing.T) {
	for _, name := range []string{"law-a", "claim_a", "decision://law-a"} {
		t.Run(name, func(t *testing.T) {
			claims := []Claim{
				stampedDecision("claim_a", "law-a", ""),
				stampedDecision("claim_b", "law-b", ""),
				stampedDecision("claim_r", "retire-a", name),
			}
			got := activeIDs(claims)
			if got["claim_a"] {
				t.Fatalf("supersedes %q left law-a authoritative: %v", name, got)
			}
			if !got["claim_b"] || !got["claim_r"] {
				t.Fatalf("supersedes %q retired a record it does not name: %v", name, got)
			}
		})
	}
}

// Guard: a generic (unstamped) claim cannot retire an operational decision,
// by any name, and an entry that names nothing retires nothing.
func TestAuthoritativeOperationalDecisionsIgnoreGenericAndEmptyNames(t *testing.T) {
	generic := Claim{ID: "claim_generic", ClaimType: "decision", Qualifiers: map[string]string{
		"kind": "ruling", "lifecycle_status": "accepted", "reviewed_by": "operator:test", "decision_id": "forged", "supersedes": "claim_a,law-a",
	}}
	claims := []Claim{
		stampedDecision("claim_a", "law-a", ""),
		stampedDecision("claim_r", "retire-nothing", "decision://, ,unknown"),
		generic,
	}
	got := activeIDs(claims)
	if !got["claim_a"] || !got["claim_r"] || got["claim_generic"] {
		t.Fatalf("authoritative set = %v, want claim_a and claim_r only", got)
	}
}

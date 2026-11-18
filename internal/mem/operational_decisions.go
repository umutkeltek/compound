package mem

import "strings"

// AuthoritativeActiveOperationalDecisions is the single pure fold for
// operational decisions that may authorize an autonomous action. It admits
// only accepted, reviewed, authority-stamped records and applies explicit
// supersession among them (DecisionSupersedesNames, RetiredDecisionIDs) before
// returning live claims.
func AuthoritativeActiveOperationalDecisions(claims []Claim) []Claim {
	accepted := make([]Claim, 0, len(claims))
	for _, claim := range claims {
		q := claim.Qualifiers
		if claim.ClaimType != "decision" || !HasOperationalDecisionAuthority(q) ||
			strings.TrimSpace(q["lifecycle_status"]) != "accepted" || strings.TrimSpace(q["reviewed_by"]) == "" {
			continue
		}
		accepted = append(accepted, claim)
	}
	retired := RetiredDecisionIDs(accepted, DecisionSupersedesNames(accepted, nil))
	out := make([]Claim, 0, len(accepted))
	for _, claim := range accepted {
		decisionID := strings.TrimSpace(claim.Qualifiers["decision_id"])
		if decisionID == "" || retired[DecisionCanonicalID(claim)] {
			continue
		}
		out = append(out, claim)
	}
	return out
}

// DecisionRefPrefix is the scheme of a canonical decision ref
// (decision://<DecisionCanonicalID>).
const DecisionRefPrefix = "decision://"

// DecisionCanonicalID is the id one decision is served under: the claim's
// decision_id qualifier when it has one, else its claim id. Imports of one
// decision page share it (an older legacy claim whose claim id is DEC-0003 and
// a newer claim whose decision_id is DEC-0003).
func DecisionCanonicalID(c Claim) string {
	if id := strings.TrimSpace(c.Qualifiers["decision_id"]); id != "" {
		return id
	}
	return strings.TrimSpace(c.ID)
}

// DecisionSupersedesNames is the one naming rule for decision retirement: it
// collects what the `supersedes` qualifiers of the decision claims that
// superseder accepts (nil accepts every decision claim) name. An entry may be
// a decision id, a claim id, or a canonical ref with or without the
// decision:// prefix; one qualifier may carry a comma list. An empty entry or
// a bare prefix names nothing.
func DecisionSupersedesNames(claims []Claim, superseder func(Claim) bool) map[string]bool {
	named := map[string]bool{}
	for _, c := range claims {
		if c.ClaimType != "decision" || (superseder != nil && !superseder(c)) {
			continue
		}
		for _, id := range strings.Split(c.Qualifiers["supersedes"], ",") {
			if key := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(id), DecisionRefPrefix)); key != "" {
				named[key] = true
			}
		}
	}
	return named
}

// RetiredDecisionIDs returns the canonical ids (DecisionCanonicalID) that
// named retires among members. A member named by its claim id or decision_id
// retires its canonical id, and with it every member that shares that id, so
// the older and newer imports of one decision leave together. Only members
// join a canonical id: a claim outside the caller's trust filter can never
// link a named id to a record nobody named. A retired record stays retired
// when its retirer is itself retired, because every superseder's list counts.
func RetiredDecisionIDs(members []Claim, named map[string]bool) map[string]bool {
	retired := map[string]bool{}
	if len(named) == 0 {
		return retired
	}
	for _, c := range members {
		if c.ClaimType != "decision" {
			continue
		}
		if named[strings.TrimSpace(c.ID)] || named[strings.TrimSpace(c.Qualifiers["decision_id"])] {
			retired[DecisionCanonicalID(c)] = true
		}
	}
	return retired
}

// applyRecallDecisionRetirements adds accepted operator decisions' named retirements
// to a recall snapshot. AllCurrent retains the input inventory for decisions --all.
// Neither the editable index nor an ordinary promoted claim can supply this authority.
func applyRecallDecisionRetirements(claims []Claim, superseded map[string]bool, asOf int64) {
	var members, protected []Claim
	for _, c := range claims {
		if c.ClaimType != "decision" || c.Seq > asOf || c.Visibility == "retired" || c.State == ClaimStateNeedsReview || superseded[c.ID] {
			continue
		}
		members = append(members, c)
		if HasProtectedOperationalDecisionAuthority(c) {
			protected = append(protected, c)
		}
	}
	named := DecisionSupersedesNames(protected, func(c Claim) bool {
		return c.Qualifiers["lifecycle_status"] == "accepted"
	})
	if len(named) == 0 {
		return
	}
	retired := RetiredDecisionIDs(members, named)
	// Only trusted imports may link a claim ID to a protected decision's canonical
	// name. A generic page can copy a decision_id but cannot thereby retire a ruling.
	retiredProtected := RetiredDecisionIDs(protected, named)
	for _, c := range members {
		ids := retired
		if HasProtectedOperationalDecisionAuthority(c) {
			ids = retiredProtected
		}
		if ids[DecisionCanonicalID(c)] {
			superseded[c.ID] = true
		}
	}
}

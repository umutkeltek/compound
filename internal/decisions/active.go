package decisions

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"compound/internal/mem"
	"compound/internal/policy"
)

const RefPrefix = mem.DecisionRefPrefix

// ActiveRecord is the exact read model served for one active decision.
type ActiveRecord struct {
	Ref       string    `json:"ref"`
	Claim     mem.Claim `json:"claim"`
	Lifecycle Lifecycle `json:"lifecycle"`
}

// LoadActive reads and resolves one active decision from the canonical memory store.
func LoadActive(store *mem.Store, query string) (ActiveRecord, error) {
	claims, err := store.AllCurrent()
	if err != nil {
		return ActiveRecord{}, err
	}
	claim, ref, ok := ResolveActive(claims, query)
	if !ok {
		return ActiveRecord{}, fmt.Errorf("active decision %q not found", query)
	}
	return ActiveRecord{Ref: ref, Claim: claim, Lifecycle: LifecycleFromClaim(claim)}, nil
}

// Render writes the stable JSON or Markdown representation of one exact decision.
func (record ActiveRecord) Render(w io.Writer, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(w).Encode(record)
	}
	var body strings.Builder
	fmt.Fprintln(&body, "# decision")
	fmt.Fprintln(&body)
	fmt.Fprintf(&body, "- ref: %s\n", record.Ref)
	fmt.Fprintf(&body, "- claim_id: %s\n", record.Claim.ID)
	fmt.Fprintf(&body, "- kind: %s\n", record.Lifecycle.Kind)
	fmt.Fprintf(&body, "- status: %s\n", record.Lifecycle.Status)
	if record.Lifecycle.Scope != "" {
		fmt.Fprintf(&body, "- scope: %s\n", record.Lifecycle.Scope)
	}
	if record.Lifecycle.Owner != "" {
		fmt.Fprintf(&body, "- owner: %s\n", record.Lifecycle.Owner)
	}
	fmt.Fprintf(&body, "- choice: %s\n", strings.Join(strings.Fields(record.Claim.Object), " "))
	if record.Lifecycle.Falsifier != "" {
		fmt.Fprintf(&body, "- falsifier: %s\n", strings.Join(strings.Fields(record.Lifecycle.Falsifier), " "))
	}
	if record.Lifecycle.ReviewTrigger != "" {
		fmt.Fprintf(&body, "- review_trigger: %s\n", strings.Join(strings.Fields(record.Lifecycle.ReviewTrigger), " "))
	}
	_, err := io.WriteString(w, body.String())
	return err
}

// CanonicalRef is the one served identity for a decision claim. Imported decisions
// keep their durable decision_id; legacy decision-shaped claims fall back to the
// claim id so every emitted ref still resolves exactly.
func CanonicalRef(claim mem.Claim) string {
	return RefPrefix + mem.DecisionCanonicalID(claim)
}

// ActiveByRef projects accepted, non-superseded decisions onto CanonicalRef and
// collapses duplicate imports to the newest claim revision.
func ActiveByRef(claims []mem.Claim) map[string]mem.Claim {
	byRef := map[string]mem.Claim{}
	for _, claim := range AllActiveDecisions(claims) {
		ref := CanonicalRef(claim)
		if prior, ok := byRef[ref]; !ok || claim.Seq > prior.Seq || (claim.Seq == prior.Seq && claim.ID > prior.ID) {
			byRef[ref] = claim
		}
	}
	return byRef
}

// ResolveActive resolves exactly one canonical ref, decision id, or claim id from
// the active decision projection. It never falls back to a full inventory search.
func ResolveActive(claims []mem.Claim, query string) (mem.Claim, string, bool) {
	query = strings.TrimSpace(query)
	if query == "" {
		return mem.Claim{}, "", false
	}
	byRef := ActiveByRef(claims)
	ref := query
	if !strings.HasPrefix(ref, RefPrefix) {
		ref = RefPrefix + ref
	}
	if claim, ok := byRef[ref]; ok {
		return claim, ref, true
	}
	for candidateRef, claim := range byRef {
		if query == claim.ID {
			return claim, candidateRef, true
		}
	}
	return mem.Claim{}, "", false
}

// AllActiveDecisions returns accepted decision claims that no accepted
// decision record retires (see RetiredRefs).
//
// This is the ONE read path the decision readers share (director's grant
// resolution, standing law, served decision refs, `decisions show`); work's
// freeze admission guard mirrors the same rule in internal/work because of an
// import cycle. It reads ONLY decision records folded through mem.Store —
// never markdown, worklog, or brain prose (grants-as-gates 2026-07-10,
// falsifier e). Undo is modeled as a superseding record (falsifier c): a NEW
// accepted record whose `supersedes` qualifier names the old decision flips
// this projection's answer without deleting the old claim — it stays in
// AllCurrent() history, just excluded from this ACTIVE projection.
func AllActiveDecisions(claims []mem.Claim) []mem.Claim {
	retired := RetiredRefs(claims)
	var out []mem.Claim
	for _, c := range claims {
		if c.ClaimType != "decision" {
			continue
		}
		if LifecycleFromClaim(c).Status != StatusAccepted {
			continue
		}
		if retired[CanonicalRef(c)] {
			continue
		}
		out = append(out, c)
	}
	return out
}

// RetiredRefs returns the canonical refs (see CanonicalRef) that an accepted
// decision record retires through its `supersedes` qualifier.
//
// A `supersedes` entry may name a decision by its decision_id, by the claim id
// of any of its imports, or by its canonical ref with or without the
// decision:// prefix; one `--undo-of` may carry a comma list. Naming one import
// retires the whole canonical ref, so an older import (claim id DEC-0003, no
// decision_id) and a newer one (decision_id DEC-0003) leave the projection
// together instead of the older one coming back under the same ref.
//
// Every accepted record's list counts, including a record that is itself
// retired: retiring the retirer never brings the first record back. Order does
// not matter, and an entry that matches no decision claim retires nothing.
// The naming rule itself is owned by mem.DecisionSupersedesNames and
// mem.RetiredDecisionIDs, which the internal/work mirrors share.
func RetiredRefs(claims []mem.Claim) map[string]bool {
	named := mem.DecisionSupersedesNames(claims, func(c mem.Claim) bool {
		return LifecycleFromClaim(c).Status == StatusAccepted
	})
	retired := map[string]bool{}
	for id := range mem.RetiredDecisionIDs(claims, named) {
		retired[RefPrefix+id] = true
	}
	return retired
}

// EffectiveStatus is a decision claim's lifecycle status as the active
// projection reads it: an accepted claim whose canonical ref is in retired
// (from RetiredRefs) counts as superseded, whatever its own qualifier says.
func EffectiveStatus(c mem.Claim, retired map[string]bool) string {
	status := LifecycleFromClaim(c).Status
	if status == StatusAccepted && retired[CanonicalRef(c)] {
		return StatusSuperseded
	}
	return status
}

// DecisionListing is the `compound decisions` list: one row per decision
// (canonical ref), never one row per import.
type DecisionListing struct {
	// Active holds the newest accepted import of each active ref, sorted by ref.
	Active []ActiveRecord `json:"active"`
	// Retired holds the newest accepted import of each ref a record retired.
	Retired []ActiveRecord `json:"retired"`
	// NotAccepted counts refs with no accepted import (proposed, superseded,
	// deprecated or rejected status).
	NotAccepted int `json:"not_accepted"`
}

// ListDecisions groups decision claims by CanonicalRef. kind, when set, keeps
// only claims of that kind. Active matches ActiveByRef row for row.
func ListDecisions(claims []mem.Claim, kind string) DecisionListing {
	retired := RetiredRefs(claims)
	kind = strings.TrimSpace(kind)
	newestAccepted := map[string]mem.Claim{}
	refs := map[string]bool{}
	for _, c := range claims {
		if c.ClaimType != "decision" || (kind != "" && Kind(c) != kind) {
			continue
		}
		ref := CanonicalRef(c)
		refs[ref] = true
		if LifecycleFromClaim(c).Status != StatusAccepted {
			continue
		}
		if prior, ok := newestAccepted[ref]; !ok || c.Seq > prior.Seq || (c.Seq == prior.Seq && c.ID > prior.ID) {
			newestAccepted[ref] = c
		}
	}
	ordered := make([]string, 0, len(refs))
	for ref := range refs {
		ordered = append(ordered, ref)
	}
	sort.Strings(ordered)
	listing := DecisionListing{Active: []ActiveRecord{}, Retired: []ActiveRecord{}}
	for _, ref := range ordered {
		claim, ok := newestAccepted[ref]
		if !ok {
			listing.NotAccepted++
			continue
		}
		record := ActiveRecord{Ref: ref, Claim: claim, Lifecycle: LifecycleFromClaim(claim)}
		if retired[ref] {
			listing.Retired = append(listing.Retired, record)
			continue
		}
		listing.Active = append(listing.Active, record)
	}
	return listing
}

// ActiveDecisions narrows the canonical active projection to one operational
// kind (for example KindGrant or KindFreeze).
func ActiveDecisions(claims []mem.Claim, kind string) []mem.Claim {
	var out []mem.Claim
	for _, c := range AllActiveDecisions(claims) {
		if LifecycleFromClaim(c).Kind == kind {
			out = append(out, c)
		}
	}
	return out
}

// AuthoritativeActiveDecisions is the stricter projection for decisions that
// can release autonomous work. It admits only claims minted by the dedicated
// policy-decision import door and lets only equally authoritative claims
// supersede them. Generic recalled meaning remains evidence even if it copies
// every visible lifecycle qualifier.
func AuthoritativeActiveDecisions(claims []mem.Claim, kind string) []mem.Claim {
	var authoritative []mem.Claim
	for _, c := range claims {
		if c.ClaimType == "decision" && mem.HasProtectedOperationalDecisionAuthority(c) && policy.HasOperatorProvenance(c.Qualifiers["reviewed_by"]) {
			authoritative = append(authoritative, c)
		}
	}
	var out []mem.Claim
	for _, c := range AllActiveDecisions(authoritative) {
		if LifecycleFromClaim(c).Kind == kind {
			out = append(out, c)
		}
	}
	return out
}

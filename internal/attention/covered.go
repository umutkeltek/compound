package attention

import (
	"sort"
	"strings"
	"time"

	"compound/internal/policy"
	"compound/internal/reviewartifact"
)

// Covered by his rule: an item whose every possible answer the operator has
// already given in advance, through a standing grant the policy owner still
// holds, is not waiting for him. It leaves the needs_you count and is listed
// in Report.CoveredByRule with the rule that covers it, so a page can say
// "your rule covers these: N" and link each one.
//
// Coverage is read, never assumed. An item is covered only when EVERY action
// it offers carries the producer's own authority bundle (ActionIntent,
// PolicyDecision, AuthorityFace) that validates, is current, is allowed, and
// cites a standing grant, AND that grant is still installed and active now in
// the grant set the policy owner loads. No kind or area name decides it. An
// item with no action, or one action without such a bundle, stays his.
//
// Attention does not evaluate policy here: the decision is the producer's
// (the review queue stamps each action with current policy on every read),
// the grant set is the policy owner's, and this file only joins the two. An
// action whose producer seeded no ActionIntent cannot be stamped, so it never
// counts as covered: it stays his until its producer says what it would do.
//
// This is a read-only projection. It never installs, approves, decides,
// creates or closes anything.

// BandCoveredByRule names the band of items his standing rules already
// cover. Like set_aside it never appears in Report.Groups, so every existing
// reader keeps treating each group there as his.
const BandCoveredByRule = "covered_by_rule"

// coveredByRuleWorker names what works covered items. No command, agent loop
// or scheduled job reads this band and decides its items yet, so it is empty
// and the next step says so. When a worker exists, name it here and in the
// next-step sentence; saying "agents handle it" over a queue nobody works
// would be a new false claim.
const coveredByRuleWorker = ""

const coveredByRuleNextStep = "Your standing rule already allows these, so they are not counted as waiting for you. Nothing works them yet: no agent or scheduled job decides them, so they stay here until one is set up."

// RulesInput is the operator's standing grants as the policy owner loads them
// (policy.LoadConfig: policy.yaml plus the installed registry), with his
// effective presence at read time. Nil Inputs.Rules means the rules were not
// read: then nothing moves to the covered band.
type RulesInput struct {
	StandingGrants []policy.StandingGrant
	// Presence is the operator's effective presence when the rules were
	// read (present, away). A grant that names a presence state covers only
	// while it matches; empty presence matches no such grant.
	Presence string
}

// RulesState says whether the rules were read and how many grants are
// active now, so a page can tell "no rule covers this" from "rules unread".
type RulesState struct {
	Read                 bool `json:"read"`
	ActiveStandingGrants int  `json:"active_standing_grants"`
}

// CoveringRule is one standing grant that covers a batch.
type CoveringRule struct {
	GrantID    string `json:"grant_id"`
	Capability string `json:"capability,omitempty"`
	ExpiresAt  string `json:"expires_at,omitempty"`
}

// CoveredByRule is one batch of live items, from one source group, that his
// standing rules already cover.
type CoveredByRule struct {
	Key             string `json:"key"`
	Band            string `json:"band"`
	Area            string `json:"area"`
	Source          string `json:"source"`
	Kind            string `json:"kind"`
	Count           int    `json:"count"`
	OperatorSummary string `json:"operator_summary"`
	SourceGroupKey  string `json:"source_group_key"`
	// ItemIDs and Records identify every covered item so a page can link
	// each one; Records carries the stored file of each.
	ItemIDs []string         `json:"item_ids"`
	Records []SetAsideRecord `json:"records"`
	// Rules lists the standing grants that cover the batch.
	Rules []CoveringRule `json:"rules"`
	// Worker names the command or job that decides these items under the
	// rule. Empty: nothing works them yet.
	Worker string `json:"worker"`
	// NextStep says in one plain sentence what happens next and who does it.
	NextStep string `json:"next_step"`
}

// activeGrants indexes the grants that can cover an item at now.
func (r *RulesInput) activeGrants(now time.Time) map[string]policy.StandingGrant {
	out := map[string]policy.StandingGrant{}
	if r == nil {
		return out
	}
	for _, g := range r.StandingGrants {
		id := strings.TrimSpace(g.ID)
		if id == "" || !grantActive(g, r.Presence, now) {
			continue
		}
		out[id] = g
	}
	return out
}

// grantActive mirrors the grant's own eligibility fields: not expired, past
// not_before, and a presence key that matches. An unreadable time fails
// closed (the grant covers nothing), as the policy owner treats it.
func grantActive(g policy.StandingGrant, presence string, now time.Time) bool {
	if raw := strings.TrimSpace(g.ExpiresAt); raw != "" {
		at, err := time.Parse(time.RFC3339, raw)
		if err != nil || !now.Before(at) {
			return false
		}
	}
	if raw := strings.TrimSpace(g.NotBefore); raw != "" {
		at, err := time.Parse(time.RFC3339, raw)
		if err != nil || now.Before(at) {
			return false
		}
	}
	switch required := strings.ToLower(strings.TrimSpace(g.RequiresPresence)); required {
	case "", "any":
		return true
	default:
		return required == strings.ToLower(strings.TrimSpace(presence))
	}
}

// coveringRules reports the standing grants that cover every action the item
// offers, or false when any action is not covered.
func coveringRules(item reviewartifact.QueueItem, grants map[string]policy.StandingGrant) ([]CoveringRule, bool) {
	if len(item.Actions) == 0 || len(grants) == 0 {
		return nil, false
	}
	seen := map[string]bool{}
	var out []CoveringRule
	for i := range item.Actions {
		action := item.Actions[i]
		projected := &Action{ActionIntent: action.ActionIntent, PolicyDecision: action.PolicyDecision, PolicyAuthority: action.PolicyAuthority}
		if !validReviewAuthority(projected) || !action.PolicyAuthority.Allowed || !action.PolicyAuthority.Current {
			return nil, false
		}
		id := strings.TrimSpace(action.PolicyDecision.StandingGrantID)
		grant, ok := grants[id]
		if id == "" || !ok {
			return nil, false
		}
		if !seen[id] {
			seen[id] = true
			out = append(out, CoveringRule{GrantID: id, Capability: grant.Capability, ExpiresAt: grant.ExpiresAt})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GrantID < out[j].GrantID })
	return out, true
}

// coveredBatches rolls covered members up per source group.
func (l *liveness) coveredBatches() []CoveredByRule {
	batches := map[int]*CoveredByRule{}
	var order []int
	for _, m := range l.members {
		if m.reason != "" || m.covered == nil {
			continue
		}
		b := batches[m.group]
		if b == nil {
			source := l.groups[m.group].group
			b = &CoveredByRule{
				Key:             source.Key + "#" + BandCoveredByRule,
				Band:            BandCoveredByRule,
				Area:            source.Area,
				Source:          source.Source,
				OperatorSummary: source.OperatorSummary,
				SourceGroupKey:  source.Key,
				Worker:          coveredByRuleWorker,
				NextStep:        coveredByRuleNextStep,
			}
			batches[m.group] = b
			order = append(order, m.group)
		}
		b.Count++
		b.ItemIDs = append(b.ItemIDs, m.item.ID)
		b.Records = append(b.Records, SetAsideRecord{ID: m.item.ID, Path: m.item.JSONPath, CreatedAt: m.item.CreatedAt, Status: m.item.Status})
		for _, rule := range m.covered {
			if !containsRule(b.Rules, rule.GrantID) {
				b.Rules = append(b.Rules, rule)
			}
		}
	}
	out := make([]CoveredByRule, 0, len(order))
	for _, gi := range order {
		b := batches[gi]
		b.ItemIDs = uniqueStrings(b.ItemIDs)
		sort.SliceStable(b.Records, func(i, j int) bool {
			if b.Records[i].Path != b.Records[j].Path {
				return b.Records[i].Path < b.Records[j].Path
			}
			return b.Records[i].ID < b.Records[j].ID
		})
		sort.Slice(b.Rules, func(i, j int) bool { return b.Rules[i].GrantID < b.Rules[j].GrantID })
		out = append(out, *b)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Area != out[j].Area {
			return out[i].Area < out[j].Area
		}
		return out[i].Key < out[j].Key
	})
	return out
}

func containsRule(rules []CoveringRule, id string) bool {
	for _, rule := range rules {
		if rule.GrantID == id {
			return true
		}
	}
	return false
}

package attention

import (
	"strings"
	"testing"
	"time"

	"compound/internal/policy"
	"compound/internal/reviewartifact"
)

// The covered-by-rule tests stamp real producer authority bundles
// (policy.EvaluateAuthority, see authority_test.go) on synthetic review
// items, then hand Build the grant set a policy owner would load. They read
// the result through the JSON wire form, the way the console reads it.

const coveredGrantID = "grant_attention_authority" // the grant attentionAuthorityAction cites

// coveredReport builds one review group in a synthetic area; item i offers
// actions[i].
func coveredReport(area string, actions ...[]reviewartifact.Action) *reviewartifact.QueueReport {
	report := &reviewartifact.QueueReport{GeneratedAt: liveNow.Format(time.RFC3339)}
	for i, offered := range actions {
		report.Items = append(report.Items, reviewartifact.QueueItem{
			Area:            area,
			ID:              area + "_" + string(rune('a'+i)),
			Status:          "review_required",
			GroupKey:        area + "|proposals",
			Band:            reviewartifact.BandNeedsYou,
			OperatorSummary: "Decide this proposal.",
			ContextLabel:    "proposal",
			SummarySource:   reviewartifact.SummarySourceWriter,
			CreatedAt:       liveNow.Add(-time.Duration(i+1) * time.Hour).Format(time.RFC3339),
			Actions:         offered,
		})
	}
	reviewartifact.Recount(report)
	return report
}

func coveredGrant(mutate func(*policy.StandingGrant)) []policy.StandingGrant {
	g := policy.StandingGrant{ID: coveredGrantID, Capability: "artifact.update", MutationClass: "local_reversible", ExpiresAt: liveNow.Add(30 * 24 * time.Hour).Format(time.RFC3339)}
	if mutate != nil {
		mutate(&g)
	}
	return []policy.StandingGrant{g}
}

func coveredWire(t *testing.T, report Report) []map[string]any {
	t.Helper()
	var out []map[string]any
	raw, _ := wireOf(t, report)["covered_by_rule"].([]any)
	for _, entry := range raw {
		out = append(out, entry.(map[string]any))
	}
	return out
}

func TestCoveredByRuleMovesOnlyItemsEveryAnswerOfWhichAnActiveGrantAllows(t *testing.T) {
	allowed := attentionAuthorityAction(t, liveNow, "compound://policy/effective", true)
	held := attentionAuthorityAction(t, liveNow, "compound://policy/effective", false)
	bare := reviewartifact.Action{ID: "reject", Label: "Reject", Command: "compound owner reject"}

	cases := []struct {
		name        string
		offered     []reviewartifact.Action
		rules       *RulesInput
		wantCovered bool
	}{
		{name: "installed active grant covers the only answer", offered: []reviewartifact.Action{allowed}, rules: &RulesInput{StandingGrants: coveredGrant(nil)}, wantCovered: true},
		{name: "rules not read", offered: []reviewartifact.Action{allowed}, rules: nil},
		{name: "no grant installed", offered: []reviewartifact.Action{allowed}, rules: &RulesInput{}},
		{name: "a different grant is installed", offered: []reviewartifact.Action{allowed}, rules: &RulesInput{StandingGrants: coveredGrant(func(g *policy.StandingGrant) { g.ID = "grant_other" })}},
		{name: "grant expired", offered: []reviewartifact.Action{allowed}, rules: &RulesInput{StandingGrants: coveredGrant(func(g *policy.StandingGrant) { g.ExpiresAt = liveNow.Add(-time.Minute).Format(time.RFC3339) })}},
		{name: "grant not active yet", offered: []reviewartifact.Action{allowed}, rules: &RulesInput{StandingGrants: coveredGrant(func(g *policy.StandingGrant) { g.NotBefore = liveNow.Add(time.Hour).Format(time.RFC3339) })}},
		{name: "grant for when he is away, he is present", offered: []reviewartifact.Action{allowed}, rules: &RulesInput{StandingGrants: coveredGrant(func(g *policy.StandingGrant) { g.RequiresPresence = "away" }), Presence: "present"}},
		{name: "grant for when he is away, he is away", offered: []reviewartifact.Action{allowed}, rules: &RulesInput{StandingGrants: coveredGrant(func(g *policy.StandingGrant) { g.RequiresPresence = "away" }), Presence: "away"}, wantCovered: true},
		{name: "the producer's decision holds", offered: []reviewartifact.Action{held}, rules: &RulesInput{StandingGrants: coveredGrant(nil)}},
		{name: "one answer carries no authority", offered: []reviewartifact.Action{allowed, bare}, rules: &RulesInput{StandingGrants: coveredGrant(nil)}},
		{name: "no answer offered", offered: nil, rules: &RulesInput{StandingGrants: coveredGrant(nil)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := Build(Inputs{Reviews: coveredReport("skill-proposals", tc.offered), Rules: tc.rules}, liveNow)
			covered := coveredWire(t, report)
			totals := wireOf(t, report)["totals"].(map[string]any)
			if !tc.wantCovered {
				if len(covered) != 0 || report.Counts.NeedsYou != 1 || wireNumber(t, totals, "covered_by_rule") != 0 {
					t.Fatalf("item moved without a rule that covers it: needs_you=%d covered=%v", report.Counts.NeedsYou, covered)
				}
				return
			}
			if report.Counts.NeedsYou != 0 || len(report.Groups) != 0 {
				t.Fatalf("covered item still counted as his: needs_you=%d groups=%+v", report.Counts.NeedsYou, report.Groups)
			}
			if len(covered) != 1 || wireNumber(t, covered[0], "count") != 1 || wireNumber(t, totals, "covered_by_rule") != 1 {
				t.Fatalf("covered_by_rule = %v, totals = %v; want one batch of one", covered, totals)
			}
			entry := covered[0]
			if entry["band"] != BandCoveredByRule || entry["area"] != "skill-proposals" || entry["source"] != SourceReviews {
				t.Fatalf("covered entry identity = %v", entry)
			}
			ids, _ := entry["item_ids"].([]any)
			if len(ids) != 1 || ids[0] != "skill-proposals_a" {
				t.Fatalf("covered item ids = %v, want the item to link", entry["item_ids"])
			}
			rules, _ := entry["rules"].([]any)
			if len(rules) != 1 || rules[0].(map[string]any)["grant_id"] != coveredGrantID {
				t.Fatalf("covering rules = %v, want %s", entry["rules"], coveredGrantID)
			}
			// Nothing works this band today; the entry must say so, never
			// claim agents handle it.
			if entry["worker"] != "" || !strings.Contains(entry["next_step"].(string), "Nothing works them yet") {
				t.Fatalf("covered entry worker = %q next_step = %q; want no worker and a plain 'nothing works them yet'", entry["worker"], entry["next_step"])
			}
		})
	}
}

func TestCoveredByRuleIsDrivenByTheGrantNotTheKindName(t *testing.T) {
	allowed := attentionAuthorityAction(t, liveNow, "compound://policy/effective", true)
	for _, area := range []string{"skill-proposals", "lock-requests", "anything-else"} {
		report := Build(Inputs{Reviews: coveredReport(area, []reviewartifact.Action{allowed}), Rules: &RulesInput{StandingGrants: coveredGrant(nil)}}, liveNow)
		if covered := coveredWire(t, report); len(covered) != 1 || covered[0]["area"] != area {
			t.Fatalf("area %s: covered = %v, want the grant to move it whatever its kind", area, covered)
		}
		report = Build(Inputs{Reviews: coveredReport(area, []reviewartifact.Action{allowed}), Rules: &RulesInput{}}, liveNow)
		if covered := coveredWire(t, report); len(covered) != 0 || report.Counts.NeedsYou != 1 {
			t.Fatalf("area %s: moved with no grant installed: %v", area, covered)
		}
	}
}

func TestCoveredByRuleCountsAddUpAndAPartlyCoveredGroupLosesItsBatchAction(t *testing.T) {
	allowed := attentionAuthorityAction(t, liveNow, "compound://policy/effective", true)
	bare := reviewartifact.Action{ID: "reject", Label: "Reject", Command: "compound owner reject"}
	reviews := coveredReport("skill-proposals", []reviewartifact.Action{allowed}, []reviewartifact.Action{allowed}, []reviewartifact.Action{bare})
	reviews.Groups[0].SafeBatchAction = &allowed
	report := Build(Inputs{Reviews: reviews, Rules: &RulesInput{StandingGrants: coveredGrant(nil)}}, liveNow)

	totals := wireOf(t, report)["totals"].(map[string]any)
	read, live, aside, waiting, covered := wireNumber(t, totals, "total_read"), wireNumber(t, totals, "live_decisions"), wireNumber(t, totals, "set_aside"), wireNumber(t, totals, "waiting_on_others"), wireNumber(t, totals, "covered_by_rule")
	if read != 3 || live != 1 || covered != 2 || live+aside+waiting+covered != read {
		t.Fatalf("totals read=%d live=%d set_aside=%d waiting=%d covered=%d; want 3 = 1 live + 2 covered", read, live, aside, waiting, covered)
	}
	for _, raw := range totals["by_source"].([]any) {
		entry := raw.(map[string]any)
		if wireNumber(t, entry, "live")+wireNumber(t, entry, "set_aside")+wireNumber(t, entry, "waiting_on_others")+wireNumber(t, entry, "covered_by_rule") != wireNumber(t, entry, "read") {
			t.Fatalf("source %v does not add up: %v", entry["source"], entry)
		}
	}
	listed := 0
	for _, entry := range coveredWire(t, report) {
		records, _ := entry["records"].([]any)
		listed += len(records)
	}
	if listed != covered || report.Counts.NeedsYou != live {
		t.Fatalf("covered records listed %d vs covered %d; needs_you %d vs live %d", listed, covered, report.Counts.NeedsYou, live)
	}
	group := groupByKey(t, report, "skill-proposals|proposals")
	if group.Count != 1 || group.Action == nil || group.Action.Kind != ActionCopyOnly {
		t.Fatalf("partly covered group = count %d action %+v; want 1 live item and a batch action that cannot run over covered items", group.Count, group.Action)
	}
	if !wireOf(t, report)["rules"].(map[string]any)["read"].(bool) {
		t.Fatalf("rules.read = false, want true when rules were handed in")
	}
}

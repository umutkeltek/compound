package attention

import (
	"strings"
	"testing"
	"time"

	"compound/internal/envelope"
	"compound/internal/policy"
)

// The queue tests hand Build synthetic owner facts, the way the CLI hands it
// what each owner's reader returned, and check the result through the JSON
// wire form.

const testPlaceholder = "WRITE ME:"

func queueGroup(t *testing.T, report Report, source string) map[string]any {
	t.Helper()
	for _, raw := range wireOf(t, report)["groups"].([]any) {
		g := raw.(map[string]any)
		if g["source"] == source && g["band"] == BandNeedsYou {
			return g
		}
	}
	return nil
}

func queueSetAside(t *testing.T, report Report, source string) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for _, raw := range wireOf(t, report)["set_aside"].([]any) {
		entry := raw.(map[string]any)
		if entry["source"] != source {
			continue
		}
		for _, id := range entry["item_ids"].([]any) {
			out[entry["reason"].(string)] = append(out[entry["reason"].(string)], id.(string))
		}
	}
	return out
}

func queueItemIDs(t *testing.T, group map[string]any) []string {
	t.Helper()
	var out []string
	if group == nil {
		return out
	}
	items, _ := group["items"].([]any)
	for _, raw := range items {
		item := raw.(map[string]any)
		if item["ref"] == "" || item["id"] == "" {
			t.Fatalf("item without identity to link: %v", item)
		}
		out = append(out, item["id"].(string))
	}
	return out
}

func TestQueuesMemoryProposalsWaitForHimUnderTheSameRules(t *testing.T) {
	created := liveNow.Add(-48 * time.Hour).Format(time.RFC3339)
	report := Build(Inputs{MemoryProposals: &MemoryProposalsInput{GeneratedAt: liveNow.Format(time.RFC3339), Items: []MemoryProposal{
		{ID: "cand_live", Claim: "service-a uses store-b", ClaimKey: "service-a|uses|store-b", Context: "service-a", CreatedAt: created, Path: "/home/example/.compound/inbox/cand_live.json"},
		{ID: "cand_repeat", Claim: "service-a uses store-b", ClaimKey: "service-a|uses|store-b", Context: "service-a", CreatedAt: liveNow.Add(-time.Hour).Format(time.RFC3339)},
		{ID: "cand_ruled", Claim: "service-c runs nightly", ClaimKey: "service-c|runs|nightly", AdjudicatedBy: "operator:example", CreatedAt: created},
	}}}, liveNow)

	group := queueGroup(t, report, SourceMemory)
	if group == nil || group["kind"] != KindMemoryProposal || wireNumber(t, group, "count") != 1 {
		t.Fatalf("memory group = %v, want one live proposal of kind %s", group, KindMemoryProposal)
	}
	wantIDs(t, "live memory proposals", queueItemIDs(t, group), "cand_live")
	aside := queueSetAside(t, report, SourceMemory)
	wantIDs(t, "duplicate", aside[SetAsideDuplicate], "cand_repeat")
	wantIDs(t, "already decided", aside[SetAsideAlreadyDecided], "cand_ruled")
	if report.Counts.NeedsYou != 1 {
		t.Fatalf("needs_you = %d, want the one live proposal", report.Counts.NeedsYou)
	}
}

func TestQueuesProductResultsCountOnlyRealOnesAndOnlyOnce(t *testing.T) {
	created := liveNow.Add(-40 * 24 * time.Hour)
	reviews := liveFixture(t,
		// A review plan for a draft nobody wrote: not his.
		liveRecord{area: "product-reviews", id: "prv_draft", status: "review_required", summary: "Judge the result?", created: created,
			body: map[string]any{"hypothesis_id": "hyp_draft", "expected_effect": testPlaceholder + " what should it do?"}},
		// A review card already asks about hyp_asked.
		liveRecord{area: "product-reviews", id: "prv_asked", status: "review_required", summary: "Judge the result?", created: created,
			body: map[string]any{"hypothesis_id": "hyp_asked", "expected_effect": "Signups rise after the new page."}},
	)
	report := Build(Inputs{
		Reviews:           reviews,
		DraftPlaceholders: []string{testPlaceholder},
		ProductResults: &ProductResultsInput{GeneratedAt: liveNow.Format(time.RFC3339), Items: []ProductResult{
			{ProductID: "product:app-a", HypothesisID: "hyp_real", ExpectedEffect: "Installs rise after the new subtitle.", CreatedAt: created.Format(time.RFC3339), SignalIDs: []string{"sig_1"}},
			{ProductID: "product:app-b", HypothesisID: "hyp_draft", ExpectedEffect: testPlaceholder + " what should it do?"},
			{ProductID: "product:app-c", HypothesisID: "hyp_asked", ExpectedEffect: "Signups rise after the new page."},
		}},
	}, liveNow)

	group := queueGroup(t, report, SourceProduct)
	if group == nil || group["kind"] != KindProductResult {
		t.Fatalf("product group = %v", group)
	}
	wantIDs(t, "live product results", queueItemIDs(t, group), "hyp_real")
	if item := group["items"].([]any)[0].(map[string]any); item["context"] != "product:app-a" {
		t.Fatalf("product result item context = %v, want the app it is about", item["context"])
	}
	aside := queueSetAside(t, report, SourceProduct)
	wantIDs(t, "placeholder draft", aside[SetAsidePlaceholderDraft], "hyp_draft")
	wantIDs(t, "asked in the review queue", aside[SetAsideDuplicate], "hyp_asked")
	wantIDs(t, "review plan for a placeholder draft", setAsideByReason(t, report)[SetAsidePlaceholderDraft], "hyp_draft", "prv_draft")
	if report.Counts.NeedsYou != 2 {
		t.Fatalf("needs_you = %d, want the real result and the one review card that asks about a written hypothesis", report.Counts.NeedsYou)
	}
}

func TestQueuesHookRequestsCountOnlyWhileTheirWindowIsOpen(t *testing.T) {
	report := Build(Inputs{HookRequests: &HookRequestsInput{GeneratedAt: liveNow.Format(time.RFC3339), Path: "/home/example/.compound/registries/frontier.json", Items: []HookRequest{
		{ID: "opactreq_open", Action: "destructive_command", Capability: "shell.destructive_command", Harness: "harness-a", MintedAt: liveNow.Add(-5 * time.Minute).Format(time.RFC3339Nano), Status: "pending"},
		{ID: "opactreq_old", Action: "network_command", Capability: "shell.network_command", Harness: "harness-b", MintedAt: liveNow.Add(-HookApprovalWindow).Format(time.RFC3339Nano), Status: "pending"},
		{ID: "opactreq_done", Action: "network_command", Capability: "shell.network_command", Harness: "harness-b", MintedAt: liveNow.Add(-time.Minute).Format(time.RFC3339Nano), Status: "approved"},
	}}}, liveNow)

	group := queueGroup(t, report, SourceHookRequests)
	if group == nil || group["kind"] != KindHookApproval {
		t.Fatalf("hook group = %v", group)
	}
	wantIDs(t, "live hook requests", queueItemIDs(t, group), "opactreq_open")
	if group["blast_radius"] != BlastDestructive || group["risk_grade"] != RiskGradeGate {
		t.Fatalf("hook group reach = %v/%v, want the held action's own capability to make it a destructive gate", group["blast_radius"], group["risk_grade"])
	}
	aside := queueSetAside(t, report, SourceHookRequests)
	wantIDs(t, "window closed", aside[SetAsideWindowClosed], "opactreq_old")
	wantIDs(t, "already decided", aside[SetAsideAlreadyDecided], "opactreq_done")
}

func TestQueuesDataSourcesCountOnlyWhenTheirNextStepIsHis(t *testing.T) {
	report := Build(Inputs{DataSources: &DataSourcesInput{GeneratedAt: liveNow.Format(time.RFC3339), Items: []DataSource{
		{ID: "source_a", Label: "Search data", Status: "stale", PrivateSetup: true, NextStep: "Supply private setup."},
		{ID: "source_b", Label: "Store data", Status: "stale", NextStep: "Refresh the source."},
		{ID: "source_c", Label: "Ads data", Status: "needs_private_input"},
	}}}, liveNow)

	// A stale source's next step is a refresh, even one that runs on
	// private setup; only a source asking for private input waits for him.
	wantIDs(t, "live sources", queueItemIDs(t, queueGroup(t, report, SourceDataSources)), "source_c")
	wantIDs(t, "agent work", queueSetAside(t, report, SourceDataSources)[SetAsideAgentWork], "source_a", "source_b")
}

func TestQueuesCountsAddUpToWhatEverySourceHolds(t *testing.T) {
	report := Build(Inputs{
		DraftPlaceholders: []string{testPlaceholder},
		MemoryProposals: &MemoryProposalsInput{Items: []MemoryProposal{
			{ID: "cand_a", ClaimKey: "a|b|c"}, {ID: "cand_b", ClaimKey: "a|b|c"}, {ID: "cand_c", AdjudicatedBy: "operator:example"},
		}},
		ProductResults: &ProductResultsInput{Items: []ProductResult{
			{ProductID: "product:app-a", HypothesisID: "hyp_a", ExpectedEffect: "Rises."}, {ProductID: "product:app-a", HypothesisID: "hyp_b", ExpectedEffect: testPlaceholder},
		}},
		HookRequests: &HookRequestsInput{Items: []HookRequest{
			{ID: "opactreq_a", MintedAt: liveNow.Format(time.RFC3339), Status: "pending"}, {ID: "opactreq_b", MintedAt: liveNow.Add(-time.Hour).Format(time.RFC3339), Status: "pending"},
		}},
		DataSources: &DataSourcesInput{Items: []DataSource{{ID: "source_a", Status: "needs_private_input", PrivateSetup: true}, {ID: "source_b", Status: "stale"}}},
	}, liveNow)

	held := map[string]int{SourceMemory: 3, SourceProduct: 2, SourceHookRequests: 2, SourceDataSources: 2}
	totals := wireOf(t, report)["totals"].(map[string]any)
	sum := 0
	for _, raw := range totals["by_source"].([]any) {
		entry := raw.(map[string]any)
		read := wireNumber(t, entry, "read")
		if read != held[entry["source"].(string)] {
			t.Fatalf("source %v read %d, want %d (what the owner holds)", entry["source"], read, held[entry["source"].(string)])
		}
		if wireNumber(t, entry, "live")+wireNumber(t, entry, "set_aside")+wireNumber(t, entry, "waiting_on_others")+wireNumber(t, entry, "covered_by_rule") != read {
			t.Fatalf("source %v does not add up: %v", entry["source"], entry)
		}
		sum += read
	}
	if sum != 9 || wireNumber(t, totals, "total_read") != 9 {
		t.Fatalf("total_read = %v (sources %d), want 9", totals["total_read"], sum)
	}
	live, aside := wireNumber(t, totals, "live_decisions"), wireNumber(t, totals, "set_aside")
	if live != 4 || live+aside != 9 || report.Counts.NeedsYou != live {
		t.Fatalf("live %d + set aside %d, needs_you %d; want 4 live of 9", live, aside, report.Counts.NeedsYou)
	}
	records := 0
	for _, entry := range report.SetAside {
		records += len(entry.Records)
	}
	if records != aside {
		t.Fatalf("set-aside records listed %d, want %d", records, aside)
	}
	for _, source := range []string{SourceMemory, SourceProduct, SourceHookRequests, SourceDataSources} {
		found := false
		for _, f := range report.Freshness.Sources {
			found = found || f.Source == source
		}
		if !found {
			t.Fatalf("freshness has no stamp for %s: %+v", source, report.Freshness.Sources)
		}
	}
}

// TestHookApprovalWindowMatchesThePolicyCap pins HookApprovalWindow to the
// policy owner's longest exact approval plan: one nanosecond more is refused
// for its length, the window itself is not.
func TestHookApprovalWindowMatchesThePolicyCap(t *testing.T) {
	store := policy.NewExactActionApprovalStore(t.TempDir())
	_, _, _, err := store.EnsurePlanForIntent(envelope.ActionIntent{}, policy.DefaultConfig(), HookApprovalWindow+time.Nanosecond, liveNow)
	if err == nil || !strings.Contains(err.Error(), "ttl") {
		t.Fatalf("plan longer than HookApprovalWindow: err = %v, want the policy owner's ttl refusal", err)
	}
	_, _, _, err = store.EnsurePlanForIntent(envelope.ActionIntent{}, policy.DefaultConfig(), HookApprovalWindow, liveNow)
	if err != nil && strings.Contains(err.Error(), "ttl") {
		t.Fatalf("plan of exactly HookApprovalWindow refused for its length: %v; the policy cap moved", err)
	}
}

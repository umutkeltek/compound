package attention

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/reviewartifact"
	"compound/internal/work"
)

// These tests pin the three ways the first live-count rules could hide a real
// request, and the narrow rules that set aside only what is provably done.

// Fault 1: a record whose writer gave it only a template sentence is not
// hidden for that alone. Job repair, lock clearing and copy retirement records
// all carry template sentences today, and one of those kinds is "keep asking"
// under the operator's own rule. Each stays live and its group is marked
// needs_wording; only a second, real reason sets one aside.
func TestHonestListKeepsTemplateOnlyRecordsLiveAndMarked(t *testing.T) {
	created := liveNow.Add(-3 * 24 * time.Hour)
	fallback := reviewartifact.SummarySourceFallback
	report := Build(Inputs{Reviews: liveFixture(t,
		liveRecord{area: "job-repair", id: "jr_open", file: "jr_open.plan.json", status: "unknown", summary: "A new job repair record needs your decision.", source: fallback, groupKey: "job-repair|review", created: created},
		liveRecord{area: "job-repair", id: "jr_done", file: "jr_done.plan.json", status: "unknown", summary: "A new job repair record needs your decision.", source: fallback, groupKey: "job-repair|review", created: created},
		liveRecord{area: "job-repair", id: "jr_done", file: "jr_done.apply.terminal.json", status: "unknown", summary: "A new job repair record needs your decision.", source: fallback, groupKey: "job-repair|review", created: created},
		liveRecord{area: "lock-clearing", id: "lc_open", status: "unknown", summary: "A new lock clearing record needs your decision.", source: fallback, created: created},
		liveRecord{area: "copy-retirement", id: "cr_open", status: "review_required", summary: "A new copy retirement record needs your decision.", source: fallback, created: created},
	)}, liveNow)

	wantIDs(t, "needs_you ids", needsYouIDs(report), "cr_open", "jr_open", "lc_open")
	for _, g := range report.Groups {
		if g.Band == BandNeedsYou && !g.NeedsWording {
			t.Fatalf("group %s counted without a question but needs_wording = false", g.Key)
		}
	}
	got := setAsideByReason(t, report)
	if len(got["no_question"]) != 0 {
		t.Fatalf("template-only records hidden as no_question: %v", got["no_question"])
	}
	wantIDs(t, "finished_change", got["finished_change"], "jr_done")
}

// Fault 2: records are the same question only when their real claim is the
// same. Different proposals that share one template sentence stay separate
// items; two records with the same claim fold into one.
func TestHonestListFoldsOnlyRecordsWithTheSameClaim(t *testing.T) {
	created := liveNow.Add(-2 * 24 * time.Hour)
	template := "Screening proposes a lifecycle change touching 5 evidence hooks."
	annotator := reviewartifact.SummarySourceAnnotator
	report := Build(Inputs{Reviews: liveFixture(t,
		liveRecord{area: "tool-proposals", id: "tp_a", status: "review_required", summary: template, source: annotator, context: "tool proposal", claim: "Review 12 recent events from harness-a for repeated tool gaps.", created: created},
		liveRecord{area: "tool-proposals", id: "tp_b", status: "review_required", summary: template, source: annotator, context: "tool proposal", claim: "Review 340 recent events from harness-b for repeated tool gaps.", created: created.Add(time.Hour)},
		liveRecord{area: "tool-proposals", id: "tp_c", status: "review_required", summary: template, source: annotator, context: "tool proposal", claim: "Review 7 recent events from harness-c for repeated tool gaps.", created: created.Add(2 * time.Hour)},
		liveRecord{area: "tool-proposals", id: "tp_d", status: "review_required", summary: template, source: annotator, context: "tool proposal", claim: "Review 7 recent events from harness-c for repeated tool gaps.", created: created.Add(3 * time.Hour)},
	)}, liveNow)

	wantIDs(t, "needs_you ids", needsYouIDs(report), "tp_a", "tp_b", "tp_c")
	if report.Counts.NeedsYou != 3 {
		t.Fatalf("needs_you = %d, want 3 distinct proposals", report.Counts.NeedsYou)
	}
	got := setAsideByReason(t, report)
	wantIDs(t, "duplicate", got["duplicate"], "tp_d")
}

// Fault 3: work that waits on something outside (another item, an account, a
// device) is not waiting for the operator. Held work is his valve and stays
// counted; blocked work moves to its own visible group outside the count,
// unless the work itself says it needs the operator.
func TestHonestListSplitsBlockedWorkFromHeldWork(t *testing.T) {
	report := Build(Inputs{Workbench: &work.WorkbenchReport{
		GeneratedAt: liveNow.Format(time.RFC3339),
		Open: []work.WorkbenchItem{
			{WorkID: "work_held_sample", State: "held", NextAction: "Release or cancel the hold."},
			{WorkID: "work_blocked_on_item", State: "blocked", NextAction: "Waits for work_other_sample to land."},
			{WorkID: "work_blocked_on_device", State: "blocked", NextAction: "Waits for a second test device."},
			{WorkID: "work_blocked_gate", State: "blocked", ReviewCards: []work.WorkbenchReviewCard{{ID: "card_gate", Kind: "operator_gate", RequiresOperator: true}}},
		},
	}}, liveNow)

	wantIDs(t, "needs_you ids", needsYouIDs(report), "work_blocked_gate", "work_held_sample")
	if report.Counts.BlockedOnYou != 2 {
		t.Fatalf("blocked_on_you = %d, want 2 (held work and the blocked item that names the operator)", report.Counts.BlockedOnYou)
	}
	waiting := groupByKey(t, report, "workbench:waiting-on-others")
	if waiting.Band != BandWatching || waiting.Count != 2 || waiting.Kind != "waiting_on_others" {
		t.Fatalf("waiting group = %+v, want watching band, 2 items, kind waiting_on_others", waiting)
	}
	wantIDs(t, "waiting ids", waiting.ItemIDs, "work_blocked_on_device", "work_blocked_on_item")
}

// A dismissal recorded on one record of a change decides the records of that
// change that existed when it was made. A record filed after the dismissal is
// new and stays live.
func TestHonestListDismissalCoversOlderRecordsOfTheSameChange(t *testing.T) {
	created := liveNow.Add(-20 * 24 * time.Hour)
	fallback := reviewartifact.SummarySourceFallback
	dismissed := &reviewartifact.ReviewResolution{Status: "recorded", Decision: "dismissed", Reason: "Already applied.", ResolvedAt: liveNow.Add(-24 * time.Hour).Format(time.RFC3339)}
	report := Build(Inputs{Reviews: liveFixture(t,
		liveRecord{area: "job-repair", id: "act_intent_sample", file: "jrp_one.apply.review-intent.json", status: "unknown", summary: "A new job repair record needs your decision.", source: fallback, created: created, resolved: dismissed},
		liveRecord{area: "job-repair", id: "jrp_one", file: "jrp_one.plan.json", status: "unknown", summary: "A new job repair record needs your decision.", source: fallback, groupKey: "job-repair|review", created: created},
		liveRecord{area: "job-repair", id: "jrp_one", file: "jrp_one.apply.review-request.json", status: "unknown", summary: "A new job repair record needs your decision.", source: fallback, groupKey: "job-repair|review", created: created},
		// No created_at: timed by its file, written before the dismissal.
		liveRecord{area: "job-repair", id: "jrp_one", file: "jrp_one.apply.review-proposal.json", status: "unknown", summary: "A new job repair record needs your decision.", source: fallback, groupKey: "job-repair|review", fileTime: created},
		liveRecord{area: "job-repair", id: "jrp_one", file: "jrp_one.apply.v3.review-request.json", status: "unknown", summary: "A new job repair record needs your decision.", source: fallback, groupKey: "job-repair|review", created: liveNow.Add(-time.Hour)},
		// A review record names its plan in a field, not in its file name.
		liveRecord{area: "job-repair", id: "jrr_one", status: "unknown", summary: "A new job repair record needs your decision.", source: fallback, groupKey: "job-repair|review", created: created, body: map[string]any{"request": map[string]any{"plan_id": "jrp_finished"}}},
		liveRecord{area: "job-repair", id: "jrp_finished", file: "act_run.outcome.json", status: "unknown", summary: "A new job repair record needs your decision.", source: fallback, groupKey: "job-repair|review", created: created},
	)}, liveNow)

	got := setAsideByReason(t, report)
	wantIDs(t, "already_decided", got["already_decided"], "jrp_one")
	wantIDs(t, "finished_change", got["finished_change"], "jrp_finished", "jrr_one")
	// The request filed after the dismissal is the one live item of the plan.
	wantIDs(t, "needs_you ids", needsYouIDs(report), "jrp_one")
	if len(got["duplicate"]) != 0 {
		t.Fatalf("set aside as duplicate: %v", got["duplicate"])
	}
	if report.Counts.NeedsYou != 1 {
		t.Fatalf("needs_you = %d, want 1 (the request filed after the dismissal)", report.Counts.NeedsYou)
	}
}

// A request to clear a stuck lock file is about that file: when the file is
// gone, so is the request. A lock that is still there keeps it live.
func TestHonestListLockRequestWhoseLockIsGone(t *testing.T) {
	base := t.TempDir()
	stillLocked := filepath.Join(base, "repo-locked")
	unlocked := filepath.Join(base, "repo-unlocked")
	for _, dir := range []string{filepath.Join(stillLocked, ".git"), filepath.Join(unlocked, ".git")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(stillLocked, ".git", "index.lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot := func(repo string) map[string]any {
		return map[string]any{"state": map[string]any{"snapshot": map[string]any{"repo": repo, "lock": map[string]any{"inode": 42}}}}
	}
	created := liveNow.Add(-30 * 24 * time.Hour)
	fallback := reviewartifact.SummarySourceFallback
	report := Build(Inputs{Reviews: liveFixture(t,
		liveRecord{area: "lock-clearing", id: "lcp_locked", file: "lcp_locked.plan.json", status: "unknown", summary: "A new lock clearing record needs your decision.", source: fallback, created: created, body: snapshot(stillLocked)},
		liveRecord{area: "lock-clearing", id: "lcp_unlocked", file: "lcp_unlocked.plan.json", status: "unknown", summary: "A new lock clearing record needs your decision.", source: fallback, created: created, body: snapshot(unlocked)},
	)}, liveNow)

	wantIDs(t, "needs_you ids", needsYouIDs(report), "lcp_locked")
	wantIDs(t, "subject_gone", setAsideByReason(t, report)["subject_gone"], "lcp_unlocked")
}

// Item 2: what was set aside has somewhere to go. Per reason the projection
// says how many, whether the item is closed (nothing left for anyone to
// decide) or still waiting on someone else, what happens next, and every
// record behind it with its file, so an owner can turn a batch into repair
// work keyed by the batch digest.
func TestHonestListExposesSetAsideReasonsAndRecords(t *testing.T) {
	created := liveNow.Add(-10 * 24 * time.Hour)
	reviews := liveFixture(t,
		liveRecord{area: "content-queue", id: "cq_live", status: "review_required", summary: "Post the spring note?", groupKey: "content-queue|review", created: created},
		liveRecord{area: "content-queue", id: "cq_done", status: "recorded", readiness: "needs_review", summary: "Post the winter note?", groupKey: "content-queue|review", created: created},
		liveRecord{area: "job-repair", id: "jr_1", file: "jr_1.plan.json", status: "unknown", summary: "Restart the sample job?", groupKey: "job-repair|all", created: created},
		liveRecord{area: "job-repair", id: "jr_1", file: "jr_1.apply.terminal.json", status: "unknown", summary: "Restart the sample job?", groupKey: "job-repair|all", created: created},
		liveRecord{area: "repo-cleanup", id: "rc_moved", status: "blocked", band: reviewartifact.BandNeedsYou, readiness: "blocked", summary: "The sample repository moved on.", created: created,
			blocked: "current source-control state changed since this dirty-plan; stored review authority is stale"},
	)
	report := Build(Inputs{Reviews: reviews}, liveNow)
	wire := wireOf(t, report)

	totals := wire["totals"].(map[string]any)
	if closed := wireNumber(t, totals, "closed_without_you"); closed != 3 {
		t.Fatalf("closed_without_you = %d, want 3 (one decided record, two records of a finished change)", closed)
	}
	reasons, _ := totals["set_aside_reasons"].([]any)
	byReason := map[string]map[string]any{}
	for _, raw := range reasons {
		entry := raw.(map[string]any)
		byReason[entry["reason"].(string)] = entry
		if text, _ := entry["next_step"].(string); strings.TrimSpace(text) == "" {
			t.Fatalf("reason %v has no plain next step: %v", entry["reason"], entry)
		}
	}
	if len(byReason) != 3 {
		t.Fatalf("set_aside_reasons = %v, want already_decided, finished_change, no_longer_current", reasons)
	}
	if byReason["finished_change"]["closed"] != true || wireNumber(t, byReason["finished_change"], "count") != 2 {
		t.Fatalf("finished_change = %v, want closed with 2 records", byReason["finished_change"])
	}
	if byReason["no_longer_current"]["closed"] != false {
		t.Fatalf("no_longer_current = %v, want not closed: it waits on a fresh check", byReason["no_longer_current"])
	}

	var finished map[string]any
	for _, raw := range wire["set_aside"].([]any) {
		if entry := raw.(map[string]any); entry["reason"] == "finished_change" {
			finished = entry
		}
	}
	records, _ := finished["records"].([]any)
	if len(records) != 2 {
		t.Fatalf("finished_change records = %v, want both files of the change", finished["records"])
	}
	for _, raw := range records {
		record := raw.(map[string]any)
		path, _ := record["path"].(string)
		if record["id"] != "jr_1" || !filepath.IsAbs(path) || record["created_at"] != created.Format(time.RFC3339) {
			t.Fatalf("record = %v, want id, absolute file path and creation time", record)
		}
	}
	digest, _ := finished["digest"].(string)
	if !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("batch digest = %q, want a sha256 key an owner can use to make repair work once", digest)
	}
	again := wireOf(t, Build(Inputs{Reviews: reviews}, liveNow.Add(time.Hour)))
	for _, raw := range again["set_aside"].([]any) {
		if entry := raw.(map[string]any); entry["reason"] == "finished_change" && entry["digest"] != digest {
			t.Fatalf("digest changed between reads of the same records: %v -> %v", digest, entry["digest"])
		}
	}
}

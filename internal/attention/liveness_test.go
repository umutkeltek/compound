package attention

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"compound/internal/reviewartifact"
	"compound/internal/work"
)

// The live-count tests build review records as real files under a temp home
// and group them with the review queue's own Recount, so the projection sees
// the producer's grouping. They read the new output fields (set_aside, totals,
// freshness) through the JSON wire form every reader consumes, which keeps
// this file compiling against the pre-change API: on that API each test fails
// by assertion, not by a missing symbol.

var liveNow = time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)

type liveRecord struct {
	area      string
	id        string
	file      string // record file name; defaults to <id>.json
	status    string
	summary   string
	source    string // summary source; defaults to writer
	context   string
	groupKey  string
	band      string
	readiness string
	blocked   string
	created   time.Time
	body      map[string]any
	sidecars  map[string]any // file name -> JSON body, written next to the record
}

func liveFixture(t *testing.T, records ...liveRecord) *reviewartifact.QueueReport {
	t.Helper()
	root := filepath.Join(t.TempDir(), "reviews")
	report := &reviewartifact.QueueReport{GeneratedAt: liveNow.Format(time.RFC3339)}
	for _, r := range records {
		dir := filepath.Join(root, filepath.FromSlash(r.area))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		file := r.file
		if file == "" {
			file = r.id + ".json"
		}
		body := map[string]any{"id": r.id, "status": r.status}
		for k, v := range r.body {
			body[k] = v
		}
		path := filepath.Join(dir, file)
		writeJSONFile(t, path, body)
		var extras []string
		names := make([]string, 0, len(r.sidecars))
		for name := range r.sidecars {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			sidecar := filepath.Join(dir, name)
			writeJSONFile(t, sidecar, r.sidecars[name])
			extras = append(extras, sidecar)
		}
		source := r.source
		if source == "" {
			source = reviewartifact.SummarySourceWriter
		}
		created := ""
		if !r.created.IsZero() {
			created = r.created.UTC().Format(time.RFC3339)
		}
		groupKey := r.groupKey
		if groupKey == "" {
			groupKey = r.area + "|" + r.id
		}
		band := r.band
		if band == "" {
			band = reviewartifact.BandForStatus(r.status, r.readiness)
		}
		report.Items = append(report.Items, reviewartifact.QueueItem{
			Area:            r.area,
			ID:              r.id,
			Status:          r.status,
			OperatorSummary: r.summary,
			ContextLabel:    reviewartifact.FirstNonEmpty(r.context, r.area),
			GroupKey:        groupKey,
			Band:            band,
			SummarySource:   source,
			CreatedAt:       created,
			JSONPath:        path,
			ExtraPaths:      extras,
			Readiness:       r.readiness,
			BlockedReason:   r.blocked,
		})
	}
	reviewartifact.Recount(report)
	return report
}

func writeJSONFile(t *testing.T, path string, body any) {
	t.Helper()
	data, err := json.MarshalIndent(body, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func wireOf(t *testing.T, report Report) map[string]any {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// setAsideByReason reads report.set_aside from the wire: reason -> item ids.
func setAsideByReason(t *testing.T, report Report) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	entries, _ := wireOf(t, report)["set_aside"].([]any)
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		if band, _ := entry["band"].(string); band != "set_aside" {
			t.Fatalf("set-aside entry band = %q, want %q: %v", band, "set_aside", entry)
		}
		reason, _ := entry["reason"].(string)
		if text, _ := entry["reason_text"].(string); strings.TrimSpace(text) == "" {
			t.Fatalf("set-aside entry %q carries no plain reason text: %v", reason, entry)
		}
		ids, _ := entry["item_ids"].([]any)
		for _, id := range ids {
			out[reason] = append(out[reason], id.(string))
		}
	}
	for reason := range out {
		sort.Strings(out[reason])
	}
	return out
}

func needsYouIDs(report Report) []string {
	var out []string
	for _, g := range report.Groups {
		if g.Band == BandNeedsYou {
			out = append(out, g.ItemIDs...)
		}
	}
	sort.Strings(out)
	return out
}

func wantIDs(t *testing.T, label string, got []string, want ...string) {
	t.Helper()
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("%s = %v, want %v", label, got, want)
	}
}

func wireNumber(t *testing.T, m map[string]any, key string) int {
	t.Helper()
	v, ok := m[key].(float64)
	if !ok {
		t.Fatalf("wire field %q missing or not a number in %v", key, m)
	}
	return int(v)
}

// Case 1: a record that is already terminal, or that already has a review
// recorded for its current version, is not the operator's. A review recorded
// for an OLDER version (the record was rewritten since) must not hide it.
func TestLiveCountSetsAsideTerminalAndReviewedRecords(t *testing.T) {
	created := liveNow.Add(-72 * time.Hour)
	report := Build(Inputs{Reviews: liveFixture(t,
		liveRecord{area: "content-queue", id: "cq_live", status: "review_required", summary: "Post the spring launch note?", groupKey: "content-queue|review", created: created},
		liveRecord{area: "content-queue", id: "cq_terminal", status: "recorded", readiness: "needs_review", summary: "Post the winter note?", groupKey: "content-queue|review", created: created},
		liveRecord{area: "repo-cleanup", id: "rc_reviewed", status: "review_required", summary: "Tidy the sample repo?", created: created,
			sidecars: map[string]any{"rc_reviewed.reviewed.json": map[string]any{"status": "recorded", "decision": "authorize", "created_at": created.Add(time.Minute).Format(time.RFC3339)}}},
		liveRecord{area: "route-review", id: "rr_rewritten", status: "review_required", summary: "Promote the sample lane?", created: created,
			sidecars: map[string]any{"rr_rewritten.reviewed.json": map[string]any{"status": "recorded", "decision": "hold", "created_at": created.Add(-30 * 24 * time.Hour).Format(time.RFC3339)}}},
	)}, liveNow)

	wantIDs(t, "needs_you ids", needsYouIDs(report), "cq_live", "rr_rewritten")
	if report.Counts.NeedsYou != 2 {
		t.Fatalf("needs_you = %d, want 2 (terminal and reviewed records are not his)", report.Counts.NeedsYou)
	}
	got := setAsideByReason(t, report)
	wantIDs(t, "already_decided", got["already_decided"], "cq_terminal")
	wantIDs(t, "already_reviewed", got["already_reviewed"], "rc_reviewed")
}

// Case 1 (guard): a record carrying a recorded generic "dismissed" resolution
// never counts. The queue files it before attention sees it; this pins that.
func TestLiveCountNeverCountsDismissedResolution(t *testing.T) {
	reviews := liveFixture(t,
		liveRecord{area: "bench", id: "b_dismissed", status: "review_required", summary: "Keep the sample bench?", created: liveNow.Add(-time.Hour)},
	)
	reviews.Items[0].Resolution = &reviewartifact.ReviewResolution{Decision: "dismissed", Status: "recorded"}
	reviewartifact.Recount(reviews)
	report := Build(Inputs{Reviews: reviews}, liveNow)
	if report.Counts.NeedsYou != 0 {
		t.Fatalf("dismissed record counted: needs_you = %d, groups %+v", report.Counts.NeedsYou, report.Groups)
	}
}

// Case 2: one thing counted many times. Several files of one record collapse
// to one item, and the same question filed in separate records collapses to
// one item, each carrying how many records stand behind it.
func TestLiveCountCollapsesRepeatsToOneItem(t *testing.T) {
	created := liveNow.Add(-48 * time.Hour)
	report := Build(Inputs{Reviews: liveFixture(t,
		liveRecord{area: "job-repair", id: "jr_1", file: "jr_1.plan.json", status: "review_required", summary: "Restart the sample sync job?", groupKey: "job-repair|sync", created: created},
		liveRecord{area: "job-repair", id: "jr_1", file: "jr_1.request.json", status: "review_required", summary: "Restart the sample sync job?", groupKey: "job-repair|sync", created: created.Add(time.Minute)},
		liveRecord{area: "job-repair", id: "jr_1", file: "jr_1.proposal.json", status: "review_required", summary: "Restart the sample sync job?", groupKey: "job-repair|sync", created: created.Add(2 * time.Minute)},
		liveRecord{area: "skill-proposals", id: "sp_a", status: "review_required", summary: "Review the sample skill change?", context: "mined proposal", created: created},
		liveRecord{area: "skill-proposals", id: "sp_b", status: "review_required", summary: "Review the sample skill change?", context: "mined proposal", created: created.Add(time.Hour)},
		liveRecord{area: "skill-proposals", id: "sp_c", status: "review_required", summary: "Review the sample skill change?", context: "mined proposal", created: created.Add(2 * time.Hour)},
	)}, liveNow)

	if report.Counts.NeedsYou != 2 || report.Counts.NeedsYouGroups != 2 {
		t.Fatalf("counts = %+v, want 2 live items in 2 groups (one per real question)", report.Counts)
	}
	wantIDs(t, "needs_you ids", needsYouIDs(report), "jr_1", "sp_a")
	for _, g := range wireOf(t, report)["groups"].([]any) {
		group := g.(map[string]any)
		if group["band"] != BandNeedsYou {
			continue
		}
		if records := wireNumber(t, group, "record_count"); records != 3 {
			t.Fatalf("group %v record_count = %d, want 3 underlying records", group["key"], records)
		}
	}
	got := setAsideByReason(t, report)
	wantIDs(t, "duplicate", got["duplicate"], "jr_1", "sp_b", "sp_c")
}

// Case 3: the paperwork of a change that already ran (an owner wrote its
// committed/terminal record) is not a decision.
func TestLiveCountSetsAsideFinishedChangePaperwork(t *testing.T) {
	created := liveNow.Add(-30 * 24 * time.Hour)
	report := Build(Inputs{Reviews: liveFixture(t,
		liveRecord{area: "service-repair", id: "svc_1", file: "svc_1.plan.json", status: "unknown", summary: "Apply the sample service repair?", groupKey: "service-repair|all", created: created},
		liveRecord{area: "service-repair", id: "svc_1.apply.0.committed", file: "svc_1.apply.0.committed.json", status: "unknown", summary: "Apply the sample service repair?", groupKey: "service-repair|all", created: created},
		liveRecord{area: "service-repair", id: "svc_2", file: "act_9.outcome.json", status: "unknown", summary: "Apply the second sample repair?", groupKey: "service-repair|all", created: created},
		liveRecord{area: "service-repair", id: "svc_2", file: "svc_2.plan.json", status: "unknown", summary: "Apply the second sample repair?", groupKey: "service-repair|all", created: created},
		liveRecord{area: "service-repair", id: "svc_3", file: "svc_3.plan.json", status: "unknown", summary: "Apply the third sample repair?", groupKey: "service-repair|all", created: created},
	)}, liveNow)

	wantIDs(t, "needs_you ids", needsYouIDs(report), "svc_3")
	if report.Counts.NeedsYou != 1 {
		t.Fatalf("needs_you = %d, want 1", report.Counts.NeedsYou)
	}
	got := setAsideByReason(t, report)
	// An outcome file named after the action whose id is the plan's (the
	// queue reads a missing id from plan_id) still finishes the plan.
	wantIDs(t, "finished_change", got["finished_change"], "svc_1", "svc_1.apply.0.committed", "svc_2")
}

// Case 4: a record about something that no longer exists — a working copy or
// repository folder that is gone, a lock wait whose waiter has ended — or a
// dirty-repository notice the owner says no longer matches the current state.
func TestLiveCountSetsAsideRecordsWhoseSubjectIsGone(t *testing.T) {
	base := t.TempDir()
	present := filepath.Join(base, "present-copy")
	if err := os.MkdirAll(present, 0o755); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(base, "removed-copy")
	created := liveNow.Add(-10 * 24 * time.Hour)
	report := Build(Inputs{Reviews: liveFixture(t,
		liveRecord{area: "copy-retirement", id: "cr_gone", status: "review_required", summary: "Remove the old sample copy?", created: created, body: map[string]any{"worktree": map[string]any{"path": gone}}},
		liveRecord{area: "copy-retirement", id: "cr_here", status: "review_required", summary: "Remove the current sample copy?", created: created, body: map[string]any{"worktree": map[string]any{"path": present}}},
		liveRecord{area: "repo-cleanup", id: "rc_gone", status: "review_required", summary: "Tidy the removed sample repo?", created: created, body: map[string]any{"git_root": filepath.Join(gone, "repo")}},
		liveRecord{area: "repo-cleanup", id: "rc_moved", status: "blocked", band: reviewartifact.BandNeedsYou, readiness: "blocked", summary: "The repository is still dirty, but this review no longer matches the current state.", created: created,
			blocked: "current source-control state changed since this dirty-plan; stored review authority is stale", body: map[string]any{"git_root": present}},
		liveRecord{area: "lock-waits", id: "lw_ended", status: "review_required", summary: "A sample job has waited 3h for the sample lock.", created: created, body: map[string]any{"waiter_pid": endedPID(t)}},
		liveRecord{area: "lock-waits", id: "lw_waiting", status: "review_required", summary: "A second sample job has waited 2h for the sample lock.", created: created, body: map[string]any{"waiter_pid": os.Getpid()}},
	)}, liveNow)

	wantIDs(t, "needs_you ids", needsYouIDs(report), "cr_here", "lw_waiting")
	got := setAsideByReason(t, report)
	wantIDs(t, "subject_gone", got["subject_gone"], "cr_gone", "lw_ended", "rc_gone")
	wantIDs(t, "no_longer_current", got["no_longer_current"], "rc_moved")
}

// Case 4 (producer shape): lock waits filed by the lock helper straight into
// reviews/lock-waits, read by the real review queue. A resolved wait never
// counts; an open wait whose waiter has ended is set aside; an open wait whose
// waiter still runs counts when its record carries a valid card. The helper's
// current records carry no context label or group key, so the queue marks
// them "fallback" and they wait in the set-aside list as "no question".
func TestLiveCountLockWaitsFromTheReviewQueue(t *testing.T) {
	homePath := t.TempDir()
	dir := filepath.Join(homePath, "reviews", "lock-waits")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	wait := func(id, status string, pid int, card bool, extra map[string]any) {
		doc := map[string]any{
			"schema_version":   "lock-helper.wait.v1",
			"id":               id,
			"status":           status,
			"created_at":       liveNow.Add(-3 * time.Hour).Format(time.RFC3339),
			"title":            "Lock queue: sample-job waited 3h for sample-lock",
			"operator_summary": "Sample job " + id + " has waited 3 hours for the sample lock.",
			"next_action":      "Nothing to do while it shrinks. Run the status command to see holders.",
			"owner":            "sample-job",
			"waiter_pid":       pid,
		}
		if card {
			doc["context_label"] = "sample lock"
			doc["group_key"] = "lock-waits|" + id
		}
		for k, v := range extra {
			doc[k] = v
		}
		writeJSONFile(t, filepath.Join(dir, id+".json"), doc)
	}
	wait("lockwait-sample-1", "review_required", endedPID(t), true, nil)
	wait("lockwait-sample-2", "resolved", endedPID(t), true, map[string]any{"resolution": "waiter gone", "resolved_at": liveNow.Add(-time.Hour).Format(time.RFC3339)})
	wait("lockwait-sample-3", "review_required", os.Getpid(), true, nil)
	wait("lockwait-sample-4", "review_required", os.Getpid(), false, nil)
	reviews, err := reviewartifact.Queue(homePath, reviewartifact.QueueOptions{}, liveNow)
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	report := Build(Inputs{Reviews: &reviews}, liveNow)

	wantIDs(t, "needs_you ids", needsYouIDs(report), "lockwait-sample-3")
	got := setAsideByReason(t, report)
	wantIDs(t, "subject_gone", got["subject_gone"], "lockwait-sample-1")
	wantIDs(t, "no_question", got["no_question"], "lockwait-sample-4")
	for reason, ids := range got {
		for _, id := range ids {
			if id == "lockwait-sample-2" {
				t.Fatalf("resolved wait listed as %s; a resolved wait is never a candidate", reason)
			}
		}
	}
}

// Case 4/2: files kept inside another record's folder (an area nested under an
// area that holds records) are the inside of one record, not decisions.
func TestLiveCountSetsAsideFilesInsideARecordFolder(t *testing.T) {
	created := liveNow.Add(-20 * 24 * time.Hour)
	report := Build(Inputs{Reviews: liveFixture(t,
		liveRecord{area: "repo-cleanup", id: "rc_parent", status: "recorded", summary: "Sample repo review recorded.", created: created},
		liveRecord{area: "repo-cleanup/rc_parent.preimage", id: "activity", status: "unknown", summary: "Tidy the sample repo?", groupKey: "folder", created: created},
		liveRecord{area: "repo-cleanup/rc_parent.preimage", id: "leases", status: "unknown", summary: "Tidy the sample repo again?", groupKey: "folder", created: created},
		liveRecord{area: "nested-area/proofs", id: "np_live", status: "review_required", summary: "Accept the sample proof?", created: created},
	)}, liveNow)

	wantIDs(t, "needs_you ids", needsYouIDs(report), "np_live")
	got := setAsideByReason(t, report)
	wantIDs(t, "record_folder_file", got["record_folder_file"], "activity", "leases")
}

// Case 5: test and demo data that reached the real store is not a decision.
func TestLiveCountSetsAsideTestData(t *testing.T) {
	created := liveNow.Add(-5 * 24 * time.Hour)
	report := Build(Inputs{Reviews: liveFixture(t,
		liveRecord{area: "route-comparisons", id: "rcmp_sample", status: "review_required", summary: "Replace model-a with model-b?", created: created,
			body: map[string]any{"decision_id": "decision_route_change_fixture", "incumbent_route": map[string]any{"provider": "fixture-provider", "model": "model-a"}}},
		liveRecord{area: "bench", id: "20261101T000000Z-demo", status: "review_required", summary: "Accept the one-question bench run?", created: created,
			body: map[string]any{"suite": "demo", "build_sha": "dev"}},
		liveRecord{area: "content-queue", id: "cq_demo_video", status: "review_required", summary: "Post the sample demo video?", created: created,
			body: map[string]any{"suite": "demo", "build_sha": "1a2b3c4"}},
	)}, liveNow)

	wantIDs(t, "needs_you ids", needsYouIDs(report), "cq_demo_video")
	got := setAsideByReason(t, report)
	wantIDs(t, "test_data", got["test_data"], "20261101T000000Z-demo", "rcmp_sample")
}

// Case 6: items put off with a date all came back on that date. They are not
// new: the one real question shows once, marked with the date that brought it
// back, and keeps its original age.
func TestLiveCountPostponedItemsComeBackOnceAndMarked(t *testing.T) {
	created := liveNow.Add(-90 * 24 * time.Hour)
	deferred := func(id string) liveRecord {
		return liveRecord{area: "skill-proposals", id: id, status: "review_required", summary: "Review the sample skill change?", context: "mined proposal", created: created,
			sidecars: map[string]any{id + ".cld_1.decision.json": map[string]any{"status": "recorded", "decision": "defer", "defer_until": "2026-11-15T00:00:00Z"}}}
	}
	report := Build(Inputs{Reviews: liveFixture(t, deferred("sp_1"), deferred("sp_2"), deferred("sp_3"))}, liveNow)

	if report.Counts.NeedsYou != 1 || report.Counts.NeedsYouGroups != 1 {
		t.Fatalf("counts = %+v, want the postponed question once", report.Counts)
	}
	group := wireOf(t, report)["groups"].([]any)[0].(map[string]any)
	if group["postponed_until"] != "2026-11-15T00:00:00Z" {
		t.Fatalf("postponed_until = %v, want the date that brought it back", group["postponed_until"])
	}
	if group["oldest_created_at"] != created.Format(time.RFC3339) {
		t.Fatalf("oldest_created_at = %v, want the original %s (not new)", group["oldest_created_at"], created.Format(time.RFC3339))
	}
}

// Case 7: a record that cannot say what it is asking is not shown as a
// decision; it waits in the set-aside list for an agent to word it.
func TestLiveCountSetsAsideRecordsWithNoQuestion(t *testing.T) {
	created := liveNow.Add(-3 * 24 * time.Hour)
	report := Build(Inputs{Reviews: liveFixture(t,
		liveRecord{area: "job-repair", id: "jr_blank", status: "unknown", summary: "A new job repair record needs your decision.", source: reviewartifact.SummarySourceFallback, groupKey: "job-repair|review", created: created},
		liveRecord{area: "job-repair", id: "jr_blank_2", status: "unknown", summary: "A new job repair record needs your decision.", source: reviewartifact.SummarySourceFallback, groupKey: "job-repair|review", created: created},
		liveRecord{area: "job-repair", id: "jr_asks", status: "review_required", summary: "Restart the sample sync job now?", created: created},
	)}, liveNow)

	wantIDs(t, "needs_you ids", needsYouIDs(report), "jr_asks")
	got := setAsideByReason(t, report)
	wantIDs(t, "no_question", got["no_question"], "jr_blank", "jr_blank_2")
}

// Case 7 (guard against hiding a real ask): an open product record whose
// writer gave it no question stays counted and is marked needs_wording,
// because a real product decision hidden from the operator costs more than an
// unworded card. Generic fallback words never make two records one question.
func TestLiveCountKeepsUnwordedOpenProductAsksMarked(t *testing.T) {
	created := liveNow.Add(-24 * time.Hour)
	generic := "A new product content queue record needs your decision."
	report := Build(Inputs{Reviews: liveFixture(t,
		liveRecord{area: "product-content-queue", id: "pcq_new_posts", status: "review_required", readiness: "needs_review", summary: generic, source: reviewartifact.SummarySourceFallback, groupKey: "product-content-queue|review", created: created},
		liveRecord{area: "product-content-queue", id: "pcq_other_posts", status: "review_required", readiness: "needs_review", summary: generic, source: reviewartifact.SummarySourceFallback, groupKey: "product-content-queue|review", created: created.Add(time.Hour)},
		liveRecord{area: "product-content-queue", id: "pcq_stuck", status: "reconciliation_required", readiness: "needs_review", summary: generic, source: reviewartifact.SummarySourceFallback, groupKey: "product-content-queue|review", created: created},
	)}, liveNow)

	wantIDs(t, "needs_you ids", needsYouIDs(report), "pcq_new_posts", "pcq_other_posts")
	group := wireOf(t, report)["groups"].([]any)[0].(map[string]any)
	if group["needs_wording"] != true {
		t.Fatalf("needs_wording = %v, want true on a group counted without a question", group["needs_wording"])
	}
	got := setAsideByReason(t, report)
	wantIDs(t, "no_question", got["no_question"], "pcq_stuck")
}

// Case 8: the output says when each source was read and how new its newest
// record is, and stamps the whole report with its stalest source.
func TestLiveCountStampsFreshnessPerSourceAndOverall(t *testing.T) {
	reviews := liveFixture(t,
		liveRecord{area: "content-queue", id: "cq_live", status: "review_required", summary: "Post the sample note?", created: liveNow.Add(-6 * time.Hour)},
	)
	reviews.GeneratedAt = liveNow.Add(-10 * time.Minute).Format(time.RFC3339)
	report := Build(Inputs{
		Reviews:   reviews,
		Workbench: &work.WorkbenchReport{GeneratedAt: liveNow.Add(-1 * time.Minute).Format(time.RFC3339)},
	}, liveNow)

	freshness, ok := wireOf(t, report)["freshness"].(map[string]any)
	if !ok {
		t.Fatal("report carries no freshness stamp")
	}
	if freshness["as_of"] != liveNow.Add(-10*time.Minute).Format(time.RFC3339) || freshness["oldest_source"] != SourceReviews {
		t.Fatalf("overall stamp = %v / %v, want the stalest source (reviews, 10 min old)", freshness["as_of"], freshness["oldest_source"])
	}
	if age := wireNumber(t, freshness, "as_of_age_seconds"); age != 600 {
		t.Fatalf("as_of_age_seconds = %d, want 600", age)
	}
	sources := map[string]map[string]any{}
	for _, raw := range freshness["sources"].([]any) {
		entry := raw.(map[string]any)
		sources[entry["source"].(string)] = entry
	}
	if sources[SourceReviews]["newest_record_at"] != liveNow.Add(-6*time.Hour).Format(time.RFC3339) {
		t.Fatalf("reviews newest_record_at = %v, want the newest record time", sources[SourceReviews]["newest_record_at"])
	}
	if wireNumber(t, sources[SourceWorkbench], "age_seconds") != 60 {
		t.Fatalf("workbench age = %v, want 60 s", sources[SourceWorkbench]["age_seconds"])
	}
	missing, _ := freshness["missing_sources"].([]any)
	if len(missing) != 1 || missing[0] != SourceExecutor {
		t.Fatalf("missing_sources = %v, want [executor] (not read in this report)", missing)
	}
}

// A live record keeps counting, and every unit read is either live or set aside
// with a reason: live + set aside = total read, held work included.
func TestLiveCountTotalsAddUpAndLiveItemsStillCount(t *testing.T) {
	created := liveNow.Add(-4 * 24 * time.Hour)
	reviews := liveFixture(t,
		liveRecord{area: "content-queue", id: "cq_live", status: "review_required", summary: "Post the spring launch note?", groupKey: "content-queue|review", created: created},
		liveRecord{area: "content-queue", id: "cq_done", status: "recorded", readiness: "needs_review", summary: "Post the winter note?", groupKey: "content-queue|review", created: created},
		liveRecord{area: "job-repair", id: "jr_blank", status: "unknown", summary: "A new job repair record needs your decision.", source: reviewartifact.SummarySourceFallback, created: created},
		liveRecord{area: "skill-proposals", id: "sp_a", status: "review_required", summary: "Review the sample skill change?", context: "mined", created: created},
		liveRecord{area: "skill-proposals", id: "sp_b", status: "review_required", summary: "Review the sample skill change?", context: "mined", created: created.Add(time.Hour)},
	)
	readBefore := 0
	for _, g := range reviews.Groups {
		if g.Band == reviewartifact.BandNeedsYou {
			readBefore += g.Count
		}
	}
	report := Build(Inputs{
		Reviews:   reviews,
		Workbench: &work.WorkbenchReport{GeneratedAt: liveNow.Format(time.RFC3339), Open: []work.WorkbenchItem{{WorkID: "work_held_sample", State: "held", OpenReason: "operator decision required"}}},
	}, liveNow)

	wantIDs(t, "needs_you ids", needsYouIDs(report), "cq_live", "sp_a", "work_held_sample")
	totals, ok := wireOf(t, report)["totals"].(map[string]any)
	if !ok {
		t.Fatal("report carries no totals")
	}
	live, setAside, read := wireNumber(t, totals, "live_decisions"), wireNumber(t, totals, "set_aside"), wireNumber(t, totals, "total_read")
	if read != readBefore+1 {
		t.Fatalf("total_read = %d, want %d review records + 1 held work item", read, readBefore)
	}
	if live != report.Counts.NeedsYou || live != 3 {
		t.Fatalf("live_decisions = %d, needs_you = %d, want both 3", live, report.Counts.NeedsYou)
	}
	if live+setAside != read {
		t.Fatalf("live %d + set aside %d != total read %d", live, setAside, read)
	}
	byReason, _ := totals["set_aside_by_reason"].(map[string]any)
	sum := 0
	for _, n := range byReason {
		sum += int(n.(float64))
	}
	if sum != setAside || int(byReason["already_decided"].(float64)) != 1 || int(byReason["no_question"].(float64)) != 1 || int(byReason["duplicate"].(float64)) != 1 {
		t.Fatalf("set_aside_by_reason = %v (sum %d), want already_decided 1, no_question 1, duplicate 1 summing to %d", byReason, sum, setAside)
	}
}

// Existing readers: every compound.attention.v0.1 field keeps its name and
// type, set-aside entries never enter groups (each group there is surfaced
// by the console, narrate, the notifier and the smoke), and a group the
// projection cannot match record by record is kept whole, never hidden.
func TestLiveCountKeepsTheV01WireShapeForExistingReaders(t *testing.T) {
	created := liveNow.Add(-2 * 24 * time.Hour)
	reviews := liveFixture(t,
		liveRecord{area: "content-queue", id: "cq_live", status: "review_required", summary: "Post the spring launch note?", created: created},
		liveRecord{area: "job-repair", id: "jr_blank", status: "unknown", summary: "A new job repair record needs your decision.", source: reviewartifact.SummarySourceFallback, created: created},
	)
	// A group with no matching items (as a hand-built or partial report has).
	reviews.Groups = append(reviews.Groups, reviewartifact.QueueGroup{Key: "opaque", Area: "panel-verdicts", Band: reviewartifact.BandNeedsYou, Count: 2, OperatorSummary: "Two verdicts need review.", SummarySource: reviewartifact.SummarySourceFallback, ItemIDs: []string{"pv_1", "pv_2"}})
	report := Build(Inputs{Reviews: reviews}, liveNow)
	wire := wireOf(t, report)

	if wire["schema_version"] != SchemaVersion || SchemaVersion != "compound.attention.v0.1" {
		t.Fatalf("schema_version = %v", wire["schema_version"])
	}
	counts := wire["counts"].(map[string]any)
	for _, key := range []string{"needs_you", "needs_you_groups", "actionable_now", "watching", "blocked_on_you", "fallback_groups"} {
		wireNumber(t, counts, key)
	}
	groupKeys := []string{"key", "area", "band", "source", "kind", "count", "operator_summary", "operator_unlock", "blast_radius", "reversibility", "leverage", "oldest_created_at", "action", "terminal_reason", "item_ids", "freshness"}
	for _, raw := range wire["groups"].([]any) {
		group := raw.(map[string]any)
		for _, key := range groupKeys {
			if _, ok := group[key]; !ok {
				t.Fatalf("group %v lost v0.1 field %q", group["key"], key)
			}
		}
		if group["band"] == "set_aside" {
			t.Fatalf("set-aside entry leaked into groups: %v", group)
		}
	}
	if top, ok := wire["top"].(map[string]any); !ok || top["band"] == "set_aside" {
		t.Fatalf("top = %v, want a surfaced group", wire["top"])
	}
	opaque := groupByKey(t, report, "opaque")
	if opaque.Band != BandNeedsYou || opaque.Count != 2 {
		t.Fatalf("unmatched group = %+v, want kept whole (band needs_you, count 2)", opaque)
	}
	if report.Counts.NeedsYou != 3 {
		t.Fatalf("needs_you = %d, want 1 live record + 2 kept unchecked", report.Counts.NeedsYou)
	}
}

// endedPID returns the id of a process that has already exited.
func endedPID(t *testing.T) int {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	if err := cmd.Run(); err != nil {
		t.Fatalf("start short process: %v", err)
	}
	return cmd.Process.Pid
}

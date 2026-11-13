package reviewartifact

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var queueEquivalenceNow = time.Date(2026, 7, 23, 11, 0, 0, 0, time.UTC)

// buildQueueEquivalenceFixture plants one crowded review area (the shape that
// made the per-item directory re-reads quadratic) plus a small second area and
// a nested area. Items carry every sidecar the queue reads (markdown, extras,
// owner results, briefs, resolutions) and every work-binding shape: none, one,
// a superseded chain, a digest drift, a name with glob meta characters, and a
// malformed glob pattern.
func buildQueueEquivalenceFixture(t *testing.T, homePath string) {
	t.Helper()
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	core := func(area, id, status string, i int) string {
		t.Helper()
		payload, err := json.Marshal(map[string]any{
			"id":            id,
			"status":        status,
			"created_at":    queueEquivalenceNow.Add(-time.Duration(i) * time.Minute).Format(time.RFC3339),
			"evidence_refs": []string{"evidence:" + id},
			"next_action":   "review " + id,
		})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(homePath, "reviews", filepath.FromSlash(area), id+".json")
		write(path, string(payload)+"\n")
		return path
	}
	bind := func(area, id, path, source, fallback, supersedes string) ReviewWorkBinding {
		t.Helper()
		binding, _, err := RecordWorkBinding(homePath, RecordWorkBindingInput{
			Area: area, ReviewID: id, CoreReviewPath: path,
			SourceWorkID: source, FallbackWorkID: fallback, SupersedesBindingID: supersedes,
		}, queueEquivalenceNow)
		if err != nil {
			t.Fatalf("bind %s: %v", id, err)
		}
		return binding
	}

	const crowded = "worktree-retirement"
	statuses := []string{"review_required", "proposed", "blocked", "applied"}
	for i := 0; i < 36; i++ {
		id := fmt.Sprintf("review_eqv_%02d", i)
		path := core(crowded, id, statuses[i%len(statuses)], i)
		stem := strings.TrimSuffix(path, ".json")
		switch i % 9 {
		case 0:
			bind(crowded, id, path, "work_eqv_source", "work_eqv_area", "")
		case 1:
			first := bind(crowded, id, path, "", "work_eqv_area", "")
			bind(crowded, id, path, "", "work_eqv_area_two", first.ID)
		case 2:
			bind(crowded, id, path, "work_eqv_source", "", "")
			// Rewrite the core review after binding: the head no longer binds
			// the current bytes, so the queue must report digest drift.
			write(path, `{"id":"`+id+`","status":"review_required","note":"rewritten"}`+"\n")
		case 3:
			write(stem+".md", "# "+id+"\n")
			write(stem+".preimage", "preimage\n")
			write(stem+".result.json", `{"id":"res_`+id+`","status":"completed"}`+"\n")
		case 4:
			write(stem+".result.json", `{"id":"res_`+id+`","status":"running"}`+"\n")
			write(stem+".log.txt", "log\n")
		case 5:
			write(filepath.Join(filepath.Dir(path), DecisionBriefSidecarName(id)), `{"schema_version":"wrong"}`+"\n")
		case 6:
			write(stem+".reviewed.json", `{"schema_version":"`+ReviewResolutionSchemaVersion+`","id":"not_rres"}`+"\n")
		case 7:
			write(stem+".md", "# "+id+"\n")
			bind(crowded, id, path, "", "work_eqv_area", "")
		}
	}
	// Names that are glob patterns: "[x]" matches only the letter x, so its
	// binding is never found by the glob; "bad[" is a malformed pattern.
	metaPath := core(crowded, "review_eqv_[x]", "review_required", 40)
	write(strings.TrimSuffix(metaPath, ".json")+".work-binding.bind_unused.json", "{}\n")
	core(crowded, "review_eqv_bad[", "review_required", 41)
	write(filepath.Join(homePath, "reviews", crowded, "review_eqv_array.json"), "[1,2,3]\n")

	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("panel_eqv_%02d", i)
		path := core("panel-verdicts", id, statuses[(i+1)%len(statuses)], 50+i)
		if i%2 == 0 {
			bind("panel-verdicts", id, path, "work_eqv_source", "work_eqv_panel", "")
		}
	}
	core(crowded+"/nested-evidence", "nested_eqv_00", "review_required", 60)
}

// TestQueueDirectoryListingMatchesPerItemReads compares Queue (one directory
// listing per call) with the reference path (every item re-reads its
// directory) byte for byte. Losing an extra path, a binding, a binding error,
// a warning or a row fails it.
func TestQueueDirectoryListingMatchesPerItemReads(t *testing.T) {
	homePath := t.TempDir()
	buildQueueEquivalenceFixture(t, homePath)
	for _, opt := range []QueueOptions{{}, {Area: "worktree-retirement"}, {Area: "panel-verdicts"}, {Area: "worktree-retirement/nested-evidence"}} {
		fast, err := Queue(homePath, opt, queueEquivalenceNow)
		if err != nil {
			t.Fatalf("area %q fast: %v", opt.Area, err)
		}
		ref, err := queue(homePath, opt, queueEquivalenceNow, nil)
		if err != nil {
			t.Fatalf("area %q reference: %v", opt.Area, err)
		}
		fastJSON, err := json.MarshalIndent(fast, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		refJSON, err := json.MarshalIndent(ref, "", " ")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(fastJSON, refJSON) {
			fastLines, refLines := strings.Split(string(fastJSON), "\n"), strings.Split(string(refJSON), "\n")
			for i := 0; i < len(fastLines) && i < len(refLines); i++ {
				if fastLines[i] != refLines[i] {
					t.Fatalf("area %q: listing path differs at line %d\nfast:      %s\nreference: %s", opt.Area, i+1, fastLines[i], refLines[i])
				}
			}
			t.Fatalf("area %q: listing path differs in length: %d vs %d lines", opt.Area, len(fastLines), len(refLines))
		}
	}

	// The fixture must keep exercising what the listing answers.
	report, err := Queue(homePath, QueueOptions{}, queueEquivalenceNow)
	if err != nil {
		t.Fatal(err)
	}
	var bound, bindingErrors, withExtras int
	for _, item := range report.Items {
		if item.WorkBinding != nil {
			bound++
		}
		if item.WorkBindingError != "" {
			bindingErrors++
		}
		if len(item.ExtraPaths) > 0 {
			withExtras++
		}
	}
	if len(report.Items) != 51 || bound < 10 || bindingErrors < 5 || withExtras < 8 || len(report.Warnings) != 1 {
		t.Fatalf("fixture lost its shape: items=%d bound=%d binding_errors=%d with_extras=%d warnings=%q",
			len(report.Items), bound, bindingErrors, withExtras, report.Warnings)
	}
}

// TestQueueReadsEachReviewDirectoryOnce proves the cost cut: the listing holds
// one entry per directory that holds a queued item, however many items it has.
func TestQueueReadsEachReviewDirectoryOnce(t *testing.T) {
	homePath := t.TempDir()
	buildQueueEquivalenceFixture(t, homePath)
	listing := newQueueDirListing()
	if _, err := queue(homePath, QueueOptions{}, queueEquivalenceNow, listing); err != nil {
		t.Fatal(err)
	}
	if len(listing.dirs) != 3 {
		dirs := make([]string, 0, len(listing.dirs))
		for dir := range listing.dirs {
			dirs = append(dirs, dir)
		}
		t.Fatalf("listed %d directories, want 3 (one per review directory): %q", len(listing.dirs), dirs)
	}
}

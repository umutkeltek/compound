package reviewqueue

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
	"compound/internal/reviewartifact"
	"compound/internal/work"
)

// buildReviewEquivalenceFixture plants a crowded review area whose items share
// a few source and fallback works (live, terminal, missing, wrong-surface), so
// the authority stamp reads the same work IDs many times.
func buildReviewEquivalenceFixture(t *testing.T) string {
	t.Helper()
	homePath := t.TempDir()
	if err := home.Init(homePath, "test", reviewAuthorityTestNow); err != nil {
		t.Fatal(err)
	}
	store, err := work.Open(homePath)
	if err != nil {
		t.Fatal(err)
	}
	const area = "panel-verdicts"
	createReviewAuthorityWork(t, store, "work_eqv_live", "", reviewAuthorityTestNow)
	createReviewAuthorityWork(t, store, "work_eqv_closed", "", reviewAuthorityTestNow)
	closeReviewAuthorityWork(t, store, "work_eqv_closed", reviewAuthorityTestNow.Add(time.Minute))
	createReviewAuthorityWork(t, store, "work_eqv_area", ReviewAreaSurface(area), reviewAuthorityTestNow)
	createReviewAuthorityWork(t, store, "work_eqv_wrong_surface", ReviewAreaSurface("other-area"), reviewAuthorityTestNow)

	dir := filepath.Join(homePath, "reviews", area)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bindings := [][2]string{
		{"work_eqv_live", ""},
		{"work_eqv_closed", "work_eqv_area"},
		{"work_eqv_closed", ""},
		{"work_eqv_missing", "work_eqv_area"},
		{"", "work_eqv_wrong_surface"},
		{"", "work_eqv_area"},
		{"", ""},
	}
	for i := 0; i < 28; i++ {
		id := fmt.Sprintf("review_eqv_%02d", i)
		path := filepath.Join(dir, id+".json")
		body := fmt.Sprintf(`{"id":%q,"status":"review_required","created_at":%q}`+"\n", id, reviewAuthorityTestNow.Add(-time.Duration(i)*time.Minute).Format(time.RFC3339))
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		pair := bindings[i%len(bindings)]
		if pair[0] == "" && pair[1] == "" {
			continue
		}
		if _, _, err := reviewartifact.RecordWorkBinding(homePath, reviewartifact.RecordWorkBindingInput{
			Area: area, ReviewID: id, CoreReviewPath: path, SourceWorkID: pair[0], FallbackWorkID: pair[1],
		}, reviewAuthorityTestNow); err != nil {
			t.Fatalf("bind %s: %v", id, err)
		}
	}
	return homePath
}

// TestBuildMemoizedWorkReadsMatchReference compares Build (one work read per
// ID per stamping pass) with the reference path (one store read per lookup),
// first as built, then with producer actions on every item and a batch action
// on every group so each stamp outcome (signed, terminal, missing, unbound,
// mismatched) appears. Any lost row, warning or stamp fails it.
func TestBuildMemoizedWorkReadsMatchReference(t *testing.T) {
	homePath := buildReviewEquivalenceFixture(t)
	now := reviewAuthorityTestNow.Add(time.Hour)
	fast, err := build(homePath, Options{Now: now}, true)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := build(homePath, Options{Now: now}, false)
	if err != nil {
		t.Fatal(err)
	}
	assertSameReviewJSON(t, "build", fast, ref)

	withActions := func(report reviewartifact.QueueReport) reviewartifact.QueueReport {
		for i := range report.Items {
			workIDs := []string{"work_eqv_live", "work_eqv_area", "work_eqv_closed", "work_eqv_missing"}
			report.Items[i].Actions = []reviewartifact.Action{reviewAuthorityActionSeed(workIDs[i%len(workIDs)], report.Items[i].ID)}
		}
		for i := range report.Groups {
			action := reviewAuthorityActionSeed("work_eqv_area", report.Groups[i].Key)
			report.Groups[i].SafeBatchAction = &action
		}
		return report
	}
	fastActions, refActions := withActions(fast), withActions(ref)
	if err := stampReviewActionAuthority(homePath, &fastActions, now, true); err != nil {
		t.Fatal(err)
	}
	if err := stampReviewActionAuthority(homePath, &refActions, now, false); err != nil {
		t.Fatal(err)
	}
	if err := stampReviewGroupActionAuthority(homePath, &fastActions, now, true); err != nil {
		t.Fatal(err)
	}
	if err := stampReviewGroupActionAuthority(homePath, &refActions, now, false); err != nil {
		t.Fatal(err)
	}
	assertSameReviewJSON(t, "stamped actions", fastActions, refActions)

	var signed, blocked int
	for _, item := range fastActions.Items {
		for _, action := range item.Actions {
			if action.PolicyDecision != nil {
				signed++
			}
		}
		for _, warning := range item.Warnings {
			if strings.Contains(warning, "blocked") {
				blocked++
				break
			}
		}
	}
	if len(fastActions.Items) != 28 || signed == 0 || blocked == 0 || len(fastActions.Groups) == 0 {
		t.Fatalf("fixture lost its shape: items=%d signed=%d blocked=%d groups=%d", len(fastActions.Items), signed, blocked, len(fastActions.Groups))
	}
}

func assertSameReviewJSON(t *testing.T, name string, got, want any) {
	t.Helper()
	gotJSON, err := json.MarshalIndent(got, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.MarshalIndent(want, "", " ")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(gotJSON, wantJSON) {
		return
	}
	gotLines, wantLines := strings.Split(string(gotJSON), "\n"), strings.Split(string(wantJSON), "\n")
	for i := 0; i < len(gotLines) && i < len(wantLines); i++ {
		if gotLines[i] != wantLines[i] {
			t.Fatalf("%s: memoized path differs at line %d\nfast:      %s\nreference: %s", name, i+1, gotLines[i], wantLines[i])
		}
	}
	t.Fatalf("%s: memoized path differs in length: %d vs %d lines", name, len(gotLines), len(wantLines))
}

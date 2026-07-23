package reviewartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var workBindingTestNow = time.Date(2026, 7, 23, 10, 0, 0, 0, time.UTC)

func TestRecordWorkBindingPreservesCoreAndLoadsOneCurrentHead(t *testing.T) {
	homePath, reviewPath, original := workBindingFixture(t, "panel-verdicts", "pver_test")
	in := RecordWorkBindingInput{
		Area:           "panel-verdicts",
		ReviewID:       "pver_test",
		CoreReviewPath: reviewPath,
		SourceWorkID:   "work_source_owner",
		SourceRun:      &ReviewSourceRun{Kind: "panel_run", ID: "prun_test", WorkID: "work_source_owner"},
		FallbackWorkID: "work_panel_area_owner",
		EvidenceRefs:   []string{"run:prun_test", "work:work_source_owner"},
	}
	first, firstPath, err := RecordWorkBinding(homePath, in, workBindingTestNow)
	if err != nil {
		t.Fatal(err)
	}
	again, againPath, err := RecordWorkBinding(homePath, in, workBindingTestNow.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || again.CreatedAt != first.CreatedAt || againPath != firstPath {
		t.Fatalf("idempotent record changed binding: first=%+v path=%s again=%+v path=%s", first, firstPath, again, againPath)
	}
	if got, err := os.ReadFile(reviewPath); err != nil || string(got) != string(original) {
		t.Fatalf("recording rewrote sealed core review: err=%v before=%s after=%s", err, original, got)
	}

	current, err := LoadCurrentWorkBinding(homePath, WorkBindingTarget{
		Area: "panel-verdicts", ReviewID: "pver_test", CoreReviewPath: reviewPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.ID != first.ID || current.SourceWorkID != "work_source_owner" ||
		current.FallbackWorkID != "work_panel_area_owner" || current.SourceRun == nil ||
		current.SourceRun.ID != "prun_test" {
		t.Fatalf("current binding = %+v", current)
	}
	sum := sha256.Sum256(original)
	if current.ReviewSHA256 != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("review digest = %q", current.ReviewSHA256)
	}

	report, err := Queue(homePath, QueueOptions{Area: "panel-verdicts"}, workBindingTestNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Items) != 1 {
		t.Fatalf("binding sidecar became a core review item: %+v", report.Items)
	}
	if report.Items[0].WorkBinding == nil || report.Items[0].WorkBinding.ID != first.ID {
		t.Fatalf("queue did not attach current binding: %+v", report.Items[0])
	}
	if !containsString(report.Items[0].ExtraPaths, firstPath) {
		t.Fatalf("binding sidecar missing from companion paths: %v", report.Items[0].ExtraPaths)
	}
}

func TestRecordWorkBindingRequiresExactSupersessionAndRejectsForks(t *testing.T) {
	homePath, reviewPath, _ := workBindingFixture(t, "decision-candidates", "dcp_test")
	base := RecordWorkBindingInput{
		Area:           "decision-candidates",
		ReviewID:       "dcp_test",
		CoreReviewPath: reviewPath,
		SourceWorkID:   "work_source",
		FallbackWorkID: "work_area",
	}
	first, _, err := RecordWorkBinding(homePath, base, workBindingTestNow)
	if err != nil {
		t.Fatal(err)
	}

	changed := base
	changed.FallbackWorkID = "work_area_next"
	if _, _, err := RecordWorkBinding(homePath, changed, workBindingTestNow.Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "supersedes") {
		t.Fatalf("conflicting write without exact supersession = %v", err)
	}
	changed.SupersedesBindingID = first.ID
	second, _, err := RecordWorkBinding(homePath, changed, workBindingTestNow.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	current, err := LoadCurrentWorkBinding(homePath, WorkBindingTarget{
		Area: "decision-candidates", ReviewID: "dcp_test", CoreReviewPath: reviewPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if current == nil || current.ID != second.ID || current.SupersedesBindingID != first.ID {
		t.Fatalf("current superseding binding = %+v", current)
	}

	fork := second
	fork.FallbackWorkID = "work_fork"
	fork.SupersedesBindingID = first.ID
	fork.CreatedAt = workBindingTestNow.Add(2 * time.Minute).Format(time.RFC3339)
	fork.ID = workBindingID(fork)
	forkPath := workBindingPath(reviewPath, fork.ID)
	data, err := json.MarshalIndent(fork, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(forkPath, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCurrentWorkBinding(homePath, WorkBindingTarget{
		Area: "decision-candidates", ReviewID: "dcp_test", CoreReviewPath: reviewPath,
	}); err == nil || !strings.Contains(err.Error(), "fork") {
		t.Fatalf("forked binding chain loaded: %v", err)
	}
}

func TestLoadCurrentWorkBindingRejectsDigestPathAndSourceRunDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, homePath, reviewPath, bindingPath string)
		want   string
	}{
		{
			name: "core digest drift",
			mutate: func(t *testing.T, _, reviewPath, _ string) {
				t.Helper()
				if err := os.WriteFile(reviewPath, []byte(`{"id":"pver_test","status":"changed"}`+"\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "digest",
		},
		{
			name: "bound path drift",
			mutate: func(t *testing.T, _, _, bindingPath string) {
				t.Helper()
				rewriteBindingFixture(t, bindingPath, func(binding *ReviewWorkBinding) {
					binding.ReviewPath = "reviews/other-area/other.json"
				})
			},
			want: "path",
		},
		{
			name: "source run work mismatch",
			mutate: func(t *testing.T, _, _, bindingPath string) {
				t.Helper()
				rewriteBindingFixture(t, bindingPath, func(binding *ReviewWorkBinding) {
					binding.SourceRun.WorkID = "work_different"
				})
			},
			want: "source_run",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			homePath, reviewPath, _ := workBindingFixture(t, "panel-verdicts", "pver_test")
			_, bindingPath, err := RecordWorkBinding(homePath, RecordWorkBindingInput{
				Area:           "panel-verdicts",
				ReviewID:       "pver_test",
				CoreReviewPath: reviewPath,
				SourceWorkID:   "work_source",
				SourceRun:      &ReviewSourceRun{Kind: "panel_run", ID: "prun_test", WorkID: "work_source"},
				FallbackWorkID: "work_area",
			}, workBindingTestNow)
			if err != nil {
				t.Fatal(err)
			}
			tc.mutate(t, homePath, reviewPath, bindingPath)
			if _, err := LoadCurrentWorkBinding(homePath, WorkBindingTarget{
				Area: "panel-verdicts", ReviewID: "pver_test", CoreReviewPath: reviewPath,
			}); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("drift loaded: %v", err)
			}
		})
	}
}

func workBindingFixture(t *testing.T, area, reviewID string) (string, string, []byte) {
	t.Helper()
	homePath := t.TempDir()
	dir := filepath.Join(homePath, "reviews", area)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"id":"` + reviewID + `","status":"review_required"}` + "\n")
	path := filepath.Join(dir, reviewID+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return homePath, path, data
}

func rewriteBindingFixture(t *testing.T, path string, mutate func(*ReviewWorkBinding)) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var binding ReviewWorkBinding
	if err := json.Unmarshal(data, &binding); err != nil {
		t.Fatal(err)
	}
	mutate(&binding)
	data, err = json.MarshalIndent(binding, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

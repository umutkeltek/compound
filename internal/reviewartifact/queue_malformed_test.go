package reviewartifact

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/home"
)

var malformedQueueTestNow = time.Date(2026, 10, 29, 3, 0, 0, 0, time.UTC)

// TestQueueSkipsMalformedArtifactWithWarning is the red-first falsifier for
// one foreign file taking down the whole review queue.
//
// The queue walks every `*.json` under COMPOUND_HOME/reviews and decodes each
// into a JSON object. A lane that preserves evidence next to a review (here a
// checksum list written as a top-level JSON array, inside a nested evidence
// directory) put a file the queue cannot decode into that tree. Before the
// fix, Queue returned that one parse error for the whole walk, so every
// command that loads reviews (attention, reviews, narrate, the console's
// Decide panel) exited 1 and showed the operator nothing.
//
// The contract pinned here: a file that is not a JSON object is skipped, the
// report carries exactly one warning per skipped file naming its path, and
// every well-formed review is still listed with its fields intact.
func TestQueueSkipsMalformedArtifactWithWarning(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		reason  string
	}{
		{
			name:    "top-level array",
			payload: `[{"path":"ARCH.log","sha256":"00","bytes":1}]`,
			reason:  "cannot unmarshal array",
		},
		{
			name:    "truncated object",
			payload: `{"id":"half_written",`,
			reason:  "unexpected end of JSON input",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			homePath := filepath.Join(t.TempDir(), ".compound")
			if err := home.Init(homePath, "test", malformedQueueTestNow); err != nil {
				t.Fatal(err)
			}
			const area = "fixture-area"
			if _, err := Write(homePath, area, "review_valid_fixture", map[string]any{
				"id":     "review_valid_fixture",
				"status": "review_required",
			}, "  ", "# Valid fixture review\n"); err != nil {
				t.Fatal(err)
			}
			evidenceDir := filepath.Join(homePath, "reviews", area, "fixture-evidence", "review-r1")
			if err := os.MkdirAll(evidenceDir, 0o700); err != nil {
				t.Fatal(err)
			}
			malformed := filepath.Join(evidenceDir, "archive-manifest.json")
			if err := os.WriteFile(malformed, []byte(tc.payload), 0o600); err != nil {
				t.Fatal(err)
			}

			// The malformed file's area is its directory. A listing of every
			// area, or of exactly that area, warns once; a listing of another
			// area would never show it, so it stays silent there.
			for _, check := range []struct {
				opt      QueueOptions
				items    int
				warnings int
			}{
				{opt: QueueOptions{}, items: 1, warnings: 1},
				{opt: QueueOptions{Area: area}, items: 1, warnings: 0},
				{opt: QueueOptions{Area: area + "/fixture-evidence/review-r1"}, items: 0, warnings: 1},
			} {
				report, err := Queue(homePath, check.opt, malformedQueueTestNow)
				if err != nil {
					t.Fatalf("Queue(%+v) failed on one malformed file; want skip with warning: %v", check.opt, err)
				}
				if len(report.Items) != check.items || report.Counts.Total != check.items {
					t.Fatalf("Queue(%+v) items=%+v counts=%+v, want %d valid review(s) and the skipped file in no counter", check.opt, report.Items, report.Counts, check.items)
				}
				if check.items == 1 {
					item := report.Items[0]
					if item.ID != "review_valid_fixture" || item.Status != "review_required" || report.Counts.ReviewRequired != 1 {
						t.Fatalf("valid review changed by the skip: item=%+v counts=%+v", item, report.Counts)
					}
				}
				if len(report.Warnings) != check.warnings {
					t.Fatalf("Queue(%+v) warnings = %q, want %d", check.opt, report.Warnings, check.warnings)
				}
				for _, warning := range report.Warnings {
					if !strings.Contains(warning, malformed) || !strings.Contains(warning, tc.reason) {
						t.Fatalf("warning = %q, want it to name %s and the parse reason %q", warning, malformed, tc.reason)
					}
				}
			}

			// The skip is read-only: the evidence file is left exactly as found.
			data, err := os.ReadFile(malformed)
			if err != nil || string(data) != tc.payload {
				t.Fatalf("malformed evidence file changed or vanished: data=%q err=%v", data, err)
			}
		})
	}
}

// TestQueueSkipsMalformedArchivedArtifactWithWarning covers the same contract
// for `--include-archived`: an archived copy that no longer decodes must not
// fail the listing of every live review.
func TestQueueSkipsMalformedArchivedArtifactWithWarning(t *testing.T) {
	homePath := filepath.Join(t.TempDir(), ".compound")
	if err := home.Init(homePath, "test", malformedQueueTestNow); err != nil {
		t.Fatal(err)
	}
	const area = "fixture-area"
	for _, fixture := range []struct{ id, status string }{
		{id: "review_live_fixture", status: "review_required"},
		{id: "review_archived_fixture", status: "recorded"},
	} {
		if _, err := Write(homePath, area, fixture.id, map[string]any{"id": fixture.id, "status": fixture.status}, "  ", "# Fixture review\n"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := home.ArchiveOperationalHistory(homePath, home.OperationalArchiveOptions{Kinds: []string{"review"}}, malformedQueueTestNow.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	entries, err := home.LoadOperationalArchiveIndex(homePath)
	if err != nil {
		t.Fatal(err)
	}
	var archivedPath string
	for _, entry := range entries {
		if entry.OriginalPath == "reviews/"+area+"/review_archived_fixture.json" {
			archivedPath = filepath.Join(homePath, filepath.FromSlash(entry.ArchivePath))
		}
	}
	if archivedPath == "" {
		t.Fatalf("fixture review was not archived: entries=%+v", entries)
	}
	if err := os.WriteFile(archivedPath, []byte(`["not","a","review"]`), 0o600); err != nil {
		t.Fatal(err)
	}

	report, err := Queue(homePath, QueueOptions{Area: area, IncludeArchived: true}, malformedQueueTestNow.Add(2*time.Hour))
	if err != nil {
		t.Fatalf("Queue with a malformed archived copy failed; want skip with warning: %v", err)
	}
	if len(report.Items) != 1 || report.Items[0].ID != "review_live_fixture" {
		t.Fatalf("items = %+v, want only the live review", report.Items)
	}
	if len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0], archivedPath) || !strings.Contains(report.Warnings[0], "cannot unmarshal array") {
		t.Fatalf("warnings = %q, want one naming %s", report.Warnings, archivedPath)
	}
}

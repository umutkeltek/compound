package observation

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"compound/internal/knowledge"
)

func TestSourceConnectionsTreatKnowledgeImportRowsAsImported(t *testing.T) {
	home := t.TempDir()
	rows := []knowledge.ImportIndexRow{
		{
			CanonicalRef: "https://example.invalid/bookmarks/synthetic-design-reference",
			EvidenceRef:  "ev_synthetic_bookmark_001",
			ImportedAt:   tObservation.Format(time.RFC3339),
			ExportDigest: "digest-synthetic-001",
			SourceKind:   "x_bookmarks",
			SourceRef:    "fixture-batch:synthetic-bookmarks",
		},
		{
			CanonicalRef: "https://example.invalid/bookmarks/synthetic-market-note",
			EvidenceRef:  "ev_synthetic_bookmark_002",
			ImportedAt:   tObservation.Add(2 * time.Minute).Format(time.RFC3339),
			ExportDigest: "digest-synthetic-002",
			SourceKind:   "x_bookmarks",
			SourceRef:    "fixture-batch:synthetic-bookmarks",
		},
	}
	writeSourceConnectionKnowledgeImports(t, home, rows)

	projection, err := LoadSourceConnectionsProjection(home, tObservation.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	xBookmarks := findSourceConnection(projection.Connections, "x_bookmarks")
	if xBookmarks == nil || xBookmarks.Status != "imported" || xBookmarks.RowCount != 2 {
		t.Fatalf("x_bookmarks connection = %+v", xBookmarks)
	}
	if xBookmarks.SourceKind != "x_bookmarks" || xBookmarks.SourceRef != "fixture-batch:synthetic-bookmarks" {
		t.Fatalf("x_bookmarks source refs = %+v", xBookmarks)
	}
	if !strings.Contains(xBookmarks.NextSafeAction, "bookmark") || !containsString(xBookmarks.Produces, "knowledge evidence") {
		t.Fatalf("x_bookmarks next/produces = %+v", xBookmarks)
	}
	if projection.Counts.Imported != 1 || projection.Counts.Connected != 0 {
		t.Fatalf("source counts should distinguish imported evidence from live connection: %+v", projection.Counts)
	}
	githubStars := findSourceConnection(projection.Connections, "github_stars")
	if githubStars == nil || githubStars.Status != "not_connected" || githubStars.RowCount != 0 {
		t.Fatalf("github stars should remain unconnected: %+v", githubStars)
	}
}

func writeSourceConnectionKnowledgeImports(t *testing.T, home string, rows []knowledge.ImportIndexRow) {
	t.Helper()
	dir := filepath.Join(home, "ledger")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "knowledge-imports.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, row := range rows {
		if err := enc.Encode(row); err != nil {
			t.Fatal(err)
		}
	}
}

func findSourceConnection(connections []SourceConnection, id string) *SourceConnection {
	for i := range connections {
		if connections[i].ID == id {
			return &connections[i]
		}
	}
	return nil
}

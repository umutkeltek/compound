package policy

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPlacementRulesContext(t *testing.T) {
	homeRoot := t.TempDir()
	t.Setenv("HOME", homeRoot)
	rules := PlacementRules{
		OpenWriteGlobs: []string{"$HOME/open-write/**"},
		Owners: []PlacementOwnerRule{{
			ID:                  "decisions",
			Owner:               "decisions",
			Status:              "active",
			CanonicalWriteGlobs: []string{"$HOME/owners/decisions/**"},
			DenyGlobs:           []string{"$HOME/Desktop/decisions/**"},
			CanonFilenameGlobs:  []string{"DEC-*.md"},
			Confidence:          "block",
		}},
	}

	if ctx := rules.PlacementContext(filepath.Join(homeRoot, "owners", "decisions", "DEC-0001.md")); ctx != nil {
		t.Fatalf("canonical owner home should not trigger placement guard: %+v", ctx)
	}

	if ctx := rules.PlacementContext(filepath.Join(homeRoot, "open-write", "DEC-0001.md")); ctx != nil {
		t.Fatalf("open-write should suppress placement context, got %+v", ctx)
	}

	wrongHome := rules.PlacementContext(filepath.Join(homeRoot, "notes", "DEC-0001.md"))
	if wrongHome == nil || wrongHome.Reason != "canon-filename-outside-owner-home" || wrongHome.OwnerRef != "placement-owner:decisions" {
		t.Fatalf("canon filename outside owner home should trigger owner context, got %+v", wrongHome)
	}

	deny := rules.PlacementContext(filepath.Join(homeRoot, "Desktop", "decisions", "draft.md"))
	if deny == nil || deny.Reason != "deny-zone" {
		t.Fatalf("deny glob should trigger deny-zone, got %+v", deny)
	}
}

func TestImportPlacementRulesWritesAuthorityAndSourceRecord(t *testing.T) {
	homePath := t.TempDir()
	sourcePath := filepath.Join(t.TempDir(), "placement-rules.json")
	body := []byte(`{
  "open_write_globs": ["$HOME/scratch/**"],
  "owners": [{
    "id": "docs",
    "owner": "docs",
    "status": "active",
    "canonical_write_globs": ["$HOME/owners/docs/**"],
    "canon_filename_globs": ["DOC-*.md"],
    "confidence": "warn"
  }]
}`)
	if err := os.WriteFile(sourcePath, body, 0o644); err != nil {
		t.Fatal(err)
	}

	report, err := ImportPlacementRules(homePath, sourcePath, fixedNow)
	if err != nil {
		t.Fatalf("import placement rules: %v", err)
	}
	if report.AuthorityRef != PlacementRulesAuthorityRef || report.OwnerCount != 1 || report.OpenWriteCount != 1 {
		t.Fatalf("unexpected import report: %+v", report)
	}
	if got := readPolicyTestFile(t, PlacementRulesPath(homePath)); string(got) != string(body) {
		t.Fatalf("authority body changed:\n%s", got)
	}
	records, err := LoadPlacementSourceAuthority(homePath)
	if err != nil {
		t.Fatalf("load source authority: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("source authority records = %d, want 1", len(records))
	}
	rec := records[0]
	if rec.AuthorityStatus != "compound_primary" || rec.SourceRole != "evidence_only_input_after_import" {
		t.Fatalf("bad source authority role/status: %+v", rec)
	}
	if !strings.Contains(rec.Retires, sourcePath) || !strings.Contains(rec.SourceRevision, "sha256:") {
		t.Fatalf("source authority missing retire/revision fields: %+v", rec)
	}
	if len(rec.OwnerIDs) != 1 || rec.OwnerIDs[0] != "docs" {
		t.Fatalf("source authority owner ids = %+v", rec.OwnerIDs)
	}
}

func TestLoadPlacementRulesRejectsInvalidConfidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "placement-rules.json")
	raw, err := json.Marshal(PlacementRules{Owners: []PlacementOwnerRule{{
		ID:         "bad",
		Confidence: "panic",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = LoadPlacementRules(path)
	if err == nil || !strings.Contains(err.Error(), "invalid confidence") {
		t.Fatalf("expected invalid confidence error, got %v", err)
	}
	if _, err := ImportPlacementRules(t.TempDir(), path, fixedNow); err == nil || !strings.Contains(err.Error(), "invalid confidence") {
		t.Fatalf("expected import invalid confidence error, got %v", err)
	}
}

func readPolicyTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

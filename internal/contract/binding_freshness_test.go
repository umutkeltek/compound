package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestComputeBindingFreshnessPinsStatusVocabulary pins the four statuses
// the doctor harness section reads: fresh / drifted / unknown / absent.
// The R3B 2026-09-04 packet requires the install → manifest refresh flow
// to flip drifted back to fresh; these tests are the falsifier that
// catches any future regression in the comparison logic.
func TestComputeBindingFreshnessPinsStatusVocabulary(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	binaryPath := filepath.Join(t.TempDir(), "compound-current")

	cases := []struct {
		name       string
		revision   string
		wantStatus string
	}{
		{"fresh", "binary:" + binaryPath, "fresh"},
		{"drifted", "binary:/path/to/old/compound", "drifted"},
		{"unknown_empty_revision", "", "unknown"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest := "schema_version: compound.installed_hooks.v0.1\n" +
				"hooks:\n" +
				" - harness: claude\n" +
				"   name: PreToolUse\n" +
				"   event: PreToolUse\n" +
				"   ref: compound\n" +
				"   revision: " + tc.revision + "\n" +
				"   status: current\n"
			if err := os.WriteFile(filepath.Join(home, "config", "installed-hooks.yaml"), []byte(manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			report, err := ComputeBindingFreshness(home, binaryPath)
			if err != nil {
				t.Fatalf("ComputeBindingFreshness: %v", err)
			}
			if len(report.Rows) != 1 {
				t.Fatalf("rows = %d, want 1", len(report.Rows))
			}
			if report.Rows[0].Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q (row=%+v)", report.Rows[0].Status, tc.wantStatus, report.Rows[0])
			}
			if tc.wantStatus == "drifted" && !strings.Contains(report.Rows[0].RepairCommand, "compound hook install --harness claude") {
				t.Fatalf("drifted row repair = %q", report.Rows[0].RepairCommand)
			}
			if tc.wantStatus != "drifted" && report.Rows[0].RepairCommand != "" {
				t.Fatalf("non-drifted row repair = %q, want empty", report.Rows[0].RepairCommand)
			}
		})
	}
}

// TestComputeBindingFreshnessMissingManifest pins the absent path: an
// empty $COMPOUND_HOME/config directory produces an empty Rows slice
// without an error, so doctor renders "(no installed hooks manifest)".
func TestComputeBindingFreshnessMissingManifest(t *testing.T) {
	home := t.TempDir()
	binaryPath := filepath.Join(t.TempDir(), "compound-current")
	report, err := ComputeBindingFreshness(home, binaryPath)
	if err != nil {
		t.Fatalf("missing manifest should not error, got: %v", err)
	}
	if len(report.Rows) != 0 {
		t.Fatalf("missing manifest rows = %d, want 0", len(report.Rows))
	}
}

// TestComputeBindingFreshnessRejectsEmptyInputs makes the fail-closed
// contract explicit: an empty home or empty binary path returns an error
// so the doctor surfaces "binding freshness unavailable" instead of a
// silently-empty report.
func TestComputeBindingFreshnessRejectsEmptyInputs(t *testing.T) {
	if _, err := ComputeBindingFreshness("", "/some/binary"); err == nil {
		t.Fatalf("empty home path returned no error; want fail-closed")
	}
	if _, err := ComputeBindingFreshness("/some/home", ""); err == nil {
		t.Fatalf("empty binary path returned no error; want fail-closed")
	}
}

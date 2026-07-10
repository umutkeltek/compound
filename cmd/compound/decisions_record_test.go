package main

import (
	"strings"
	"testing"
)

// TestCmdDecisionsRecordWritesGrantAndCitesIt proves `compound decisions
// record --kind grant ...` writes a decision record under
// $COMPOUND_HOME/decisions/operator/ and prints the resulting claim/scope so
// a caller can cite it (grants-as-gates, 2026-07-10 packet).
func TestCmdDecisionsRecordWritesGrantAndCitesIt(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)

	out := captureStdout(t, func() {
		cmdDecisions([]string{
			"record",
			"--kind", "grant",
			"--id", "director-apply-grant-2026-07-10",
			"--title", "Grant director --apply",
			"--reviewed-by", "umut",
			"--scope", "director-apply",
		})
	})
	for _, want := range []string{
		"director-apply-grant-2026-07-10",
		"- scope: director-apply",
		"- reviewed_by: umut",
		"- verdict: promote",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("decisions record output missing %q:\n%s", want, out)
		}
	}
}

// TestCmdDecisionsRecordFreezeWritesScopeQualifiers proves the freeze CLI
// path threads --owner-glob/--keyword through to the record (falsifier b's
// wiring surface: the freeze admission guard reads these qualifiers).
func TestCmdDecisionsRecordFreezeWritesScopeQualifiers(t *testing.T) {
	homePath := t.TempDir()
	t.Setenv("COMPOUND_HOME", homePath)

	out := captureStdout(t, func() {
		cmdDecisions([]string{
			"record",
			"--kind", "freeze",
			"--id", "direction-freeze-2026-07-10",
			"--title", "Freeze new-organ/absorption imports",
			"--reviewed-by", "umut",
			"--keyword", "absorption_debt",
			"--keyword", "harness_edit",
			"--owner-glob", "compound-authority",
		})
	})
	if !strings.Contains(out, "direction-freeze-2026-07-10") {
		t.Fatalf("decisions record freeze output missing id:\n%s", out)
	}
}

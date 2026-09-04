package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDoctorReportsDriftedBindingAfterStaleInstall pins the R3B 2026-09-04
// contract for the doctor harness section: when the installed-hooks
// manifest records `binary:/path/to/old/compound` and the binary at
// `/path/to/new/compound` is the one install.sh just wrote, doctor must
// surface one row per drifted harness with status=drifted, the exact
// recorded revision, the current-binary revision, and the repair command
// `compound hook install --harness <lane>`. Doctor itself never invokes
// the installer.
func TestDoctorReportsDriftedBindingAfterStaleInstall(t *testing.T) {
	t.Setenv("COMPOUND_PLACEMENT_GUARD_MODE", "")
	t.Setenv("PLACEMENT_GUARD_MODE", "")

	home := t.TempDir()
	base := t.TempDir()
	binary := filepath.Join(t.TempDir(), "compound-new")

	// Stale manifest entry: recorded revision points at a path the
	// install path no longer matches. install.sh just overwrote
	// `binary` (the new install path), so the binding is drifted.
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema_version: compound.installed_hooks.v0.1\n" +
		"hooks:\n" +
		" - harness: claude\n" +
		"   name: PreToolUse\n" +
		"   event: PreToolUse\n" +
		"   ref: compound\n" +
		"   revision: binary:/path/to/old/compound\n" +
		"   status: current\n"
	if err := os.WriteFile(filepath.Join(home, "config", "installed-hooks.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	args := []string{"--home", home, "--base", base, "--binary", binary, "--json"}
	// Exit contract (landed with the doctor exit-truth change): 0 = no problems,
	// 1 = problems found, 2 = the doctor could not run. Host-coupled checks can
	// legitimately report problems on a dev box, so only exit 2 is a failure here;
	// the binding_freshness section below is the assertion that matters.
	if code := runDoctor(args, &out, &errOut); code != 0 && code != 1 {
		t.Fatalf("doctor could not run on a drifted binding: code=%d stderr=%q", code, errOut.String())
	}
	var rep doctorReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("parse doctor json: %v (%q)", err, out.String())
	}
	if len(rep.BindingFreshness.Rows) != 1 {
		t.Fatalf("binding_freshness rows = %d, want 1: %+v", len(rep.BindingFreshness.Rows), rep.BindingFreshness)
	}
	row := rep.BindingFreshness.Rows[0]
	if row.Harness != "claude" || row.Status != "drifted" {
		t.Fatalf("binding_freshness row = %+v, want harness=claude status=drifted", row)
	}
	if row.RecordedAt != "binary:/path/to/old/compound" {
		t.Fatalf("binding_freshness recorded = %q", row.RecordedAt)
	}
	if row.CurrentBinary != "binary:"+binary {
		t.Fatalf("binding_freshness current = %q, want binary:%s", row.CurrentBinary, binary)
	}
	if row.RepairCommand != "compound hook install --harness claude" {
		t.Fatalf("binding_freshness repair = %q", row.RepairCommand)
	}

	// Text rendering must include the drift line and the repair line.
	// The doctor-exit-truth branch fixes the problem/warning wiring;
	// this section only adds one block, so re-render in text mode and
	// pin its shape.
	out.Reset()
	errOut.Reset()
	if code := runDoctor([]string{"--home", home, "--base", base, "--binary", binary}, &out, &errOut); code != 0 {
		t.Fatalf("doctor text mode failed: %s", errOut.String())
	}
	body := out.String()
	if !strings.Contains(body, "binding freshness (hook manifest vs installed binary)") {
		t.Fatalf("doctor text omitted binding freshness section:\n%s", body)
	}
	if !strings.Contains(body, "claude") || !strings.Contains(body, "drifted") {
		t.Fatalf("doctor text omitted drifted row:\n%s", body)
	}
	if !strings.Contains(body, "compound hook install --harness claude") {
		t.Fatalf("doctor text omitted repair command:\n%s", body)
	}
}

// TestDoctorReportsFreshBindingWhenRecordedRevisionMatchesCurrentBinary
// pins the inverse: a manifest whose recorded revision already names the
// current binary path is reported as fresh, with no repair command. This
// is the post-install happy path after the installer runs
// `compound hook install --harness all`.
func TestDoctorReportsFreshBindingWhenRecordedRevisionMatchesCurrentBinary(t *testing.T) {
	t.Setenv("COMPOUND_PLACEMENT_GUARD_MODE", "")
	t.Setenv("PLACEMENT_GUARD_MODE", "")

	home := t.TempDir()
	base := t.TempDir()
	binary := filepath.Join(t.TempDir(), "compound-current")
	if err := os.MkdirAll(filepath.Join(home, "config"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "schema_version: compound.installed_hooks.v0.1\n" +
		"hooks:\n" +
		" - harness: claude\n" +
		"   name: PreToolUse\n" +
		"   event: PreToolUse\n" +
		"   ref: compound\n" +
		"   revision: binary:" + binary + "\n" +
		"   status: current\n"
	if err := os.WriteFile(filepath.Join(home, "config", "installed-hooks.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	args := []string{"--home", home, "--base", base, "--binary", binary, "--json"}
	if code := runDoctor(args, &out, &errOut); code != 0 && code != 1 { // see the exit-contract note above
		t.Fatalf("doctor could not run on a fresh binding: code=%d stderr=%s", code, errOut.String())
	}
	var rep doctorReport
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("parse doctor json: %v", err)
	}
	if len(rep.BindingFreshness.Rows) != 1 {
		t.Fatalf("binding_freshness rows = %d", len(rep.BindingFreshness.Rows))
	}
	if rep.BindingFreshness.Rows[0].Status != "fresh" {
		t.Fatalf("binding_freshness row = %+v, want status=fresh", rep.BindingFreshness.Rows[0])
	}
	if rep.BindingFreshness.Rows[0].RepairCommand != "" {
		t.Fatalf("fresh binding reported a repair command: %q", rep.BindingFreshness.Rows[0].RepairCommand)
	}
}

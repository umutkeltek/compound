package contract

import (
	"fmt"
	"os"
	"strings"
)

// BindingFreshnessRow is one harness's binding-freshness verdict, surfaced
// in `compound doctor` (R3B 2026-09-04). It answers the question "did the
// last `./install.sh` leave my hook binding pointing at the new binary, or
// is it still pointing at the previous binary?". The latter is the failure
// that silently re-drifts every Claude lane on every install: install.sh
// rebuilt `compound` and overwrote the binary, but the manifest entry that
// the readiness guard consults still records `binary:<old-path>`. Until
// `compound hook install --harness <lane>` refreshes the entry, the lane
// resolves to `plan` for any operator who asks for a mutation-capable mode.
//
// Status vocabulary:
//   - "fresh"     — manifest revision equals "binary:" + binaryPath (no drift)
//   - "drifted"   — manifest revision differs from "binary:" + binaryPath
//                   (reinstall refreshed the binary but not the binding)
//   - "absent"    — manifest has no entries for this harness
//   - "unknown"   — the comparison could not be made (no binaryPath, etc.)
type BindingFreshnessRow struct {
	Harness        string `json:"harness"`
	Status         string `json:"status"`
	RecordedAt     string `json:"recorded_revision,omitempty"`
	CurrentBinary  string `json:"current_binary,omitempty"`
	RepairCommand  string `json:"repair_command,omitempty"`
}

// BindingFreshnessReport aggregates one BindingFreshnessRow per harness
// found in the manifest. Installers and dashboards consume this in JSON.
type BindingFreshnessReport struct {
	BinaryPath string                `json:"binary_path"`
	Rows       []BindingFreshnessRow `json:"rows"`
}

// ComputeBindingFreshness reads the installed-hooks manifest and compares
// every entry's recorded revision to the binary that would be invoked today
// (`"binary:" + binaryPath`). Per-harness statuses collapse to fresh /
// drifted / absent / unknown. The function is read-only: it never writes
// to the manifest and never calls the installer. Doctor uses it to surface
// the exact lanes that need `compound hook install --harness <lane>` to
// recover from a `./install.sh` that did not refresh them.
func ComputeBindingFreshness(homePath, binaryPath string) (BindingFreshnessReport, error) {
	report := BindingFreshnessReport{BinaryPath: strings.TrimSpace(binaryPath)}
	if strings.TrimSpace(homePath) == "" {
		return report, fmt.Errorf("compute binding freshness: home path is required")
	}
	if strings.TrimSpace(binaryPath) == "" {
		return report, fmt.Errorf("compute binding freshness: binary path is required")
	}
	manifest, err := LoadInstalledHooksManifest(homePath)
	if err != nil {
		// A missing manifest is the absent path: doctor renders
		// "(no installed hooks manifest)" instead of an error. This is
		// the common case on a fresh home before any lane has been
		// installed, and is NOT a binding-freshness failure.
		if os.IsNotExist(err) {
			return report, nil
		}
		return report, fmt.Errorf("compute binding freshness: %w", err)
	}
	wantRevision := "binary:" + binaryPath
	byHarness := map[string]InstalledHook{}
	for _, hook := range manifest.Hooks {
		h := strings.TrimSpace(hook.Harness)
		if h == "" {
			continue
		}
		// Multiple entries per harness can exist (one per event). The
		// mutation-covering binding (PreToolUse) is the one that drifts
		// readiness; if any entry per harness is drifted we report the
		// harness as drifted.
		if _, ok := byHarness[h]; !ok {
			byHarness[h] = hook
		} else if hook.Event == "PreToolUse" {
			byHarness[h] = hook
		}
	}
	for harnessName, hook := range byHarness {
		status := classifyBindingFreshness(hook.Revision, wantRevision)
		row := BindingFreshnessRow{
			Harness:       harnessName,
			Status:        status,
			RecordedAt:    strings.TrimSpace(hook.Revision),
			CurrentBinary: wantRevision,
		}
		if status == "drifted" {
			row.RepairCommand = "compound hook install --harness " + harnessName
		}
		report.Rows = append(report.Rows, row)
	}
	return report, nil
}

// classifyBindingFreshness collapses the recorded revision and the
// current-binary revision into the status vocabulary. Empty recorded
// revisions report as unknown (the manifest entry is malformed, not a
// drift); mismatch reports as drifted.
func classifyBindingFreshness(recorded, want string) string {
	recorded = strings.TrimSpace(recorded)
	want = strings.TrimSpace(want)
	if recorded == "" {
		return "unknown"
	}
	if recorded == want {
		return "fresh"
	}
	return "drifted"
}

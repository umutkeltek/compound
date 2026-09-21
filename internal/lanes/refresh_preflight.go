package lanes

import (
	"fmt"
	"os"
	"strings"

	"compound/internal/home"
	"compound/internal/hookplan"
)

// Readiness means the native refresh owner can handle this lane, not that its
// hooks, instruction pack, or provider have been refreshed or proven.
const statusRefreshReady = "ready"

func preflightLaneRefresh(opts RefreshLaneOptions, harnessName string) LaneVerdict {
	harnessName = strings.TrimSpace(harnessName)
	v := LaneVerdict{Harness: harnessName, Status: StatusError}
	switch hookInstallHarness(harnessName) {
	case "claude-code", "claude-zai", "claude-minimax", "claude-qwen", "codex":
		if _, err := hookplan.InstallHooksWithRoster(opts.BaseDir, opts.BinaryPath, "global",
			[]string{hookInstallHarness(harnessName)}, "", nil, true, nil); err != nil {
			v.Detail = "native hook refresh preflight: " + err.Error()
			return v
		}
		v.Status = statusRefreshReady
		v.Detail = "native hook writer available; no refresh performed"
	case "oh_my_pi", "oh-my-pi":
		v.Detail = "requires separate adapter activation: oh-my-pi shim is owned by compound hook install --harness oh-my-pi; its module report cannot refresh settings-style installed-hook authority; resolve the activation contract before retrying"
	case "opencode":
		v.Detail = "requires separate adapter activation: OpenCode plugin and registration are owned by adapters/opencode/install.ts; native refresh cannot attest this observation-only adapter; resolve the activation contract before retrying"
	default:
		v.Detail = "no native hook refresh owner for this harness; resolve its adapter activation contract before retrying"
	}
	return v
}

// PreflightInstalledRefresh is read-only, including on an absent home. Validate
// the complete required set before the first native config or authority write.
func PreflightInstalledRefresh(opts RefreshLaneOptions) ([]LaneVerdict, error) {
	names, err := InstalledHarnesses(opts.HomePath)
	if err != nil {
		return nil, err
	}
	verdicts := make([]LaneVerdict, 0, len(names))
	for _, name := range names {
		verdicts = append(verdicts, preflightLaneRefresh(opts, name))
	}
	return verdicts, nil
}

func refreshPreflightReady(verdicts []LaneVerdict) bool {
	for _, v := range verdicts {
		if v.Status != statusRefreshReady {
			return false
		}
	}
	return true
}

func runCLIRefreshPreflight(opts CLIRefreshOpts) error {
	// The installer asks before initializing a genuinely absent home. Existing
	// homes still need the same manifest integrity check as ordinary refresh.
	if _, err := os.Lstat(opts.HomePath); err == nil {
		if err := home.RequireMutationCompatible(opts.HomePath); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	refreshOpts := RefreshLaneOptions{HomePath: opts.HomePath, BaseDir: opts.BaseDir, BinaryPath: opts.BinaryPath, Now: opts.Now}
	var verdicts []LaneVerdict
	if opts.HarnessName != "" {
		verdicts = []LaneVerdict{preflightLaneRefresh(refreshOpts, opts.HarnessName)}
	} else {
		var err error
		verdicts, err = PreflightInstalledRefresh(refreshOpts)
		if err != nil {
			return err
		}
	}
	if opts.JSON {
		if err := PrintVerdicts(os.Stdout, verdicts, true); err != nil {
			return err
		}
	} else {
		for _, v := range verdicts {
			fmt.Fprintf(os.Stdout, "refresh-preflight: %s %s: %s\n", v.Harness, v.Status, v.Detail)
		}
	}
	if !refreshPreflightReady(verdicts) {
		return fmt.Errorf("refresh preflight refused; no hook or instruction-pack refresh performed")
	}
	return nil
}

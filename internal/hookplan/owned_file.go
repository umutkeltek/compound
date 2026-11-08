package hookplan

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ownedFile is a hook file Compound writes whole into a harness's own discovery folder:
// oh-my-pi's TS hook module and OpenCode's plugin. Neither harness has a settings file to
// reconcile entry by entry, so ownership is a marker line and the install is all-or-nothing:
// identical bytes are a no-op, a compound-owned file is refreshed wholesale, and a foreign
// file at the same path fails closed rather than being clobbered. These files are not
// published into the installed-hooks authority: that authority reconciles settings-style
// hook entries, and a whole file has no entries to reconcile.
type ownedFile struct {
	harness  string               // installer label: "oh-my-pi", "opencode"
	marker   string               // ownership line the source starts with
	source   string               // the exact bytes to install
	legacy   []string             // earlier compound-written contents without the marker, adopted as owned
	bindings []HookInstallBinding // what the file wires, for the report
}

// errForeignHookFile marks a file at an owned path that Compound did not write.
var errForeignHookFile = errors.New("not compound-owned")

func (f ownedFile) owns(existing string) bool {
	if strings.Contains(existing, f.marker) {
		return true
	}
	for _, legacy := range f.legacy {
		if existing == legacy {
			return true
		}
	}
	return false
}

// install writes f to target, or with check=true only reports. Entry actions follow the
// settings installers: installed, updated, ok, drift, absent.
func (f ownedFile) install(target string, check bool) (HookInstallReport, error) {
	report := HookInstallReport{Check: check, Scope: "global"}
	if !strings.HasPrefix(f.source, f.marker) {
		return report, fmt.Errorf("embedded %s hook file lost its ownership marker", f.harness)
	}
	existing, readErr := os.ReadFile(target)
	switch {
	case readErr == nil && !f.owns(string(existing)):
		return report, fmt.Errorf("%s exists and is %w; refusing to clobber it", target, errForeignHookFile)
	case readErr != nil && !os.IsNotExist(readErr):
		return report, fmt.Errorf("read %s: %w", target, readErr)
	}
	changed := string(existing) != f.source
	report.Entries = append(report.Entries, HookInstallEntry{
		Harness:    f.harness,
		ConfigPath: target,
		Action:     shimAction(changed, check, readErr == nil),
		Bindings:   f.bindings,
	})
	if check || !changed {
		return report, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return report, fmt.Errorf("create %s hook dir: %w", f.harness, err)
	}
	if err := os.WriteFile(target, []byte(f.source), 0o644); err != nil {
		return report, fmt.Errorf("write %s hook file: %w", f.harness, err)
	}
	return report, nil
}

// uninstall removes target only when Compound owns it. Actions mirror UninstallHooks:
// "removed", "would remove" under check, "absent" when nothing of Compound's is there.
// A foreign file at the path stays untouched and reports absent.
func (f ownedFile) uninstall(target string, check bool) (HookInstallEntry, error) {
	entry := HookInstallEntry{Harness: f.harness, ConfigPath: target, Action: "absent"}
	existing, err := os.ReadFile(target)
	if os.IsNotExist(err) {
		return entry, nil
	}
	if err != nil {
		return entry, fmt.Errorf("read %s: %w", target, err)
	}
	if !f.owns(string(existing)) {
		return entry, nil
	}
	entry.Bindings = f.bindings
	if check {
		entry.Action = "would remove"
		return entry, nil
	}
	if err := os.Remove(target); err != nil {
		return entry, fmt.Errorf("remove %s: %w", target, err)
	}
	entry.Action = "removed"
	return entry, nil
}

// ownedFileHarness returns the whole-file installer and the file's path under the user's
// base folder for the harnesses that have one. Settings-style harnesses report ok=false.
func ownedFileHarness(name string) (file ownedFile, relPath string, ok bool) {
	switch name {
	case "oh-my-pi", "oh_my_pi":
		return ompShimFile(), OmpShimRelPath, true
	case "opencode":
		return openCodePluginFile(), OpenCodePluginRelPath, true
	}
	return ownedFile{}, "", false
}

package hookplan

import (
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"
)

//go:embed omp_hook_shim.ts
var ompHookShimSource string

// OmpShimMarker is the ownership line compound writes at the top of the shim.
// An update replaces only compound-owned bytes; a file without the marker is a
// user's own hook and is never clobbered. Bumped to @2 by slice 3 (the shim
// gained transcript_path and session_id derivation).
const OmpShimMarker = "// compound-owned: oh-my-pi-hook-shim@2"

// OmpShimRelPath is the shim's location inside omp's agent dir. omp's builtin
// discovery loads every non-dotfile under hooks/{pre,post} as a Bun module;
// the pre/ directory is right because the shim registers handlers (it is not
// a shell matcher whose filename binds a tool).
const OmpShimRelPath = ".omp/agent/hooks/pre/compound.ts"

// ompShimFile is the whole-file installer for oh-my-pi (see ownedFile for the
// no-clobber contract). The shim is deliberately NOT published into the
// installed-hooks authority: that authority reconciles settings-style hook
// entries, and omp's TS-module mechanism has no settings surface to reconcile.
func ompShimFile() ownedFile {
	return ownedFile{
		harness: "oh-my-pi",
		marker:  OmpShimMarker,
		source:  ompHookShimSource,
		bindings: []HookInstallBinding{
			{Event: "SessionStart", Matcher: "*", Command: "omp hook shim: session_start → compound hook session-start", Blocking: false},
			{Event: "UserPromptSubmit", Matcher: "*", Command: "omp hook shim: before_agent_start → compound hook user-prompt-submit", Blocking: false},
			{Event: "PreToolUse", Matcher: "*", Command: "omp hook shim: tool_call → compound hook pre-tool-use", Blocking: true},
			{Event: "PostToolUse", Matcher: "*", Command: "omp hook shim: tool_result → compound hook post-tool-use", Blocking: false},
			{Event: "SessionEnd", Matcher: "*", Command: "omp hook shim: session_shutdown → compound hook session-end", Blocking: false},
		},
	}
}

// InstallOhMyPiShimWithConfigDir is the per-profile variant of InstallOhMyPiShim: when
// configDir is non-empty the shim is installed under
// <baseDir>/<configDir>/.omp/agent/hooks/pre/compound.ts instead of the default
// operator-home location, so each isolated lane profile gets its own shim copy.
func InstallOhMyPiShimWithConfigDir(baseDir, configDir string, check bool) (HookInstallReport, error) {
	if strings.TrimSpace(configDir) == "" {
		return InstallOhMyPiShim(baseDir, check)
	}
	if strings.TrimSpace(baseDir) == "" {
		return HookInstallReport{Check: check, Scope: "global"}, fmt.Errorf("base dir required")
	}
	return ompShimFile().install(filepath.Join(baseDir, configDir, ".omp", "agent", "hooks", "pre", "compound.ts"), check)
}

// InstallOhMyPiShim materializes the compound ↔ oh-my-pi hook shim into baseDir (the
// operator home). Idempotent: identical bytes are a no-op; compound-owned content is
// refreshed wholesale; a foreign file at the path fails closed rather than being
// clobbered. check=true performs the comparison without writing.
func InstallOhMyPiShim(baseDir string, check bool) (HookInstallReport, error) {
	if strings.TrimSpace(baseDir) == "" {
		return HookInstallReport{Check: check, Scope: "global"}, fmt.Errorf("base dir required")
	}
	return ompShimFile().install(filepath.Join(baseDir, OmpShimRelPath), check)
}

func shimAction(changed, check, existed bool) string {
	switch {
	case check && !existed:
		return "absent"
	case check && changed:
		return "drift"
	case check:
		return "ok"
	case !existed:
		return "installed"
	case changed:
		return "updated"
	default:
		return "ok"
	}
}

package hookplan

import (
	_ "embed"
	"fmt"
	"path/filepath"
	"strings"
)

// opencode_plugin.ts is the OpenCode plugin `compound init` installs. It is byte-identical to
// adapters/opencode/compound.ts plus the ownership marker on line one; a hookplan test fails
// when the two drift, so the adapter folder stays the place to change the plugin and this
// copy is what a release binary carries. OpenCode loads every .ts file under
// ~/.config/opencode/plugins/ on its own, so the install writes no OpenCode config.
//
//go:embed opencode_plugin.ts
var openCodePluginSource string

// OpenCodePluginMarker is the ownership line Compound writes at the top of the plugin.
const OpenCodePluginMarker = "// compound-owned: opencode-plugin@1"

// OpenCodePluginRelPath is the plugin's location under the user's home folder.
const OpenCodePluginRelPath = ".config/opencode/plugins/compound.ts"

// openCodePluginFile is the whole-file installer for OpenCode. The plugin gives OpenCode the
// session briefing (session.created + chat.message) and records tool calls
// (tool.execute.after). OpenCode has no session-end callback, so no receipt binding exists.
// The copy that `bun adapters/opencode/install.ts` wrote before this marker existed is adopted
// as compound-owned, so the first `compound init` after an upgrade refreshes it in place.
func openCodePluginFile() ownedFile {
	return ownedFile{
		harness: "opencode",
		marker:  OpenCodePluginMarker,
		source:  openCodePluginSource,
		legacy:  []string{strings.TrimPrefix(openCodePluginSource, OpenCodePluginMarker+"\n")},
		bindings: []HookInstallBinding{
			{Event: "SessionStart", Matcher: "*", Command: "opencode plugin: session.created → compound hook session-start", Blocking: false},
			{Event: "UserPromptSubmit", Matcher: "*", Command: "opencode plugin: chat.message → compound hook user-prompt-submit", Blocking: false},
			{Event: "PostToolUse", Matcher: "*", Command: "opencode plugin: tool.execute.after → compound hook post-tool-use", Blocking: false},
		},
	}
}

// InstallOpenCodePlugin writes the plugin under baseDir (the user's home folder), or with
// check=true only reports. A foreign file at the path fails closed.
func InstallOpenCodePlugin(baseDir string, check bool) (HookInstallReport, error) {
	if strings.TrimSpace(baseDir) == "" {
		return HookInstallReport{Check: check, Scope: "global"}, fmt.Errorf("base dir required")
	}
	return openCodePluginFile().install(filepath.Join(baseDir, OpenCodePluginRelPath), check)
}

package policy

import (
	"path/filepath"
	"strings"
	"testing"
)

// The desktop surface is a fact about WHERE an action lands on the machine the
// operator is working on, independent of what it mutates. These rows pin the
// recognizers one door at a time: a recognizer that silently stops matching
// downgrades a desktop-stealing action to "none", and the unattended hold that
// depends on it disappears with no other test going red.
func TestClassifyToolActionDesktopSurface(t *testing.T) {
	bash := func(command string) (string, map[string]any) {
		return "Bash", map[string]any{"command": command}
	}
	tests := []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		// agent-browser: its own profile, headless unless told otherwise.
		{"agent-browser default is headless", "", nil, DesktopSurfaceIsolatedHeadless},
		{"agent-browser --headed", "", nil, DesktopSurfaceIsolatedVisible},
		{"agent-browser --cdp", "", nil, DesktopSurfaceOperatorBrowser},
		{"agent-browser --auto-connect", "", nil, DesktopSurfaceOperatorBrowser},
		{"agent-browser --connect", "", nil, DesktopSurfaceOperatorBrowser},
		{"agent-browser behind timeout wrapper", "", nil, DesktopSurfaceOperatorBrowser},

		// Claude-in-Chrome drives the operator's own Chrome.
		{"claude-in-chrome navigate", "mcp__claude-in-chrome__navigate", map[string]any{"url": "https://example.invalid"}, DesktopSurfaceOperatorBrowser},
		{"claude-in-chrome read_page", "mcp__claude-in-chrome__read_page", map[string]any{"tabId": 1}, DesktopSurfaceOperatorBrowser},
		{"claude-in-chrome computer", "mcp__claude-in-chrome__computer", map[string]any{"action": "left_click"}, DesktopSurfaceOperatorBrowser},

		// cmux-cua is background by contract; these shapes take the foreground.
		{"cmux-cua get_window_state", "mcp__cmux-cua__get_window_state", map[string]any{"pid": 1, "window_id": 2}, DesktopSurfaceBackgroundGUI},
		{"cmux-cua click", "mcp__cmux-cua__click", map[string]any{"pid": 1, "element_index": 3}, DesktopSurfaceBackgroundGUI},
		{"cmux-cua launch_app background", "mcp__cmux-cua__launch_app", map[string]any{"bundle_id": "com.apple.calculator"}, DesktopSurfaceBackgroundGUI},
		{"cmux-cua type_text background", "mcp__cmux-cua__type_text", map[string]any{"pid": 1, "text": "x"}, DesktopSurfaceBackgroundGUI},
		{"cmux-cua bring_to_front", "mcp__cmux-cua__bring_to_front", map[string]any{"pid": 1}, DesktopSurfaceForegroundGUI},
		{"cmux-cua launch_app foreground intent", "mcp__cmux-cua__launch_app", map[string]any{"bundle_id": "com.apple.calculator", "delivery_mode": "foreground"}, DesktopSurfaceForegroundGUI},
		{"cmux-cua hotkey", "mcp__cmux-cua__hotkey", map[string]any{"pid": 1, "keys": []any{"cmd", "c"}}, DesktopSurfaceForegroundGUI},
		{"cmux-cua press_key", "mcp__cmux-cua__press_key", map[string]any{"pid": 1, "key": "return"}, DesktopSurfaceForegroundGUI},
		{"cmux-cua type_text foreground target", "mcp__cmux-cua__type_text", map[string]any{"pid": 1, "text": "x", "delivery_mode": "foreground"}, DesktopSurfaceForegroundGUI},

		// peekaboo: capture is background, input is foreground.
		{"peekaboo image", "", nil, DesktopSurfaceBackgroundGUI},
		{"peekaboo see", "", nil, DesktopSurfaceBackgroundGUI},
		{"peekaboo list", "", nil, DesktopSurfaceBackgroundGUI},
		{"peekaboo click", "", nil, DesktopSurfaceForegroundGUI},
		{"peekaboo type", "", nil, DesktopSurfaceForegroundGUI},
		{"peekaboo hotkey", "", nil, DesktopSurfaceForegroundGUI},
		{"peekaboo window focus", "", nil, DesktopSurfaceForegroundGUI},
		{"peekaboo --foreground", "", nil, DesktopSurfaceForegroundGUI},
		{"peekaboo unlisted subcommand fails closed", "", nil, DesktopSurfaceForegroundGUI},

		// osascript / open.
		{"osascript activate", "", nil, DesktopSurfaceForegroundGUI},
		{"osascript System Events keystroke", "", nil, DesktopSurfaceForegroundGUI},
		{"osascript System Events click", "", nil, DesktopSurfaceForegroundGUI},
		{"osascript notification", "", nil, DesktopSurfaceBackgroundGUI},
		{"open -a", "", nil, DesktopSurfaceForegroundGUI},
		{"open -na cluster", "", nil, DesktopSurfaceForegroundGUI},

		// MCP computer-use and Playwright.
		{"mcp computer tool", "mcp__desktop__computer", map[string]any{"action": "screenshot"}, DesktopSurfaceForegroundGUI},
		{"computer_use tool", "computer_use", map[string]any{"action": "left_click"}, DesktopSurfaceForegroundGUI},
		{"playwright mcp browser server", "mcp__browser__browser_click", map[string]any{"target": "@e1"}, DesktopSurfaceIsolatedVisible},
		{"playwright named tool", "mcp__playwright__browser_navigate", map[string]any{"url": "https://example.invalid"}, DesktopSurfaceIsolatedVisible},

		// Composition: the worst segment decides, including a PTY write.
		{"composed command worst segment", "", nil, DesktopSurfaceForegroundGUI},
		{"pty write", "write_stdin", map[string]any{"session_id": 7, "chars": "peekaboo click --on B1\n"}, DesktopSurfaceForegroundGUI},

		// Ordinary work touches no desktop surface.
		{"go test", "", nil, DesktopSurfaceNone},
		{"file write", "Write", map[string]any{"file_path": "fixture.txt", "content": "x"}, DesktopSurfaceNone},
		{"connector read", "mcp__github__get_issue", map[string]any{"repo": "example/repo"}, DesktopSurfaceNone},
	}
	commands := map[string]string{
		"agent-browser default is headless":         "agent-browser open https://example.invalid",
		"agent-browser --headed":                    "agent-browser --headed open https://example.invalid",
		"agent-browser --cdp":                       "agent-browser --cdp 9444 snapshot",
		"agent-browser --auto-connect":              "agent-browser --auto-connect snapshot -i",
		"agent-browser --connect":                   "agent-browser --connect ws://127.0.0.1:9222 click @e1",
		"agent-browser behind timeout wrapper":      "timeout 30 agent-browser --cdp=9444 snapshot",
		"peekaboo image":                            "peekaboo image --app Safari --path /tmp/shot.png",
		"peekaboo see":                              "peekaboo see --app Safari",
		"peekaboo list":                             "peekaboo list apps",
		"peekaboo click":                            "peekaboo click --on B1",
		"peekaboo type":                             "peekaboo type 'hello'",
		"peekaboo hotkey":                           "peekaboo hotkey --keys cmd,c",
		"peekaboo window focus":                     "peekaboo window focus --app Safari",
		"peekaboo --foreground":                     "peekaboo see --app Safari --foreground",
		"peekaboo unlisted subcommand fails closed": "peekaboo app launch Safari",
		"osascript activate":                        `osascript -e 'tell application "Safari" to activate'`,
		"osascript System Events keystroke":         `osascript -e 'tell application "System Events" to keystroke "v" using command down'`,
		"osascript System Events click":             `osascript -e 'tell application "System Events" to click button 1 of window 1 of process "Finder"'`,
		"osascript notification":                    `osascript -e 'display notification "done" with title "lane"'`,
		"open -a":                                   "open -a Safari https://example.invalid",
		"open -na cluster":                          "open -na Simulator",
		"composed command worst segment":            "peekaboo see --app Safari && peekaboo click --on B1",
		"go test":                                   "go test ./internal/policy",
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tool, args := tt.tool, tt.args
			if tool == "" {
				command, ok := commands[tt.name]
				if !ok {
					t.Fatalf("row %q has neither a tool nor a command", tt.name)
				}
				tool, args = bash(command)
			}
			got := ClassifyToolAction(ToolActionRequest{Tool: tool, Arguments: args})
			if got.DesktopSurface != tt.want {
				t.Fatalf("desktop surface = %q, want %q (%+v)", got.DesktopSurface, tt.want, got)
			}
		})
	}
}

// TestDesktopSurfacePostureMatrix pins the policy consequence under the shape
// lanes actually run (an L3 envelope over the project): the same command is
// held on an unattended lane and stays automatic, with an obligation naming the
// surface, where the operator is at the keyboard. An empty posture is the
// pre-existing caller and must keep the interactive answer.
func TestDesktopSurfacePostureMatrix(t *testing.T) {
	scope := t.TempDir()
	cfg := envelopeConfig(scope)
	monitor := func(command, posture string) MonitoredAction {
		return MonitorToolAction(ToolActionRequest{
			WorkID:             "work_desktop_surface",
			Actor:              "fixture",
			Harness:            "fixture",
			Tool:               "Bash",
			Cwd:                filepath.Join(scope, "repo"),
			Arguments:          map[string]any{"command": command},
			ToolSchemaRevision: "builtin:bash",
			Posture:            posture,
		}, cfg, envelopeNow())
	}
	const operatorBrowser = "agent-browser --cdp 9444 snapshot -i"

	held := monitor(operatorBrowser, PostureUnattended)
	if held.Decision.Outcome != "hold" {
		t.Fatalf("(operator_browser, unattended) = %q, want hold: %+v", held.Decision.Outcome, held.Decision)
	}
	if !containsString(held.Decision.Reasons, "desktop_surface:operator_browser:unattended") {
		t.Fatalf("hold reason missing exact desktop_surface reason: %+v", held.Decision.Reasons)
	}
	if held.Intent.SecurityFloorOutcome != "hold" {
		t.Fatalf("unattended desktop hold must ride the intent's security floor, got %q", held.Intent.SecurityFloorOutcome)
	}

	for _, posture := range []string{PostureInteractive, ""} {
		allowed := monitor(operatorBrowser, posture)
		if allowed.Decision.Outcome != "allow_with_obligations" {
			t.Fatalf("(operator_browser, %q) = %q, want allow_with_obligations: %+v",
				posture, allowed.Decision.Outcome, allowed.Decision)
		}
		if !obligationNames(allowed.Decision.Obligations, DesktopSurfaceOperatorBrowser) {
			t.Fatalf("(operator_browser, %q) obligations do not name the surface: %+v", posture, allowed.Decision.Obligations)
		}
		if containsSubstring(allowed.Intent.SecurityReasons, "desktop_surface:") {
			t.Fatalf("interactive desktop action raised a desktop floor: %+v", allowed.Intent.SecurityReasons)
		}
	}

	if got := monitor("peekaboo click --on B1", PostureUnattended); got.Decision.Outcome != "hold" ||
		!containsString(got.Decision.Reasons, "desktop_surface:foreground_gui:unattended") {
		t.Fatalf("(foreground_gui, unattended) = %+v, want hold with desktop_surface reason", got.Decision)
	}

	headlessBaseline := monitor("agent-browser open https://example.invalid", "")
	headless := monitor("agent-browser open https://example.invalid", PostureUnattended)
	if headless.Classification.DesktopSurface != DesktopSurfaceIsolatedHeadless ||
		headless.Decision.Outcome != headlessBaseline.Decision.Outcome ||
		strings.Join(headless.Decision.Reasons, "\n") != strings.Join(headlessBaseline.Decision.Reasons, "\n") {
		t.Fatalf("(isolated_headless, unattended) changed: got %+v, baseline %+v", headless.Decision, headlessBaseline.Decision)
	}

	// A posture the policy cannot read is not a human at the keyboard.
	if got := monitor(operatorBrowser, "lane"); got.Decision.Outcome != "hold" {
		t.Fatalf("unrecognized posture must fail closed to unattended, got %+v", got.Decision)
	}
}

// TestDesktopSurfaceLeavesIsolatedAndBackgroundUnchanged: posture changes
// nothing for surfaces that cannot take the operator's desktop, including the
// existing local_reversible browser path.
func TestDesktopSurfaceLeavesIsolatedAndBackgroundUnchanged(t *testing.T) {
	for _, tc := range []struct {
		name string
		tool string
		args map[string]any
		want string
	}{
		{"isolated_headless", "Bash", map[string]any{"command": "agent-browser open https://example.invalid"}, DesktopSurfaceIsolatedHeadless},
		{"isolated_visible local_reversible", "mcp__browser__browser_click", map[string]any{"target": "@e1"}, DesktopSurfaceIsolatedVisible},
		{"background_gui", "mcp__cmux-cua__get_window_state", map[string]any{"pid": 1, "window_id": 2}, DesktopSurfaceBackgroundGUI},
	} {
		t.Run(tc.name, func(t *testing.T) {
			baseline := MonitorToolAction(monitorRequest(tc.tool, tc.args), DefaultConfig(), fixedNow)
			request := monitorRequest(tc.tool, tc.args)
			request.Posture = PostureUnattended
			unattended := MonitorToolAction(request, DefaultConfig(), fixedNow)
			if unattended.Classification.DesktopSurface != tc.want {
				t.Fatalf("surface = %q, want %q", unattended.Classification.DesktopSurface, tc.want)
			}
			if unattended.Decision.Outcome != baseline.Decision.Outcome ||
				strings.Join(unattended.Decision.Reasons, "\n") != strings.Join(baseline.Decision.Reasons, "\n") ||
				strings.Join(unattended.Decision.Obligations, "\n") != strings.Join(baseline.Decision.Obligations, "\n") ||
				unattended.Intent.SecurityFloorOutcome != baseline.Intent.SecurityFloorOutcome {
				t.Fatalf("unattended posture changed an isolated/background decision:\n got %+v\nwant %+v",
					unattended.Decision, baseline.Decision)
			}
		})
	}

	click := monitorRequest("mcp__browser__browser_click", map[string]any{"target": "@e1"})
	click.Posture = PostureUnattended
	if got := MonitorToolAction(click, DefaultConfig(), fixedNow); got.Decision.Outcome != "allow_with_obligations" ||
		got.Intent.MutationClass != "local_reversible" {
		t.Fatalf("local_reversible browser click path changed: %+v / %s", got.Decision, got.Intent.MutationClass)
	}
}

// TestDesktopSurfaceInteractiveNeverLowersAHold: the interactive obligation
// only rides an outcome that was already allowed. A connector write into the
// operator's browser keeps the hold it had before this dimension existed.
func TestDesktopSurfaceInteractiveNeverLowersAHold(t *testing.T) {
	request := monitorRequest("mcp__claude-in-chrome__navigate", map[string]any{"url": "https://example.invalid"})
	request.Posture = PostureInteractive
	got := MonitorToolAction(request, DefaultConfig(), fixedNow)
	if got.Decision.Outcome != "hold" {
		t.Fatalf("interactive desktop surface lowered a connector hold: %+v", got.Decision)
	}
}

// TestUnattendedDesktopHoldSurvivesEnvelopeAndReevaluation is the depth test:
// the live hook does not reuse the monitor's decision, it re-evaluates the
// intent, and an L3 autonomy envelope clears ordinary holds inside its scope.
// The same command under the same envelope must clear for an interactive
// session and stay held for an unattended lane, on both paths.
func TestUnattendedDesktopHoldSurvivesEnvelopeAndReevaluation(t *testing.T) {
	scope := t.TempDir()
	cfg := envelopeConfig(scope)
	request := func(posture string) ToolActionRequest {
		return ToolActionRequest{
			WorkID:             "work_desktop_surface",
			Actor:              "claude_minimax",
			Harness:            "claude_minimax",
			Tool:               "Bash",
			Cwd:                filepath.Join(scope, "repo"),
			Arguments:          map[string]any{"command": "agent-browser --cdp 9444 click @e1"},
			ToolSchemaRevision: "builtin:bash",
			Posture:            posture,
		}
	}

	interactive := MonitorToolAction(request(PostureInteractive), cfg, envelopeNow())
	if !Allowed(interactive.Decision) {
		t.Fatalf("envelope should clear the interactive operator_browser command: %+v", interactive.Decision)
	}
	if !obligationNames(interactive.Decision.Obligations, DesktopSurfaceOperatorBrowser) {
		t.Fatalf("interactive envelope-cleared decision lost the surface obligation: %+v", interactive.Decision.Obligations)
	}
	// The hook re-evaluates the intent; the obligation must come back from it.
	if again := Evaluate(interactive.Intent, cfg, envelopeNow()); !Allowed(again) ||
		!obligationNames(again.Obligations, DesktopSurfaceOperatorBrowser) {
		t.Fatalf("re-evaluated interactive intent lost the surface obligation: %+v", again)
	}

	unattended := MonitorToolAction(request(PostureUnattended), cfg, envelopeNow())
	if unattended.Decision.Outcome != "hold" {
		t.Fatalf("autonomy envelope cleared an unattended operator_browser action: %+v", unattended.Decision)
	}
	reevaluated := Evaluate(unattended.Intent, cfg, envelopeNow())
	if reevaluated.Outcome != "hold" {
		t.Fatalf("re-evaluated intent lost the unattended desktop hold: %+v", reevaluated)
	}
	if !containsSubstring(reevaluated.Reasons, "desktop_surface:operator_browser:unattended") {
		t.Fatalf("re-evaluated hold does not name the desktop surface: %+v", reevaluated.Reasons)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func containsSubstring(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(value, want) {
			return true
		}
	}
	return false
}

func obligationNames(obligations []string, surface string) bool {
	return containsSubstring(obligations, "desktop_surface:"+surface)
}

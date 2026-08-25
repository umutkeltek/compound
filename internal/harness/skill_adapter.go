package harness

// skill_adapter.go — one Browser skill body, one adapter per harness.
//
// The canonical Browser body lives at ~/.agents/skills/Browser/SKILL.md
// (harness-neutral: backend vocabulary, profile registry, safety rules). Each
// enrolled harness gets an adapter rendered from that body + a per-harness
// template embedded below. Adapters are derived, not hand-edited; the door
// detects drift by byte-comparing the installed file against a fresh render
// and re-writes on --apply.
//
// Public surface (this is the contract the CLI and tests bind to):
//   - HarnessAdapter: name + skill-dir resolver + header template.
//   - DefaultSkillAdapters(): the three adapters the packet calls for
//     (claude_code, codex, hermes), in install order.
//   - SkillAdaptersFor(names []string): subset by name (case-insensitive).
//   - RenderSkillAdapter(body []byte, h HarnessAdapter) ([]byte, error):
//     deterministic render — same body + same adapter ⇒ byte-identical bytes.
//   - InstallReport: per-adapter target path, action, sha256 of body.
//   - InstallSkillAdapters(home, skill, adapters, apply, now) (InstallReport, error):
//     writes to ~/.claude/skills/<skill>/SKILL.md etc.; dry-run by default;
//     replaces a symlink-into-shared-canon with a real dir and reports it.
//
// Privacy: templates are tracked (Go source). They are GENERIC — they name
// tool verbs and CLI shapes, never product handles or personal paths.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// SkillAdapterSchemaVersion tags the rendered adapter footer so a reader can
// detect a stale adapter when the body's content sha256 differs.
const SkillAdapterSchemaVersion = "compound.skill_adapter.v0.1"

// HarnessAdapter is the per-harness render contract: a stable id, the skill
// directory under that harness's HOME, and the harness-specific header that
// is prepended to the canonical body. Templates are kept here as Go string
// constants so they are diff-reviewable in tracked source.
type HarnessAdapter struct {
	// ID is the harness id (matches compound harness list ids). Stable across
	// renders; used in the footer + for path lookups.
	ID string
	// SkillDir returns the absolute path to the per-harness skill dir for the
	// given skill name, rooted at the user's HOME. Implementation may resolve
	// env or hard-coded layouts; tests inject a temp HOME.
	SkillDir func(skill string) string
	// SkillPath is the rendered file path (SkillDir + skill + "/SKILL.md").
	SkillPath func(skill string) string
	// Header is the per-harness invocation block prepended to the body.
	// Generic — names tool verbs, never product handles.
	Header string
}

// DefaultSkillAdapters returns the three adapters the packet specifies.
// Order matters for the install report — it matches the order the CLI prints.
func DefaultSkillAdapters() []HarnessAdapter {
	return []HarnessAdapter{
		claudeCodeAdapter(),
		codexAdapter(),
		hermesAdapter(),
	}
}

// SkillAdaptersFor returns the subset of default adapters whose ids match
// (case-insensitive, trimmed). Empty/unknown list ⇒ nil.
func SkillAdaptersFor(names []string) []HarnessAdapter {
	if len(names) == 0 {
		return nil
	}
	want := make(map[string]bool, len(names))
	for _, n := range names {
		if id := strings.ToLower(strings.TrimSpace(n)); id != "" {
			want[id] = true
		}
	}
	var out []HarnessAdapter
	for _, a := range DefaultSkillAdapters() {
		if want[strings.ToLower(a.ID)] {
			out = append(out, a)
		}
	}
	return out
}

// claudeCodeAdapter: MCP-native invocation (mcp__browser__* tool names), plus
// the Claude-only extras the body explicitly drops (voice-notify curl, PAI
// USER/SKILLCUSTOMIZATIONS dir, MEMORY/SKILLS/execution.jsonl log).
func claudeCodeAdapter() HarnessAdapter {
	header := `---
name: Browser
description: Claude browser-automation adapter. Shared backend (mcporter ` + "`browser`" + ` / Playwright MCP) loaded as native MCP tools; canonical body below. Claude-only extras: voice notify, PAI customizations, execution.jsonl log.
---

# Claude Browser Adapter (rendered, do not edit by hand)

This adapter is rendered by ` + "`compound harness skill-adapter`" + ` from the canonical body at
` + "`~/.agents/skills/Browser/SKILL.md`" + `. If the body changes, re-run the door to refresh.

## Claude invocation

The shared backend (` + "`mcporter browser`" + `) is loaded as MCP tools — call them directly:

- ` + "`mcp__browser__browser_navigate`" + ` url=<url>
- ` + "`mcp__browser__browser_snapshot`" + ` (a11y tree with @eN refs)
- ` + "`mcp__browser__browser_click`" + ` element=<ref> ref=<@eN>
- ` + "`mcp__browser__browser_type`" + ` element=<ref> ref=<@eN> text=<text>
- ` + "`mcp__browser__browser_screenshot`" + ` filename=<path>
- ` + "`mcp__browser__browser_evaluate`" + ` function=<js>  (run JS / extract)

For the agent-browser CLI equivalent:
` + "```" + `bash
agent-browser --profile ~/.agent-browser/profiles/<site> open <url>
agent-browser --profile ~/.agent-browser/profiles/<site> snapshot
` + "```" + `

## Claude-only extras (intentional — body has dropped them)

**MANDATORY: Voice Notification** — when this skill is invoked, run:
` + "```" + `bash
curl -s -X POST http://localhost:31337/notify -H "Content-Type: application/json" \
  -d '{"message":"Running Browser skill to <ACTION>"}' > /dev/null 2>&1 &
` + "```" + `

**Customization lookup** — check ` + "`~/.claude/PAI/USER/SKILLCUSTOMIZATIONS/Browser/`" + ` for
PREFERENCES.md and apply overrides if present.

**Execution log** — append ONE jsonl entry to ` + "`~/.claude/PAI/MEMORY/SKILLS/execution.jsonl`" + `:
` + "```" + `bash
echo '{"ts":"'$(date -u +%Y-%m-%dT%H:%M:%SZ)'","skill":"Browser","input":"8_WORD_SUMMARY","status":"ok|error","duration_s":N}' >> ~/.claude/PAI/MEMORY/SKILLS/execution.jsonl
` + "```" + `

---

`
	return HarnessAdapter{
		ID: "claude_code",
		SkillDir: func(skill string) string {
			return filepath.Join(homeEnv(), ".claude", "skills", skill)
		},
		SkillPath: func(skill string) string {
			return filepath.Join(homeEnv(), ".claude", "skills", skill, "SKILL.md")
		},
		Header: header,
	}
}

// codexAdapter: shell-driven mcporter call (no native MCP for every lane),
// with an explicit note that the bundled browser plugin is disabled by the
// governed `compound act repoint` write — codex reaches the shared backend
// through the shell, not through the bundled plugin.
func codexAdapter() HarnessAdapter {
	header := `---
name: Browser
description: Codex browser-automation adapter. Shared backend via shell (` + "`mcporter call browser.*`" + `); the bundled browser plugin is disabled by governed config (compound act repoint). Canonical body below.
---

# Codex Browser Adapter (rendered, do not edit by hand)

This adapter is rendered by ` + "`compound harness skill-adapter`" + ` from the canonical body at
` + "`~/.agents/skills/Browser/SKILL.md`" + `. If the body changes, re-run the door to refresh.

## Codex invocation (shell-driven)

codex reaches the shared backend through ` + "`mcporter call browser.<tool>`" + `:

` + "```" + `bash
# Find tool names (one-time):  mcporter list browser | jq '.tools[].name'
mcporter call browser.browser_navigate '{"url":"https://example.com"}'
mcporter call browser.browser_snapshot '{}'
mcporter call browser.browser_click '{"element":"<ref>","ref":"@e12"}'
mcporter call browser.browser_type '{"element":"<ref>","ref":"@e15","text":"hello"}'
mcporter call browser.browser_screenshot '{"filename":"/tmp/shot.png"}'
mcporter call browser.browser_evaluate '{"function":"() => document.title"}'
` + "```" + `

For the agent-browser CLI equivalent:
` + "```" + `bash
agent-browser --profile ~/.agent-browser/profiles/<site> open <url>
agent-browser --profile ~/.agent-browser/profiles/<site> snapshot
` + "```" + `

## Bundled plugin is OFF

The bundled ` + "`plugins.\"browser@openai-bundled\"`" + ` in ` + "`~/.codex/config.toml`" + ` is
intentionally disabled (governed ` + "`compound act repoint`" + ` write). codex reaches the
browser through the shared mcporter backend, not through the bundled plugin.
If you find the bundled plugin enabled, that is drift — surface it; do not
re-enable it by hand.

---

`
	return HarnessAdapter{
		ID: "codex",
		SkillDir: func(skill string) string {
			return filepath.Join(homeEnv(), ".codex", "skills", skill)
		},
		SkillPath: func(skill string) string {
			return filepath.Join(homeEnv(), ".codex", "skills", skill, "SKILL.md")
		},
		Header: header,
	}
}

// hermesAdapter: native hermes `browser_*` CDP tools (16 tools) ARE the
// cheapest path on a hermes lane, but they MUST point at the registry's
// user_data_dir so cookies/IndexedDB persist across the shared backend and
// the hermes native tool. Adapter says: use the shared backend for any
// logged-in work; when using the native tool, point it at the same profile.
func hermesAdapter() HarnessAdapter {
	header := `---
name: Browser
description: Hermes browser-automation adapter. Native ` + "`browser_*`" + ` CDP tools (cheapest on hermes) OR shared backend (` + "`mcporter browser`" + `); both MUST point at the same registry ` + "`user_data_dir`" + `. Canonical body below.
---

# Hermes Browser Adapter (rendered, do not edit by hand)

This adapter is rendered by ` + "`compound harness skill-adapter`" + ` from the canonical body at
` + "`~/.agents/skills/Browser/SKILL.md`" + `. If the body changes, re-run the door to refresh.

## Hermes invocation

Hermes has TWO reachable paths. Pick by situation:

**1. Shared backend (mcporter ` + "`browser`" + `)** — for any logged-in work, or when
   hermes's native browser_* tools are blocked:

` + "```" + `bash
mcporter call browser.browser_navigate '{"url":"https://example.com"}'
mcporter call browser.browser_snapshot '{}'
mcporter call browser.browser_click '{"element":"<ref>","ref":"@e12"}'
mcporter call browser.browser_screenshot '{"filename":"/tmp/shot.png"}'
` + "```" + `

**2. Native hermes ` + "`browser_*`" + ` CDP tools (cheapest path on hermes)** — when
   the hermes lane is already in a browser session and you want the in-process
   CDP path. Names map one-to-one to the shared backend's verbs:

- ` + "`browser_navigate`" + ` url=<url>
- ` + "`browser_snapshot`" + ` (a11y tree with @eN refs)
- ` + "`browser_click`" + ` ref=<@eN>
- ` + "`browser_type`" + ` ref=<@eN> text=<text>
- ` + "`browser_screenshot`" + ` path=<path>
- ` + "`browser_evaluate`" + ` expression=<js>

**Profile discipline.** When using the native hermes tools, point them at the
profile's ` + "`user_data_dir`" + ` from ` + "`~/.compound/registries/browser-profiles.json`" + ` so
cookies and IndexedDB persist across the shared backend and the native tool.
Two harnesses sharing the same ` + "`user_data_dir`" + ` fight over cookies — serialize or
use separate profiles.

---

`
	return HarnessAdapter{
		ID: "hermes",
		SkillDir: func(skill string) string {
			return filepath.Join(homeEnv(), ".hermes", "skills", skill)
		},
		SkillPath: func(skill string) string {
			return filepath.Join(homeEnv(), ".hermes", "skills", skill, "SKILL.md")
		},
		Header: header,
	}
}

// homeEnv is the indirection point for $HOME — overridable in tests by
// setting the env var HOME to a temp dir. Production reads the real env.
func homeEnv() string {
	return os.Getenv("HOME")
}

// RenderSkillAdapter deterministically combines header + body + footer.
// Same (body, adapter, now) ⇒ byte-identical output. The footer carries the
// body's sha256 so a reader can verify the adapter matches the current body.
// now is injected so tests can pin time and prove byte-identical re-renders.
func RenderSkillAdapter(body []byte, h HarnessAdapter, now time.Time) ([]byte, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("render skill adapter %q: body is empty", h.ID)
	}
	if strings.TrimSpace(h.ID) == "" {
		return nil, fmt.Errorf("render skill adapter: id is empty")
	}
	if now.IsZero() {
		now = time.Now()
	}
	sum := sha256.Sum256(body)
	bodyHash := hex.EncodeToString(sum[:])

	var b strings.Builder
	b.WriteString(h.Header)
	// Body verbatim — no rewriting, no template expansion. The whole point
	// of the door is that the body is the canonical owner; the adapter is
	// just a thin harness-syntax wrapper.
	b.Write(body)
	if len(body) > 0 && body[len(body)-1] != '\n' {
		b.WriteByte('\n')
	}
	// Footer: schema version + harness id + body sha256 + generation time.
	// The generation time is injected (zero ⇒ now) so two renders of the
	// same body+adapter+time are byte-identical.
	fmt.Fprintf(&b, "\n<!-- %s harness=%s body_sha256=%s generated=%s -->\n",
		SkillAdapterSchemaVersion, h.ID, bodyHash, now.UTC().Format(time.RFC3339))
	return []byte(b.String()), nil
}

// InstallAction is one of: write (fresh install or replace symlink with real
// dir), update (content changed), unchanged (byte-identical), skip (target
// path is outside the user's HOME).
type InstallAction string

const (
	ActionWrite     InstallAction = "write"
	ActionUpdate    InstallAction = "update"
	ActionUnchanged InstallAction = "unchanged"
	ActionSkip      InstallAction = "skip"
)

// AdapterInstall is one adapter's outcome.
type AdapterInstall struct {
	Harness    string       `json:"harness"`
	Path       string       `json:"path"`
	Action     InstallAction `json:"action"`
	BodySHA256 string       `json:"body_sha256"`
	Detail     string       `json:"detail,omitempty"`
}

// InstallReport summarizes one install run.
type InstallReport struct {
	Skill    string           `json:"skill"`
	Apply    bool             `json:"apply"`
	Adapters []AdapterInstall `json:"adapters"`
}

// InstallSkillAdapters renders each adapter and (when apply=true) writes it
// to the harness's skill dir. dry-run by default; the report records what
// WOULD happen. The home argument is accepted for testability but currently
// the path resolution reads $HOME (each adapter's SkillPath is HOME-rooted).
//
// bodyOverride is for tests — when non-empty, the renderer reads from a
// fixture path instead of the canonical ~/.agents/skills/Browser/SKILL.md.
// Production callers pass nil.
//
// Symlink policy: if a target like ~/.codex/skills/Browser/SKILL.md is a
// symlink into a shared-canon dir (a real entry in the spine-owned symlink
// layer), we replace the symlink with a real file in a real dir, record the
// replacement in the report's Detail, and continue. codex has no Browser
// entry today; claude has a symlink-shaped dir under .claude/skills/Browser —
// that is detected and replaced by a real dir here.
func InstallSkillAdapters(skill string, adapters []HarnessAdapter, apply bool, now time.Time, bodyOverride []byte) (InstallReport, error) {
	if strings.TrimSpace(skill) == "" {
		return InstallReport{}, fmt.Errorf("install skill adapters: skill name is empty")
	}
	if now.IsZero() {
		now = time.Now()
	}
	body := bodyOverride
	if body == nil {
		body = mustReadBody()
	}
	rep := InstallReport{Skill: skill, Apply: apply}
	for _, h := range adapters {
		entry := AdapterInstall{Harness: h.ID}
		path := h.SkillPath(skill)
		entry.Path = path

		rendered, err := RenderSkillAdapter(body, h, now)
		if err != nil {
			return rep, fmt.Errorf("render %s: %w", h.ID, err)
		}
		sum := sha256.Sum256(rendered)
		entry.BodySHA256 = hex.EncodeToString(sum[:])

		// If the target's parent is a symlink into shared canon, replace with
		// a real dir before writing the file. The packet calls this out: codex
		// has no Browser entry (so writing creates a fresh dir); claude has
		// one that may be a symlink (so we materialize a real dir).
		parent := filepath.Dir(path)
		replaced, repDetail, err := materializeSymlinkedParent(parent, apply)
		if err != nil {
			entry.Action = ActionSkip
			entry.Detail = err.Error()
			rep.Adapters = append(rep.Adapters, entry)
			continue
		}
		if replaced {
			entry.Detail = repDetail
		}

		existing, err := os.ReadFile(path)
		switch {
		case err != nil && !os.IsNotExist(err):
			entry.Action = ActionSkip
			entry.Detail = err.Error()
			rep.Adapters = append(rep.Adapters, entry)
			continue
		case err != nil && os.IsNotExist(err):
			entry.Action = ActionWrite
		case string(existing) == string(rendered):
			entry.Action = ActionUnchanged
		default:
			entry.Action = ActionUpdate
		}

		if apply {
			if err := os.MkdirAll(parent, 0o755); err != nil {
				entry.Action = ActionSkip
				entry.Detail = err.Error()
				rep.Adapters = append(rep.Adapters, entry)
				continue
			}
			if err := os.WriteFile(path, rendered, 0o644); err != nil {
				entry.Action = ActionSkip
				entry.Detail = err.Error()
				rep.Adapters = append(rep.Adapters, entry)
				continue
			}
		}
		rep.Adapters = append(rep.Adapters, entry)
	}
	// Stable order in the report.
	sort.SliceStable(rep.Adapters, func(i, j int) bool {
		return rep.Adapters[i].Harness < rep.Adapters[j].Harness
	})
	return rep, nil
}

// materializeSymlinkedParent ensures the parent dir of the target is a real
// directory under the user's HOME. If any ancestor of the parent (within
// HOME) is a symlink into a shared canon dir (the spine-owned symlink
// layer), removing that symlink and mkdir'ing a real dir at the same path
// is what lets the door OWN the adapter file content. Stops at HOME so we
// never touch system dirs (/var, /tmp) — those can be symlinks too and we
// must not rewrite them. Returns (replaced, detail, err).
func materializeSymlinkedParent(parent string, apply bool) (bool, string, error) {
	home := homeEnv()
	if strings.TrimSpace(home) == "" {
		return false, "", nil
	}
	// Walk up from `parent` to HOME (inclusive). HOME itself is the floor;
	// do not try to rewrite anything at or above it.
	abs, err := filepath.Abs(parent)
	if err != nil {
		return false, "", err
	}
	homeAbs, err := filepath.Abs(home)
	if err != nil {
		return false, "", err
	}
	for cur := abs; ; cur = filepath.Dir(cur) {
		// Stop when we have walked ABOVE HOME (cur shorter than HOME OR cur
		// not a path-prefix match of HOME). HOME itself can be a symlink
		// (e.g. on macOS /var -> /private/var) — do not touch it.
		if cur == homeAbs || !strings.HasPrefix(cur, homeAbs+string(filepath.Separator)) {
			return false, "", nil
		}
		fi, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				// Missing path is fine — the caller will MkdirAll.
				return false, "", nil
			}
			return false, "", err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(cur)
			detail := fmt.Sprintf("replaced symlink %s -> %s with real dir", cur, target)
			if apply {
				if err := os.Remove(cur); err != nil {
					return false, "", fmt.Errorf("remove symlink %s: %w", cur, err)
				}
				if err := os.MkdirAll(cur, 0o755); err != nil {
					return false, "", fmt.Errorf("mkdir %s: %w", cur, err)
				}
			}
			return true, detail, nil
		}
		if !fi.IsDir() {
			return false, "", fmt.Errorf("parent %s is not a directory (mode=%v)", cur, fi.Mode())
		}
	}
}

// mustReadBody reads the canonical body from the standard location. The body
// path is HOME-rooted because ~/.agents/skills is a symlink that the spine
// owns; we resolve it once per call (cheap).
func mustReadBody() []byte {
	home := homeEnv()
	body, err := os.ReadFile(filepath.Join(home, ".agents", "skills", "Browser", "SKILL.md"))
	if err != nil {
		// Tests inject a HOME that points at a temp dir; if the body is
		// missing the caller will get a render error from the empty-body
		// check above. Production reads the real body.
		return []byte{}
	}
	return body
}

// FormatInstallReport renders an InstallReport as the compact CLI text
// shape. Used by the CLI dispatcher; --json path uses json.Marshal directly.
func FormatInstallReport(rep InstallReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# harness skill-adapter skill=%s apply=%t\n", rep.Skill, rep.Apply)
	for _, a := range rep.Adapters {
		fmt.Fprintf(&b, "- harness=%s action=%s path=%s\n", a.Harness, a.Action, a.Path)
		if a.Detail != "" {
			fmt.Fprintf(&b, "  detail: %s\n", a.Detail)
		}
	}
	return b.String()
}

// RunSkillAdapterCLI parses flags + dispatches + prints. It exists so the
// cmd/compound dispatcher stays at a tiny delta — the work lives here.
func RunSkillAdapterCLI(args []string, apply, asJSON bool, now time.Time) error {
	skill := strings.TrimSpace(skillAdapterFlag(args, "--skill"))
	if skill == "" {
		return fmt.Errorf("usage: compound harness skill-adapter --skill NAME [--harness H] [--apply] [--json]")
	}
	adapters := SkillAdaptersFor(strings.Split(skillAdapterFlag(args, "--harness"), ","))
	if len(adapters) == 0 {
		adapters = DefaultSkillAdapters()
	}
	rep, err := InstallSkillAdapters(skill, adapters, apply, now, nil)
	if err != nil {
		return err
	}
	if asJSON {
		b, err := json.Marshal(rep)
		if err != nil {
			return err
		}
		fmt.Println(string(b))
		return nil
	}
	fmt.Print(FormatInstallReport(rep))
	return nil
}

// skillAdapterFlag parses --flag value out of an args slice. Tiny copy of
// cmd/compound's flagValue so the package owns its CLI.
func skillAdapterFlag(args []string, name string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(args[i], name+"=") {
			return strings.TrimPrefix(args[i], name+"=")
		}
	}
	return ""
}
# Harness Wiring — connect Compound to your agent

> Compound is **agent-agnostic**: it works with Claude Code, Codex, OpenCode,
> or any harness that can call a CLI at lifecycle boundaries. This guide shows
> how to wire the hooks so your agent sessions compound instead of evaporating.

## How it works

Compound exposes **lifecycle hooks** — CLI commands your harness calls at
session boundaries. Each hook produces a typed spine object:

```text
session-start     → ContextPack (what the agent should know right now)
user-prompt-submit → ContextPack (refreshed context mid-session)
pre-tool-use      → PolicyDecision (allow / deny / hold the mutation)
post-tool-use     → ToolEvent (evidence of what happened)
session-end       → Receipt + Transition (what was learned, what changed)
```

The harness calls Compound; Compound never calls the harness. This is
**hooks-as-clients**: the harness is the client, Compound is the authority.

## Claude Code

Claude Code supports hooks in `.claude/settings.json` (project-level) or
`~/.claude/settings.json` (global).

### Option A: native blocking hooks (recommended)

Add to your project's `.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "*",
        "hooks": [
          {
            "type": "command",
            "command": "compound hook pre-tool-use --enforce --live --harness claude-code"
          }
        ]
      }
    ]
  }
}
```

With `--enforce`, Compound returns policy gate exit codes:
- Exit 0 → allow (the tool call proceeds)
- Exit 2 → deny (the tool call is blocked, agent sees the reason)

### Option B: import a generated hook roster

If you have a generated hooks JSON (e.g., from Codex's hook generation):

```sh
compound contract import-hooks --codex-hooks-json fixtures/harness/codex-hooks-json.json
compound contract hooks-authority   # verify the import
```

This installs the roster as `compound://config/installed-hooks`. After import,
`compound orient` and `compound contract compile` read the Compound-owned
manifest.

## Codex

Codex supports a `hooks.json` in the project root:

```json
{
  "session_start": {
    "command": "compound hook session-start --live --harness codex"
  },
  "pre_tool_use": {
    "command": "compound hook pre-tool-use --enforce --live --harness codex"
  },
  "session_end": {
    "command": "compound hook session-end --live --harness codex"
  }
}
```

Or import it:

```sh
compound contract import-hooks --codex-hooks-json hooks.json
```

## OpenCode / any CLI harness

Any harness that can run a shell command at lifecycle boundaries can integrate:

```sh
# At session start:
compound hook session-start --live --harness opencode

# Before each tool call:
compound hook pre-tool-use --enforce --live --harness opencode

# At session end:
compound hook session-end --live --harness opencode
```

The `--harness` flag tells Compound which harness is calling, so it can
tailor the ContextPack (e.g., Codex gets different context than Claude Code).

## What each hook does

### session-start

Produces a **ContextPack**: task-scoped orientation for the agent.

```sh
compound hook session-start --live --harness claude-code
```

The agent receives:
- Relevant memory claims (only what's relevant to the task)
- Active capability offers (skills/tools that apply)
- Policy posture (what's allowed, what needs approval)
- Excluded context (what was deliberately left out and why)

### pre-tool-use

Evaluates an **ActionIntent** against policy before the tool runs.

```sh
compound hook pre-tool-use --enforce --live --harness claude-code
```

Returns a **PolicyDecision**:
- `allow` → proceed
- `allow_with_obligations` → proceed with conditions
- `hold` → needs human approval
- `deny` → blocked (agent sees the reason)

Policy is **enforced, not asked**. No prompt injection can bypass it — the
gate is in the CLI exit code, not in the prompt.

### session-end

Produces a **Receipt** (what was learned) and a **Transition** (what changed).

```sh
compound hook session-end --live --harness claude-code
```

This is where compounding happens: the session's learnings become durable
spine objects that the next session's `session-start` will recall.

## Fixture mode (testing without a live harness)

You can test hooks without a running harness using fixtures:

```sh
# See available fixtures:
ls fixtures/hooks/

# Run a fixture:
compound hook session-start fixtures/hooks/session-start.json
compound hook pre-tool-use fixtures/hooks/pre-tool-use.claude.json
```

Fixtures are synthetic — no personal data, no live harness needed. They're the
same fixtures CI uses to prove hook behavior.

## Verifying your wiring

```sh
# Check hook installation status:
compound contract hooks-authority

# Run the full orientation and see what context your agent would get:
compound orient --harness claude-code --cwd /path/to/your/project --task "my task"

# Check the harness registry:
compound harness observe --os-entrypoints
```

## The harness registry

Compound maintains a **HarnessRegistry** that records what each harness
actually supports (native blocking hooks, fallback adapters, etc.):

```sh
compound harness observe --os-entrypoints   # detect installed harnesses
```

This is evidence-only — it observes what's installed, never configures or
mutates your harness.

## Agent-agnosticism proof

Compound proves agent-agnosticism by **checkpoint replacement**: Claude starts
a task → checkpoint → Codex resumes → verification succeeds from spine context
alone. The ContextPack carries everything the next agent needs, regardless of
which harness runs it.

## Troubleshooting

| Symptom | Fix |
|---|---|
| `compound: command not found` in hook | Ensure `~/.local/bin` is on PATH |
| Hook returns empty context | Run `compound home doctor` — home may not be initialized |
| Policy always denies | Check `compound policy operator-authority` — operator state may not be imported |
| ContextPack has no memory | Run `compound memory doctor` — memory may be empty (expected on fresh home) |

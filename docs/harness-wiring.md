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

## One semantic front door

Choose the posture in Compound terms, not in vendor event names:

```sh
compound harness posture-plan --harness claude-code
compound harness posture-plan --harness claude-zai
compound harness posture-plan --harness codex
compound harness posture-plan --harness opencode
```

The stable intentions are `prepare_context`, `guard_mutation`, and
`observe_outcome`. The selected adapter owns native events, settings schema,
matchers, and limitations. Every compiled intention says `native`, `fallback`,
`degraded`, or `unsupported` and cites the harness attestation revision.

Default postures may select a subset. Claude-family defaults select all three
intentions and install `PreToolUse` with explicit `--enforce`. The Codex
default selects `prepare_context` and `observe_outcome`; `hook install` omits
and reconciles away only Compound's `PreToolUse` binding.

Claude-family and Codex `compound hook install` consume this projection when
they write managed hooks. The OpenCode installer asks Compound for the same
projection and refuses adapter drift before writing its config/plugin. OpenCode
currently reports `guard_mutation=unsupported`; its before-tool callback cannot
deny, so Compound will not call it mutation-ready.

The vendor-shaped examples below are adapter evidence and troubleshooting
fallbacks. They are not the operator contract.

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
- Exit 3 → hold (the tool call waits for operator approval)
- Exit 4 → deny (the tool call is blocked and the agent sees the reason)

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

```sh
compound harness posture-plan --harness codex
compound hook install --harness codex --scope global
compound hook install --harness codex --scope global --check --json
```

The Codex adapter compiles the stable intentions into the current native
`hooks.json` event schema and no-clobber merges Compound's bindings. Operators
do not maintain that schema directly. The default is observation-only and does
not claim mutation readiness: it installs `SessionStart`, `UserPromptSubmit`,
and `PostToolUse`, removes a stale Compound-owned `PreToolUse`, and preserves
unrelated user hooks. Native blocking remains an explicit posture choice.

## OpenCode

OpenCode is a context-injection and outcome-observation lane. Its current
before-tool callback cannot deny a tool call, so it is **not** a mutation-control
harness. Do not wire `pre-tool-use --enforce` for OpenCode and do not use it for
mutation-gated execution. Use a mutation-ready Claude-family or Codex hook lane
when Compound must block an action before it runs.

Install the repo-owned adapter from the Compound checkout:

```sh
bun adapters/opencode/install.ts
compound harness posture-plan --harness opencode
```

The installer registers only the native lifecycle it can honestly provide:

```text
chat.message        → context injection
tool.execute.after  → outcome observation
guard_mutation      → unsupported
```

It validates that posture before changing `opencode.jsonc` or the plugin.

## Other CLI harnesses

A custom harness may call Compound at lifecycle boundaries, but it is
mutation-ready only if it can synchronously propagate Compound's deny result
before tool execution. Model the harness attestation and inspect
`compound harness posture-plan --harness <name>` before relying on it as a
guard. Context injection and post-tool observation alone do not create a
mutation-control boundary.

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

With `--enforce`, Compound publishes the harness-native blocking decision and
returns the policy gate exit (`3` for hold, `4` for deny; Codex maps a hold to
native `deny` until approval). Without `--enforce`, the hook still records the
ActionIntent and PolicyDecision, but publishes no native permission decision
and does not turn that policy outcome into a blocking process exit.

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

## Governed execution and supervised sessions

The registry says what a harness can do; `compound://registries/model-router`
owns the executable lane tuple used to run it. A tuple binds the harness id,
wrapper aliases, requested provider route, permission posture, and optional
capacity source. Compound resolves that tuple before moving work to `running`.
An unknown or ambiguous tuple, missing wrapper, invalid posture, or stale/
exhausted declared capacity leaves the work `claimed` and creates no execution
RunAttempt.

Run a bounded task inline:

```sh
compound work advance <work_id> --to running --execute \
  --harness claude_minimax --workspace /path/to/project \
  --prompt "implement the claimed slice"
```

Run a supervised task in tmux so an operator or agent can inspect and steer it:

```sh
compound work delegate <work_id> --harness claude_qwen \
  --workspace /path/to/project --prompt "process the remaining batch"
compound work delegate status <work_id>
compound work delegate progress <work_id> \
  --done 12 --empty 2 --failed 1 --remaining 5
compound work steer <work_id> --text "finish the current item, then stop" --enter
compound work attach <work_id>
```

A raw command after `--` replaces only the executable. It cannot bypass tuple
existence, provider attribution, or capacity admission. RunAttempt stores the
requested tuple separately from observed `model_id`/`provider_route`; those
observed fields remain empty until route evidence attests what actually ran.
The console reads the same binding and RunAttempt, including cumulative
done/empty/failed/remaining progress.

Idle schedulers are clients of this path, not parallel execution owners. They
may decide *when* a machine is unattended, but select lanes from Compound's
capability route plan and invoke `work delegate`; provider thresholds, wrapper
tables, STOP files, local attempt logs, and task receipts do not belong in the
scheduler. A terminal delegated run closes through the same WorkEnvelope
Receipt + Transition chain as an interactive run.

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

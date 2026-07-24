# Getting Started — Compound + your repo + your agent in 15 minutes

> Goal: install Compound, point it at **your** repo, wire it into **your** agent
> (Claude Code, Codex, or any harness), and see the spine work end to end — one
> work envelope from creation through a receipt, with the next session starting
> smarter. Zero personal data required; everything runs locally.

Compound is local-first and makes agent work **accumulate**: it records the
work as it actually happens — every session leaves claims, receipts, and
lenses behind, and the next session starts smarter because of them. This
guide proves that loop on your machine.

Hold the whole system as five places; each step below visits one:

| Place | What happens there | Step |
|---|---|---|
| **the door** | your task enters as a typed envelope, validated first | 3 |
| **the ledger** | adjudicated meaning; serves the briefing your agent starts from | 2 |
| **the edge** | your agent — a rented executor — claims and runs the job | 4 |
| **the gate** | mutations ask policy before they happen | 6 |
| **the receipt** | proof of what ran, plus what it should change next time | 5 |

## Prerequisites

- **Git** (Compound uses git for delivery proof).
- **Go 1.22+** — only required if you build from source (the prebuilt binary
  path needs no Go).
- That's it. No cloud account, no API key, no external service.

Optional (for the console): **Bun** and **Node 20+**.
Optional (for semantic memory): a local **Ollama** server — see
[`memory-quickstart.md`](./memory-quickstart.md).

## 1. Install

Pick one:

### Option A — prebuilt binary (no Go required)

Download a signed tarball from the [latest release](https://github.com/umutkeltek/compound/releases/latest)
and run the installer. Defaults install to `~/.local/bin` and initialize
`~/.compound`; override with `GOBIN` and `COMPOUND_HOME`.

```sh
curl -fsSL https://raw.githubusercontent.com/umutkeltek/compound/main/scripts/install-release.sh \
  | sh
```

That one-liner downloads the right tarball for your OS/arch, verifies its
SHA256 against the bundled `SHA256SUMS`, drops the binaries on your PATH, and
runs `compound home init` + `home doctor`.

Offline / air-gapped? Point the installer at a tarball you already have:

```sh
COMPOUND_RELEASE_TARBALL=/path/to/compound_0.1.0_<os>_<arch>.tar.gz \
  sh scripts/install-release.sh
```

### Option B — build from source

```sh
git clone https://github.com/umutkeltek/compound.git
cd compound
make install
```

This builds the `compound` binary into `~/.local/bin/`, initializes a
`COMPOUND_HOME` at `~/.compound`, and runs `home doctor` to verify the layout.

```sh
# Make sure it's on your PATH:
export PATH="$HOME/.local/bin:$PATH"
compound version
```

## 2. Orient on YOUR repo

Point Compound at any repo you work on. This produces a **ContextPack** — the
frozen context an agent would consume, including what was included **and
excluded** and why.

```sh
cd /path/to/your/project
compound orient --harness claude-code --cwd . --task "describe what this project does"
```

You'll see a JSON ContextPack. Key fields:

- `summary_md` — plain-language orientation summary
- `included[]` — what context was served (and why)
- `excluded[]` — what was deliberately left out (and why)
- `task_contract` — the intent you gave

This is the spine's first move: **discover reality before acting.** The agent
gets task-scoped context, not a wall of everything.

## 3. Create a work envelope

A WorkEnvelope is one unit of work with a stable identity (`work_id`) that
links intent → context → action → receipt → transition.

```sh
compound work create \
  --owner "getting-started" \
  --predicate "the README has a contributors section" \
  --state ready \
  --by "operator:you"
```

Note the returned `work_id` (e.g., `work_a1b2c3d4e5f6`).

```sh
# See it on the board:
compound workbench

# See the full envelope:
compound work show <work_id>
```

## 4. Claim and advance

```sh
# Claim it (single-holder lease):
compound work claim <work_id> --by "agent:claude-code:session-1"

# Advance to running:
compound work advance <work_id> --to running --by "agent:claude-code:session-1"
```

## 5. Close with proof

When the work is done, close it with evidence. This mints a **Receipt** (what
was learned) and a **Transition** (what changed and how future behavior should
differ).

```sh
compound work close <work_id> \
  --by "agent:claude-code:session-1" \
  --move-type code_change \
  --changed \
  --proof-ref "commit:$(git rev-parse HEAD)"
```

```sh
# See the proof chain:
compound work show <work_id>

# See the transition:
compound transition show <transition_id>
```

The Transition is the ledger entry: the system moved from state A to B because
of this work — what changed, which evidence supports it, and how future
behavior should differ. This is what makes the next session start smarter.

## 6. Wire your agent (hooks)

Compound integrates with agent harnesses through **lifecycle hooks**. See
[`harness-wiring.md`](./harness-wiring.md) for the full guide. The short
version for Claude Code:

```sh
# In your project's .claude/settings.json, add hook commands that call:
compound hook session-start --live --harness claude-code
compound hook pre-tool-use --live --harness claude-code
compound hook session-end --live --harness claude-code
```

When hooks are wired:
- **Session start** → agent receives a ContextPack (task-scoped orientation)
- **Pre-tool-use** → mutations pass through policy (allow/deny/hold)
- **Session end** → work produces a Receipt + Transition (learning persists)

## 7. Verify the loop

```sh
# The workbench shows your closed work with its receipt:
compound workbench

# Memory doctor confirms the system is healthy:
compound memory doctor

# Architecture doctor confirms structural integrity:
compound architecture doctor --strict
```

## What just happened

```text
orient          → the briefing (ContextPack: task-scoped context, not a preamble)
work create     → the job at the door (WorkEnvelope: one stable identity)
claim/advance   → the edge at work (single-holder lease, state machine)
close           → the receipt (Receipt + Transition: proof behavior changed)
hooks           → the gate, plus next session's briefing carries prior learning
```

**The loop compounds.** The next `compound orient` on this repo includes the
transition evidence. The next agent session starts smarter because of what the
last one proved.

## Next steps

| I want to... | Read |
|---|---|
| Wire Compound into my agent harness | [`harness-wiring.md`](./harness-wiring.md) |
| Set up semantic memory (search over my docs) | [`memory-quickstart.md`](./memory-quickstart.md) |
| See the visual console | [`console.md`](./console.md) |
| Understand the architecture | [`LAYERS.md`](./LAYERS.md) |
| Contribute to Compound itself | [`CONTRIBUTING.md`](../CONTRIBUTING.md) |

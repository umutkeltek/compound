# Console — the visual surface for Compound

> The console is a **local, operator-owned** web dashboard on port 3777. It
> reads from `COMPOUND_HOME` through the `compound` CLI — it never mutates
> state, never owns meaning, and never becomes a second authority. It is a
> **projection**: a rebuildable view over the spine's objects.

## Quick start

```sh
cd console
bun install
compound console --operator
# → http://localhost:3777
```

Or using the package.json scripts:

```sh
cd console
bun install
bun run dev        # development mode (hot reload)
bun run build      # production build
bun run serve      # serve the production build
```

The `--operator` flag enables operator-level views (workbench, reviews,
decisions). Without it, the console runs in a restricted read-only mode.

## What you'll see

### Briefing (home page)

The main page is an **operator briefing** — one screen that answers "what
needs my attention right now?"

| Section | What it shows |
|---|---|
| **Headline** | The single most important thing right now |
| **Continue card** | The highest-leverage next action (one click to continue) |
| **Decide list** | Decisions that genuinely need your judgment |
| **Needs me** | Tasks, sources, or decisions blocked on operator input |
| **Pipeline strip** | Work items by lifecycle stage (proposed → ready → running → closed) |
| **KPI strip** | Key metrics: open work, claims, transitions, capability count |
| **Activity feed** | Recent receipts and transitions |
| **Self-healing panel** | Divergences between expected and observed state |
| **Knows strip** | What Compound currently knows (promoted claims) |

### Work (`/work`)

The work board shows all WorkEnvelopes with their lifecycle state, owner,
success predicate, and proof chain. Click any item to see the full envelope
with receipts, transitions, and run attempts.

### Memory (`/memory`)

Browse promoted claims, positions, and patterns. Search the memory ledger.
See the evidence chain behind any claim.

### Reviews (`/reviews`)

The owner-review queue: candidates, hypotheses, and proposals that need your
judgment. Each review card shows the evidence, the decision options, and the
impact of each choice.

### Executors (`/executors`)

Live view of running agent sessions, their work items, and liveness status.
Dead executors are flagged for re-dispatch.

### Routing (`/routing`)

Model routing evidence: which lanes/models served which calls, at what cost,
with what outcome. Route evidence, not task success.

### Sources (`/sources`)

Source observations and their adjudication status. What came in, what was
promoted, what was parked or rejected.

### Flow (`/flow`)

Visual flow diagram of the spine: intent → context → work → policy → receipt
→ transition. See how objects connect.

### Standing Law (`/standing-law`)

The policy posture: standing grants, operator state, placement rules, and
autonomy ceiling. What the system is allowed to do without asking.

### Settings (`/settings`)

Instance identity, home path, and configuration.

## Design principles

The console follows three orthogonal axes (see `console/DESIGN.md`):

| Axis | Question | Visual encoding |
|---|---|---|
| **Domain** | Which subsystem? | Cool hues (blue, violet, cyan) — accent only, never fill |
| **State** | Where in lifecycle? | Neutral + blue-live + warm trio — the pill on each card |
| **Severity** | How alarming? | Green / amber / red — health tiles, meters, banners |

Amber-as-fill is reserved for one thing: **the admission seam** — a card that
needs the operator gets a 3px amber left seam. Scanning left edges down a page
answers "what needs me."

## Architecture

```text
Browser
  → Next.js SSR (console/src/app/)
    → compound CLI (subprocess calls, e.g. `compound workbench --json`)
      → COMPOUND_HOME (read-only)
```

- The console **never writes** to `COMPOUND_HOME`. It is a read-only projection.
- All data comes through the `compound` CLI as JSON subprocess calls.
- Mutations (when enabled) go through the same policy-gated seams as the CLI.
- The console is rebuildable: delete it, re-run `compound console --operator`,
  and it regenerates from the same spine objects.

## Live stream

The console includes a server-sent events (SSE) stream at `/api/stream` that
tails the work-event log and ledger in real time. New receipts, transitions,
and memory updates appear without refresh.

```text
compound stream --follow   → SSE frames → console auto-updates
```

## Console health check

```sh
compound console status --json
```

Verifies: console identity, HTTP reachability, and static-asset health.

## Troubleshooting

| Symptom | Fix |
|---|---|
| Port 3777 in use | `lsof -i :3777` to find the process; kill or change port |
| Blank page | Run `compound home doctor` — home may not be initialized |
| No data on briefing | Expected on a fresh home — create some work first |
| Build errors | `cd console && bun install` — dependencies may be missing |
| Stale data | The console reads live; refresh the page or check the stream |

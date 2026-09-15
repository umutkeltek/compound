# Console — the visual surface for Compound

> The console is a **local, operator-owned** web dashboard on port 3777. It
> reads from `COMPOUND_HOME` through the `compound` CLI — it never owns
> meaning and never becomes a second authority. It is a **projection**: a
> rebuildable view over the spine's objects. The default launcher leaves both
> mutations and private fallbacks disabled. `--mutations` is an explicit local
> API-extension opt-in; even then, mutation routes require a same-origin
> localhost request carrying an out-of-band 64-hex-character capability in
> `x-compound-console-capability`. The standard browser never receives that
> capability and remains projection-only. Direct private document reads are
> unavailable; use a Compound-owned projection or scoped CLI read surface.

## Quick start

```sh
cd console
bun install
compound console --operator
# → http://localhost:3777
```

This default is projection-only. A trusted local operator may opt into the
existing governed mutation adapter outside the supported public-alpha console
surface:

```sh
export COMPOUND_HOME="/path/to/operator-home"
export COMPOUND_CONSOLE_CAPABILITY="$(openssl rand -hex 32)"
compound console --operator --mutations
```

The operator owns high-entropy generation; Compound validates the exact
64-hex-character transport encoding and value. A direct extension client sends
that exact value only in the capability header. Missing, malformed, or wrong
configuration fails closed, and Compound does not issue capability cookies or
include the value in launch JSON.

Or using the package.json scripts:

```sh
cd console
bun install
bun run dev        # development mode (hot reload)
bun run build      # production build
bun run serve      # serve the production build
```

The `--operator` flag explicitly selects the local operator console lifecycle;
the launcher refuses to start without it. It does not grant mutation authority.
The normal operator console remains projection-only, and only the unsupported
`--mutations` API extension enables mutation routes for an out-of-band client.

## What you'll see

The current left rail is a stable, needs-led map. Backend commands and
projections appear inside the destination that owns their operator question;
they do not automatically become new navigation rows.

| Destination | Operator question | Context it owns |
|---|---|---|
| **Today** | What deserves my attention now? | Cross-domain attention and recently proven change |
| **Live** | What is observed moving, waiting, stale, or unreachable? | RunAttempts, heartbeats, executors, leases, and coordination |
| **Work** | What is moving, blocked, ready, or waiting for proof? | WorkEnvelopes, ownership, lifecycle, Receipts, and Transitions |
| **Decide** | Which consequential calls need my judgment? | Evidence, consequence, reversibility, authority, and safe actions |
| **Knowledge** | What does Compound know, why, and how current is it? | Claims, candidates, positions, patterns, Sources, and provenance |
| **System** | What technical and authority configuration is effective? | Capabilities, routing, hooks, health, Settings, and Standing law |
| **Outcomes** | What external change fed learning back into the system? | Product signals, hypotheses, reviews, learned next moves, and paid acquisition (Ads: spend, CPA, ROAS per platform, product, campaign — see [`docs/operations/ads-signals.md`](operations/ads-signals.md)) |

Ask Compound is a global read-only overlay rather than another destination. A
Live row opens the same WorkEnvelope in Work; Today links to the owning Work or
decision record; evidence links to its Knowledge source; contextual authority
links to the full System record.

The binding context-ownership and progressive-disclosure rules live in
[`console/DESIGN.md`](../console/DESIGN.md#10-experience-architecture--simple-first-depth-on-demand).

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

- The supported standard console **never writes** to `COMPOUND_HOME`. It is a
  read-only projection.
- All data comes through the `compound` CLI as JSON subprocess calls.
- The unsupported mutation API extension, when explicitly enabled, goes
  through the same policy-gated seams as the CLI.
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

## Capability non-disclosure canary

Before certifying a release candidate, run:

```sh
node console/scripts/capability-profile-canary.mjs
```

The canary builds the candidate binary, initializes a throwaway home, performs
a sanitized production build, and launches real Next development and
production profiles on an ephemeral loopback port with a nonce capability. It
checks HTML, RSC payloads, every referenced static asset, mutation error
responses, cookies, and captured process output for raw or encoded capability
or legacy-session material. It also proves that missing, wrong, cookie-only,
query-only, and body-only credentials fail closed while the exact header reaches
request validation without executing a mutation.

## Troubleshooting

| Symptom | Fix |
|---|---|
| Port 3777 in use | `lsof -i :3777` to find the process; kill or change port |
| Blank page | Run `compound home doctor` — home may not be initialized |
| No data on briefing | Expected on a fresh home — create some work first |
| Build errors | `cd console && bun install` — dependencies may be missing |
| Stale data | The console reads live; refresh the page or check the stream |

# compound console

The operator control surface. It renders **compound projections only** — every card's data
comes from read-only `compound … --json` output (workbench, reviews, work) against
`COMPOUND_HOME`; nothing else. That single constraint is what keeps it a control surface
instead of status theater.

## Run

```
cd console
bun install
bun run dev        # http://localhost:3777
```

Requirements: the `compound` binary on PATH (or `COMPOUND_BIN=/path/to/compound`), a
`COMPOUND_HOME` (defaults to `~/.compound`).

## Truth contract

- Every card declares its source and freshness (the chip in its corner).
- **Reads are projections only** — every card's data comes from read-only `compound … --json`.
- Most acts surface as **exact CLI commands you copy** (DEC-0021: auto-surface, never
  auto-execute). Three routes under `src/app/api/` DO mutate — admit/cancel work, execute a
  server-resolved decision, record a review — the allowlisted, localhost-only acts the operator
  runs daily; each re-resolves its command server-side against the projection (client argv is
  never trusted). These are **not yet token-gated**; default-off gating is a tracked hardening
  step. Everything else is read-only.
- If a card can't name its projection, it doesn't ship.

## Base + append

The base grid is stable: Dashboard (needs-me → running → changed), Work board, Reviews,
Memory, the lens-switchable **System Map** (`/map` — Runtime + Registry lenses over live
projections), and the **Feedback loops** board (`/loops` — each product/subject as its loop:
change → signal → hypothesis → review → next move, read from `product loops --json`).
New surfaces append by:

1. adding a read-only fetcher in `src/lib/` (a `compound <verb> --json` call),
2. rendering a `Card`/lens/loop on a page (or a new route),
3. adding the nav row in `src/components/Nav.tsx`.

The remaining *soon* slot (Cost) is a reserved slot — it lights up when its projection lands,
not before.

## Executor attribution

Work cards show `owner` / `claimed_by` from the work envelopes, so it is always visible who
(which lane/harness) is doing what — codex, a Claude session, a local lane — and what is
merely offered and unclaimed.

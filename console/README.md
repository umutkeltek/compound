# compound console

Operator control surface. It is projection-first: cards should prefer read-only
`compound ... --json` output against `COMPOUND_HOME`, not private schema reads.
That constraint keeps it a control surface instead of a second runtime owner.

## Run

```bash
cd console
bun install
bun run dev # http://localhost:3777
```

Requirements: the `compound` binary on PATH, or
`COMPOUND_BIN=/path/to/compound`; `COMPOUND_HOME` defaults to `~/.compound`.

Optional local-only gates:

- `COMPOUND_CONSOLE_MUTATIONS=1` enables POST mutation routes. Without it, the
  console stays copy-command/read-only.
- `COMPOUND_CONSOLE_SESSION_TOKEN=<token>` additionally requires header
  `x-compound-console-token` on mutation requests.
- `COMPOUND_CONSOLE_PRIVATE_FALLBACKS=1` enables temporary direct reads from
  `COMPOUND_HOME` / `docs_private` for surfaces without projection doors. Keep it
  off for public-facing/default operation.

## Truth Contract

- Every card declares its source freshness.
- Reads are projection-first. Temporary direct runtime/private readers are
  disabled unless `COMPOUND_CONSOLE_PRIVATE_FALLBACKS=1`.
- Most acts surface as exact CLI commands you copy. Mutation routes are
  default-off, require `COMPOUND_CONSOLE_MUTATIONS=1`, require same-origin
  localhost POSTs, and execute through the server-resolved decision endpoint.
  Legacy mutation routes are compatibility adapters only.
- If a card cannot name its projection or explicit private fallback, it does not
  ship.

## Base + Append

Stable grid: Dashboard, Work board, Reviews, Memory, lens-switchable System Map,
and Feedback loops. New surfaces append by adding a read-only fetcher, rendering
a card/lens/loop, and adding the nav row.

## Executor Attribution

Work cards show `owner` / `claimed_by` from work envelopes, so the active lane is
visible even when work is merely offered or unclaimed.

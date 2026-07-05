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

Requirements: `compound` binary on PATH, or `COMPOUND_BIN=/path/to/compound`;
`COMPOUND_HOME` defaults to `~/.compound`.

Optional local-only gates:

- `COMPOUND_CONSOLE_MUTATIONS=1` enables POST mutation routes. Without it,
  console stays copy-command/read-only.
- `COMPOUND_CONSOLE_SESSION_TOKEN=<token>` additionally requires header
  `x-compound-console-token` on mutation and private document requests.
- `COMPOUND_CONSOLE_PRIVATE_FALLBACKS=1` enables temporary direct reads from
  `COMPOUND_HOME` and the local repo when projection doors do not exist. Keep it
  off public-facing/default operation.
- `COMPOUND_CONSOLE_DOCUMENT_ROOT=/path/to/root` allows one extra private
  document root for local inspection.

## Truth Contract

- Every card declares source freshness.
- Reads are projection-first. Temporary direct runtime/private readers are
  disabled unless `COMPOUND_CONSOLE_PRIVATE_FALLBACKS=1`, require local operator
  requests, and return `scope: private_local`.
- Most acts surface exact CLI commands you copy. Mutation routes are
  default-off, require `COMPOUND_CONSOLE_MUTATIONS=1`, require same-origin
  localhost POSTs, and execute through the server-resolved decision endpoint.
  Legacy mutation routes are compatibility adapters only.
- If a card cannot name a projection or an explicit private fallback, it does
  not ship.

## Base + Append

Stable grid: Dashboard, Work board, Reviews, Memory, lens-switchable System Map,
Feedback loops. New surfaces append by adding a read-only fetcher, rendering a
card/lens/loop, and adding the nav row.

## Executor Attribution

Work cards show `owner` / `claimed_by` from work envelopes, so the active lane is
visible even when work is merely offered or unclaimed.

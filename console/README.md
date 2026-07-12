# compound console

Operator control surface. It is projection-first: cards should prefer read-only
`compound ... --json` output against `COMPOUND_HOME`, not private schema reads.
That constraint keeps it a control surface instead of a second runtime owner.

## Run

```bash
compound console --operator # pins COMPOUND_HOME and enables governed local acts
```

Requirements: `compound` binary on PATH, or `COMPOUND_BIN=/path/to/compound`;
the operator launcher resolves and explicitly pins `COMPOUND_HOME`. A direct
`bun run dev` without an explicit home remains read-only even when the mutation
flag is set, because silently defaulting a write target is unsafe.

Optional local-only gates:

- `COMPOUND_CONSOLE_MUTATIONS=1` is necessary but not sufficient for POST
  mutation routes. The serving process must also start with an explicit
  `COMPOUND_HOME`; otherwise the console stays copy-command/read-only and the
  API fails closed.
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
  default-off, require `COMPOUND_CONSOLE_MUTATIONS=1` plus an explicit frozen
  `COMPOUND_HOME`, require same-origin localhost POSTs, and execute through the
  server-resolved decision endpoint.
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

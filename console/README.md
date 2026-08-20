# compound console

Operator control surface. It is projection-first: cards should prefer read-only
`compound ... --json` output against `COMPOUND_HOME`, not private schema reads.
That constraint keeps it a control surface instead of a second runtime owner.

## Run

```bash
compound console dev --operator    # interactive development on :3777
compound console build --operator  # exclusive production build while :3777 is free
compound console serve --operator  # serve the already-verified production build
compound console status --json     # identity, lifecycle kind, HTTP + asset health
```

Requirements: `compound` binary on PATH, or `COMPOUND_BIN=/path/to/compound`;
the operator launcher resolves and explicitly pins `COMPOUND_HOME`. Package
lifecycle commands refuse to run without an explicit `COMPOUND_HOME`, because
silently turning a developer command into a canonical mutating instance is
unsafe. Use `compound console dev --operator` when choosing the canonical
default is intentional.

`compound console --operator` remains a compatibility alias for `dev`. The
documented package scripts delegate back to these CLI lifecycle owners; no
package script exposes an unmanaged Next lifecycle. The CLI invokes the local
Next binary only after acquiring an endpoint-scoped advisory lock shared by
every checkout. `build` also holds the
canonical `127.0.0.1:3777` listener for the full build, so no second runtime can
start while production artifacts are changing. Development writes
`.next/development`; production build and serve use `.next/production`.

`serve` never builds. It refuses to start unless the production `BUILD_ID` and
the manifests Next requires are present. Build once while the supervised job is
stopped, then let launchd restart only the immutable serve step. The CLI keeps
the lifecycle lock while Next runs and forwards termination to the whole child
process group, so supervision cannot orphan a server on `:3777`.

`status` is not a bind-only guess. It probes the existing instance badge on `/`,
matches the expected home hash and lifecycle kind, then fetches every referenced
local Next static asset. Its states distinguish `stopped`, `healthy`,
`unhealthy`, and `occupied_foreign` without adding a health route.

The `ai.compound.console` launchd unit is an adapter to this owner. Its command
must invoke the installed CLI (with its configured repo path), rather than
calling `next start` directly:

```bash
exec "${HOME}/.local/bin/compound" console serve --operator \
  --console-dir "/path/to/compound/console" --private-fallbacks
```

Run `compound console build --operator --console-dir /path/to/compound/console`
before bootstrapping the unit. The CLI owns artifact validation, port and
lifecycle exclusion, the frozen home binding, and the final Next process. The
launchd label owns only process supervision.

Optional local-only gates:

- `compound console --operator` is projection-only. `--mutations` explicitly
  sets `COMPOUND_CONSOLE_MUTATIONS=1`; that environment value is necessary but
  not sufficient for POST
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

## Stable destinations + contextual append

The left rail is the stable seven-surface map defined by
[`DESIGN.md` §10](./DESIGN.md#10-experience-architecture--simple-first-depth-on-demand):
Today, Live, Work, Decide, Knowledge, System, and Outcomes. Ask Compound is a
global read-only overlay.

New projections append inside their context owner. They do not add a top-level
navigation row merely because a command, object, or read model exists. When a
fact appears outside its owner, render only the decision-relevant summary,
freshness, and a link back to the canonical surface.

## Executor Attribution

Work cards show `owner` / `claimed_by` from work envelopes, so the active lane is
visible even when work is merely offered or unclaimed.

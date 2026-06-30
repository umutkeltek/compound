# compound

Local-first, agent-agnostic meaning + execution substrate.

Compound owns the spine: context, decisions, work, policy, evidence,
receipts, transitions, memory, capabilities, products, routes, and runtime
bindings. Executors are rented edges. Search is not truth. Model output is not
canon. Mutation requires an envelope and proof.

## What It Is

Compound is built envelope-first, not memory-first.

```text
ContextRequest
-> ContextPack
-> WorkEnvelope
-> ActionIntent
-> PolicyDecision
-> Receipt
-> typed Transition
-> candidates / adjudication / projections
```

Memory is one object family on that spine, alongside decisions, capabilities,
products, route evidence, runtime bindings, and policy. The proof apex is the
`Transition`: what changed, which evidence supports it, and how future behavior
should differ.

## Quickstart

```bash
go test ./...

COMPOUND_HOME="$(mktemp -d)"
go run ./cmd/compound home init --home "$COMPOUND_HOME"
COMPOUND_HOME="$COMPOUND_HOME" go run ./cmd/compound architecture doctor --strict
COMPOUND_HOME="$COMPOUND_HOME" go run ./cmd/compound orient \
  --harness codex \
  --cwd . \
  --task "inspect compound"
```

For contract consumers:

```bash
go run ./cmd/compound schema list
go run ./cmd/compound schema export context_pack
go run ./cmd/compound envelope validate fixtures/envelopes/*.json
```

## Where State Lives

The repo is source: code, schemas, tests, docs, and fixtures.

`COMPOUND_HOME` is the runtime body: ledgers, events, work, context packs,
candidate inboxes, registries, artifacts, reviews, exports, and rebuildable
indexes/projections.

Resolution order:

```text
--home -> COMPOUND_HOME -> nearest .compound/manifest.yaml -> ~/.compound
```

Private paths, credentials, accounts, raw personal corpora, and real operator
data belong in a private Compound instance, not tracked public files.

Useful home commands:

```bash
compound home backup [--target archive.tar.gz]
compound home export --public-fixture [--target fixture.tar.gz]
compound home verify [archive.tar.gz]
compound home restore archive.tar.gz --target <empty-home-path>
compound home compact [--dry-run] [--rebuildable]
```

Backups preserve durable evidence/work/config state while excluding transient or
rebuildable roots such as `tmp`, `lock`, `exports`, `indexes`, and
`projections`. Public fixture export emits sanitized layout and manifest
metadata only.

## Main Surfaces

Runtime and contracts:

- `compound home ...` manages the runtime body.
- `compound envelope validate/kinds` validates typed runtime envelopes.
- `compound schema list/export/validate` exports JSON Schema and validates files
  through the owning runtime validators.
- `compound contract compile` and `compound orient` produce EffectiveContract
  and ContextPack artifacts, including included/excluded refs, active hook
  rosters, and structured capability offers.
- `compound contract import-hooks --codex-hooks-json <hooks.json>` or
  `--hook-registration-ts <hook-registration.ts>` installs generated hook
  rosters as `compound://config/installed-hooks`; `compound contract
  hooks-authority` reports that handoff.

Execution and policy:

- `compound work ...` creates, claims, advances, closes, and imports work.
- `compound policy evaluate` turns ActionIntent into an enforceable
  PolicyDecision.
- `compound policy import-placement-rules <placement-rules.json>` installs owner
  placement rules into `COMPOUND_HOME/registries/placement-rules.json`;
  `compound policy placement-authority` reports that handoff.
- `compound policy import-operator-state <operator-state.json>` installs the
  global autonomy ceiling into `COMPOUND_HOME/registries/operator-state.json`;
  `compound policy operator-authority` reports handoff.
- `compound hook ...` and `compound harness ...` normalize harness events into
  spine objects; `compound hook pre-tool-use --enforce ...` returns policy gate
  exit codes for wrapper enforcement.

Meaning and evidence:

- `compound memory ...` owns candidates, evidence, claims, recall, adjudication,
  timelines, and reviewed write plans.
- `compound distiller ...` turns traces/runtime source material into candidate
  sets; it never promotes model output by confidence alone.
- `compound decisions ...` imports, repairs, and audits DecisionLifecycle source authority.
- `compound authority ...`, `compound source observe`, and `compound event
  import-os` adapt existing sources into evidence and candidates.

Adaptive systems:

- `compound capability ...` owns the capability graph. It surfaces offers with
  constraints; it does not execute skills/tools.
- `compound route ...` records route evidence, provider-call cost coverage,
  receipt-linked route outcome reviews, and Compound-owned model-router route
  plans from imported capability/default-lane authority.
- `compound runtime doctor ...` checks RuntimeBinding/ServiceContract bundles;
  `--live` runs explicit HTTP/TCP/file/env probes.
- `compound product ...` handles product identity, signals, hypotheses, source
  issues, reviews, and product timelines.
- `compound projection ...` checks generated-view freshness and records scoped
  projection context.

## Architecture Map

Read in this order:

1. `docs/LAYERS.md`
2. `docs/architecture/status.md`
3. `docs/architecture/runtime.md`
4. `docs/architecture/contracts/README.md`
5. `docs/memory-architecture.md`
6. `docs/architecture/source-adapters.md`

Core packages:

```text
cmd/compound/             CLI boundary
internal/home/            COMPOUND_HOME runtime body
internal/envelope/        typed runtime grammar
internal/schema/          JSON Schema export + contract-aware validation
internal/contract/        EffectiveContract + ContextPack assembly
internal/work/            WorkEnvelope lifecycle
internal/policy/          ActionIntent -> PolicyDecision
internal/harness/         HarnessRegistry
internal/hook/            lifecycle hook runner
internal/candidates/      candidate kind -> inbox dir + verdicts
internal/mem/             canonical memory object family
internal/distiller/       trace -> candidate fan-out + critique
internal/capgraph/        capability graph
internal/runtimebinding/  RuntimeBinding + ServiceContract doctor
internal/projection/      generated-view freshness
internal/product/         product signals, hypotheses, reviews
internal/routeevidence/   execution route evidence
internal/arch/            architecture doctor
```

Importer packages adapt existing systems into the spine without becoming new
owners: `authority`, `decisions`, `eventsource`, `sourceobs`, and
`tooltopology`.

## Rented Edge

Search engines, vector indexes, model providers, harnesses, MCPs, browser
tools, analytics APIs, and sidecars are replaceable edge adapters. They may
broaden retrieval or execute work; they do not own promotion, visibility,
supersession, policy, or truth. The ledger is authority. Engines are rebuildable
views.

## Status

- Gen-1 memory packages are retired from `internal/`.
- Gen-1 top-level verbs are tombstones, not compatibility aliases.
- `compound architecture doctor --strict` enforces package classification,
  public-doc leak checks, candidate-map integrity, contract guards, and a
  convergence budget of `0`.
- `compound memory doctor` verifies capture, promotion, retrieval, authority,
  and optional engine sync.

Compound is dogfooded against a private reference system, but this repo is the
generic public substrate. Public behavior must be proven through leak-free
fixtures and explicit runtime inputs.

# compound

> Local-first, agent-agnostic substrate preserving, governing, and reusing what a
> person or team knows, decides, and does: own meaning, rent engines.

Compound is opinionated: evidence is not canon, search is not truth, mutation
requires a recorded decision, and current knowledge must be placed, cited,
promoted, superseded, or deliberately parked.

## What Compound Is

Compound is built **envelope-first, not memory-first**. One typed runtime spine
threads harnesses, hooks, and runs through a root `work_id`. Memory is one object
family on that spine, not the system boundary.

The current public surface is:

| command | what it does |
|---|---|
| `compound home init/doctor/where` | `COMPOUND_HOME` runtime body: generated state, never repo source |
| `compound envelope validate/kinds` | typed envelope spine |
| `compound contract compile` / `compound orient` | EffectiveContract -> ContextPack; records included and excluded refs |
| `compound work create/show/list/claim/advance/close/act` | one `work_id` threads intent -> context -> action -> policy -> receipt -> transition |
| `compound policy evaluate` | ActionIntent -> PolicyDecision, enforced at adapters |
| `compound hook <name>` / `compound harness list` | capability-aware hook fixtures and HarnessRegistry |
| `compound route import` / `compound route plan --candidate <id>` | route metadata and route_observation candidates become evidence/review artifacts; no gateway or provider config mutation |
| `compound memory candidate/adjudicate/recall/timeline/write-plan/query/doctor` | spine memory: candidates, claims, positions, patterns, evidence, supersession, as-of recall, reviewed curated-source write plans |
| `compound distiller request/run/eval/eval-plan/rule-plan/taste-plan` | typed trace/runtime/event source material -> RetrievedPriorState -> CandidateSet -> Critic -> Adjudicator; eval_case, failure_pattern, and taste candidates become reviewed drafts before corpus, doctrine, or product mutation |
| `compound capability <add/surface/observe/consume/list>` | capability graph: auto-surface, never auto-execute |
| `compound authority import/path/path-registry/git` / `compound decisions import` | import external authority, OS-style path authority, source-control snapshots, and DecisionLifecycle records through spine memory |
| `compound event import-os <events.ndjson>` | adapt append-only OS-style events into `TraceEnvelope` evidence; no memory promotion |
| `compound runtime doctor <runtime-binding.yaml>` / `runtime repair-plan <runtime-binding.yaml>` | check a RuntimeBinding + ServiceContract bundle; failing reports can emit gated review artifacts |
| `compound runtime doctor --project-contract <project.yaml>` / `runtime repair-plan --project-contract <project.yaml>` | adapt an infra-os-style project contract into the same runtime doctor/repair path |
| `compound projection doctor <freshness.yaml>` | check generated-view freshness/drift from source and output hashes |

Gen-1 memory-era top-level verbs are retired tombstones:

```text
compound distill
compound scope
compound promote
compound build
compound reconcile
compound timeline
compound recall
compound pack
compound ledger
```

They exit with status `2` and point to the canonical `compound memory ...` or
`compound distiller ...` surface. There is no alias layer.

## Architecture

One architecture: the **Runtime / Envelope Spine**. "Layers" are object families
processed by the spine, not services, phases, or peer owners.

Object families currently named in the architecture:

- **Memory**: Evidence, Claim, Position, Pattern.
- **Authority**: Entity, RegistryEntry, RuntimeBinding, ServiceContract.
- **Decisions**: committed choices and their lifecycle.
- **Procedures / Capabilities**: skills, tools, MCPs, model profiles, workflows.
- **Permissions**: policy, autonomy envelopes, gates.
- **Doctrine / Product / Taste**: durable standards, product signals, hypotheses.

Read in this order:

1. `docs/LAYERS.md`
2. `docs/architecture/status.md`
3. `docs/architecture/runtime.md`
4. `docs/architecture/contracts/README.md`
5. `docs/memory-architecture.md`
6. `docs/architecture/source-adapters.md`

## Layout

```text
cmd/compound/          CLI, one local binary boundary

internal/home/         COMPOUND_HOME runtime body
internal/envelope/     typed runtime grammar
internal/contract/     EffectiveContract compiler + ContextPack assembler
internal/work/         WorkEnvelope lifecycle
internal/policy/       ActionIntent -> PolicyDecision
internal/harness/      HarnessRegistry
internal/hook/         lifecycle hook runner
internal/candidates/   candidate kind -> inbox dir + verdicts
internal/mem/          canonical memory object family
internal/distiller/    trace -> candidate fan-out + critique
internal/capgraph/     capability graph
internal/runtimebinding/ RuntimeBinding + ServiceContract doctor + gated repair-plan artifacts
internal/projection/   GeneratedViewObservation + ProjectionFreshness doctor
internal/arch/         architecture doctor

internal/authority/    importer into spine memory
internal/decisions/    DecisionLifecycle importer/projection into spine memory
internal/eventsource/  append-only event stream -> TraceEnvelope adapter

docs/                  public architecture and contracts
fixtures/              public, leak-free fixtures
prototype-ts/          historical prototype reference
```

Runtime state belongs in `COMPOUND_HOME`, resolved as:

```text
--home -> COMPOUND_HOME -> nearest .compound/manifest.yaml -> ~/.compound
```

Public core has no private path defaults. Private paths, credentials, engines,
accounts, and real operator data live in runtime state or private overlay files,
never tracked public docs or fixtures.

## Rented Edge

Search, vector indexes, model providers, harnesses, MCPs, browser tools, and
analytics APIs are replaceable edge adapters. They may broaden retrieval or
execute work; they never own promotion, visibility, supersession, policy, or
truth.

The ledger is authority. Engines are rebuildable views.

## Status

- Gen-1 memory packages are retired from `internal/`.
- Gen-1 top-level verbs are tombstones, not compatibility aliases.
- `compound architecture doctor --strict` enforces package classification, public
  doc leak checks, candidate-map integrity, and a convergence budget of `0`.
- `compound memory doctor` verifies the memory hop: capture, promotion,
  retrieval, authority, and optional engine sync.

Compound is dogfooded against a private reference system, but the product is the
generic public substrate. Public behavior must be proven with synthetic fixtures
or explicit overlay inputs.

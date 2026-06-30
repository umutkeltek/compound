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
| `compound home init/doctor/where/backup/export/verify/restore/compact` | `COMPOUND_HOME` runtime body: generated state, archive/restore, public fixture export, safe compaction; never repo source |
| `compound envelope validate/kinds` | typed envelope spine |
| `compound contract compile` / `compound orient` | EffectiveContract -> ContextPack; records included and excluded refs; can include constraint-aware `capability_offers[]` plus recorded projection freshness refs as generated context evidence |
| `compound work create/import-packet/show/list/claim/advance/close/act` | one `work_id` threads intent -> context -> action -> policy -> receipt -> transition; packet markdown imports as source-backed proposed work, not proof |
| `compound policy evaluate` | ActionIntent -> PolicyDecision, enforced at adapters |
| `compound hook <name>` / `compound harness <list\|observe [registry.yaml\|--os-entrypoints file]>` | capability-aware hook fixtures and HarnessRegistry; observe records built-in, normalized, or OS-style entrypoint registry evidence/no-promote without installing hooks |
| `compound route import` / `compound route import-provider-ledger` / `compound route cost-doctor` / `compound route outcome-review` / `compound route plan --candidate <id>` | route metadata, provider-call NDJSON rows, cost-coverage gaps, receipt-linked outcome reviews, and route_observation candidates become evidence/review artifacts; no gateway or provider config mutation |
| `compound memory candidate/import-page/adjudicate/recall/timeline/write-plan/query/doctor` | spine memory: candidates, claims, positions, patterns, evidence, supersession, as-of recall, curated markdown page import, reviewed curated-source write plans |
| `compound distiller request/run/eval/eval-plan/improvement-plan/rule-plan/taste-plan` | typed trace/runtime/event source material -> RetrievedPriorState with bounded `prior_signals[]` -> request-grounded CandidateSet -> Critic -> Adjudicator; eval/live extraction rejects missing or ungrounded candidate evidence refs plus thin candidate shape (memory requires entity/predicate/object; non-memory requires subject/statement) before inbox write; eval_case, method_upgrade, policy_patch, failure_pattern, and taste candidates become reviewed drafts before corpus, prompt, policy, code, doctrine, or product mutation |
| `compound capability <add/surface/observe/consume/list/authority/import-topology/import-skills/import-evaluations/import-surfacing-misses/evaluation-plan/evaluation-review/skill-plan>` | capability graph: auto-surface, never auto-execute; topology and skill manifests create offers and record a source-authority handoff making the graph authoritative while imported views remain evidence/rollback inputs; capability-evaluation records enter as evidence-only graph nodes plus SourceObservation/no-promote rows; skill candidates become reviewed graph-node drafts before import |
| `compound authority import/path/path-registry/git/dirty-plan` / `compound decisions import/candidate-plan` | import external authority, OS-style path authority, source-control snapshots, source-control cleanup review artifacts, DecisionLifecycle records, and decision-candidate review drafts through spine memory |
| `compound event import-os <events.ndjson>` | adapt append-only OS-style events into `TraceEnvelope` evidence; no memory promotion |
| `compound runtime doctor <runtime-binding.yaml> [--live]` / `runtime repair-plan <runtime-binding.yaml> [--live]` | check a RuntimeBinding + ServiceContract bundle; `--live` runs explicit HTTP/TCP/file/env probes; failing reports can emit gated review artifacts |
| `compound runtime doctor --project-contract <project.yaml> [--live]` / `runtime repair-plan --project-contract <project.yaml> [--live]` | adapt an infra-os-style project contract into the same runtime doctor/repair path |
| `compound product import/signal/change-stream/hypothesis-plan/review-plan/source-issue-plan/review/source-issue-review/timeline/doctor` | product identity, market signals, mined change-stream candidates, hypotheses, signal-backed review drafts, reviews, and source-issue closure through evidence/review gates before product strategy, analytics, or store-surface changes |
| `compound projection doctor <freshness.yaml>` | check generated-view freshness/drift from source and output hashes; `--record-context` makes freshness available to scoped `orient` |

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
- **Doctrine / Product / Taste**: durable standards, product signals, mined change candidates, hypotheses.

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

Home archive commands:

```text
compound home backup [--target archive.tar.gz]
compound home export --public-fixture [--target fixture.tar.gz]
compound home verify [archive.tar.gz]
compound home restore archive.tar.gz --target <empty-home-path>
compound home compact [--dry-run] [--rebuildable]
```

`backup` preserves durable evidence/work/config state while excluding transient
or rebuildable roots (`tmp`, `lock`, `exports`, `indexes`, `projections`).
`export --public-fixture` emits layout and sanitized manifest metadata only,
with no private runtime files.

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
  doc leak checks, candidate-map integrity, retired-surface/distiller-contract/
  llm-grounding-contract/route-outcome-contract/knowledge-page-contract/
  packet-work-contract/mcporter-topology-contract/memory-engine-contract/
  harness-entrypoint-contract/product-contract-contract/capability-evaluation-contract guards, and a convergence
  budget of `0`.
- `compound memory doctor` verifies the memory hop: capture, promotion,
  retrieval, authority, and optional engine sync.

Compound is dogfooded against a private reference system, but the product is the
generic public substrate. Public behavior must be proven with synthetic fixtures
or explicit overlay inputs.

# compound

Local-first, agent-agnostic meaning + execution substrate.

Agent sessions are amnesiac by default: whatever an agent learned, decided, or
broke evaporates when the context window closes. Compound is the substrate that
makes agent work accumulate instead — every session leaves claims, receipts,
and lenses behind, and the next session starts smarter because of them. Across
any harness, any model, any vendor.

The thesis: meta-thinking must be engineered — compounding is not a feeling
that emerges on its own. Ideas die when they are captured wrong. Captured at
execution-abstraction — a predicate, a falsifier, an envelope — an idea
survives the moment it was had and reaches a factory that can act on it and
adapt because of it. Compound is that capture format and that factory, as one
substrate.

Compound owns the spine: context, decisions, work, policy, evidence, receipts,
transitions, memory, capabilities, products, routes, and runtime bindings.
Executors are rented edges. Search is not truth. Model output is not canon.
Mutation requires an envelope and proof.

## Why

Four failure classes eat agent-assisted work, and most stacks have no organ
against any of them:

- **The rediscovery tax.** An agent burns rounds re-deriving what the system
  already knew, because knowledge existed but nothing served it at the moment
  of need.
- **Landed is not working.** A change merges, a status doc says done, and the
  behavior never actually changed. Reports are trusted; flows are not exercised.
- **The blind cron.** Scheduled automation asserts effects on a schedule,
  exits 0 while broken, and nothing consumes its absence-of-effect as a signal.
  A blind cron is a liar with a schedule.
- **Decision homework.** Every judgment reaches the human as a CLI incantation
  or a wall of context to reverse-engineer, instead of a plain-language card
  with one honest act.

Compound closes these as loops, not as features: evidence comes in, receipts go
out, and the system's next behavior provably differs. When it doesn't, that gap
is itself recorded as a failure class — and compiled into a lens that fires the
next time the same shape appears.

## One night, end to end

A real sequence from building this system (details sanitized; the mechanics are
exactly what this repo ships):

An operator's work sat blocked on a host everyone believed was on. A network
probe said it was dead; a second look inverted the probe — it was the overlay
network's plane that had failed, and no single observation had the authority to
say so. The moment compiled into a lens: *every single-plane observation is a
hypothesis — assertions and probes alike.* It now fires at orientation whenever
work depends on a host.

The operator then ruled, in one sentence, that the system should self-heal
rather than run blind cron jobs. By morning the ruling was runtime: divergence
observations (`expected_state != observed_state`) surface immediately as cards,
a director proposes remediation envelopes from them, and evidence-thresholded
grants let a proven work class admit itself — while anything external,
destructive, or irreversible stays with the human by construction, not by
policy prose.

Then the punchline: a decision the operator kept being handed as homework
turned out to have been made days earlier. Nothing could say so until an organ
owned that state. The card built to *ask* the question instead correctly
reported there was nothing to ask.

Feeling → compiled lens → landed code → live proof, each step with a receipt.
The loop this repo exists to make ordinary.

## Principles

- **Discover reality before acting.** Probe first; the map is never the
  territory, including the system's own map of itself.
- **Own the spine; rent the executors.** Meaning, policy, and proof are owned;
  models, engines, and harnesses are replaceable edges.
- **Store evidence, not truth.** Every claim carries provenance and a
  falsifier; supersession is a record, never a prose edit.
- **Recall only what is relevant.** Context is served in task-scoped slices,
  not preambles.
- **Keep humans on irreversible decisions** — by setting envelopes and standing
  grants, not by being paged. A nagging agent violates this rule; it does not
  enforce it.
- **Prove the change; don't trust the report.** A close without a live-flow
  observation is a claim, not a fact.
- **Convert repetition into infrastructure.** Repetition count — not idea
  elegance — is what earns a new organ.
- **Subtract by default.** Everything created is either enforcement (compiled
  from a receipted ruling, blocks silently, carries undo) or evidence (names
  its consumer, or decays). Anything else is not built.
- **Articulate boldly, hold falsifiably.** Even a wrong articulation is a kind
  of sense: a specific claim gets corrected with one line and a receipt, while
  unarticulated sense stays mush — and mush cannot compound.

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
make install
compound workbench
compound proof-check --json
compound orient --harness codex --cwd . --task "inspect compound"
cd console && bun install
compound console --operator # optional local console on port 3777
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
- `compound workbench` is a read-only board over open WorkEnvelopes,
  terminal receipts, transitions, and optional CapabilityGraph offers; it does
  not dispatch, execute offers, or mutate work.
- `compound transition ...` exposes the proof apex joining work, receipts,
  evidence, and typed projections.
- `compound proof-check [--json]` runs the deterministic local proof bundle:
  tests, vet, build, diff check, strict architecture doctor, memory doctor, and
  public redact scan.
- `compound reviews [--json]` reads owner-review artifacts without applying
  them.
- `compound policy evaluate` turns ActionIntent into an enforceable
  PolicyDecision.
- `compound policy import-placement-rules <placement-rules.json>` installs owner
  placement rules into `COMPOUND_HOME/registries/placement-rules.json`;
  `compound policy placement-authority [--json]` reports the machine-readable
  handoff for OS/operator consumers.
- `compound policy import-operator-state <operator-state.json>` installs the
  global autonomy ceiling into `COMPOUND_HOME/registries/operator-state.json`;
  `compound policy operator-authority [--json]` reports the machine-readable
  handoff for OS/operator consumers.
- `compound writeback ...` gates owner writeback and no-promote decisions
  through policy and receipts.
- `compound hook ...` and `compound harness ...` normalize harness events into
  spine objects; native blocking hooks return deny decisions, while `compound
  hook pre-tool-use --enforce ...` returns policy gate exit codes for
  non-blocking adapter fallbacks.

Meaning and evidence:

- `compound memory ...` owns candidates, evidence, claims, recall, adjudication,
  timelines, and reviewed write plans.
- `compound knowledge ...` imports source-backed knowledge items into evidence
  and candidate paths without direct promotion.
- `compound synthesis ...` records memory synthesis sweeps; contradiction
  sweeps may reopen claims to review, while cluster proposals stay report-only.
- `compound distiller ...` turns traces/runtime source material into candidate
  sets; it never promotes model output by confidence alone.
- `compound decisions ...` imports, repairs, and audits DecisionLifecycle source authority.
- `compound authority ...`, `compound source observe`, and `compound event
  import-os` adapt existing sources into evidence and candidates.

Adaptive systems:

- `compound capability ...` owns the capability graph. It surfaces offers with
  constraints and flat JSON summaries; it does not execute skills/tools.
- `compound panel ...` runs lens-based reviews over candidate/review queues and
  applies only through governed proof paths.
- `compound local-models advise ...` reports local model context/load/unload
  intent without mutating provider runtime.
- `compound route ...` records route evidence, exposes read-only route
  evidence authority, checks provider-call cost coverage, writes
  receipt-linked route outcome reviews, and uses Compound-owned model-router
  route plans from imported capability/default-lane authority.
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
3. `docs/architecture/epistemics.md`
4. `docs/architecture/runtime.md`
5. `docs/architecture/contracts/README.md`
6. `docs/memory-architecture.md`
7. `docs/architecture/source-adapters.md`

Core packages:

```text
cmd/compound/             CLI boundary
internal/home/            COMPOUND_HOME runtime body
internal/envelope/        typed runtime grammar
internal/schema/          JSON Schema export + contract-aware validation
internal/contract/        EffectiveContract + ContextPack assembly
internal/work/            WorkEnvelope lifecycle + read-only workbench
internal/policy/          ActionIntent -> PolicyDecision
internal/harness/         HarnessRegistry
internal/hook/            lifecycle hook runner
internal/knowledge/      source-backed knowledge intake
internal/candidates/      candidate kind -> inbox dir + verdicts
internal/mem/             canonical memory object family
internal/distiller/       trace -> candidate fan-out + critique
internal/capgraph/        capability graph
internal/panel/          lens review panel
internal/localmodel/     local model advice seam
internal/runtimebinding/  RuntimeBinding + ServiceContract doctor
internal/projection/      generated-view freshness
internal/product/         product signals, hypotheses, reviews
internal/routeevidence/   execution route evidence
internal/reviewartifact/ shared review artifact writes
internal/writeback/      owner writeback/no-promote gate
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

## Direction

The maturity ladder this substrate is built to climb, in order:

1. Reduce its operator's cognitive overhead.
2. Orchestrate many specialized agents reliably.
3. Run parts of a real business under human oversight.
4. Serve as a research instrument that generates and tests genuinely new ideas.
5. Evolve because of the work done with it, not because it keeps being
   redesigned.

This is direction, not commitment; sequencing lives with the operator and is
decided by evidence, one receipted step at a time.

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

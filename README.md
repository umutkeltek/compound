# compound

Local-first, agent-agnostic substrate that makes the flow of agent work leave
reusable structure behind — without freezing the flow.

Agent sessions are amnesiac by default: whatever an agent learned, decided, or
broke evaporates when the context window closes. Compound records the work as
it actually happens — every session leaves claims, receipts, and lenses
behind, and the next session starts smarter because of them. Across any
harness, any model, any vendor.

The thesis: meta-thinking must be engineered — compounding is not a feeling
that emerges on its own. Probabilistic intelligence needs an owned, structured
world around it, or it cannot accumulate into reliable action. Ideas die when
they are captured wrong. Captured at execution-abstraction — a predicate, a
falsifier, an envelope — an idea survives the moment it was had and reaches a
factory that can act on it and adapt because of it. Compound is that capture
format and that factory, as one substrate.

The whole system in one line: **typed envelopes at the door · adjudicated
meaning in the ledger · rented executors at the edge · policy before mutation
· receipts and transitions after action.** Compound owns that structure;
models, engines, and harnesses are replaceable edges. Search is not truth.
Model output is not canon. Mutation requires an envelope and proof.

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

Compound is built envelope-first, not memory-first. Hold the shape as five
places, not eleven nouns:

- **The door.** Everything that enters — a task, an observation, a model's
  output — arrives as a typed envelope and is validated before it can matter.
- **The ledger.** Meaning lives in one adjudicated home: nothing becomes
  ambient truth without a recorded verdict, and supersession is a record,
  never a prose edit.
- **The edge.** Executors — models, harnesses, engines — are rented: they
  propose, extract, and execute, but never own truth, promotion, or policy.
- **The gate.** Every mutation asks policy first; destructive and irreversible
  actions stay with the human by construction.
- **The receipt.** After action, a receipt records what actually ran, and a
  transition records what it means for future behavior.

In ontology terms, Compound's typed object families form an operational
ontology: shared entities, constraints, lifecycles, and evidence rules that
every executor must use at the runtime boundary. Probabilistic models may
extract, propose, or execute; deterministic code validates and adjudicates
their output before mutation; receipts and transitions verify what actually
changed.

The typed grammar behind those five places, for contract consumers:

```text
ContextRequest
-> ContextPack        the briefing: what the agent is told, and what was left out
-> WorkEnvelope       the job: one stable identity for one unit of work
-> ActionIntent       what an executor wants to do
-> PolicyDecision     the gate's answer: allow, deny, or hold
-> Receipt            proof the job ran: what was touched, what came out
-> Transition         the ledger entry: state A -> B because of this work
-> candidates / adjudication / projections
```

Memory is one object family on that spine, alongside decisions, capabilities,
products, route evidence, runtime bindings, and policy. The apex is the
`Transition`: what changed, which evidence supports it, and how future behavior
should differ.

## Quickstart

**New here?** Start with the [Getting Started guide](docs/getting-started.md) —
Compound + your repo + your agent in 15 minutes.

```bash
make install
compound orient --harness claude-code --cwd /path/to/your/project --task "describe this project"
compound work create --owner "my-first-task" --predicate "the thing works" --state ready --by "operator:me"
compound workbench
```

Wire your agent harness: see [Harness Wiring](docs/harness-wiring.md).

### Inspect one governed execution

This local loop creates work, claims it, runs a bounded harness command, and
leaves the proof chain inspectable:

```bash
compound home init
compound doctor

compound work create \
  --owner "quickstart" \
  --predicate "the harness prints hello and records proof" \
  --state ready \
  --by "operator:me"

# Copy the returned work_... id into the commands below.
compound work claim <work_id> --by "operator:me"
compound work advance <work_id> --to running --by "operator:me" --execute \
  --harness os-harness-entrypoints \
  --workspace <demo-repo> \
  -- echo hello

compound work show <work_id> --json
compound run-attempt list --json
compound workbench

# Copy a transition id from the work JSON to inspect its exact evidence.
compound transition show <transition_id>
```

A successful command moves the work to `verifying`. The work, RunAttempt,
transcript, Receipt, and Transition remain linked so the next state is based on
inspectable proof rather than the harness's claim that it succeeded.

Optional local console on port 3777:

```bash
cd console && bun install
compound console --operator
```

See the [Console guide](docs/console.md) for what you'll see.

For contract consumers:

```bash
go run ./cmd/compound schema list
go run ./cmd/compound schema export context_pack
go run ./cmd/compound envelope validate fixtures/envelopes/*.json
```

For semantic memory (search over your docs): see the
[Memory Quickstart](docs/memory-quickstart.md).

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
- `compound proof-check --work <work_id> --actor <lease-holder> --out
  <report.json>` runs the canonical deterministic local proof bundle: tests,
  vet, build, diff check, strict architecture doctor, memory doctor, and public
  redact scan. A full local run is admitted against the existing lease/work
  owners before its first subprocess. It needs no interactive terminal: custody
  of the `--out` report under the still-held lease is the proof, so any harness
  or model invoked as a capability can certify, as can a scheduled queue with no
  controlling terminal. Custom
  TTLs below 3s are refused as usage before reservation; accepted windows refresh
  at `min(TTL/3, 5s)` and continuously monitor holder loss. Expiry alone never
  makes a successor executable: every proof gate inherits a kernel execution
  barrier from the existing land-lease owner, and ordinary landing acquire,
  expiry recovery, generic sweep, and exceptional steal all fail closed while
  any descendant still holds it. SIGINT/SIGTERM and governed steal requests
  cancel and wait for the whole gate process group. A stale already-dead
  predecessor is recovered through the stricter exceptional policy with
  `--steal --reason ... --terminated-run <ratt_*>`; Compound itself attests the
  released barrier and writes the immutable Work-reachable termination evidence.
  Bare/reused PIDs and caller-authored JSON never authorize takeover.
  Ambiguous or cyclic recovery lineages still fail closed.
  Every reserved `canonical_full` gate must finish `ok` to certify; a skipped gate
  cannot certify. A complete canonical red battery—every gate terminal
  `ok|failed`, with at least one `failed`—is retained as non-certifying governed
  evidence, releases the scarce resource, prints one truthful `battery: FAILED`
  verdict, and exits 1 so it does not wedge a successor.
  Refusal leaves an existing report byte-identical and a nonexistent report
  absent, while successful publication uses a synced sibling temp plus atomic
  rename. Any failure after a complete report is durable prints exactly one final
  non-green battery verdict; pre-report usage/admission errors print none. A
  post-release installed-runtime publication failure is therefore non-green, and
  that marker is also published by synced atomic rename with a completion footer
  so an interrupted prefix cannot suppress Workbench action.
  committed green or red result whose lease release remains pending returns a
  non-green command error and retains its exact retry journal until recovery;
  its report stays intact and stdout ends `battery: FAILED release_pending=true`.
  Invoking that exact command again resumes the owned result before reservation,
  atomically restores and verifies `--out` from the immutable owned artifact
  before release, returns the prior report, and releases without rerunning the
  gates or minting another holder. The installed-runtime Workbench artifact is
  published only after a green governed completion has released; release-pending,
  invalid, failed, dry-run, and CI reports cannot suppress that action. Dry-run
  and CI subsets remain lightweight and non-certifying.
- `.woodpecker.yml` runs the portable core, console, and gitleaks gates on the
  owned Woodpecker agent. A scheduled/manual run is the reliable trigger while
  the forge cannot reach the LAN-only webhook; GitHub-hosted Actions are
  intentionally not part of the runtime contract.
- `compound reviews [--json]` reads owner-review artifacts without applying
  them.
- `compound policy evaluate` turns ActionIntent into an enforceable
  PolicyDecision.
- `compound authority dirty-plan <repo> --work-id <owner-work>
  --action-intent <persisted-id>...` derives an exact dirty-mutation review
  capability from persisted hook actions; it never trusts caller-authored
  action semantics or performs source-control cleanup. Authorization requires
  `dirty-review <review.yaml> --reviewed-by operator:<actor>` and a sealed,
  source-checked report.
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

## Guides

| Guide | For |
|---|---|
| [Getting Started](docs/getting-started.md) | First-time users: install, orient, create work, wire your agent |
| [Harness Wiring](docs/harness-wiring.md) | Connect Compound to Claude Code, Codex, OpenCode, or any CLI harness |
| [Memory Quickstart](docs/memory-quickstart.md) | Semantic search over your docs in 10 minutes |
| [Console](docs/console.md) | The visual operator dashboard |
| [Contributing](CONTRIBUTING.md) | Build, test, architecture rules, and PR expectations |

## Architecture Map

Read in this order:

1. `docs/LAYERS.md`
2. `docs/architecture/status.md`
3. `docs/architecture/epistemics.md`
4. `docs/architecture/runtime.md`
5. `docs/architecture/contracts/README.md`
6. `docs/memory-architecture.md`
7. `docs/architecture/source-adapters.md`
8. `docs/operations/legacy-system-retirement.md`

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

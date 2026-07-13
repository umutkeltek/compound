# AGENTS.md — operating contract for agents working in compound

compound is an owned, agent-agnostic **unified meaning + execution substrate**: one meaning owner that turns
real traces into evidence, evidence into adjudicated memory/registries/skills/decisions, those into routed
action, action into receipts, and receipts into learning and hardened infrastructure — while agents, models,
tools, and indexes stay rented and replaceable. You are a **rented executor**. compound owns the spine; you
do not become the source of truth.

> This file is the **build guide** for agents *building* compound. A *running* compound instance serves OTHER
> repos a tiny client-contract instead (see `docs/architecture/runtime.md` §4) — do not confuse the two.

## Operating ceremonies
`compound orient --harness <you> --task "<task>"` serves the binding ceremonies for
landing/executing/verifying work here as an `operating-ceremonies` context source. The ceremony bodies are
**tracked in-repo** under [`skills/`](./skills/README.md) (`compound-execute`, `compound-land`,
`compound-verify`, `extract-approach`) — plain markdown any harness reads directly, reachable in a fresh
public clone with no private overlay. A harness may also mirror them as local auto-loading skills (e.g. under
`.claude/skills/`), but that overlay is a convenience, not the source of truth. No hook injection available?
Read `skills/README.md` (or run orient manually) before orchestrating or landing — never improvise the ceremony.

## Read first
- `docs/ARCHITECTURE.md`, `docs/LAYERS.md` — the information kernel (monolith, deep modules, cwd-independent).
- `docs/architecture/substrate.md` — target topology; **one meaning owner**; two planes; the three places.
- `docs/architecture/runtime.md` — the `.compound` home, the **envelope spine** (schemas), hooks-as-clients.
- `docs/architecture/transition.md` — the `Transition` apex object, the 9-field invariant, second-order tests.
- `docs/architecture/execution-plane.md` — the model-traffic gateway (route evidence ≠ task success).
- `docs/architecture/epistemics.md` — machine-checked doctrine table: principles → implementing objects → proof commands.
- `docs/architecture/contracts/README.md` — the contracts (lego seams) + `move_type` + entity precedence.
- `docs/memory-architecture.md` — the memory slice detail (built at P6; memory is one slice; ledger is authority).
- `docs/roadmap/phases.md` — envelope-first construction order.
- Operator-specific sequencing is in `docs_private/` (gitignored, absent in clones). If absent, use the roadmap.

## Doctrine (non-negotiable)
1. **One meaning owner.** Many sources, many indexes, many harnesses, many projections — **one** meaning
   owner: compound. A private corpus is compound's meaning layer *inside* a running instance (not an external
   overlay). A legacy vector/fact store is a **bridge/index, never authority**: `compound → objects → index`,
   never `index → truth → compound reads`.
2. **Own the meaning, rent the index.** The ledger owns visibility, promotion, supersession, identity, truth.
   Engines/models/tools/harnesses are disposable. A gateway `RouteDecision` is route evidence, not task success.
3. **The runtime grammar is the envelope spine, hung off one `work_id`.** Intent → EffectiveContract →
   ContextPack → WorkEnvelope → ActionIntent → PolicyDecision → Receipt → Transition. Governance is **enforced
   at adapters** (a mutation requires a `PolicyDecision`), not asked for in a prompt.
4. **Evidence, not truth.** Incoming info is adjudicated (`observed → reject | park | promote | operationalize
   → superseded → retired`), never written straight to authority. Only `promoted` enters ambient recall;
   unknown/low-confidence → `parked`. Resolve identity by the precedence order; uncertain → parked only.
5. **Second-order before first-order.** No field is neutral; every affordance is an attractor. Ask what a
   skill/registry-entry/route/memory makes more likely tomorrow and what it can fossilize. Ensemble-aware, not lookup.

## Build envelope-first (not memory-first)
Build the runtime spine **before** memory ingestion: `.compound` home → envelope schemas → EffectiveContract
+ ContextPack → WorkEnvelope lifecycle → policy → hook fixtures → *then* memory candidates → dogfood overlay.
**Memory is one slice, not the substrate.** Extract the control-spine repo's *load-bearing contracts*
(EffectiveContract, ContextPack, WorkEnvelope, ActionIntent→PolicyDecision, hook-as-client) — never its mass.
Every migrated surface gets a **retirement condition**, or compound becomes one more layer.

## Execution is a feedback loop (the path generates the architecture)
The architecture docs are a **starting hypothesis, not a frozen spec.** Building compound *is* the self-healing
loop compound provides:
- **Execute a slice → write tests that probe the system's depth (not happy paths) → let tests surface flaws →
  fix and harden → feed what you learned back into the docs, contracts, tests, AND these instruction files, in
  the same change.** Updating AGENTS.md/CLAUDE.md/a contract locally as you learn is expected, not a violation.
- Tests are how the system understands its own depth. A slice is done when tests prove it survives the relevant
  acceptance tests (`transition.md`) and the failure modes it will actually hit. Write tests that would *fail*
  if the depth were wrong. "It is about the path, not the direction." This loop is generic for all agents.
- **Compounding-knowledge:** when a move teaches something about compound's future architecture, write it as a
  transition-oriented, evidence-linked observation kept packet-local until it **earns promotion** — to a
  contract, script, check, decision, or recalled context — *only when a real executor will consume it.* A move
  that produces "another document" failed unless the document changes a future branch for a real executor.
- **Agents are tunnels too:** a durable mid-work observation about an *external* surface (a product, source
  system, or upstream repo — not compound's own architecture) does not belong in prose or a packet note; emit
  it through the intake door as a `SourceObservation` (`compound source observe <obs.yaml>`, or
  `observation_batch` JSON via `compound source observe-batch` for multiple) so it enters adjudication like any
  other tunnel's evidence. Adapters observe, agents observe — nobody promotes from the field.

## Build discipline
- The topology is the **target**, not a license to build all of it. One vertical slice, proven with a command +
  fixtures, then retire the parallel handling. Never create a kernel service ahead of a slice that needs it.
- Every move ends in one canonical `move_type`; `audit_only` (docs/dashboards) does not count.
- Tests never compare wall-clock time against fixed fixture dates (a fixture that ages past a
  staleness window becomes a time bomb that reds main days after a green landing). Inject the
  clock, or freshen fixture timestamps at runtime.
- A timeout-guarded lane needs a falsifier for the timeout path at realistic scale, and must
  persist partial progress. Instant stubs cannot catch a cold start that can never finish inside
  the deadline — without partial persistence the lane re-fails identically forever and reads as
  "degraded" while every test is green. (Lived: semantic capgraph cold start, 574 nodes vs 3s.)
- Integration has levels: **orientation < pointer < merge < proof.** Only merge and runtime proof count.
- **A capability with no live production consumer is bench-only.** A great bakeoff/test number is not proof the
  feature reaches its real caller — `grep` for the production consumer of the code you landed. (Lived example:
  the 0.955 semantic recall was consumed only by `memory query`; the agent-facing orient/hook path used a naive
  ranker and never called it, so live sessions got worse recall than the bench claimed.) And a fix can land
  *correct but dormant* if its env/config gate never engages on the operator's live config — verify the gate is
  live where the consumer actually runs, not just where your test shell exports it.
- Console tests are `.selftest.ts` files (exporting `run…SelfTest(): {ok, details}`) executed by a
  `console/scripts/<name>-smoke.mjs` runner wired into `proof-check`'s gate list — never `bun:test`
  files: the console-tsc gate cannot resolve `bun:test` types, and an unwired test file is a check
  that never runs. (Lived: decision-brief falsifiers, 2026-07.)
- Generated runtime state goes under `COMPOUND_HOME`, never the repo source tree. No `cwd` as authority.

## Public / private boundary (this repo is destined to be public)
- `docs_private/`, `*.private.md`, `.compound-private/` are gitignored — **never tracked, never shipped.** Never
  put a personal path, credential, account, or private body into any tracked file, including comments/examples.
- Public core = contracts, lifecycle, CLI, generic adapters, synthetic fixtures. Private overlay = personal
  data, real paths, credentials, account-specific connectors — the dogfood dataset, not a private fork of core.
- Test fixtures use `.invalid`/`.example` hosts, never `localhost`/private URLs: the public redact gate scans the
  FULL content of any changed file, so a pre-existing private-URL fixture fails the gate the first time the file
  is touched — long after the fixture was written.

## Commit & safety
- Scoped commits, clear boundaries (source/contracts; tests/fixtures; docs). Do not "commit all."
- **Push is pre-authorized** (standing operator grant 2026-07-12): a normal `git push` of committed work to the
  project's existing remote — public included — needs no per-push confirmation. Two floors remain: a clean
  redact scan before any public push, and force-push/history rewrites still require explicit confirmation.
- Stop and ask before deleting/archiving major surfaces, changing approval boundaries, spending, publishing, or
  any irreversible/external action. Prove the change by a state delta, not a report.

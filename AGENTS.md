# AGENTS.md — operating contract for agents working in compound

compound is an owned, agent-agnostic **state-transition substrate**: it turns real traces into
evidence, evidence into adjudicated memory/registries/skills/decisions, those into routed action,
action into receipts, and receipts into learning and hardened infrastructure — while agents,
models, tools, and indexes stay rented and replaceable. You are a **rented executor**. compound
owns the spine; you do not become the source of truth.

## Read first (orientation)
- `docs/ARCHITECTURE.md`, `docs/LAYERS.md` — the information kernel (monolith, deep modules, cwd-independent).
- `docs/architecture/substrate.md` — the substrate-kernel **target topology** (map, not work); the two
  planes (compound control + execution gateway); the unified base + host/placement awareness.
- `docs/architecture/transition.md` — the `Transition` primitive, the 10 spaces, the 9-field invariant,
  second-order discipline, and the 8 acceptance tests.
- `docs/architecture/execution-plane.md` — the model-traffic gateway sibling (route evidence ≠ task success).
- `docs/architecture/contracts/README.md` — the kernel contracts (the lego seams) + the canonical
  `move_type` enumeration + the entity-resolution precedence order.
- `docs/roadmap/phases.md` — construction order (vertical slices).
- `docs/BOUNDARIES.md` — public-core vs private-overlay rules.
- Operator-specific execution sequencing lives in the private overlay `docs_private/` (gitignored,
  not present in clones). If it is absent, work from the public roadmap.

## Doctrine (non-negotiable)
1. **Own the meaning, rent the index.** The append-only ledger owns visibility, promotion,
   supersession, identity, and truth. Engines (`MemoryEngine`), models, tools, and harnesses are
   disposable views/executors. Never let one become authority. A gateway `RouteDecision` is
   execution-route evidence, **not** proof of task success.
2. **The core primitive is `Transition`, not a record.** Compounding = changed future behavior.
   Every non-trivial change answers: what changed, across which spaces, what future behavior it makes
   more likely. Every projection preserves `entity_id, scope, evidence_ref, time, actor, uncertainty,
   risk, cost, state_delta`.
3. **Evidence, not truth.** Incoming information is adjudicated (`observed → candidate → reject |
   park | promote | operationalize → superseded → retired`), never written straight to authority.
   Only `promoted` claims enter ambient recall. Unknown entity or low confidence → `parked`. Resolve
   identity by the precedence order in `contracts/README.md`; an uncertain match yields parked evidence
   only, never a promoted claim.
4. **Second-order before first-order.** No field is neutral; every affordance is an attractor. Before
   adding a skill/registry entry/route/memory, ask what it makes more likely tomorrow and what it can
   fossilize or flatten. Selection is ensemble-aware, not lookup.

## Execution is a feedback loop (the path generates the architecture)
The architecture docs are a **starting hypothesis, not a frozen spec.** Building compound is itself the
self-healing loop compound exists to provide — so work this way, and make sure every agent that touches
this repo does too:

- **Execute a slice → write tests that probe the system's *depth*, not just happy paths → let the tests
  surface flaws → fix and harden (self-heal) → feed what you learned back into the architecture docs,
  the contracts, the tests, AND these instructions (AGENTS.md / CLAUDE.md), in the *same* change.**
- **Tests are how the system understands its own depth and finds its own flaws.** A slice is not done
  when it runs; it is done when tests prove it survives the relevant acceptance tests (`transition.md`)
  and the failure modes it will actually hit. Prefer tests that would *fail* if the depth were wrong.
- **When execution teaches you the design was wrong, change the design.** The path is the source of
  truth, not the plan. "It is about the path, not the direction." Updating AGENTS.md / CLAUDE.md / a
  contract locally as you learn is expected, not a violation.
- This loop is **generic** — it applies to any agent in any repo, not just compound.

## Build discipline
- The substrate topology is the **target**, not a license to build all of it. Build **one vertical
  slice** (`docs/roadmap/phases.md`), prove it with a real command + synthetic fixtures, then retire
  the parallel handling it replaces. **Never create a kernel service ahead of a slice that needs it.**
- Every move ends in one canonical `move_type` (`contracts/README.md`): `outward_delta | memory_delta |
  rule_delta | control_plane_repair | audit_only`. A `control_plane_repair` only counts as progress if
  it removes a duplicate owner, fixes a recurring failure, or creates runtime proof — otherwise it is
  `audit_only`, which does **not** count. Default to subtraction; name what each move retires.
- Integration has levels: **orientation < pointer < merge < proof.** Only merge and runtime proof count.
- No `cwd` as authority. Resolve identity from explicit ids / aliases / fingerprints / source metadata;
  return `unresolved`/`multi_scope` rather than guess.

## Public / private boundary (this repo is destined to be public)
- `docs_private/`, `*.private.md`, `.compound-private/` are gitignored — **never tracked, never
  shipped.** **Never** put a personal path, credential, account, or private body into any tracked
  (`docs/`, code) file, including comments and example strings.
- Public core = contracts, lifecycle, CLI, generic adapters, synthetic fixtures. Private overlay =
  personal data, real paths, credentials, account-specific connectors.
- The **public/private contamination test** gates every commit; run a secret/PII scan before any push.

## Commit & safety
- Scoped commits with clear boundaries (source/contracts; tests/fixtures; docs). Do not "commit all."
- **Do not push to a public remote without explicit operator confirmation** + a redact scan.
- Stop and ask before: deleting/archiving major surfaces, changing approval boundaries, spending,
  publishing, or any irreversible/external action.

## Verify, don't claim
Prove the change by a state delta (a command's real output, a passing test), not by a report that says
it worked.

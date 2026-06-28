# AGENTS.md — operating contract for agents working in compound

compound is an owned, agent-agnostic **state-transition substrate**: it turns real traces into
evidence, evidence into adjudicated memory/registries/skills/decisions, those into routed action,
action into receipts, and receipts into learning and hardened infrastructure — while agents,
models, tools, and indexes stay rented and replaceable. You are a **rented executor**. compound
owns the spine; you do not become the source of truth.

## Read first (orientation)
- `docs/ARCHITECTURE.md`, `docs/LAYERS.md` — the information kernel (monolith, deep modules, cwd-independent).
- `docs/architecture/substrate.md` — the substrate-kernel **target topology** (map, not work).
- `docs/architecture/transition.md` — the `Transition` primitive, the 10 spaces, the central
  invariant, second-order discipline, and the 8 acceptance tests.
- `docs/architecture/contracts/README.md` — the kernel contracts (the lego seams).
- `docs/roadmap/phases.md` — construction order (vertical slices).
- `docs/BOUNDARIES.md` — public-core vs private-overlay rules.
- Operator-specific execution sequencing lives in the private overlay `docs_private/` (gitignored,
  not present in clones). If it is absent, work from the public roadmap.

## Doctrine (non-negotiable)
1. **Own the meaning, rent the index.** The append-only ledger owns visibility, promotion,
   supersession, identity, and truth. Engines (`MemoryEngine`), models, tools, and harnesses are
   disposable views/executors. Never let one become authority.
2. **The core primitive is `Transition`, not a record.** Compounding = changed future behavior.
   Every non-trivial change must answer: what changed, across which spaces, what future behavior
   it makes more likely. Every projection preserves `entity_id, scope, evidence_ref, time, actor,
   uncertainty, risk, cost, state_delta`.
3. **Evidence, not truth.** Incoming information is adjudicated (`observed → candidate → parked |
   promoted | operationalized | retired`), never written straight to authority. Only `promoted`
   claims enter ambient recall. Unknown entity or low confidence → `parked`, never `promoted`.
4. **Second-order before first-order.** No field is neutral; every affordance is an attractor.
   Before adding a skill/registry entry/route/memory, ask what it makes more likely tomorrow and
   what it can fossilize or flatten. Selection is ensemble-aware, not lookup.

## Build discipline
- The substrate topology is the **target**, not a license to build all of it. Build **one vertical
  slice** (`docs/roadmap/phases.md`), prove it with a real command + synthetic fixtures, then
  retire the parallel handling it replaces. **Never create a kernel service ahead of a slice that
  needs it.** A diagram is not a substrate; the map is not the work.
- Every move ends in exactly one of: outward delta · memory delta · rule delta · retired surface ·
  evidenced blocker. A doc/dashboard/diagram does not count. Default to subtraction; name what each
  move retires.
- No `cwd` as authority. Identity is resolved from explicit ids / aliases / source metadata /
  content signals; return `unresolved`/`multi_scope` rather than guess.

## Public / private boundary (this repo is destined to be public)
- `docs_private/`, `*.private.md`, `.compound-private/` are gitignored. **Never** put a personal
  path, credential, account, or private body into any tracked (`docs/`, code) file.
- Public core = contracts, lifecycle, CLI, generic adapters, synthetic fixtures. Private overlay =
  personal data, real paths, credentials, account-specific connectors — outside the public tree.
- The **public/private contamination test** gates every commit.

## Commit & safety
- Scoped commits with clear boundaries (source/contracts; tests/fixtures; docs; generated only if
  source changed). Do not "commit all."
- Run `redact` before any push. **Do not push to a public remote without explicit operator
  confirmation.**
- Stop and ask before: deleting/archiving major surfaces, changing approval boundaries, spending,
  publishing, or any irreversible/external action.

## Verify, don't claim
Prove the change by a state delta (a command's real output, a passing fixture), not by a report
that says it worked.

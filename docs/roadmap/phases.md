# Roadmap — Phase-Transition Proofs

Construction order is **vertical slices**, not horizontal layers. Each phase proves one
*phase transition* of the living system with a real command and real (or faithfully synthetic)
data. A phase is done only when its acceptance test (behavioral, not a document) passes.

Discipline (from `../LAYERS.md` + the operator's laws): build one concern into the existing
kernel, prove it, retire the parallel handling it replaces. Never build a kernel service ahead
of a slice that needs it. A diagram is not a substrate; the map is not the work.

> This file is the **generic** roadmap. The operator-specific sequencing, real data sources,
> and coordination live in the private overlay (`docs_private/plan/COHERENT-PLAN.md`).

## Phase 0 — Reality coherence + the unified base
**Question:** does the system know what exists (and where it runs) without hallucinating from
docs/folders?
**Adds:** identity + a reality inventory + the **unified base** (the single generated home for
settings + information) + a **placement map** (what runs on which host).
**Proof:** one discovered entity survives a path move *and* a host move (same entity, old binding
retired, new binding active, no duplicate); one duplicate owner merged/killed; one stale path retired;
one live automation mapped to owner + host + receipt surface.

## Phase 1 — Projection coherence  (the first real vertical slice)
**Question:** can one real event project into multiple spaces without losing identity?
**Adds:** `TraceEnvelope` + `Transition` + adjudicated `Claim`/`MemoryDelta`.
This replaces a thin "ingest contradictions → timeline" demo. The slice:
```text
one real session trace
  → entity resolved (canonical id via the precedence order, not a duplicate)
  → evidence written
  → memory candidate created → ADJUDICATED (reject/park/promote) → delta (was→now→evidence→date)
  → ALSO emit ≥2 more projections: one job receipt, one skill-used/missed signal
  → all written as ONE Transition
  → a future orientation packet is built from it
  → a tiny future agent task consumes the packet
  → a receipt proves behavior changed
```
**Acceptance (`compound transition show <id>` + behavioral):** timeline shows old→new→evidence;
recall packet built; a future agent *used* it; receipt proves the use; **no duplicate entity**;
unknown entity/low confidence → `parked`, never `promoted`. State plainly: *this proves multi-space
projection + adjudicated delta; relevance-gated recall is tested by the agent-consumption step.*

> Sub-steps if needed: **contract-first** (pin the trace/lens schema + entity+predicate
> canonicalization + adjudication lifecycle) → **ingest** (public core + private glue separate) →
> **quality** (`compound memory doctor`: processed/promoted/parked/rejected, unresolved entities,
> duplicate candidates, claims-missing-evidence, timeline round-trip).

## Phase 2 — Recall / action coherence
**Question:** does recalled context change agent behavior in the intended way?
**Adds:** recall broker (scope/visibility/retired/freshness/confidence filters) before any engine.
**Proof:** same job without the compound packet → weaker execution; with it → correct context, better
route, better receipt. Engine swap (one `MemoryEngine` implementation → another) leaves the ledger
byte-identical. Same recall packet consumed by ≥2 harnesses, citing the same claim/evidence ids.
(Agent-agnosticism becomes real — proven by replacement.)

## Phase O — Orientation coherence  (context that fits the work)
**Question:** does an agent get context appropriate to *what it is working on*, not global governance?
**Adds:** orientation router (`compound orient --cwd --harness --task`).
**Proof:** the **same task run in a product repo vs the control-plane repo produces different context
packets** — the product repo omits control-plane/OS-governance context and "all skills / all MCP tools
by default"; the control-plane repo includes them. This directly attacks the fragmentation the operator
feels when working on a non-OS project.

## Phase 3 — Routing coherence
**Question:** can compound choose the right executor across model/tool/harness/script/human?
**Adds:** `JobUnit` + routing engine with an **uncertainty escalator** and **routing remorse**.
**Proof:** one job → deterministic script; one → small model; one escalated to frontier/vision/human
because uncertainty or risk demanded it; each route explains its tradeoff.

## Phase 4 — Product-feedback coherence
**Question:** can a product action become a hypothesis, metric observation, taste judgment, next move?
**Adds:** `ProductSignal` + `Hypothesis` + the `Product` market-control surface.
**Proof:** `compound product timeline <product>` shows change → metric signal → *competing*
interpretations (no causal over-claim) → confidence → next **gated** action.

## Phase 5 — Hardening / subtraction coherence
**Question:** does repetition change the system's structure?
**Adds:** evaluation/hardening engine (Law 7 + Law 8) + OS-tax receipt.
**Proof:** `compound mine repetitions` → repeated success → skill/script/checker candidate; repeated
failure → rule/guard; dead surface → retirement; control-plane work → OS-tax signal.

## Execution-plane track (S0–S5) — runs alongside, read-only first
The model-traffic gateway integrates *read-only first*, then controlled ingress, then relay canary
(`../architecture/execution-plane.md`). Build only when a slice needs it.
- **S0** — one low-risk request → gateway trace → imported as `ExecutionRouteEvidence` → shown as a
  projection of a `Transition` (selected/rejected routes, cost, latency, status; no prompt/response).
- **S1** — normalize gateway lanes into the `Harness`/gateway registry + classify them (direct-native,
  observed-tap, ingress, relay-canary, manual/pro, cheap-scout) so one lane name means one thing.
- **S2** — join route evidence with outcome evidence (route success ≠ task success).
- **S3** — gateway eval evidence stays *preview-only* until compound has enough outcome data to promote
  a route policy for a scope.
- **S4** — compound defines autonomy/risk/cost policy; the gateway enforces provider/account/budget/quota.
- **S5** — native relay only after its conformance/fixture gate is green (fail-closed; rollback keeps
  direct-native usable).

## Cross-phase rules
- **Integration has levels: orientation < pointer < merge < proof.** Knowing a thing exists, linking to
  it, actually consolidating it, and proving it runs are four different states. Only **merge** and
  **runtime proof** count toward de-fragmentation; a pointer or a doc does not.
- Every phase splits **public core** (contracts, lifecycle, CLI, fixtures) vs **private overlay**
  (paths, credentials, accounts, personal memory).
- Every phase must pass the relevant **acceptance tests** in `../architecture/transition.md`.
- Every phase ends with a state delta and names what it **retired** — or it does not count.
- **Execution generates the architecture.** When a slice teaches you the design was wrong, update the
  docs/contracts/tests/instructions in the same change (see `../../AGENTS.md` → "Execution is a
  feedback loop"). It is about the path, not the direction.

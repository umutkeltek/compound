# Roadmap — Phase-Transition Proofs

Construction order is **vertical slices**, not horizontal layers. Each phase proves one
*phase transition* of the living system with a real command and real (or faithfully synthetic)
data. A phase is done only when its acceptance test (behavioral, not a document) passes.

Discipline (from `../LAYERS.md` + the operator's laws): build one concern into the existing
kernel, prove it, retire the parallel handling it replaces. Never build a kernel service ahead
of a slice that needs it. A diagram is not a substrate.

> This file is the **generic** roadmap. The operator-specific sequencing, real data sources,
> and coordination live in the private overlay (`docs_private/plan/COHERENT-PLAN.md`).

## Phase 0 — Reality coherence
**Question:** does the system know what exists without hallucinating from docs/folders?
**Adds:** identity + a reality inventory.
**Proof:** one discovered entity survives a path change; one duplicate owner is merged/killed;
one stale path retired; one live automation mapped to owner + receipt surface.

## Phase 1 — Projection coherence  (the first real vertical slice)
**Question:** can one real event project into multiple spaces without losing identity?
**Adds:** `TraceEnvelope` + `Transition` + adjudicated `Claim`/`MemoryDelta`.
This is the convergence of all prior critiques — and it replaces a thin "ingest contradictions →
timeline" demo. The slice:
```text
one real session trace
  → entity resolved (canonical id, not a 2nd copy of the same product)
  → evidence written
  → memory candidate created → ADJUDICATED (reject/park/promote) → delta (was→now→evidence→date)
  → ALSO emit ≥2 more projections: one job receipt, one skill-used/missed signal
  → all written as ONE Transition
  → a future orientation packet is built from it
  → a tiny future agent task consumes the packet
  → a receipt proves behavior changed
```
**Acceptance (`compound transition show <id>` + behavioral):** timeline shows old→new→evidence;
recall packet is built; a future agent *used* it; receipt proves the use; **no duplicate entity
created**; unknown entity/low confidence → `parked`, never `promoted`. Must explicitly state:
*this proves multi-space projection + adjudicated delta; relevance-gated recall is tested by the
agent-consumption step, not assumed.*

> Sub-steps if needed: **contract-first** (pin the trace/lens schema + entity+predicate
> canonicalization + adjudication lifecycle) → **ingest** (build it, public core + private glue
> separate) → **quality** (`compound memory doctor`: processed/promoted/parked/rejected,
> unresolved entities, duplicate candidates, claims-missing-evidence, timeline round-trip).

## Phase 2 — Recall / action coherence
**Question:** does recalled context change agent behavior in the intended way?
**Adds:** recall broker (scope/visibility/retired/freshness/confidence filters) before any engine.
**Proof:** same job without the compound packet → weaker execution; with it → correct context,
better route, better receipt. Engine swap (one `MemoryEngine` implementation → another) leaves the ledger
byte-identical. Same recall packet consumed by ≥2 harnesses, citing the same claim/evidence ids.
(This is where agent-agnosticism becomes real — proven by replacement.)

## Phase 3 — Routing coherence
**Question:** can compound choose the right executor across model/tool/harness/script/human?
**Adds:** `JobUnit` + routing engine with an **uncertainty escalator** and **routing remorse**.
**Proof:** one job → deterministic script; one → small model; one escalated to frontier/vision/
human because uncertainty or risk demanded it; each route explains its tradeoff.

## Phase 4 — Product-feedback coherence
**Question:** can a product action become a hypothesis, metric observation, taste judgment, and
next gated move?
**Adds:** `ProductSignal` + `Hypothesis`, product entities as market-control surfaces.
**Proof:** `compound product timeline <product>` shows change → metric signal → *competing*
interpretations (no causal over-claim) → confidence → next **gated** action.

## Phase 5 — Hardening / subtraction coherence
**Question:** does repetition change the system's structure?
**Adds:** evaluation/hardening engine (Law 7 + Law 8) + OS-tax receipt.
**Proof:** `compound mine repetitions` → repeated success → skill/script/checker candidate;
repeated failure → rule/guard; dead surface → retirement; control-plane work → OS-tax signal.

## Cross-phase rules
- Every phase splits **public core** (contracts, lifecycle, CLI, fixtures) vs **private overlay**
  (paths, credentials, accounts, personal memory).
- Every phase must pass the relevant **acceptance tests** in `../architecture/transition.md`.
- Every phase ends with a state delta and names what it **retired** — or it does not count.

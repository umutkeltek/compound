# Compound Layers — the canonical architecture

> **The normalizing sentence every other doc points back to:**
>
> **Compound is a local-first meaning + execution substrate with two internal axes — the
> Runtime / Envelope Spine that controls *how work flows*, and the Knowledge / Meaning Fold
> that owns *what meaning becomes*. Rented engines and executors sit at the edge. The
> compounding loop turns traces into candidates, adjudicates them into the Knowledge Fold,
> and records their effect through Transitions on the Spine.**

Compound is **one architecture with two axes, not two architectures.** Two construction
generations exist in this repo — an older "information kernel" and a newer "envelope spine."
They are **not rivals.** The spine is the *verbs* (how work flows); the fold is the *nouns*
(what meaning is owned). Everything else is rented at the edge or is the loop that joins the
two axes. When a doc, package, or command seems to belong to "the other architecture," it
belongs to **the other axis of this one.** See `architecture/status.md` for which package and
command belongs to which axis and which are legacy/bridge.

---

## Axis A — Runtime / Envelope Spine *(how work flows)*

The typed runtime grammar every harness, hook, and run flows through, hung off one root
`work_id`:

```text
Intent
→ EffectiveContract      what applies right now (kills global instruction bloat)
→ ContextPack            frozen context an agent consumes (records included AND excluded)
→ WorkEnvelope           one root identity unit of work (the JobUnit)
→ ActionIntent           proposed mutation, pre-policy
→ PolicyDecision         allow / allow_with_obligations / hold / deny (enforced, not asked)
→ Run / ToolEvent / RouteEvidence
→ Receipt                how a run is learned from (state_delta, cost, next_consumer)
→ Transition             the proof apex — state change + projections
```

**Axis A owns:** `work_id`, context consumed, harness capabilities, **policy / permission gates**,
receipts, state deltas, route evidence, observability, proof.

**Packages:** `internal/{home, envelope, contract, work, policy, harness, hook}`.
**Built:** P0–P5. Full schemas: `architecture/runtime.md`. Proof object: `architecture/transition.md`.

> Permissions and governance live **here** (PolicyDecision / AutonomyEnvelope), not as a separate
> knowledge layer — a permission is a gate on the flow of work, not a kind of meaning.

---

## Axis B — Knowledge / Meaning Fold *(what meaning becomes)*

The durable object kinds compound is the **authority** for. This is the older "information
kernel" recast into contract vocabulary — it did not disappear, it is this axis:

```text
Source
→ EvidenceItem           immutable, citeable raw material (never truth alone)
→ Entity                  identity that survives path/host moves
→ Claim / MemoryDelta     a stance, with supersession (alive, not static)
   · Decision             committed choice with status + falsifier + review_trigger
   · Capability           skill / tool / mcp / model / harness node in the graph
   · ProductSignal        market observation (evidence, not truth)
   · Hypothesis           testable product belief
→ Position / Pattern / RegistryEntry
```

**The six knowledge layers** (reconciled from the earlier 6-layer table; "Permissions" moved to
Axis A, "Capability" added):

| # | Layer | Owns | Shape |
|---|---|---|---|
| 1 | **Memory** | evidence-backed claims, positions, patterns, context packs | built (P6, `internal/mem`) |
| 2 | **Authority** (inside-world registries) | repo / host / runtime / storage / deploy / cost / product facts | early importer (`internal/authority`); **RuntimeBinding / ServiceContract** missing |
| 3 | **Decisions** | committed choices with status + review triggers | early importer (`internal/decisions`); **status lifecycle not yet first-class** |
| 4 | **Capability** | skills / tools / MCPs / models / harnesses as a typed graph | built (P8, `internal/capgraph`) |
| 5 | **Procedures** | repeatable deterministic work | planned |
| 6 | **Doctrine** | durable principles, taste, red-lines | planned |

**Axis B owns:** meaning, memory, identity, decisions, authority/infra facts, capabilities,
skills, product-truth candidates, doctrine, taste. Detail: `memory-architecture.md` (the
Memory slice), `architecture/contracts/README.md` (every contract).

---

## The Rented Edge *(NOT a layer)*

Engines and executors sit behind adapters. They can index, retrieve, route, execute, import,
or render — they **never own meaning, promotion, visibility, or supersession.**

```text
MemoryEngine            turbovec / a legacy vector+fact index — breadth, not authority
model gateway           a model-routing gateway — route evidence, not task success
LLM providers           swappable
harnesses               agent CLIs (Claude Code, Codex, OpenCode, …) — clients
MCPs / tools / CLIs      on-demand capabilities
source adapters         read existing files in place, emit candidates/evidence only
analytics APIs          app-store / revenue / product-analytics feeds — product feedback
```

Agent-agnosticism is proven by **replacement** (one harness checkpoints, another resumes from
spine context), not by "adapters exist."

---

## The Compounding Loop *(the alive part)*

What makes the system *compound* instead of *accumulate* — it joins Axis A and Axis B:

```text
Trace / Receipt / HookEvent / RouteEvidence / ProductSignal
→ TraceDistiller
→ RetrievedPriorState          distill against canon, not blind
→ CandidateSet                 all candidate-first, typed fan-out
→ DistillationCritic           new? supersedes? contradicts? lower-quality? become skill/rule/eval?
→ AdjudicationDecision         reject | park | promote | operationalize | retire
→ Knowledge Fold (Axis B)
→ ContextPack / Capability surfacing / Policy / Hypothesis
→ future WorkEnvelope
→ Receipt → Transition (Axis A)
```

Model extracts; **code decides.** Nothing reaches ambient recall unadjudicated.

---

## The two seams — where the axes meet *(read this twice)*

The axes do **not** meet only at distillation. They meet **twice**:

1. **Intake seam** — `Trace → Distill → Candidate → Critic → Adjudication → Knowledge Fold`.
   A trace *becomes* meaning.
2. **Proof seam** — `WorkEnvelope → Receipt → Transition → projections` back into
   memory / skill / product / cost / risk. A run *proves* meaning changed future behaviour.

**Transition is the proof apex — distillation is not.** If the architecture meets only at
distillation, compound looks like a knowledge-ingestion tool. Its actual product is a
**state-transition substrate**: storing a timeline or a memory delta does **not** prove
relevance-gated recall or a behaviour change — a Transition does.

```text
Distillation = the intake seam.
Transition   = the proof seam.
Both are required.
```

---

## Cross-cutting invariants

- **Provenance** — every durable object is citeable.
- **Visibility** — privacy and eligibility are filtered before recall (`source_class`).
- **Time** — claims are superseded without destroying evidence (MVCC-by-fold).
- **Placement** — every object has one canonical lookup path; cwd is a hint, never authority.
- **Subtraction** — adding a layer must retire scattered handling, not create another parallel
  home. Every move ends in one `move_type` (`outward_delta | memory_delta | rule_delta |
  control_plane_repair | audit_only`); `audit_only` does not count.

## Rented engines are not layers

```text
external source / engine → adapter → Evidence / Entity / Claim / Edge
→ promotion / reconciliation → Position / ContextPack
```

Storage engines, retrieval engines, LLM providers, editors, and external tools improve recall
or import/export, but do not own the object lifecycle.

## Build discipline

Build one concern into the existing kernel along the axis it belongs to, prove it with a
working command and fixtures, then **retire** the parallel handling it replaces. Do not start a
new service because an information type feels new, and do not let a second front door (doc,
package, or command) survive once its canonical replacement is proven — that is how
fragmentation survives while everyone believes it was solved.

# Compound as a Substrate Kernel (target topology)

This extends `../ARCHITECTURE.md` and `../LAYERS.md`. Those describe Compound as an
*information* kernel. This describes the larger target: Compound as a **cognitive +
execution substrate kernel** — the owned core that lets agents, models, tools, memory,
skills, registries, product loops, evidence, cost, taste, and autonomy compose without
fragmenting.

Read this as the **map, not the work.** The topology below is the destination. Execution
is one thin vertical slice at a time (see `../roadmap/phases.md`). A substrate diagram is
not a substrate. The discipline in `../LAYERS.md` still governs: *build one concern, prove
it with a working command + synthetic fixtures, retire the parallel handling it replaces;
do not start a service because an information type feels new.*

## What the substrate does (six jobs, at once)

```text
1. Know what exists.            (identity + registries)
2. Know what can act.           (capability graph: skills/tools/MCPs/models/harnesses)
3. Know what matters now.       (memory + orientation, relevance-gated)
4. Route the right work.        (job + routing engine: cheapest sufficient capability)
5. Convert outcomes to learning.(evidence → memory/skills/rules/product hypotheses)
6. Reduce future entropy.       (evaluation/hardening + subtraction)
```

## Two planes: control + execution

compound is the **control / meaning plane**. Model traffic flows through a separate
**execution / data plane** — an AI provider gateway (e.g. an OpenAI/Anthropic-compatible
router). They are siblings with a clean split, *not* nested:

```text
compound       = decides WHAT work is, WHAT matters, WHAT future behavior should change.
exec gateway   = decides HOW model/provider traffic flows, what it cost, where it went,
                 why there, and what evidence (RouteDecision/usage/cost) it left.
```

A gateway `RouteDecision` is **execution-route evidence, not proof of task success** — it
becomes one *projection* inside a compound `Transition` (the cost/routing projection),
joined with the harness receipt and the real state delta. compound owns the route **policy**;
the gateway owns the route **decision + enforcement** (budgets/quotas/credentials/fallback).
Detail: `execution-plane.md`.

## Core doctrine: own the meaning, rent everything replaceable

```text
OWN  (the spine, never delegated):
  identity, evidence/trace ledger, memory promotion, registries, routing policy,
  receipts, promotion/supersession, writeback, cost/evidence accounting, doctrine.

RENT (disposable, swappable behind adapters):
  vector/search engines (e.g. turbovec, pgvector, a future index) — views rebuildable from the ledger
  models / providers (behind the execution gateway)
  tools, MCPs, browsers
  harnesses (Claude Code, Codex, OpenCode, ChatGPT, local LLMs, future agents)
```

No executor, framework, model, or memory engine becomes the source of truth. Agent-
agnosticism is proven by **replacement** (swap the executor mid-task and the work
continues from spine context), not by an adapter interface existing.

## compound owns and generates the unified BASE (host-aware / distributed)

Today the operator's context is scattered across several roots. compound's job is to
**generate and own one unified base** — a single home that unifies settings + information —
and to retire the scattered roots over time (subtraction, not another parallel root).

compound is **host-aware and distributed**: parts may run on other servers or edge devices,
but the base lives on the working computer, and compound **knows what runs where**. Placement
is a first-class fact: every component/entity can carry `host_bindings` and `deployment_bindings`
(which machine, which service, which port/endpoint, health). "Know what is working in what place"
is an identity + registry concern, resolved cwd-independently (a moved folder or a component that
migrated to another host stays the same entity).

## Kernel services (target) mapped onto existing layers

The 6 layers in `../LAYERS.md` (Memory = building; Authority, Decisions = early importers;
Procedures, Permissions, Doctrine = planned) are not replaced — the substrate services are how
those layers express at runtime:

| Substrate service | Question it answers | Maps onto / extends |
|---|---|---|
| Identity kernel | what *is* this thing? (survives path/host moves) | scope resolver + entity model |
| Registry kernel | what exists, and when should it be used? | Authority layer → registry-of-registries |
| Trace / evidence ledger | what actually happened? | ledger (the ground; everything else is a view) |
| Memory / knowledge | what should be remembered/recalled/retired? | Memory layer |
| Capability graph | what can act, think, judge, execute? | new (skills/tools/MCPs/models/harnesses as one graph) |
| Orientation router | what context should this agent receive *now*? | new (uses identity scope; see phases "Orientation coherence") |
| Job / routing engine | smallest meaningful unit of work, and who does it? | Procedures layer |
| Execution broker / gateway | how do agents act without becoming the truth? | new (rented executors + the exec plane) |
| Governance / autonomy gates | how does it act without chaos? | Permissions layer |
| Product feedback engine | how does work become product learning? | new (Authority for products + hypotheses) |
| Evaluation / hardening | what should become infrastructure / a rule? | new (Law 7 + Law 8 engine) |
| Observability / control surface | what is happening, why, cost, what next? | new (operator cockpit) |

**The registry is not flat inventory.** It distinguishes **inside-world** (this system's own
projects/tools/products/deployments/configs/constraints) from **outside-world**
(frameworks/libraries/methods/models/expert skills/repos), joined by a **synthesis layer** that
answers *"given THIS job/context/cost/risk/evidence, which option is currently preferred, and why"*
— not a static "X is good / Y is good" lookup — and which adapts over time.

**Memory is inside the substrate, not above it.** The trace/evidence ledger is the ground;
memory, product loops, skill-mining, and self-healing are all *views over the ground*.

## The reconciliation principle (why this won't become overbuild)

The substrate vision is seductive and the operator's #1 failure mode is overbuilding. Hold
both:

- The **topology above is the target** — captured so it is never re-derived (the rediscovery tax).
- **Execution is thin vertical slices** — each touches several services but only as deep as a
  single proof needs. The `Transition` primitive (`transition.md`) is what makes a small slice
  substrate-shaped.
- **Never build a service ahead of a proven need.** A new kernel service earns its place only when
  a slice cannot complete without it. Default to subtraction; the control plane must earn its keep
  (every move = an outward/memory/rule delta, or it is telemetry).

The order of construction is in `../roadmap/phases.md`.

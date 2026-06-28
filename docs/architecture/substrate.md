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

## Core doctrine: own the meaning, rent everything replaceable

```text
OWN  (the spine, never delegated):
  identity, evidence/trace ledger, memory promotion, registries, routing logic,
  receipts, promotion/supersession, writeback, cost/evidence accounting, doctrine.

RENT (disposable, swappable behind adapters):
  vector/search engines (e.g. turbovec, pgvector, or a future index) — views rebuildable from the ledger
  models / providers
  tools, MCPs, browsers
  harnesses (Claude Code, Codex, OpenCode, Hermes, ChatGPT, local LLMs)
```

No executor, framework, model, or memory engine becomes the source of truth. Agent-
agnosticism is proven by **replacement** (swap the executor mid-task and the work
continues from spine context), not by an adapter interface existing.

## Kernel services (target) mapped onto existing layers

The 6 layers in `../LAYERS.md` (Memory ✓, Authority ✓, Decisions ✓, Procedures, Permissions,
Doctrine) are not replaced — the substrate services are how those layers express at runtime:

| Substrate service | Question it answers | Maps onto / extends |
|---|---|---|
| Identity kernel | what *is* this thing? (survives path moves) | scope resolver + entity model |
| Registry kernel | what exists, and when should it be used? | Authority layer → registry-of-registries |
| Trace / evidence ledger | what actually happened? | ledger (the ground; everything else is a view) |
| Memory / knowledge | what should be remembered/recalled/retired? | Memory layer |
| Capability graph | what can act, think, judge, execute? | new (skills/tools/MCPs/models/harnesses as one graph) |
| Orientation router | what context should this agent receive *now*? | new (uses identity scope) |
| Job / routing engine | smallest meaningful unit of work, and who does it? | Procedures layer |
| Execution broker | how do agents act without becoming the truth? | new (rented executors) |
| Governance / autonomy gates | how does it act without chaos? | Permissions layer |
| Product feedback engine | how does work become product learning? | new (Authority for products + hypotheses) |
| Evaluation / hardening | what should become infrastructure / a rule? | new (Law 7 + Law 8 engine) |
| Observability / control surface | what is happening, why, what does it cost, what next? | new (operator cockpit) |

**Memory is inside the substrate, not above it.** The trace/evidence ledger is the ground;
memory, product loops, skill-mining, and self-healing are all *views over the ground*.

## The reconciliation principle (why this won't become overbuild)

The substrate vision is seductive and the operator's #1 failure mode is overbuilding. Hold
both:

- The **topology above is the target** — captured so it is never re-derived (the rediscovery tax).
- **Execution is thin vertical slices** — each slice touches several services but only as
  deep as a single proof needs. The `Transition` primitive (`transition.md`) is what makes a
  small slice substrate-shaped: one real event, projected into several spaces, proving the
  *geometry* without building every service.
- **Never build a service ahead of a proven need.** A new kernel service earns its place only
  when a slice cannot complete without it. Default to subtraction; the control plane must earn
  its keep (every move = an outward/memory/rule delta, or it is telemetry).

The order of construction is in `../roadmap/phases.md`.

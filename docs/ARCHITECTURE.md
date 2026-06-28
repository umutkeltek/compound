# Compound Architecture — overview & reading order

> This file is an **overview and index**, not a second object model. The canonical
> architecture — the two axes, the rented edge, the compounding loop, and the two seams — is
> defined **once** in [`LAYERS.md`](./LAYERS.md). Read that first; everything here points into it.

Compound is a **local-first meaning + execution substrate** — a local monolith with deep
internal modules. Agents and users see one coherent system, not a scattered microservice estate.
It has two internal axes:

- **Axis A — Runtime / Envelope Spine** (how work flows): `architecture/runtime.md`,
  `architecture/substrate.md`, proof object in `architecture/transition.md`.
- **Axis B — Knowledge / Meaning Fold** (what meaning becomes): `memory-architecture.md` (the
  Memory slice), all contracts in `architecture/contracts/README.md`.

Plus the **rented edge** (engines / harnesses / providers / adapters — `architecture/execution-plane.md`)
and **the compounding loop** that joins the axes (`LAYERS.md`).

## Reading order

1. [`LAYERS.md`](./LAYERS.md) — **the canonical architecture.** Start here.
2. [`architecture/status.md`](./architecture/status.md) — which package / command is
   **canonical vs legacy vs bridge vs planned vs retired.** Read before touching code, so you
   don't improve the wrong family.
3. [`architecture/runtime.md`](./architecture/runtime.md) — Axis A: `COMPOUND_HOME`, the
   envelopes, hooks-as-clients.
4. [`architecture/contracts/README.md`](./architecture/contracts/README.md) — every contract
   (the lego seams), both axes.
5. [`memory-architecture.md`](./memory-architecture.md) — the Memory slice of Axis B in depth.
6. [`architecture/transition.md`](./architecture/transition.md) — the proof seam / compounding.
7. [`roadmap/phases.md`](./roadmap/phases.md) — the envelope-first construction order.
8. [`BOUNDARIES.md`](./BOUNDARIES.md) — public core vs private overlay.

## Why a monolith, not microservices

The reference-system failure mode: many useful surfaces become hard to operate when every tool
has a different root, cwd assumption, generated view, and authority interpretation. Compound
removes that tax by giving agents **one stable interface** over both axes:

```text
compound orient        # Axis A: what applies right now
compound work ...       # Axis A: one unit of work
compound policy ...     # Axis A: gates
compound memory ...     # Axis B: meaning / memory
compound capability ... # Axis B: skills / tools / models graph
compound transition ... # the proof seam
```

Adapters can read files where they already live, but **compound owns the lifecycle of derived
knowledge.** Implementation packages, adapters, and optional engines are internal seams — they
must not become separate authority centers.

## Information placement

- **Compound-owned** canonical ledger objects (append-only JSONL under one ledger root):
  `evidence`, `entities`, `claims`, `positions`, `patterns`, `edges`, `scope_assignments`.
- **Source-in-place**: external sources (repo files, notes, decision docs, runtime contracts,
  transcripts) stay where they belong; compound stores durable identifiers, evidence refs,
  claims, and citations. It does not copy every source into one folder to make it findable.

## Cwd independence

Compound never infers truth from the working directory. Correct attribution comes from explicit
identifiers, registered aliases, content signals, decision-turn signals, and source metadata. If
unclear, it returns `unresolved` / `multi_scope` rather than guessing.

## Public core and private overlay

The open-source core ships the substrate (object model, ledger, scope resolver, recall planner,
promotion/reconciliation lifecycle, context-pack assembly, fixtures, adapter interfaces). A
private running instance owns private meaning **inside `COMPOUND_HOME`** — not as a permanent
external "overlay." The public core is useful without it. Detail and rules: `BOUNDARIES.md`.

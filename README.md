# compound

> A personal OS that preserves and reuses how you think, know, decide, and operate —
> so your thinking compounds instead of disappearing.

Not generic. Opinionated. The opposite of a memory tool that "distills" by reading only the
first and last message.

## The six layers (separation of concerns)

A personal OS **knows** (Memory) · knows **what's true** (Authority) · remembers **what it
decided** (Decisions) · knows **how to do things** (Procedures) · knows **what it's allowed to
do** (Permissions) · and has **a point of view** (Doctrine). Built one at a time; each absorbed
layer **retires** its old scattered home. See `docs/LAYERS.md`.

## Memory layer

The foundation. Architecture: `docs/memory-architecture.md` (object model, ledger-as-authority,
scope resolver, query planner — converged from Umut's doctrine + a GPT-5.5-Pro consult + mempalace).

```
compound distill <session.jsonl> [--ingest]   # deep-distill a whole conversation → candidate claims
compound scope   <session.jsonl> [--explain]  # attribute a session to entities (content, not cwd)
compound recall  "<query>"                     # query planner: active positions + owned claims + engine breadth
compound promote <claim-id> --basis=<b>        # candidate → promoted (the only path to canon)
compound build                                 # derive current Positions + recurring Patterns from claims
compound reconcile [--entity=E]                # alive memory: supersede contradicted/duplicate claims
compound pack    "<topic>"                     # breadth via the rented engine
compound ledger  stat                          # record counts per canonical object kind
compound version
```

Pipeline: `distill --ingest` cleans the whole session → worth-gates → map-reduces over a reasoning
model → resolves scope → appends EvidenceItems + **candidate** Claims to the append-only ledger.
`recall` plans a mode and surfaces owned claims; `promote` is the gate that turns a candidate into
canon (the distiller can never self-canonize — evidence ≠ canon).

## Language & layout — Go

Go is the spine (CLI + daemon + ingestion/indexer + single binary). TypeScript/Bun is kept for
the proven distiller prototype (`prototype-ts/`, being ported) and any future UI; Python only at
the ML edge.

```
cmd/compound/          # the CLI (single binary)
internal/
  memory/              # OWNED object model: Evidence → Claim → Position → Pattern, Entity, Edge
  ledger/              # append-only canonical store — THE authority; engine is a rebuildable view
  engine/              # RENTED storage/retrieval behind MemoryEngine (gbrain adapter today, swappable)
docs/
  LAYERS.md            # the six-layer architecture
  memory-architecture.md  # the Memory design (canonical)
  memory-study.md      # gbrain + mempalace study
prototype-ts/          # the proven TS spike (distiller etc.) — reference for the Go port
```

## Status

🟢 Memory layer (Go): object model + append-only ledger + swappable engine adapter + **deep
  distiller** (parity-confirmed vs the TS prototype) + **scope resolver** (content, not cwd) +
  **query planner** (entity/hybrid, hard filters) + **promotion gate** + **Position/Pattern
  construction** (`compound build` — current stance per entity/topic, active⇔promoted; recurring
  through-lines as hypotheses). Builds + vets clean; full chain proven on real data
  (distill → scope → candidate claims → build positions → entity recall → promote → position goes active).
🔜 Next tuning: distinctiveness/IDF downweight in scope; temporal & ambient recall modes; supersedes
  edges (conflict detection); semantic Pattern naming. Then layers 2–6 (Authority first, from infra-os).

## Provenance

Graduated from a proven spike (`~/Projects/operating-system/Plans/even-though-i-was-silly-thimble.md`;
verdict `~/brain/reviews/system-audit/2026-06-24-context-engine-spike.md`). Dogfood-first; architected
so another person could load *their* Memory/Authority/Decisions later.

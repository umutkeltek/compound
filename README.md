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
compound recall <entity>      # entity-anchored recall (precision) via the rented engine
compound pack "<topic>"       # breadth via the rented engine
compound ledger stat          # record counts per canonical object kind
compound version
```

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

🟢 Go foundation: object model, append-only ledger, engine interface + gbrain/null adapters, CLI
  (build + vet clean, binary runs, gbrain adapter proven).
🔜 Next: port the deep distiller (TS → Go, with parity check) · scope resolver · query planner ·
  promotion gate. Then layers 2–6.

## Provenance

Graduated from a proven spike (`~/Projects/operating-system/Plans/even-though-i-was-silly-thimble.md`;
verdict `~/brain/reviews/system-audit/2026-06-24-context-engine-spike.md`). Dogfood-first; architected
so another person could load *their* Memory/Authority/Decisions later.

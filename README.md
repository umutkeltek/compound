# compound

> Local-first information kernel for preserving, adjudicating, and reusing what a
> person or team knows, decides, and operates.

Compound is opinionated: evidence is not canon, search is not truth, and current
knowledge must be placed, cited, promoted, and superseded.

## Architecture

Compound is a **local monolith information kernel** with deep internal modules and
adapters at the edges. It is not a microservice estate and not an agent framework.

Start with:

- `CONTEXT.md` - domain glossary.
- `docs/ARCHITECTURE.md` - monolith boundary, placement, cwd-independent scope,
  and extensibility.
- `docs/BOUNDARIES.md` - public core versus private overlay.
- `docs/adr/0001-local-monolith-information-kernel.md` - accepted architecture
  decision.

## Layers

Compound separates six concerns:

- **Memory** - what is known.
- **Authority** - what is currently true about external things.
- **Decisions** - what has been committed to.
- **Procedures** - how repeatable work is done.
- **Permissions** - what actors are allowed to do.
- **Doctrine** - durable principles and preferences.

Each layer should be absorbed into the same object lifecycle instead of becoming a
new service or scattered folder. See `docs/LAYERS.md`.

## Memory Layer

Architecture: `docs/memory-architecture.md`.

Core pipeline:

```text
source material
-> EvidenceItem
-> candidate Claim
-> promotion / adjudication
-> Position / Pattern
-> ContextPack
```

CLI shape:

```text
compound distill <session.jsonl> [--ingest]      # whole-session distillation -> candidate claims
compound scope <session.jsonl> [--explain]       # content-based scope, not cwd
compound recall "<query>"                        # active positions + claims + optional engine breadth
compound promote <claim-id> --basis=<basis>      # candidate -> promoted
compound build                                   # derive current positions and recurring patterns
compound reconcile [--entity=E]                  # append supersession edges for contradictions/duplicates
compound timeline <entity>                       # show position evolution
compound pack "<topic-or-entity>"                # assemble a context pack
compound authority import [registry.yaml]        # import authority registry rows
compound decisions import                        # import configured decision sources
compound ledger stat                             # record counts per object kind
compound version
```

The distiller can emit evidence and candidate claims. It cannot make canon by
itself. Promotion and reconciliation are explicit lifecycle steps.

## Layout

```text
cmd/compound/       CLI, one local binary boundary
internal/memory/    owned object model: Evidence -> Claim -> Position -> Pattern
internal/ledger/    append-only canonical store for derived knowledge objects
internal/engine/    optional retrieval adapters behind MemoryEngine
internal/scope/     cwd-independent attribution
internal/recall/    query planner and visibility filters
internal/authority/ authority registry importer
internal/decisions/ decision source importer
docs/               architecture notes and ADRs
prototype-ts/       historical prototype reference while Go implementation matures
```

## Configuration Seams

The public core has no private path defaults.

- `COMPOUND_LEDGER` overrides the local ledger root. Default: `~/.compound/ledger`.
- `COMPOUND_ENGINE_BIN` enables an optional local retrieval CLI adapter. If unset,
  Compound uses the no-op engine and still runs from its ledger.
- `COMPOUND_AUTHORITY_REGISTRY` points `compound authority import` at a registry.
- `COMPOUND_POLICY_DECISIONS_DIR` points `compound decisions import` at reusable
  policy decision files.
- `COMPOUND_OPERATOR_DECISIONS_GLOB` points `compound decisions import` at an
  operator decision ledger pattern.

Private data, private paths, and private retrieval engines belong in an overlay
outside the public repository.

## Status

- Memory layer: object model, append-only ledger, whole-session distillation,
  content-based scope resolver, query planner, promotion gate, positions,
  patterns, timeline, pack, and reconciliation.
- Authority layer: imports configured registry rows into the same Claim lifecycle.
- Decisions layer: imports configured policy and operator decisions into one
  kind-separated store.
- Next proof: synthetic end-to-end fixture that shows source -> evidence ->
  candidate claim -> promoted claim -> position -> context pack -> supersession
  -> updated context pack without any private overlay.

## Provenance

Compound is dogfooded against a private reference system, but that system is not
the product. Public Compound must run with synthetic fixtures and explicit overlay
inputs, without inheriting private data, paths, accounts, or doctrine.

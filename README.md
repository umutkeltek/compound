# compound

> Local-first, agent-agnostic substrate for preserving, governing, and reusing what a
> person or team knows, decides, and does — own the meaning, rent the engines.

Compound is opinionated: evidence is not canon, search is not truth, a mutation requires a
recorded decision, and current knowledge must be placed, cited, promoted, and superseded.

## What compound is (envelope-first)

Compound is built **envelope-first, not memory-first**: one typed runtime spine that every
harness, hook, and run flows through, hung off one root `work_id`. Memory is one slice *on top of*
that spine — not the system boundary. It is a local monolith information kernel with deep internal
modules and adapters at the edges, not a microservice estate and not an agent framework.

The proven foundation (P0-P6 first slice, all tested):

| command | what it does |
|---|---|
| `compound home init/doctor/where` | the `COMPOUND_HOME` runtime body — generated state, never in repo source |
| `compound envelope validate/kinds` | the typed envelope spine (9 envelopes + universal header) |
| `compound contract compile` · `compound orient` | EffectiveContract → ContextPack; the same task differs by repo kind, and records what was excluded |
| `compound work create/show/list/claim/advance/close/act` | one `work_id` threads intent → context → action → policy → receipt → transition |
| `compound policy evaluate` | governance v0: ActionIntent → PolicyDecision (allow / allow_with_obligations / hold / deny), enforced at adapters |
| `compound hook <name>` · `compound harness list` | capability-aware hook fixture loop + the HarnessRegistry |
| `compound memory candidate/adjudicate/recall/timeline/doctor` | P6 first slice: candidate-first memory ledger, MVCC-by-fold, supersession edges, as-of recall, hop-specific doctor |

Start with `CONTEXT.md` (glossary), `docs/architecture/{substrate,runtime,transition}.md`, and
`docs/roadmap/phases.md`. Architecture decision: `docs/adr/0001-local-monolith-information-kernel.md`.

## Memory (P6)

Memory is now started as a spine-bound slice, not the system boundary:

- an OWNED ledger is authority — Evidence → Claim → Position → Pattern, with supersession/contradiction edges (delta-aware: it remembers what a fact *was* and what it became);
- engines (vector/embedding/search indexes) are **rented** behind a swappable `MemoryEngine` (Query/Recall/Put) and never own visibility, promotion, or supersession;
- recall is **relevance-gated** — hard filters (visibility/retired/scope/time) run before engine breadth;
- incoming knowledge is **adjudicated** (reject | park | promote), candidate-first — nothing is piped straight to canon.

Architecture: `docs/memory-architecture.md`.

### Legacy / prototype memory commands

These predate the envelope spine and will be **re-bound through P6** (candidate → claim → transition →
relevance-gated recall). They run, but are not yet the proven path:

```text
compound distill <session.jsonl> [--ingest]   # whole-session distillation -> candidate claims
compound scope <session.jsonl> [--explain]    # content-based scope, not cwd
compound recall "<query>"                     # active positions + claims + optional engine breadth
compound promote <claim-id> --basis=<basis>   # candidate -> promoted
compound build | reconcile | timeline | pack  # derive positions, supersede, evolve, assemble a pack
compound authority import | decisions import | ledger stat | version
```

The distiller emits evidence and candidate claims; it cannot make canon by itself.

## Layers

Compound separates six concerns, each absorbed into the same object lifecycle rather than becoming a
new service or scattered folder (`docs/LAYERS.md`):

- **Memory** — what is known.
- **Authority** — what is currently true about external things.
- **Decisions** — what has been committed to.
- **Procedures** — how repeatable work is done.
- **Permissions** — what actors are allowed to do.
- **Doctrine** — durable principles and preferences.

## Layout

```text
cmd/compound/       CLI, one local binary boundary
internal/home/      COMPOUND_HOME runtime body (P0)
internal/envelope/  the typed runtime grammar — 9 envelopes + universal header (P1)
internal/contract/  EffectiveContract compiler + ContextPack assembler (P2)
internal/work/      WorkEnvelope lifecycle — one work_id, claim-before-touch (P3)
internal/policy/    ActionIntent -> PolicyDecision, enforced at adapters (P4)
internal/harness/   the HarnessRegistry — per-harness capabilities + limits (P5)
internal/hook/      capability-aware hook fixture loop (P5)
internal/memory/    owned object model: Evidence -> Claim -> Position -> Pattern (legacy/P6)
internal/ledger/    append-only canonical store for derived knowledge objects
internal/engine/    optional retrieval adapters behind MemoryEngine
internal/scope/     cwd-independent attribution
internal/recall/    query planner and visibility filters
docs/               architecture notes, contracts, roadmap, ADRs
fixtures/           public, leak-free envelope / action-intent / hook fixtures
prototype-ts/       historical prototype reference while the Go implementation matures
```

## Configuration seams

The public core has no private path defaults. Private data, paths, and engines live in an overlay
outside the public repository.

- `COMPOUND_HOME` — the runtime body (generated state). Resolution: `--home` → `$COMPOUND_HOME` → nearest
  ancestor `.compound/manifest.yaml` → `~/.compound`.
- `COMPOUND_LEDGER` — legacy ledger root (`~/.compound/ledger`); to be derived from `COMPOUND_HOME`.
- `COMPOUND_ENGINE_BIN` — optional local retrieval CLI adapter; unset → the no-op engine (still runs from the ledger).
- `COMPOUND_AUTHORITY_REGISTRY` / `COMPOUND_POLICY_DECISIONS_DIR` / `COMPOUND_OPERATOR_DECISIONS_GLOB` — legacy importers.

## Status

- **Proven spine (P0-P6 first slice):** COMPOUND_HOME runtime body; typed envelope grammar; EffectiveContract +
  ContextPack orientation; WorkEnvelope lifecycle on one `work_id`; ActionIntent -> PolicyDecision
  enforced at adapters; capability-aware hook loop + HarnessRegistry; candidate-first memory ledger with
  as-of recall and hop-specific doctor. All tested; generated state lives under `COMPOUND_HOME`, never in repo source.
- **Remaining P6:** broaden recall beyond exact entity matches, add engine breadth, structural patterns,
  computed temperature, and other candidate kinds.
- Legacy memory / authority / decisions commands (above) exist and will be **migrated, not deleted**, as P6 lands.

## Provenance

Compound is dogfooded against a private reference system, but that system is not the product. Public
Compound must run with synthetic fixtures and explicit overlay inputs, without inheriting private data,
paths, accounts, or doctrine.

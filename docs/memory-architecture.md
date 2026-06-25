# Memory Architecture

Memory's differentiator is not storage or summarization. Distillation produces
candidates; memory decides what survives, why it survives, and how it is recalled.

The moat is:

```text
deterministic scope
+ temporal authority
+ promotion discipline
+ typed recall
+ evidence-backed assembly
```

## Object Model

`Entity` is the routing backbone. Typed `Edge` records evolution. `Claim` is the
smallest promotable durable memory object.

- **EvidenceItem**: immutable, citable raw material. Evidence supports claims; it
  is never truth by itself.
- **Entity**: routed thing such as a project, product, repo, server, person,
  concept, doctrine, or tool.
- **Claim**: smallest durable statement Compound can reason about. A claim can be
  candidate, promoted, superseded, retired, or rejected.
- **Position**: current stance for an entity/topic, derived from active claims.
  Current truth is a position, not the newest note or highest-ranked search hit.
- **Pattern**: repeated through-line across claims or positions.
- **Edge**: relation such as supersedes, contradicts, refines, supports, mentions,
  owns, or part_of.

## Ledger Is Authority; Engine Is View

The append-only ledger owns canonical derived knowledge objects:

```text
evidence.jsonl
entities.jsonl
claims.jsonl
positions.jsonl
patterns.jsonl
edges.jsonl
scope_assignments.jsonl
```

Retrieval engines are rebuildable views. They may own indexes, ranking, embeddings,
and low-level query execution, but they must not own visibility, promotion,
supersession, source identity, or canonical IDs.

## Scope Resolution

Compound must not trust cwd. Symlinks, shells, agent launchers, and generated
workspaces make cwd a weak operational hint.

Scope is resolved from deterministic signals:

1. Explicit identifiers.
2. Registered aliases.
3. Source metadata.
4. Human decision-turn mentions.
5. Agent mentions.
6. Session title.
7. Cwd only as a future weak hint, never as authority.

When attribution is unclear, Compound should return `unresolved` or `multi_scope`
instead of guessing.

## Recall Planning

Recall starts with hard filters before any engine breadth:

- visibility;
- permissions;
- source trust;
- retired/superseded status;
- explicit time bounds.

Soft ranking can then use entity, topic, source, confidence, recency, and last
retrieved signals.

Modes:

- entity-anchored precision;
- hybrid breadth through an optional engine;
- temporal evolution through positions and supersession edges;
- conservative ambient injection from operational/world-visible promoted memory.

Raw evidence and private quotes are not ambient by default.

## Nine Questions

Every durable object should answer:

```text
what was claimed
who said it
when it was true
which entity it is about
why it is believed
how it can be cited
whether it is active/superseded/retired/candidate
whether it is safe to recall ambiently
when it was last useful
```

## Language And Runtime

The core is Go: CLI, ledger, scope, recall planning, importers, and future local
daemon. TypeScript remains useful for UI and historical prototypes. Python should
stay at ML edges when needed.

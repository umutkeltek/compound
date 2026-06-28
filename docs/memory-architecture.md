# Memory Architecture

> Scope note: in the substrate, **memory is one slice, not the whole system** — see
> `architecture/substrate.md` (one meaning owner) and `architecture/transition.md` (the apex object is
> `Transition`, not a memory record). This doc details the memory slice (built at roadmap **P6**); promoted
> claims/positions are durable ledger authority, not rebuildable views.

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

## Memory Health Is Per-Transition

A memory miss is a *failed transition*, not just "missing memory." A health check (`memory doctor`,
P6) reports WHICH hop failed, because the fix differs per hop:

```text
source capture → promotion → sync → embedding → retrieval → authority framing
```

The recurring legacy-index anchor drift was a failed `promotion/sync → retrieval` hop — curated
facts never became retrieval facts — not absent memory. Reporting "memory missing" sends the fix to
the wrong owner; reporting the failed hop sends it to the right one.

## Distillation Is A Transition Format, Not A Memory Format

Mining raw generated artifacts (session scratch, product-repo outputs) must not turn scratch into
canon. The discipline, learned under execution pressure:

- **Classify metadata before reading content.** Path/owner/size metadata routes most artifacts into
  review lanes (promote-candidate, durable-mining, stale-compare, source-control, archive/ignore)
  *without* opening their bodies — content is read only inside a bounded owner step.
- **Emit bounded snippets, not copies.** Mining output is hashes + owner targets + short snippets +
  `no_promote` rows — enough to route the next move, not enough to make the source's scratch into
  canon. The source stays source.
- **Owner writeback is a gate, not a write.** Before any canon mutation, a writeback gate maps each
  candidate to its current owner surface, proves upstream status unchanged, and names the later apply
  step as the *only* write path. Canon promotion happens at the owner, never in the mining artifact.
- **Checkpoint long mines.** A rolling distillation checkpoints evidence at each bounded stage and
  supports finalize/resume from those checkpoints — never re-pay upstream chunk calls when a late
  synthesis step fails or exceeds context.

## Language And Runtime

The core is Go: CLI, ledger, scope, recall planning, importers, and future local
daemon. TypeScript remains useful for UI and historical prototypes. Python should
stay at ML edges when needed.

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
  owns, or part_of. Relation hints are structural ledger edges, not a `relation`
  candidate kind or inbox.

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

## Adopted Patterns (external-systems synthesis, 2026-06-28)

A blind audit of eight memory/storage systems (mem0, mempalace, agentmemory, OpenViking, tidb,
turbovec, a Postgres brain-layer, graphify/codegraph) converged on a small set of mechanisms. They validated
this architecture (owned ledger + rented engine + relevance-gated recall + candidate-first adjudication)
and sharpen it. The patterns, by the axis they improve:

**ALIVE (delta / temporal) — MVCC-by-fold.** The append-only ledger carries a monotonic `seq` (a logical
version clock). A supersession edge *is* the version chain; a claim/edge carries `valid_from` / `valid_to`
(`current = valid_to is null`) and supersedes by stamping `valid_to`, never by deleting. "As of T" recall
is a deterministic **fold** of the ledger bounded by `seq ≤ T`; "what changed from X to Y" is fold-to-X vs
fold-to-Y diffed. Compaction is a periodic **materialized checkpoint snapshot** of folded Positions/Patterns
at `seq N` — so `now` reads are fast — but claims are **never garbage-collected**: history is the product.

**ACCURATE — provenance everywhere, contradiction over deletion.** Every Evidence→Claim and Claim→Claim
edge is tagged with how it was derived (`deterministic | extracted | inferred | ambiguous`) and a
confidence; `ambiguous` auto-surfaces for review, and unresolved claims sit in a queue awaiting evidence.
A contradicted claim gets a **contradiction edge** and supersession, never a hard delete (retire by
visibility). Adjudication is **LLM-extracts / code-decides**: the model proposes facts only — it may not
update or delete — with relative dates resolved to absolute at extraction (observation date ≠ current date),
and existing claim ids remapped to small integers before the model sees them (it cannot invent ids).

**SMART — task-shaped recall, not flat top-k.** Hard filters (visibility/retired/scope/time) run before any
engine breadth. Recall then seed-matches, walks **bounded typed edges** (BFS/DFS) around the seeds, and
returns a **token-budget-trimmed subgraph** plus a **coverage verdict** — on a gap it names the missing
claim rather than fabricating a trace. Patterns are computed **structurally** (high-degree claims =
load-bearing positions; high-betweenness cross-cluster bridges = contradiction/cross-domain candidates),
no LLM required. Temperature is a **derived** score (`salience·decay + reinforcement-on-access`), not a
hand-assigned tier, with `salience` driven by evidence-count and position-strength.

**EFFICIENT — the engine rents, and is cheap.** The `MemoryEngine` advertises a `capabilities` set
(unsupported ops raise, never silently no-op) and holds only vectors/ids/metadata — zero authoritative
state. Vector rows are stamped `<provider:model>:<dims>` + a corpus-generation hash, so a model/prompt/dim
swap is detected as stale and only drifted rows re-embed; opening a store with a different embedder is a
hard error. Claims carry tiered summaries (abstract / overview / detail) so recall returns the cheap tier
and drills down on demand. The default engine is a no-op/local one (the ledger alone answers exact and
delta queries); a quantized ANN engine (turbovec) is the breadth `Query` adapter — its allowlist is honored
in-kernel, so "hard filters before breadth" stays cheap rather than a recall-losing post-filter.

**Mechanical invariants (so the patterns are true, not just phrases).** `seq` is allocated under an
exclusive advisory file lock (`COMPOUND_HOME/lock`) that wraps the prior-lookup + seq-bump + claim/edge
append — so monotonic-unique holds even across concurrent writers (single-writer is the norm; the lock
makes it correct), and the lock releases on fd close so a crash can't wedge the ledger. An engine that
lacks a capability returns an explicit `ErrUnsupported` — never a silent `nil` — so a missing index can
never masquerade as "no results"; the default no-op engine raises, and recall stays answerable from the
ledger alone.

> Rationale, per-system attribution, the turbovec integration decision, and the rejected anti-patterns
> (LLM-as-memory-manager, lexical contradiction, post-engine filtering, graph-RAG, hyperscale machinery)
> are recorded in the private audit synthesis. Engines stay swappable; the typed Claim/Position/Pattern
> edge graph with provenance is the part compound owns.

## The breadth-engine protocol (how a real ANN engine plugs in)

The default engine is in-process and lexical; a real semantic engine (turbovec) is rented as a
**sidecar** behind one compound-owned wire protocol — chosen over cgo because cgo can't be tested
without the native toolchain and shares a crash domain with the core, while a sidecar keeps the
core a single pure-Go binary and the engine genuinely swappable.

- **compound owns the protocol; the engine is rented behind it.** Newline-framed JSON, schema-
  versioned, on stdout (logs to stderr only): `capabilities` · `index` · `query` · typed error
  envelope. Defined in `internal/mem/protocol.go`; the pure-Go reference engine
  (`RunReferenceEngine`, shipped as `compound-mock-engine`) is the executable conformance baseline
  the real turbovec sidecar must match — so the whole adapter is proven with **zero Rust**.
- **Transport-pluggable.** A `Transport` interface carries one round-trip; the default is a **held
  coprocess** (spawn once, keep the index warm — no reload per query), with a per-op exec and a
  socket as alternate impls behind the same client.
- **The embedder lives in the sidecar.** Only claim *text* crosses the wire; the core never holds
  model weights or serializes vectors, and stays a single cross-compilable binary.
- **The relevance gate crosses explicitly.** The ledger computes the allowlist and sends it under
  `gate:list`; an empty allowlist matches **nothing** (fail-closed — it can never leak retired or
  superseded claims). `gate:all` is admin-only and the core never emits it.
- **Index is idempotent on the corpus generation** (= ledger max seq), so the engine never
  re-embeds the whole corpus per query.
- **Engine ownership is mechanically checked.** `compound memory engine-doctor <query>` hashes
  `COMPOUND_HOME/ledger` before/after a breadth query, reports engine/ledger generation, and fails if
  a rented engine mutates owned ledger bytes.
- **Configured engine health is part of the normal memory chain.** `compound memory doctor` leaves
  `sync/embedding` skipped when no sidecar is configured; when `COMPOUND_MEM_ENGINE_BIN` is set, the
  same hop probes the configured sidecar, reports engine generation/hits and ledger immutability, and
  fails degraded/unhealthy configured engines instead of treating them as absent.
- **Engine swap parity mechanically checked.** `compound memory engine-compare <query>` requires
`COMPOUND_MEM_ENGINE_BIN`, runs the owned lexical reference and configured sidecar over the same
ledger-gated query, and fails if ledger bytes change. Exact mode fails if hydrated claim ids diverge.
`--allow-expansion` is explicit for semantic/legacy-memory sidecars: every reference hit must remain
covered, degraded sidecars still fail, and extra allowlisted current claims are reported as
`expansion`, not `match` or authority. This is the acceptance gate for legacy/vector sidecar adapters
before they are trusted as rented breadth.
- **Identity guard, fail-closed.** compound records the embedder it indexed with; if the sidecar
  reports a different one, breadth is **refused** (`ErrEmbedderMismatch`), never silently degraded —
  a silent model swap would wreck recall invisibly. A down sidecar degrades to `ErrEngineUnavailable`
  (the ledger still answers exact/as-of); the two are distinct on purpose.

Config (overlay): `COMPOUND_MEM_ENGINE_BIN` (the sidecar command) and
`COMPOUND_MEM_ENGINE_EMBEDDER` (the indexed-with stamp). Older `COMPOUND_ENGINE_BIN`
adapters speak a different protocol and are not part of the spine memory engine seam.
`compound-memory-cli-engine` is a read-only external memory CLI sidecar: it uses a
configured query command as expansion context over claim text that compound just
indexed, returns only allowed claim ids, maps a broken backend to `ErrEngineUnavailable`,
and rejects mutating command templates before the external CLI is invoked. The real
`COMPOUND_MEMORY_CLI_PROFILE` is an opt-in profile selector for built-in
read-only local memory CLI presets. Bin, template, and embedder remain explicit
environment overrides, so these profiles are adapter convenience rather than spine
dependencies. A real vector sidecar
(Rust plus its embedder) is an **external artifact**, never vendored
into the public core.

## Language And Runtime

The core is Go: CLI, ledger, scope, recall planning, importers, and future local
daemon. TypeScript remains useful for UI and historical prototypes. Python should
stay at ML edges when needed.

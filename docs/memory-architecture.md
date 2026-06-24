# Memory architecture (canonical)

Converged from three sources that agree more than they disagree:
- **Umut's own doctrine** — `~/brain/operating-system/recall-system-ground-truth.md`, `memory-corpus-contract.md`.
- **Oracle / GPT-5.5-Pro consult** — 2026-06-24, `~/.oracle/sessions/compound-memory-arch/artifacts/transcript.md`.
- **mempalace** (github.com/mempalace/mempalace) — a working local-first memory product, used as a reference.

## The moat (read this first)

Memory's differentiator is **not** distillation. Distillation produces candidates;
**memory decides what survives; recall proves it was worth saving.** The moat is:
`deterministic scope · temporal authority · promotion discipline · typed recall · evidence-backed assembly`.

## Object model — Evidence → Claim → Position → Pattern

`Entity` is the routing backbone; typed `Edge`s carry evolution. Claim is the atom — *not*
page/chunk/fact (chunk is storage-shaped, page is human-shaped, fact is too vague; a Claim is
memory-shaped). But don't let every extracted thing become a Claim — that's "a beautiful landfill."

- **EvidenceItem** — immutable, citable raw material. `{source_kind, source_id, session_id?, turn_id?, quote_text?, content_hash, event_time, observed_at, actor, visibility, provenance}`. Evidence is material, never "truth."
- **Entity** — routing backbone. `{slug, type(project|product|repo|server|person|concept|doctrine|tool), aliases[], identifiers[](repo URL, bundle id, domain, path prefix), parent_slug?, visibility_default}`. A **Concept is a promoted Entity** — not every embedding cluster (that's semantic fog).
- **Claim** — smallest promotable durable memory. `{subject_entity_slug, predicate, object, claim_type(preference|decision|architecture|status|constraint), effective_from/to, event_time, confidence, visibility, status(candidate|promoted|superseded|retired|rejected), evidence_refs[], promotion_basis}`. **The distiller emits CANDIDATES; it may not canonize.** Promotion needs a gate: explicit confirmation · curated page · decision record · runtime proof. (This is the "evidence ≠ canon" law, enforced structurally.)
- **Position** — the current durable stance that can evolve. `{entity_slug, topic_slug, active_claim_id, claim_chain[], status}`. Current truth is *not* found by sorting claims by date and praying.
- **Pattern** — a promoted aggregate with support, *not* first-class canon in v1. "Across 17 claims and 43 evidence items, the owner repeatedly rejects generic-agency framing." That is memory; "the model found a theme" is not.
- **Edge** — relational edge table `{from, from_type, edge_type(supersedes|contradicts|refines|supports|mentions|owns|part_of), to, to_type, effective_from/to, evidence_refs[]}`. **Relational graph, not a graph database.** Add a graph DB only when relational edges demonstrably can't answer a real query.

## The ledger is the authority; the engine is a materialized view

Append-only canonical **ledger** on disk (`/compound-ledger/{evidence,entities,claims,positions,edges,scope_assignments}/*.jsonl`). The rented engine is rebuildable from it: `compound rebuild --from ./compound-ledger --to {sqlite|postgres|future}`.

- **Owned layer owns:** canonical IDs, object schema, event-time semantics, visibility gates, promotion rules, entity + scope resolvers, claim lifecycle, distillation contracts, temporal conflict logic, the query planner, citation format, golden retrieval tests, canonical export/import.
- **Rented engine owns:** physical storage, vector index, keyword index, embedding storage, ranking impl, projections, low-level query execution.
- **Adapter leak points to design around:** filter-before-vs-after-rank, hybrid-ranking semantics, embedding model identity (dim/tokenizer/version), graph traversal, idempotent/restartable ingestion, tombstones (retire, don't delete), ordering determinism (testable top-N), access control. **Visibility enforcement is never delegated to the engine.**

## Scope resolution — the cwd problem, solved

Don't trust cwd (symlinks, compaction, sub-shells make it a weak prior). Don't "tag sessions" —
emit **ScopeAssignments with evidence**. Pipeline:
1. **Entity Registry** (seed: slug + aliases + identifiers per project/product/server). Without it, semantic search becomes your fake OS.
2. **Deterministic signals**, ranked: explicit markers (repo URL, bundle id, domain) > files touched / git remote > human decision-turn mentions > assistant mentions > session title > **cwd (weak prior)**.
3. **Boring weighted scorer** (not ML): explicit marker +100, exact identifier +90, git remote +80, file under registered path +70, alias in decision-turn +50, … cwd +10, generic-alias collision −40. `primary_about` only if top ≥100 and beats 2nd by ≥40; else `multi_scope` or `unresolved`. **Unresolved is healthier than wrong canon.**
4. **Scope Packet** — a compact JSON artifact per session, part of the evidence chain.
5. **Human decision-turns outrank tool noise.** LLMs may *propose* scope; the resolver accepts only when deterministic signals back it.

## Recall — a query planner, not one mode

`Query → Planner → hard filters → candidate set → semantic/keyword rank → graph/temporal expansion → assembly with citations`.
- **Hard filters (always, before retrieval, outside the engine):** visibility, permissions, source-trust, retired status, explicit time bounds.
- **Soft filters (bias ranking):** entity, topic, source harness, confidence, recency, last_retrieved.
- **Modes:** A entity-anchored (precision) · B hybrid corpus (breadth) · C temporal evolution (how did this change — where Claim beats chunk) · D ambient injection (conservative: operational/world only, active Positions + high-confidence Claims, no raw private quotes). The planner returns an explicit plan object. This is the line between a memory system and "vector search with vibes."

## Every durable object answers 9 questions, or it's residue

What is claimed · who said it · when was it true · what entity · why believed · can it be cited ·
is it active/superseded/retired/candidate · safe to inject ambiently · when last useful.

## mempalace — what it validates, where compound differs

- **Validates:** structured-storage / unstructured-retrieval (Zettelkasten wings→rooms→drawers ≈ entity→topic→evidence); a **pluggable backend interface** (their `backends/base.py`, default ChromaDB) — same bet as our engine adapter; a two-tier **pointer→content** (their AAAK "closet" scannable shorthand → "drawer" verbatim ≈ our Claim → Evidence); **background, off-chat ingestion** via hooks; "file precisely, recall vaguely."
- **Differs:** mempalace stores **verbatim only — it deliberately does not distill, extract, or promote.** Compound adds the distillation + promotion + claim-evolution layer mempalace omits: it keeps everything as evidence but *decides what survives* as durable Claims. (Also: mempalace is Python; see language note.)

## Language — pending Umut's decision

Oracle's call: **Go** for the core (CLI/daemon/indexer), TypeScript/Bun for the prototype/UI/adapters,
Python only at the ML edge, no Rust until profiling forces it. Rationale: the heavy path is
file-walking / streaming / normalizing / dedup / LLM I/O / indexing / daemon — Go territory, not
matrix math; and Go is the lowest solo-maintenance, most boring-reliable choice for a distributable
local daemon. (Decision recorded separately once Umut chooses.)

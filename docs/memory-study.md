# Memory layer — study: gbrain + mempalace

Goal: build Memory **better** than gbrain by taking its good engineering and rejecting its
shortcuts. gbrain is the *rented engine*, not the model to copy.

## What to TAKE from gbrain

- **The Facts fence as system-of-record.** `## Facts` fenced tables on a page, with kind /
  confidence / valid_from / superseded_by / source. Clean, git-diffable, human-auditable. Compound
  writes these (`ledger-to-facts.ts`).
- **Hybrid retrieval.** RRF over vector + keyword, recency/salience boosts, graph signals. Good
  enough to rent for breadth — `engine.query()`.
- **Portable storage.** PGLite (zero-config) ↔ Postgres+pgvector parity. Worth preserving if/when
  compound owns its own engine.
- **Provenance fields on facts.** valid_from / valid_until / superseded_by / confidence / source —
  this is the right shape for evidence-not-truth.

## What to REJECT / do better (the whole reason compound exists)

- **Shallow "dream" synthesis.** gbrain distills by reading only the **first + last ~4K chars** of a
  conversation (`gbrain/src/core/cycle/synthesize.ts`). It rejected 99.9% of a real session as
  "meta-operational." Compound reads the **whole conversation**, map-reduce over a reasoning model,
  preserving verbatim quotes (`distill.ts`). This is the differentiator. ✅ proven.
- **Facts ineligibility surprises.** gbrain silently refuses fact extraction for non-allowlisted
  page types (`facts/eligibility.ts` — e.g. `type:decision-ledger` drops out). Compound owns the
  fence write and uses an eligible type; long-term, owning the engine removes this trap.
- **Async-only extraction.** Facts queue for a worker; recall isn't synchronous. Compound should be
  able to read its own fence directly (own the claim store), not wait on a backend job.
- **No first-class Claim / Concept / Pattern.** gbrain has pages + chunks + flat facts; no claim
  ancestry, no concept identity, no recurring-pattern objects, no temporal evolution DAG
  (appeared → mutated → retired). Compound's object model centers these — that's the deep layer.
- **Generic, no opinion.** gbrain is un-opinionated by design; that's why it disappoints. Compound
  is opinionated: read everything, evidence every node, evolve decisions, know the authorities.

## What to TAKE from mempalace (method of loci)

The memory-palace idea is not "store more" — it's **structure + deliberate cues**:

- **Loci = structure.** A palace works because each idea has a *place*. Compound's six layers are
  the loci: a fact about infra goes to Authority, a commitment to Decisions, a how-to to Procedures.
  Placement is what stops the fragmentation Umut named ("I know the system is there but can't
  specify it").
- **Cues = recall triggers.** A palace is walked along a route; recall is cue-driven, not a search
  box. Compound's recall should be **trigger-based** (this topic/intent → load this pack), echoing
  the IntentRecall nudge pattern — not "semantic search and hope."
- **Encoding effort = compression with meaning.** Loci force you to *encode*, not just dump. The
  distiller's verbatim-quote-preserving reasoning pass is that encoding step.

> If there is a specific `mempalace` repo to study, point me at it and I'll harvest concretely;
> this section is the method-level reading until then.

## Compound's Memory thesis (one line)

**Own deep, evidence-backed encoding (distill the whole thing → claims → context packs, every node
cited) and structured cue-based recall; rent storage and vector search.** Depth + evidence +
reusable output is the moat — exactly the three things gbrain's generic shortcut skips.

## Alive memory — what compound took from mem0 (+ gbrain, mempalace)

mem0's core is a **memory manager** that compares new facts against existing memory and emits
**ADD / UPDATE / DELETE / NONE** (`mem0/configs/prompts.py:DEFAULT_UPDATE_MEMORY_PROMPT`) — memory
evolves, it doesn't just accumulate. compound was append-only; now it has the same loop, mapped to
its own primitives and kept append-only (nothing destroyed, only marked):

- **NONE → deterministic dedup at ingest** (`distill.ToLedger`): a new candidate claim whose
  statement is ≥0.82 Jaccard-similar to an existing claim on the same (entity, topic) is dropped.
  Cheap, no LLM. Stops cross-session duplicate bloat (gbrain's no-dup, without the async).
- **UPDATE/DELETE → `compound reconcile`** (`internal/reconcile`): per (entity, topic) group with
  ≥2 live claims, an LLM decides which claims **supersede** or **duplicate** which; the older/dup
  claim is marked `superseded` with `EffectiveTo` set and a `supersedes` **edge** is appended — the
  same shape as gbrain's `superseded_by`/`valid_until`. Conservative by prompt; append-only.
- Superseded claims drop out of recall (hard filter) and out of a Position's active claim, but stay
  in the claim chain as history. "Current truth" = the surviving claim.

From **gbrain**: the temporal/supersession field shape (`valid_until`, `superseded_by`, confidence)
— compound sets these on supersession. From **mempalace**: the structured index (entity→topic→
evidence ≈ wings→rooms→drawers) compound already mirrors; reconcile operates *within* a topic, the
mempalace "room" boundary. Net: memory that adjudicates (reject/dedup/supersede/keep), not a pile.

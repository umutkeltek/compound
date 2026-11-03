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
  concept, topic, doctrine, or tool.
- **Claim**: smallest durable statement Compound can reason about. A claim can be
  candidate, promoted, superseded, retired, or rejected.
- **Position**: current stance for one fact slot (`entity + topic/domain +
  predicate + claim_type`), derived from promoted claims and
  supersession/review edges. For a decision carrying an explicit `decision_id`,
  that named identity is stronger than predicate: predicate aliases inside the
  same topic/domain intentionally share one slot. Topic remains the broader
  aggregation axis used by Patterns and exact-object echo detection; it is not
  version identity.
  Position rows are rebuildable materialized folds; claims and edges remain
  authority. Current truth is a position, not the newest note or highest-ranked
  search hit. Fold schema v0.2 stamps each row and ignores an older checkpoint
  until a global `compound memory fold` rebuilds it.
- **Pattern**: repeated through-line across claims or positions.
- **Edge**: relation such as supersedes, contradicts, refines, supports,
  mentions, owns, part_of, or narrows_to. Relation hints are structural ledger
  edges, not a `relation` candidate kind or inbox.

## Ledger Is Authority; Engine Is View

The append-only ledger owns canonical derived knowledge objects:

```text
evidence.jsonl
entities.jsonl
claims.jsonl
positions.jsonl
patterns.jsonl
edges.jsonl
```

Layered scope currently rides typed topic/entity edges such as `part_of` and
`narrows_to`; a separate `scope_assignments.jsonl` ledger is not implemented.
Claims may carry optional `topic_refs[]`. Rows that predate `topic_refs[]`
remain valid and recallable; topic-scoped recall simply excludes them when a
topic subtree filter is requested.

Topic narrowing now has a live work-path consumer, not just the `memory recall
--topic` / `memory position --topic` CLI. The ContextPack builder threads an
optional topic (`Input.Topic`, surfaced as `compound orient --topic <slug>` and
`hook.Fixture.topic`): when set, ambient recall resolves the topic subtree once
via `TopicSubtree` and keeps only claims/positions whose `topic_refs[]` intersect
it, using the same exported `mem.ClaimHasTopicRef` gate the recall/position paths
use. Out-of-topic claims that would otherwise have surfaced are recorded in the
pack's `excluded` list with a topic reason, and the pack summary stamps the
narrowing so the artifact shows recall was topic-scoped. An empty topic is a
guaranteed no-op: the gate is skipped, ids are byte-identical, and ambient recall
behaves exactly as before.

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

Raw evidence and private evidence quotes are not ambient by default. This is a
statement about evidence bodies, not about claim visibility: a promoted claim
with `visibility: private` still passes the orient ambient gate by design
(`memoryVisibilityAllowedForOrient` in `internal/contract`), subject to the
per-repo-kind `MemoryRule`, which can mark `personal-corpus`/`self-model`
claims explicit-only or forbidden.

Admission happens before operator review: deterministic source metadata
(`source=knowledge_import`) enters the source-index tier and knowledge extraction
outputs (`source=knowledge_extraction`) enter the annotated-evidence tier. Both
are parked, not `proposed` operator approval rows. Durable memory candidates,
decisions, principles, conflicts, and other canon-changing claims remain
`proposed` and operator-gated unless a later class-tier automation rule
explicitly proves a clean audit window.

Annotated knowledge-class candidates are evidence, not ambient memory. When a
panel verdict for a knowledge candidate (`knowledge:*` entity or
`source=knowledge_extraction`) asks for `needs_human` / `needs_trial`, Compound
records that panel finding as a candidate annotation and keeps the candidate
parked. Default recall remains promoted-claim-only; `memory recall <entity>
--include-evidence` may surface annotated candidates as opt-in evidence. A panel
`promote` verdict still requires a valid promotion basis and reviewed provenance;
annotation is not an adjudication verdict and never widens the memory verdict set.

The pending-adjudication sweep acts only on an explicit `proposed` state. It skips
records with a missing or blank state, including source-candidate records, without
rewriting them or freezing a promotion operation for them.

After recovery abandons a frozen promotion pass, the next pass validates that
terminal frame's provenance, frozen intent, planned summaries, recorded partial
results and any recorded finalization before planning fresh work from current
candidate generations.

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
claim rather than fabricating a trace. Runtime context uses the same rule: `ContextPack.active_memory[]` contains
only current `claim://...` refs from the owned ledger that match repo/task context and repo-kind memory policy.
Session-start hooks may render those compact claim lines, but rented source stores and MemoryEngine indexes remain
evidence/adapters, never authority. `compound memory candidates --json` is the read-only candidate inbox
door for renderers/agents: it reports counts and filtered candidate rows without evidence refs by default;
`--include-evidence` is the explicit gate. Patterns are computed **structurally** (high-degree claims =
load-bearing positions; high-betweenness cross-cluster bridges = contradiction/cross-domain candidates),
no LLM required. Temperature is a **derived** score (`salience·decay + reinforcement-on-access`), not a
hand-assigned tier, with `salience` driven by evidence-count and position-strength. Built as the
`compound://projections/memory-temperature` projection (`compound memory temperature --rebuild`): a
build-time, recency-decayed join of the capability-usage projection's injected/consumed/ignored signal
against current claims, fed into ambient recall as a bounded (±0.25) additive rank term. That
`ignored` requires an exact `skills.explicit_non_use` declaration bound to a work, actor,
and receipt proof. An absent transcript match, including a readable scan with no match,
is unknown and neutral. A transcript mention enters `skills.referenced`; only an explicit
application method may warm a claim. Historical `used_declared` alone supplies neither
negative nor positive claim evidence because its writer was ambiguous.
Capability usage itself is a rebuildable view: v0.2 joins task-scoped `ContextPack.capability_offers[]`
with bounded `PostToolUse` Skill metadata and later close receipts. Each immediate row carries capability,
harness/lane, work/session, trigger explanation, and outcome; per-capability `fired_count` / `last_fired`
become visible before SessionEnd without writing observations back into the capability authority.

Temperature RANKS; the **volatile** visibility EXPIRES. A volatile claim is the ladder's "useful now,
expires unless reinforced" made real: promoted and recallable under matching scope, never ambient, and
carrying a mandatory `expires_at` clock (a clockless volatile write is refused at every door; doctrine-class
claims — canon `operator_permanent` — may never be volatile at all). The `volatile` synthesis-sweep stage
reads the temperature projection as data: net consumption (consumed − ignored > 0) within one half-life
extends the clock to `last_injected+30d` (deterministic target + strictly-later guard = idempotent re-runs);
an expired, unreinforced volatile claim supersedes to a cooled version (`visibility: retired`,
`cooled_reason: volatile_expired_unreinforced`) — decay, never delete — with a receipt row carrying the
one-line undo, `compound memory volatile-restore <cooled_claim_id>`, which re-arms with a FRESH `now+30d`
clock so a restore never instantly re-cools. Gated-tier (product_fact/unclassified) expiries are carded for
the operator, not silently cooled: only low-risk classes are authorized for silent state change.

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
  a rented engine mutates owned ledger bytes. A generation-correlated owned-ledger advance during
  the probe gets one stable retry instead of being blamed on the engine; an engine generation truly
  ahead of the ledger is refused and is never rebuilt backward.
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
- **Bake-off misses are clinic evidence, not one undifferentiated failure.** `compound memory bakeoff
  run` records every miss as one of `absent_claim`, `held_by_review_gate`, `duplicate_twin`,
  `wrong_entity`, `synonym_alias_missing`, `semantic_lane_inactive`, `rank_miss`, `privacy_filter`,
  `stale_or_volatile_claim`, `expected_id_wrong`, or `noisy_claim_pollution`, then emits the measured
  top three with query/claim evidence refs and bounded owner fixes.
- **Identity guard, fail-closed.** compound records the embedder it indexed with; if the sidecar
  reports a different one, breadth is **refused** (`ErrEmbedderMismatch`), never silently degraded —
  a silent model swap would wreck recall invisibly. A down sidecar degrades to `ErrEngineUnavailable`
  (the ledger still answers exact/as-of); the two are distinct on purpose.

Config (overlay): `COMPOUND_HOME/config/memory-engine.yaml` can persist the default
rented sidecar profile, while `--engine-profile`, `COMPOUND_MEM_ENGINE_BIN`, and
`COMPOUND_MEM_ENGINE_EMBEDDER` stay explicit overrides. Older `COMPOUND_ENGINE_BIN`
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

## Target doctrine (operator-ratified 2026-07-09): quiet memory, conflict-only questions

The end-state this architecture serves, stated once so every agent aims at the same thing:
**memory should be so smooth the operator does not notice it, and the system may ask the
operator a question only when evidence genuinely conflicts.** The operator's decision load
must trend down over time; per-item "is this memory correct?" review is a design failure,
not a workflow. Four pillars, each with a deterministic floor so the whole layer works with
zero models available and gets richer — never merely functional — as models attach:

1. **Embedder-agnostic by structure.** Every embedder (local or hosted) is an identity-stamped
   adapter behind the engine seam — swappable, never authority, never load-bearing. Named
   defaults per lane are configuration (private lanes default local; hosted embedders are
   legitimate for non-private lanes). With no embedder at all, lexical + graph lanes still
   answer correctly. The identity guard refuses mixed-embedder indexes; an unreachable engine
   degrades, never breaks.
2. **Queries are enriched before retrieval.** A deterministic expansion stage (aliases and
   entity/term bridges mined from the capability graph and the ledger's own vocabulary) maps
   casual phrasing onto system terms — boost-never-gate, byte-identical when disabled. An
   optional local-model rewrite lane (vocabulary-constrained to the corpus's real tokens) may
   sit on top, behind the same fail-open discipline; it is never required for correctness.
3. **The system reconciles itself in the background.** Maintenance runs compare what memory
   claims against what receipts and transitions show actually happened; parked, un-corroborated
   candidates decay on schedule; corroborated ones raise themselves; usage warms what sessions
   actually consume and cools what they ignore. All of it acts through receipts with one-line
   undo — propose-only where a surface is governed, silent where evidence is decisive.
4. **The operator sees exactly two surfaces.** A weekly digest (what promoted, reopened,
   decayed, and how to undo any of it) and a conflict card that appears only when evidence is
   genuinely split — "in this project, in this context, memory holds A but evidence shows B;
   which representation is correct?" Everything else is the system's job.

The standing invariants are unchanged by this doctrine: the ledger gates and the engine ranks;
adjudication is the only birthplace of a claim; privacy classes never cross silently; and a
claim promoted by evidence reopens the moment evidence turns.

## Lessons and status (operator ruling 2026-11-03)

Eight collect -> propose -> review loops stopped at one review step that nobody
staffed: tens of thousands of parked candidates, four adjudications ever. The
learning that worked bypassed the machinery: the agent that tripped on a
problem wrote a dated why/how note and edited the skill it tripped on. It works
because the writer is the adjudicator and the consumer is known. The rules
below make that the governed path. Humans gate by setting this envelope, not by
being paged per item.

1. **Lessons promote on write.** `compound memory lesson --why W --how H --by A
   [--skill S] [--trigger TOOL:REGEX] [--evidence REF]... [--slot entity::predicate]`
   appends one claim with `claim_type: lesson`, `valid_from` = now,
   `promotion_basis: lesson_on_write`, and provenance qualifiers (`why`,
   `written_by`, `written_on`, `harness`, `skill`, `trigger`). It emits the
   usual `memory_delta` Transition. No candidate, no adjudication step. Why,
   how and actor are required; each text is one line (detail goes behind
   `--evidence`). `lesson` is not in the candidate `ClaimTypes`, and
   `lesson_on_write` is not a candidate promotion basis, so no other path can
   mint a lesson.
   - **Slot.** Default slot is `skill:<name>` (or `lessons`) `::
     lesson.<hash of the normalized why>`; pass `--slot` to target an existing
     slot on purpose. Positions read `<entity>::lesson::slot-<hash>`.
   - **Skill line.** With `--skill`, the door resolves `<repo>/skills/<name>/SKILL.md`,
     then each `--skills-dir`, then each dir in `$COMPOUND_SKILLS_DIRS`, and
     appends one dated line (why + how + claim id + trigger) to the file's
     `## Lessons` section (created at the end when absent). Append-only and
     idempotent by claim id. An unresolved skill refuses before any ledger write.
   - **Trigger format** (consumed by the tool-call hook): `<tool>:<regex>`.
     `<tool>` is the harness tool name (`Bash`, `Edit`, `Write`, `Read`, ...)
     or `*`; it may not contain `:`. `<regex>` is Go RE2, matched against the
     tool call's primary input (the command for `Bash`, the file path for
     file tools). Example: `Bash:proof-check.*\|.*jq`. `mem.LessonTrigger.Matches`
     is the reference matcher.
2. **Status never promotes.** Machine status (`claim_type: status`: git heads
   and counts, deploy hosts, run output) is state, not a lesson. The operator
   adjudicate door, the explicit-confirmation door and the governed promote
   driver refuse a status promotion with `ErrStatusNeverPromotes`; the driver
   lists such rows under `status_refused` and leaves them untouched, and the
   source-control import (`compound authority git`) reports status values but
   no longer captures or promotes them. Existing status claims are history and
   are not rewritten. Not yet covered: the unstamped `Adjudicate` door still
   accepts status from the legacy machine-state importers (authority registry,
   path authority, product signals, runtime-proof fixtures); moving those to a
   state projection is a separate change.
3. **Contradictions park.** A lesson whose slot already holds a current lesson
   with a different `how` keeps both. Each gets a `needs_review` edge naming the
   other (`contested_by:<id>`), the position fold serves the newest with status
   `contested`, and default recall holds both until resolved
   (`--include-needs-review` shows them). The same `how` again is a duplicate
   and writes nothing.
4. **The next consumer supersedes.** `compound work close --used <claim_id>`
   (or `lesson:<claim_id>`) records a use; when the used lesson is contested,
   the used lesson wins: a `supersedes` edge from it to each contesting lesson
   in the slot, and `needs_review_cleared` edges on both sides.
5. **Automatic producers are opt-in.** The automatic candidate writers run only
   with `COMPOUND_CANDIDATE_PRODUCERS=1`: knowledge import metadata rows and
   knowledge extraction (which then skips the model call), session-mining
   apply, the distiller fan-out, observation review candidates, and the
   work-close side effects (surprise, EV-card calibration, standing-proof
   graduation). Explicit paths always work: `compound capture`,
   `compound memory candidate`, `compound memory lesson`. A skipped write says
   so (`producer_note` or a warning), never silently.

## Language And Runtime

The core is Go: CLI, ledger, scope, recall planning, importers, and future local
daemon. TypeScript remains useful for UI and historical prototypes. Python should
stay at ML edges when needed.


## Explicit recall and automatic source capture

`compound memory search "a question"` searches configured source indexes without
a lane or path. The typed report separates adjudicated current claims from
retrieved source evidence. Current claims use the same owned BM25/vector ranker and gated alias expansion
as the existing memory engine. Recall reads stamped vectors without writing or
embedding missing documents; an incomplete cache reports vector degradation and
retains lexical retrieval. Source candidates use configured embeddings. Scores are compared only within
one embedding identity; different identities are interleaved by rank. One best
chunk per distinct body plus bounded adjacent context prevents duplicate exports
from taking the whole budget. `--history` includes superseded source versions.
A source's latest snapshot is not an adjudication of the statements inside it.
Cross-source search ranks and deduplicates candidates before reading their bodies.
Only selected evidence receives source text and adjacent chunks. Before those reads,
the consumer rechecks the current source manifest and privacy classification;
a changed classification cannot disclose private text or private file-derived titles.
The existing query deadline and body integrity checks remain in force.

Claim excerpts and retrieved source text share a 16,000-rune context budget.
Truncation and omitted adjacent context are explicit; use the cited claim/source
or the lane-specific query to inspect the full evidence.

Private snippets remain suppressed unless the caller passes
`--include-private-text`. Source text never enters ambient ContextPacks through
this feature. Existing claim promotion and personal-corpus recall gates still
apply. An empty result is not proof that a conversation never happened: the
report names every configured lane and its coverage state. Missing indexes,
failed sources, incomplete capture, unfinished backfill and stale refreshes are
visible. Similarity candidates do not assert that an answer is supported.

For automatic capture, supply an approved `corpus_lanes` configuration to
`compound-memory-service setup --binary /absolute/bin/compound --home
/absolute/compound-home --workspace /absolute/project --config sources.json`
inside an authorized `work advance --execute` setup action. The source installer
ships this helper; it does not silently enable capture. Each lane declares a
private, local embedder and `source: {root: /absolute/approved/root, format:
markdown|codex|claude}`. JSONL conversations preserve original bytes in source
custody. The derived retrieval view retains user/assistant text and record
attribution while excluding tools, reasoning and known harness injections.
Compressed archives and unlisted formats are outside this connector's coverage.

Setup creates a dedicated maintenance WorkEnvelope through native commands.
Existing live PostToolUse and SessionEnd hooks request background
`work advance --execute --resume-verifying` refreshes. The native execution rail
owns each action, policy decision, attempt and failure; a held or foreign-owned
work item is not taken over. Normal child failures return to verifying and can
retry. Abrupt host/process death during a running reservation remains a visible
stop requiring governed recovery; an audit sweep is not treated as proof that
execution is quiescent. Persisted snapshots and indexes remain readable. No new OS scheduler is installed. Ordinary hooks
throttle refresh to five minutes; session end can refresh a newly changed
transcript inside an approved root. File changes while no harness is active are
picked up at the next eligible hook. Source indexing never runs on the prompt
injection path.

Each capture pass selects recent changes, up to 128 files and normally 128 MiB;
one file may exceed the batch byte budget but cannot exceed 1 GiB. Oversized,
missing, changing and incomplete files are reported, not called complete.
Unfinished JSONL records never enter the owned version chain.
A changed file of 1 MiB or more earns a new owned version only after it has grown
by a quarter or stayed quiet for an hour, and is reported as pending until then;
its last owned version stays readable. Each version is a whole exact body, so on
APFS capture clones it: versions of an append-only transcript share their blocks
with each other and with the source. Size tools count every clone in full, so read
free space, not `du`, to see what a version costs.
Automatic refresh explicitly opts into partial capture: it refuses symlinked source
files individually, records their count as `refused_symlinks`, and captures eligible
regular files. After successful indexing, coverage stays `capture_incomplete` while
refusals exist; indexing failures keep their own status. It never
follows links, including outside the approved root; the strict directory and default
batch APIs still refuse the whole batch. A linked root itself remains invalid.
Subsequent passes resume historical backfill. Unchanged-file detection uses size and nanosecond
mtime and reports that basis; it is not a full disk-integrity audit.
Owned snapshots survive source removal. A return to earlier bytes creates a new
observed occurrence rather than reviving a superseded version.

The full-flow CLI test initializes a fresh home, uses normal setup, writes a
conversation, invokes Codex PostToolUse, recalls from a new process, then invokes
Claude SessionEnd after a correction and verifies both latest and history.
It performs no manual indexing or ledger repair between those steps. The
synthetic embedder proves wiring and privacy, not natural-language recall quality;
real-history evaluation is a separate acceptance check.

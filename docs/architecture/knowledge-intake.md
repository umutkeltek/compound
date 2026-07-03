# Knowledge intake — connect any medium to the meaning ledger

One generic door turns captured content from ANY medium (papers, bookmarks, articles,
transcripts, feeds, notes) into adjudicable meaning. The contract is deliberately split
in two so instances never leak into the substrate:

- **The core owns the contract**: envelope validation, evidence recording, candidate
  proposal, echo folding, source registry, idempotency. It is medium-agnostic; no medium
  name appears in core logic. Guarantee, enforced by test: **a new medium = one capture
  edge + zero core changes.**
- **Edges own capture**: a capture edge is any script/tool/service — any language,
  anywhere — that emits the export envelope below. Edges are rented and replaceable;
  they make no meaning decisions.

## The envelope: `compound.knowledge_export.v0.1`

```json
{
  "schema_version": "compound.knowledge_export.v0.1",
  "source": {"kind": "arxiv", "ref": "arxiv:cs.AI+cs.CL", "captured_at": "2026-07-02T12:00:00Z"},
  "privacy": "public",
  "producer": {"kind": "edge-script", "name": "arxiv-capture", "version": "0.1"},
  "items": [{
    "ref": "arxiv:2506.12345v1",
    "url": "https://arxiv.org/abs/2506.12345",
    "title": "…",
    "authors": ["…"],
    "published_at": "2026-06-30",
    "topics": ["cs.AI"],
    "content": "abstract or body text …",
    "lang": "en"
  }]
}
```

Rules: strict decode (unknown fields rejected); `source.kind` is a lowercase slug;
`source.captured_at` is RFC3339; every item needs `ref` and `content` (or `content_ref`);
`privacy` ∈ {public, private}, default public.

Conversation exports are capture edges, not core types: `chatgpt`, `claude-code`,
`codex`, and other harness/session exports use the same envelope with different
`source.kind` values. Long private conversations stay bounded evidence quotes;
per-message/windowed refs such as `<conversation-uuid>#w2` are a later capture-edge
refinement, not a new internal package.

## What `compound knowledge import <export.json>` does per item

Current implementation note: deterministic reference metadata is field-level.
An imported item can emit separate proposed candidates for title, authors,
published date, topics, and URL; all share the same evidence ref and normalized
`knowledge:<kind>:<ref-without-leading-kind-prefix>` entity id. This keeps
field changes independently supersedable while preserving candidate-only intake.

1. **Evidence** — a content-addressed `EvidenceItem` (source kind/id + bounded quote).
   Evidence is citable raw material, never truth.
2. **One proposed candidate** — a reference/metadata claim about the item
   (`knowledge:<kind>:<ref>`), state `proposed`. **No import path writes promoted
   claims; adjudication is the only birthplace of a claim.**
   Current behavior fans deterministic metadata out to field-level candidates,
   not one opaque metadata row.
3. **An extraction request** — `inbox/knowledge-extractions/kx_*.json` (state `pending`),
   consumed later by the gated extraction bridge (rented model proposes meaning
   candidates from the content; code decides; candidate-only).
4. **Echo folding** — same canonical identity (url, else ref) seen again within 7 days,
   from ANY source: no new candidate/extraction; a row in
   `ledger/knowledge-distribution.jsonl` records the distribution event pointing at the
   first evidence. Ten shares of one thing = one signal + nine distribution events.
   (v0.1 matches identity exactly; URL normalization is a known refinement.)
5. **Source registry** — `registries/knowledge-sources.jsonl` gains
   `source:<kind>:<ref>` with a neutral `trust_posterior` (0.5). Trust
   calibration lands at P2; today the registry row is written once and frozen at
   the neutral prior.
6. **Idempotency** — re-importing the same export is a no-op (`reused`). Durable
   truth lives in `ledger/knowledge-imports.jsonl` (`canonical_ref`,
   `evidence_ref`, `imported_at`, `export_digest`, `source_kind`, `source_ref`);
   `indexes/knowledge-imports.jsonl` is a rebuildable lookup cache of that
   ledger, so backup restore and `home compact --rebuildable` can delete indexes
   without losing import state.

## Writing a capture edge (any language)

Fetch → shape items → emit the envelope → hand the file to `compound knowledge import`.
Keep edges dumb: no dedup (the core folds echoes), no summarization (the extraction
bridge owns meaning proposals), no direct writes into `COMPOUND_HOME`. A reference
synthetic export lives at `fixtures/knowledge/arxiv-export.json`; the future-proof test
(`internal/knowledge`) imports a never-seen medium kind to prove the zero-core-change
guarantee.

The proof fixture set includes `fixtures/knowledge/arxiv-export.json` and
`fixtures/knowledge/chatgpt-conversation-export.json`; `internal/knowledge`
tests import new medium kinds to prove the zero-core-change guarantee.

## Downstream (owned by other contracts)

Extraction bridge → proposed meaning candidates; `memory adjudicate` / panel →
promote/park/reject; background synthesis relates and compounds claims across mediums;
`compound orient --task` serves the result. See `docs/memory-architecture.md` and
`docs/architecture/runtime.md`.

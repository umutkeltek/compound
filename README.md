# compound

> A personal OS that preserves and reuses how you think, know, decide, and operate —
> so your thinking compounds instead of disappearing.

*(Working name. Rename freely — it's one `mv` on a fresh repo.)*

Not generic. Opinionated. The opposite of a memory tool that "distills" by reading only the
first and last message.

## The six layers (separation of concerns)

A personal OS **knows** · knows **what's true** · remembers **what it decided** · knows **how to
do things** · knows **what it's allowed to do** · and has **a point of view**.

| Layer | Owns | Status |
|---|---|---|
| **Memory** | what you know — history distilled into evidence-backed claims + context packs | 🟢 building now |
| **Authority** | what's true about your world — projects → AWS/server/repo/domain | ⬜ roadmap (from infra-os) |
| **Decisions** | what you've committed to, evolving with new info + falsifiers | ⬜ roadmap (from decisions project) |
| **Procedures** | how you do things — repeatable SOPs / recipes | ⬜ roadmap |
| **Permissions** | what agents may do on your behalf — autonomy envelopes, gates | ⬜ roadmap |
| **Doctrine** | what the system believes — the opinionated laws | ⬜ roadmap |

**Rented engine (swappable):** storage + embedding + retrieval live behind `src/engine` — gbrain
today, replaceable tomorrow. The owned spine never imports a backend directly.

Built one layer at a time; each absorbed layer **retires** its old scattered home, so the system
gets *more* coherent with each step, not more fragmented.

## Memory layer (current)

```
compound distill <session.jsonl>     # deep-distill a whole conversation → durable, quoted positions
compound pack    "<topic>"           # assemble one evidence-backed context pack from your history
```

Every node in a pack cites a source; synthesized connections are grounding-checked (a connection
citing a source not in the material is flagged, not trusted).

## Layout

```
src/
  cli.ts            # entry: distill | pack
  engine/           # RENTED — swappable storage/retrieval adapter (gbrain today)
  memory/           # OWNED — distill, ledger→facts, pack (the spine)
docs/
  LAYERS.md         # the six-layer architecture
  memory-study.md   # what to take from gbrain + mempalace; why this is better
```

## Provenance

Graduated from a proven spike (`Projects/operating-system/Plans/even-though-i-was-silly-thimble.md`;
verdict `~/brain/reviews/system-audit/2026-06-24-context-engine-spike.md`). Dogfood-first; architected
so another person could load *their* Memory/Authority/Decisions later.

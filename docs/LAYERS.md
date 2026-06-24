# The six layers

Compound is a personal OS. Its architecture is a **separation of concerns** pulled from Umut's
own words and own systems — each layer already existed as a separate thing he built; compound is
the coherent home that absorbs them **one at a time**, each absorption retiring its old scattered
home.

> A personal OS **knows** (Memory) · knows **what's true** (Authority) · remembers **what it
> decided** (Decisions) · knows **how to do things** (Procedures) · knows **what it's allowed to
> do** (Permissions) · and has **a point of view** (Doctrine).

| # | Layer | The concern | Source organ harvested | Status |
|---|-------|-------------|------------------------|--------|
| 1 | **Memory** | what you know — history distilled into evidence-backed claims + context packs | the proven deep distiller; gbrain + mempalace studied | 🟢 building |
| 2 | **Authority** | what's *true about your world* — project → AWS/server/repo/domain | `infra-os` (~80% structured YAML, alive) | ⬜ roadmap |
| 3 | **Decisions** | what you've *committed to*, evolving with new info + falsifiers | `decisions` project (DEC-#### with supersession) | ⬜ roadmap |
| 4 | **Procedures** | *how you do things* — repeatable SOPs / recipes | brain recipes / SOPs (scattered) | ⬜ roadmap |
| 5 | **Permissions** | what agents may do *on your behalf* — autonomy envelopes, gates | D0/D3 governance (doctrine only) | ⬜ roadmap |
| 6 | **Doctrine** | what the system *believes* — the opinionated laws | UNIVERSAL-PRINCIPLES | ⬜ roadmap |

## The rented engine (not a layer you own)

Storage + embedding + retrieval + LLM providers sit **behind `src/engine`**. gbrain implements the
`Engine` interface today; nothing in `src/memory` imports a backend directly. Swap gbrain for raw
Postgres+pgvector by writing one new adapter. "Own the spine, rent the executors" — applied to the
storage engine itself.

## Cross-cutting (every layer)

- **Provenance** — every node carries a source + confidence; nothing is asserted as bare truth.
- **Observability** — what ran, what it cost.
- **Self-healing** — the system hunts its own decay and proposes the smallest fix.

## Build discipline

- **One layer at a time** — each becomes real and proven before the next starts.
- **Subtractive** — absorbing a layer retires its old home; fewer systems after each step.
- **Dogfood first, architect for people** — built on Umut's data, with boundaries clean enough that
  someone else could load *their* Memory/Authority/Decisions later. That clean boundary is the
  open-source product.
- **Proof, not report** — each layer ships a working command on real data.

Full plan + sequence: `~/Projects/operating-system/Plans/even-though-i-was-silly-thimble.md`.

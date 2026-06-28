# Package & command status — which family is canonical

> Compound carries **two construction generations** (see [`../LAYERS.md`](../LAYERS.md): one
> architecture, two axes). Several packages and commands exist in **duplicate** — a legacy
> "information kernel" family and the canonical "envelope spine / P6+" family. This file is the
> single source of truth for **which is which**, so work lands on the canonical family and the
> legacy family is retired, not extended. `compound architecture doctor` enforces this map
> mechanically (a legacy package imported by `main` without a status entry is a finding).

Status values: **canonical** (build here) · **canonical-target** (the right home, still
thin — upgrade in place) · **bridge** (shared/transitional util — verify ownership before
retiring) · **legacy** (Gen-1; retire when its canonical replacement is proven) · **retired**.

## Packages (`internal/`)

| Package | Axis | Status | Replaced by / note | Retirement condition |
|---|---|---|---|---|
| `home` | A | canonical | runtime habitat | — |
| `envelope` | A | canonical | 9 envelopes | — |
| `contract` | A | canonical | EffectiveContract + ContextPack | — |
| `work` | A | canonical | WorkEnvelope lifecycle | — |
| `policy` | A | canonical | ActionIntent → PolicyDecision | — |
| `harness` | A | canonical | HarnessRegistry | — |
| `hook` | A | canonical | hook fixture loop | — |
| `mem` | B | canonical | P6 ledger, MVCC-by-fold, candidate-first | — |
| `mem` (Engine protocol) | edge | canonical | breadth-engine protocol | — |
| `distiller` | loop | canonical | TraceDistiller / CandidateSet / Critic | — |
| `capgraph` | B | canonical | Capability Graph (P8) | — |
| `authority` | B | canonical-target | importer → claims; needs **RuntimeBinding / ServiceContract** | upgrade in place, do not fork |
| `decisions` | B | canonical-target | importer → claims; needs **DecisionLifecycle** | upgrade in place, do not fork |
| `memory` | B | **legacy** | → `mem` | retire when `mem` recall + candidate flow covers all callers |
| `distill` | loop | **legacy** | → `distiller` | retire when `distiller` consumes real spine traces |
| `engine` | edge | **legacy** | → `mem` Engine protocol | retire when the breadth adapter is the only engine seam |
| `position` | B | **legacy** | → `mem` positions | retire with `memory` |
| `pattern` | B | **legacy** | → `mem` patterns | retire with `memory` |
| `recall` | B | **legacy** | → `mem` recall | retire with `memory` |
| `reconcile` | B | **legacy** | → `mem` supersession | retire with `memory` |
| `scope` | B | bridge | cwd-independent attribution | verify whether `mem` subsumes before retiring |
| `ledger` | B | bridge | append/read JSONL | verify `mem` ledger subsumes before retiring |

> The duplicate pairs to resolve, in priority order: **`memory` ↔ `mem`**, **`distill` ↔
> `distiller`**, **`engine` ↔ `mem.Engine`**. Until resolved, every PR that touches one must
> state which family it lands on. Do **not** migrate them in this coherence patch — flag only;
> the migration is its own bounded move with its own proof (each retirement records the future
> wrong move it prevents).

## Commands (`compound <verb>`)

| Family | Verbs | Status |
|---|---|---|
| Canonical — Axis A | `home`, `envelope`, `contract`, `orient`, `work`, `policy`, `hook`, `harness`, `transition` | canonical |
| Canonical — Axis B | `memory` (candidate/adjudicate/promote/timeline/doctor/query/relate/graph), `distiller`, `capability` | canonical |
| Legacy — Gen-1 memory-era | `distill`, `scope`, `promote`, `build`, `reconcile`, `timeline` (top-level), `recall`, `pack`, `ledger` | **legacy** — label in `--help`, do not present as a peer of the canonical verbs |
| Importers (canonical-target) | `authority`, `decisions` | upgrade toward first-class contracts |

**Rule:** a legacy verb may remain temporarily for migration, but it must be **labelled
"legacy memory-era" in help output** and must not be documented as the canonical path. The
canonical recall/meaning path is `compound memory …`; the canonical distillation path is
`compound distiller run`.

## What the architecture doctor checks (Patch B)

`compound architecture doctor` fails (or warns) on:

- a `legacy` package imported by `main` that has **no status entry** here;
- a CandidateKind with **no inbox directory** (or an inbox dir with no producer/consumer) —
  see `runtime.md` candidate map;
- a **public doc that contains a private path** (leak gate);
- `README` / CLI `--help` / docs **disagreeing on the current phase**;
- a **legacy command presented as canonical** (no "legacy" label).

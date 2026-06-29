# Package & command status — what is canonical, what must converge

> Compound carries a **spine** (one canonical runtime) plus a **memory object family with TWO
> implementations that must converge.** A verified audit (2026-06-29, 11 packages, every one
> adversarially checked) overturned the lazy reading that "Gen-1 is legacy, `mem` replaced it."
> **It did not.** `mem` is the spine-bound ledger but is *thinner* than the Gen-1 stack: the
> canonical family has **no** `Position` (topic-grouped stance), **no** `Pattern`, **no** `Entity`
> registry (mem's entity is a bare string), no `ClaimType`, no stored `EvidenceItem`, no
> `PromotionBasis`, and none of the ingestion/recall/reconcile capability. So the Gen-1 packages
> are **not redundant** — each has a logical job `mem` cannot yet do. The coherent move is
> **migrate-then-retire** (absorb the object richness into the spine-bound family, *then* retire),
> **gated on test coverage** — never a delete-before-tests. `compound architecture doctor`
> tracks this debt so it stays observable and cannot silently grow.

Status values: **canonical** (the spine / the convergence target — build here) · **converge→retire**
(a Gen-1 package whose capability must move onto the spine before it retires; retirement is
**BLOCKED** until parity + tests) · **canonical-target** (an importer to upgrade in place) ·
**retired**.

## Packages (`internal/`)

### Spine — canonical, no duplication
| Package | Role |
|---|---|
| `home`, `envelope`, `contract`, `work`, `policy`, `harness`, `hook` | the runtime grammar (Axis-A spine) |
| `candidates` | single source of truth for candidate kinds → inbox dirs |
| `distiller` | spine-bound intake (the compounding loop's front-end) |
| `capgraph` | the P8 Capability Graph (capability object family) |

### Memory object family — convergence-pending (retirement BLOCKED on parity + tests)
`mem` is the **convergence target** (spine-bound: MVCC-by-fold, candidate-first, Transitions).
The rest hold object richness / ingestion / recall that `mem` must absorb first.

| Package | Status | Capability `mem` still lacks (the retire-blocker) |
|---|---|---|
| `mem` | **canonical (target)** | the destination; gains the rows below. ✓ already landed: `PromotionBasis` (the "why" of canon), `Position` (`mem.Positions` + `compound memory positions`) |
| `memory` | converge→retire | `Entity` registry (aliases/identifiers/parent), ~~`Position`~~ ✓on-spine, `Pattern`, `ClaimType`, ~~`PromotionBasis`~~ ✓on-spine, stored `EvidenceItem`, richer `EdgeType` (refines/supports/mentions/owns/part_of) |
| `position` | converge→retire | ✓ **capability on the spine** (`mem.Positions`, topic-grouped current stance + supersession chain, parity-tested). Package retires with the Gen-1 stack (its sole consumer `cmdBuild` reads the Gen-1 ledger, so it retires *wholesale* when the spine reaches CLI parity — not in isolation) |
| `pattern` | converge→retire | cross-session recurrence aggregation → typed `Pattern` hypothesis (no equivalent anywhere) |
| `recall` | converge→retire | Position-aware + Pattern-surfacing recall; `PromotionBasis`-driven promote; scope alias resolution in query planning |
| `reconcile` | converge→retire | LLM/semantic + topic-level reconciliation; batch maintenance pass marking stored claims superseded |
| `distill` | converge→retire | raw session JSONL ingestion + cleaning; worth-gate / decision-turn detection; whole-conversation map-reduce |
| `engine` | converge→retire | free-text snippet breadth; page-level ingest; external rented retrieval-CLI adapter |
| `scope` | converge→retire | deterministic session→entity attribution; `primary_about`/`multi_scope`/`unresolved` verdict; alias registry |
| `ledger` | converge→retire | generic multi-kind append store; positions/patterns/evidence as stored objects |

### Importers — keep, upgrade in place (NOT redundant)
| Package | Status | Becomes |
|---|---|---|
| `authority` | canonical-target | infra ingestion → `RuntimeBinding` / `ServiceContract` |
| `decisions` | canonical-target | DEC/operator-ledger import → `DecisionLifecycle` (status + review_trigger) |

> **The convergence direction is set by "spine is primary":** the object richness moves **into**
> the spine-bound family (`mem` + new object types), not the reverse. Until a capability lands on
> the spine **with tests**, its Gen-1 package stays — deleting it first would lose functionality
> (the audit's unanimous `retire_safe=false`). This is a tracked debt, not a quick subtraction.
>
> **Convergence shape (verified by the entanglement audit):** capabilities land on the spine **one
> at a time** (`PromotionBasis` ✓, `Position` ✓ — each parity-tested), but the Gen-1 **packages**
> retire **wholesale**: `position`/`pattern`/`scope`/`recall`/`reconcile` are imported only by
> `main.go`'s Gen-1 verbs, which read the Gen-1 ledger and share `memory.Claim` + each other's
> imports. So `ConvergePendingBudget` drops in a **step** when the spine reaches CLI parity, not by
> 1 each time a capability lands — landing capabilities is the work that *earns* that step.

## Commands (`compound <verb>`)

| Family | Verbs | Status |
|---|---|---|
| Spine (canonical) | `home`, `envelope`, `contract`, `orient`, `work`, `policy`, `hook`, `harness`, `transition`, `memory`, `distiller`, `capability` | canonical |
| Gen-1 memory-era (convergence-pending) | `distill`, `scope`, `promote`, `build`, `reconcile`, `timeline` (top-level), `recall`, `pack`, `ledger` | **not "legacy to delete"** — they are the *only* access to the un-migrated capabilities above; label them "Gen-1 (converging)" in `--help`, do not present as the canonical path, retire per-verb only as its capability lands on the spine |
| Importers | `authority`, `decisions` | upgrade toward first-class contracts |

## What the architecture doctor checks (Patch B2)

- candidate-map integrity: every `candidates.Kind` has one non-empty inbox dir **and** `home.Dirs`
  creates it; no inbox dir without a producer/consumer;
- the **public-doc leak gate** (no private path/token in `docs/`);
- **package classification is total**: every `internal/` package is classified (spine /
  convergence-pending / importer); an **unclassified** package fails the doctor — so a new package
  can't silently appear and the two-stack debt stays visible until it converges.

# Package & Command Status

This file is the current package/command map. `compound architecture doctor
--strict` enforces the mechanical side: package classification, candidate-map
integrity, public-doc leak checks, and the memory convergence ratchet.

## Current Verdict

The Gen-1 memory stack has been migrated into the spine memory family and
retired.

- `ConvergePendingBudget = 0`.
- No `internal/` packages are classified `converge-pending`.
- Top-level Gen-1 verbs are tombstones only; they do not import or route through
  retired packages.
- The canonical surface is `compound memory ...` plus `compound distiller ...`.

## Packages (`internal/`)

| Package | Status | Role |
|---|---|---|
| `home`, `envelope`, `contract`, `work`, `policy`, `harness`, `hook` | canonical | runtime spine |
| `candidates` | canonical leaf | candidate kinds -> inbox dirs and verdicts |
| `mem` | canonical | memory object family: entities, evidence, claims, positions, patterns, edges, adjudication, recall |
| `distiller` | canonical | trace -> prior state -> candidate set -> critique -> adjudication input |
| `capgraph` | canonical | capability graph: surface, observe, consume |
| `arch` | canonical | architecture doctor |
| `authority` | importer | external authority records imported through spine memory |
| `decisions` | importer | DecisionLifecycle records imported/projected through spine memory; doctor surfaces missing falsifier/review trigger |

Retired Gen-1 packages:

```text
internal/memory
internal/position
internal/pattern
internal/recall
internal/reconcile
internal/distill
internal/engine
internal/scope
internal/ledger
```

They must not be resurrected or imported. Any missing capability should land on
`internal/mem`, `internal/distiller`, or a new explicitly classified package only
when it represents a new object family or adapter seam.

## Commands

| Family | Verbs | Status |
|---|---|---|
| Spine | `home`, `envelope`, `contract`, `orient`, `work`, `policy`, `hook`, `harness`, `transition` | canonical |
| Memory | `memory candidate/adjudicate/recall/timeline/query/doctor` | canonical |
| Distillation | `distiller run` | canonical |
| Capability | `capability add/surface/observe/consume/list` | canonical |
| Importers | `authority`, `decisions` | canonical import surfaces |
| Gen-1 tombstones | `distill`, `scope`, `promote`, `build`, `reconcile`, top-level `timeline`, top-level `recall`, `pack`, `ledger` | retired; exit `2` with replacement hint |

There is intentionally no alias layer. Alias layers keep obsolete surfaces alive
and recreate fragmentation.

## What Architecture Doctor Checks

- candidate-map integrity: every candidate kind has exactly one created inbox dir
  and a consumer;
- package classification totality: every `internal/` directory is classified, and
  every classified package exists;
- public-doc leak gate: generic path/email leaks plus private leak-token list;
- convergence ratchet: `converge-pending` packages must not exceed budget, now
  fixed at `0`.

## Next Coverage Moves

The next work is no longer memory-stack convergence. It is adapting existing
operating-system functions into first-class Compound contracts without creating
parallel owners:

- runtime/service ownership -> `RuntimeBinding` / `ServiceContract`;
- decisions -> `DecisionLifecycle` first slice landed; next hardening is source-specific adapters and lifecycle transitions beyond import;
- source documents -> source adapters that emit evidence/candidates;
- orientation scope -> project-aware context loading;
- rented retrieval -> `MemoryEngine` adapter that leaves the ledger unchanged;
- skills/tools/MCPs/hooks -> `CapabilityGraph` imports and observations.

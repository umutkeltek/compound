# Package & Command Status

This file is the current package/command map. `compound architecture doctor --strict` enforces the mechanical side: package classification, candidate-map integrity, public-doc leak checks, and the memory convergence ratchet.

## Current Verdict

Gen-1 memory stack migrated into the spine memory family and retired.

- `ConvergePendingBudget = 0`.
- No `internal/` packages are classified `converge-pending`.
- Top-level Gen-1 verbs are tombstones only; do not import or route through retired packages.
- Canonical surfaces are `compound memory ...` plus `compound distiller ...`.

## Packages (`internal/`)

| Package | Status | Role |
|---|---|---|
| `home`, `envelope`, `contract`, `work`, `policy`, `harness`, `hook` | canonical | runtime spine |
| `candidates` | canonical leaf | candidate kinds -> inbox dirs and verdicts |
| `mem` | canonical | memory object family: entities, evidence, claims, positions, patterns, edges, adjudication, recall |
| `distiller` | canonical | trace -> prior state -> candidate set -> critique -> adjudication input |
| `capgraph` | canonical | capability graph: surface, observe, consume |
| `runtimebinding` | canonical | RuntimeBinding + ServiceContract bundle and doctor |
| `projection` | canonical | GeneratedViewObservation + ProjectionFreshness doctor |
| `writeback` | canonical | OwnerWritebackGate + NoPromoteDecision: candidate/observation -> owner gate -> policy/no-promote ledger |
| `arch` | canonical | architecture doctor |
| `authority` | importer | external source/path authority records imported through spine memory; path authority preserves entity identity across path/source moves |
| `decisions` | importer | DecisionLifecycle records imported/projected through spine memory; doctor surfaces missing falsifier/review trigger and can route gaps through owner writeback |
| `sourceobs` | importer | SourceObservation records map generated/source views into candidate inboxes plus no-promote decisions; observation only, never promotion |
| `tooltopology` | importer | Tool/MCP topology observations import CLI/MCP/adapter nodes and fallback recommendations into CapabilityGraph; offers only, never executes |

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

They must not be resurrected or imported. Any missing capability should land on `internal/mem`, `internal/distiller`, or a new explicitly classified package only when it represents a new object family or adapter seam.

## Commands

| Family | Verbs | Status |
|---|---|---|
| Spine | `home`, `envelope`, `contract`, `orient`, `work`, `policy`, `hook`, `harness`, `transition` | canonical |
| Memory | `memory candidate/adjudicate/recall/timeline/query/doctor` | canonical |
| Distillation | `distiller run` | canonical; duplicate memory outputs create idempotent `NoPromoteDecision` rows |
| Capability | `capability add/surface/observe/consume/list/import-topology` | canonical; topology import maps tools/MCPs into CapabilityGraph nodes + fallback edges |
| Runtime | `runtime doctor <runtime-binding.yaml> [--writeback]`; `runtime doctor --project-contract <project.yaml> [--writeback]` | canonical first slice + infra-os project-contract adapter; failing reports can enter work -> gate -> receipt/transition |
| Projection | `projection doctor <freshness.yaml> [--writeback]` | canonical first slice; stale/drifted observations can enter work -> gate -> receipt/transition |
| Writeback | `writeback gate <gate.json> --policy <policy-decision.json> [--record]`; `writeback no-promote --candidate C --basis B --evidence E1[,E2]` | canonical first slice |
| Importers | `authority import [registry.yaml]`; `authority path <path-authority.yaml>`; `authority resolve <token>`; `decisions doctor [--writeback]`; `source observe <source-observation.yaml>` | canonical import/report surfaces; source/path authority imports resolve moved paths to stable entities; decision lifecycle gaps can enter work -> gate -> receipt/transition; source observations enter inbox/no-promote without direct promotion |
| Gen-1 tombstones | `distill`, `scope`, `promote`, `build`, `reconcile`, top-level `timeline`, top-level `recall`, `pack`, `ledger` | retired; exit `2` replacement hint |

Intentionally no alias layer. Alias layers keep obsolete surfaces alive and recreate fragmentation.

## Architecture Doctor Checks

- candidate-map integrity: every candidate kind has exactly one created inbox dir and consumer;
- package classification totality: every `internal/` directory is classified, and every classified package exists;
- public-doc leak gate: generic path/email leaks plus private leak-token list;
- convergence ratchet: `converge-pending` packages must not exceed budget, now fixed at `0`.

## Next Coverage Moves

Next work is no longer memory-stack convergence. It is adapting existing operating-system functions into first-class Compound contracts without creating parallel owners:

- runtime/service ownership -> `RuntimeBinding` / `ServiceContract` first slice + infra-os project-contract adapter landed opt-in `--writeback` route through `OwnerWritebackGate`, receipt, transition; next hardening broader private parity fixtures owner-regeneration adapter;
- decisions -> `DecisionLifecycle` first slice landed; `decisions doctor --writeback` routes lifecycle gaps through `OwnerWritebackGate`, receipt, transition; next hardening source-specific adapters and lifecycle transitions beyond import;
- generated views -> `GeneratedViewObservation` / `ProjectionFreshness` first slice landed hash-based doctor opt-in `--writeback` route through `OwnerWritebackGate`, receipt, transition; source observations now map generated/source views into candidate inbox + `NoPromoteDecision`; distiller duplicate memory outputs route to `NoPromoteDecision`; next hardening tool/MCP topology observations;
- source documents -> source adapters emit evidence/candidates/no-promote without direct promotion;
- source-map / path-authority -> `authority path` imports `PathAuthority` into entity identifiers + authority claims; `authority resolve` proves old path/new path/source remote map to one entity;
- orientation scope -> project-aware context loading;
- rented retrieval -> `MemoryEngine` adapter leaves ledger unchanged;
- skills/tools/MCPs/hooks -> `CapabilityGraph` imports observations; `compound capability import-topology` imports tool/MCP topology and CLI-vs-MCP fallback recommendation.

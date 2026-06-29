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
| `distiller` | canonical | trace/runtime-work/LLM-response -> richer prior state -> candidate set -> critique -> adjudication input; doctor surfaces eval/method/policy/capability debt and can route method/policy debt through writeback gate |
| `capgraph` | canonical | capability graph: surface, observe, consume; strict skill-manifest imports land here as graph nodes/edges, never a parallel skill registry |
| `runtimebinding` | canonical | RuntimeBinding + ServiceContract bundle and doctor |
| `projection` | canonical | GeneratedViewObservation + ProjectionFreshness doctor |
| `writeback` | canonical | OwnerWritebackGate + NoPromoteDecision: candidate/observation -> owner gate -> policy/no-promote ledger |
| `product` | canonical | Product registry + standalone/source-export ProductSignal adapter + strict signal doctor/no-promote path + joined product timeline over signals/hypotheses/transitions/open and closed source issues; signal/review/source-issue-review imports emit product Transition projections; doctor routes open feedback review debt through writeback gate |
| `routeevidence` | canonical | ExecutionRouteEvidence import/record/join path; metadata-only route decisions become Transition routing/cost projections, never task success |
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

Decision source import hardening: `compound decisions import --policy <DEC.md>` and `--policy-dir <dir>` now record policy markdown as evidence-backed `DecisionLifecycle`, preserve supersedes/replaced_by transitions, promote only accepted decisions, and park non-accepted lifecycle records. The legacy policy directory env var routes through the same adapter, not a candidate-only parser.

| Family | Verbs | Status |
|---|---|---|
| Spine | `home`, `envelope`, `contract`, `orient`, `work`, `policy`, `hook`, `harness`, `transition`, `route import` | canonical; `orient` freezes scope-aware ContextPacks and includes surfaced CapabilityGraph offers when graph state exists, filtering harness-specific offers by `surfaces_with -> harness:*`; hooks persist normalized `HookEvent` envelopes to `events/<hook_event_id>.json` and `events/hook-events.ndjson`; `hook session-start` uses the same harness-filtered pack path and persists redacted `context/requests/<ctxreq_id>.json`, `context/packs/<ctx_id>.json`, plus exact `context/injections/<hook_event_id>.md`; `route import` writes external `ExecutionRouteEvidence` metadata plus an audit-only Transition projection |
| Memory | `memory candidate/adjudicate/recall/timeline/query/engine-doctor/engine-compare/doctor` | canonical; `engine-doctor` proves one rented breadth engine leaves owned ledger bytes unchanged; `engine-compare` proves a configured sidecar hydrates the same claim ids as the owned lexical reference |
| Distillation | `distiller eval <case.json\|cases-dir>`; `distiller request --work-id <work_id>`; `distiller extract`; `distiller run [--llm-response]`; `distiller doctor [--writeback]`; `distiller prior-state` | canonical; `eval` proves request/response/adjudication fixtures offline before live model lanes, runs sorted `*-eval.json` corpus directories, and treats expected extractor rejections as first-class checks (`error_contains`) for malformed/adversarial model output; `request` builds provider-neutral LLM extraction packets from typed spine artifacts or a closed `COMPOUND_HOME` work chain (WorkEnvelope + ContextPack + Receipt + Transition + matching HookEvents) without calling model; `extract` is a rented OpenAI-compatible provider call gated by a passing eval corpus, validated through `LLMExtractor`, then handed to `routeevidence` for metadata-only `ExecutionRouteEvidence` + Transition routing/cost projection, with no provider config mutation and no direct promotion; route evidence alone is explicitly not task success; LLM-shaped responses are adapter input only and cannot carry promotion basis; duplicate memory outputs create idempotent `NoPromoteDecision` rows; prior-state reads current claims, typed inboxes, capability nodes, and no-promote rows; doctor routes method/policy debt through `OwnerWritebackGate` |
| Capability | `capability add/surface/observe/consume/list/import-topology/import-skills` | canonical; topology import maps tools/MCPs into CapabilityGraph nodes + fallback edges; skill-manifest import maps external/OS skills and lenses into the same graph, rejects unknown fields, records harness compatibility edges, and only offers surfaced nodes |
| Runtime | `runtime doctor <runtime-binding.yaml> [--writeback]`; `runtime doctor --project-contract <project.yaml> [--writeback]` | canonical first slice + infra-os project-contract adapter; failing reports can enter work -> gate -> receipt/transition |
| Projection | `projection doctor <freshness.yaml> [--writeback]` | canonical first slice; stale/drifted observations can enter work -> gate -> receipt/transition |
| Product | `product import <product.yaml>`; `product signal <signal.yaml\|source-export.yaml>`; `product signal-doctor <signal.yaml\|source-export.yaml> [--record]`; `product review <review.yaml>`; `product source-issue-review <review.yaml>`; `product timeline <product_id>`; `product doctor <product_id> [--writeback]` | canonical first product feedback-loop slice; imports product identity, deterministic claims, standalone metric-source signal evidence, source-export metric evidence, parked hypotheses; signal/review/source-issue-review imports emit product Transition projections; invalid signal source contracts can enter `NoPromoteDecision` through `signal-doctor --record`; timeline joins product signals, hypotheses, transition deltas, and open/closed source issues without declaring causality; pending hypothesis review or open product source issues can enter work -> gate -> receipt/transition; review results update the same parked hypothesis with evidence; source-issue reviews close source issues while preserving the original no-promote evidence |
| Writeback | `writeback gate <gate.json> --policy <policy-decision.json> [--record]`; `writeback no-promote --candidate C --basis B --evidence E1[,E2]` | canonical first slice |
| Importers | `authority import [registry.yaml]`; `authority path <path-authority.yaml>`; `authority resolve <token>`; `decisions import --brain <markdown>`; `decisions import --policy <DEC.md>`; `decisions import --policy-dir <dir>`; `decisions doctor [--writeback]`; `source observe <source-observation.yaml>` | canonical import/report surfaces; source/path authority imports resolve moved paths to stable entities; brain and policy markdown decisions import as EvidenceItem + DecisionLifecycle; policy batch imports use the same evidence-backed adapter; decision lifecycle gaps can enter work -> gate -> receipt/transition; source observations enter inbox/no-promote without direct promotion |
| Gen-1 tombstones | `distill`, `scope`, `promote`, `build`, `reconcile`, top-level `timeline`, top-level `recall`, `pack`, `ledger` | retired; exit `2` replacement hint |

Intentionally no alias layer. Alias layers keep obsolete surfaces alive and recreate fragmentation.

## Architecture Doctor Checks

- candidate-map integrity: every candidate kind has exactly one created inbox dir and consumer;
- package classification totality: every `internal/` directory is classified, and every classified package exists;
- public-doc leak gate: generic path/email leaks plus private leak-token list;
- convergence ratchet: `converge-pending` packages must not exceed budget, now fixed at `0`.

## Next Coverage Moves

Recently closed: source-specific policy markdown decisions can now enter through the `DecisionLifecycle` owner without flattening lifecycle transitions into loose claims; batch policy imports use the same evidence-backed adapter. Next decision hardening is private parity against a real decision-source page, not a new public decision registry.

Next work is no longer memory-stack convergence. It is adapting existing operating-system functions into first-class Compound contracts without creating parallel owners:

- runtime/service ownership -> `RuntimeBinding` / `ServiceContract` first slice + infra-os project-contract adapter landed opt-in `--writeback` route through `OwnerWritebackGate`, receipt, transition; next hardening broader private parity fixtures owner-regeneration adapter;
- decisions -> `DecisionLifecycle` first slice landed; `decisions import --brain <markdown>` imports curated markdown decision pages as EvidenceItem + DecisionLifecycle; `decisions import --policy <DEC.md>` and `--policy-dir <dir>` import policy markdown through the same evidence-backed lifecycle adapter; `decisions doctor --writeback` routes lifecycle gaps through `OwnerWritebackGate`, receipt, transition; next hardening is private parity against a real decision-source page;
- generated views -> `GeneratedViewObservation` / `ProjectionFreshness` first slice landed hash-based doctor opt-in `--writeback` route through `OwnerWritebackGate`, receipt, transition; source observations now map generated/source views into candidate inbox + `NoPromoteDecision`; distiller duplicate memory outputs route to `NoPromoteDecision`; next hardening tool/MCP topology observations;
- source documents -> source adapters emit evidence/candidates/no-promote without direct promotion;
- source-map / path-authority -> `authority path` imports `PathAuthority` into entity identifiers + authority claims; `authority resolve` proves old path/new path/source remote map to one entity;
- orientation scope -> project-aware context loading bridged with fixture repo markers; `compound orient` excludes governance context in product repos and includes it in control-plane repos, recording exclusions; it also consumes harness-compatible `CapabilityGraph` offers into ContextPack included refs without executing them; `hook session-start` now persists the redacted request, same pack, and exact injected markdown under `COMPOUND_HOME/context`;
- hook work admission -> `hook user-prompt-submit` persists redacted request + context pack + exact injection + proposed WorkEnvelope tied to `context_pack_id` when a work store is available; hook calls persist normalized HookEvents without raw prompt bodies; package proof closes that work through `session-end` and asserts Receipt/Transition retain the same context pack in ids and `before_refs`; `distiller request --work-id` can turn that closed runtime chain plus matching HookEvents into candidate-only extraction source material;
- product feedback -> `product import` registers product identity, promoted deterministic product claims, `ProductSignal` evidence rows, and parked `ProductHypothesis` candidates; `product signal` maps normalized signal docs and source-export analytics-shaped metric documents into the same `ProductSignal` ledger with EvidenceItem refs and a product Transition projection; `product signal-doctor --record` records invalid source-export shapes as no-promote rows instead of silently ignoring provider fields; `product timeline` joins signals, hypotheses, product transition deltas, and open/closed product-scoped source issues without declaring causality; `product doctor --writeback` routes pending hypothesis review and open product source issues through governed owner writeback; `product review` records a review EvidenceItem, updates the same hypothesis candidate result without promotion, and emits a product Transition projection; `product source-issue-review` records closure evidence, preserves the original no-promote issue, and removes the issue from open doctor/writeback debt;
- rented retrieval -> `MemoryEngine` protocol + `memory engine-doctor` prove lexical/reference-sidecar breadth engines leave ledger bytes unchanged; `memory engine-compare` compares a configured sidecar against the owned lexical reference so legacy/vector adapters must prove swap parity before adoption; next hardening real legacy/vector adapter wiring;
- skills/tools/MCPs/hooks -> `CapabilityGraph` imports observations; `compound capability import-topology` imports tool/MCP topology and CLI-vs-MCP fallback recommendation; `compound capability import-skills` imports versioned skill/lens manifests into the same graph without auto-execution.

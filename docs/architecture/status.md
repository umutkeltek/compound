# Package & Command Status

This file is the current package/command map. `compound architecture doctor --strict` enforces the mechanical side: package classification, candidate-map integrity, public-doc leak checks, and the memory convergence ratchet.

## Recent Runtime Proof
- `compound orient` and hook context injection now resolve repo kind from explicit flag, committed repo marker, or private `COMPOUND_HOME/config/repo-kinds.yaml` before product default, so private/control-plane roots can include governance refs while product roots keep them excluded with reasons.

- `compound projection repair-plan` now routes stale/drifted `ProjectionFreshness` through owner writeback gate and SourceObservation/no-promote, then writes reviewable regeneration JSON/Markdown artifacts without running generators or editing owner files.

- `compound projection doctor --source-observe` now bridges stale/drifted `ProjectionFreshness` into the existing `SourceObservation -> method_upgrade candidate -> NoPromoteDecision` path without writing promoted claims.

- `compound source observe` is now command-boundary proven to turn a generated-view `SourceObservation` into a candidate inbox record plus `NoPromoteDecision` without writing a promoted claim.

- `compound-memory-cli-engine` now rejects mutating external-memory command templates before the CLI process is invoked; unsafe templates return protocol `bad_request`, not a degradable backend outage.

- `compound-memory-cli-engine` now has an expansion-only regression: a query that does not lexically match indexed claims can still return the correct allowlisted claim through external memory CLI output, while empty allowlists fail closed before the CLI is called.

- LLM extraction requests now expose explicit `capability_feedback[]` from `Receipt.skills.missed` / `Receipt.skills.false_triggered`, with instructions forbidding inference from inert `skills.candidate` offers.

- Command-level runtime proof now covers `import-skills -> hook user-prompt-submit -> hook session-end explicit feedback -> distiller request capability_feedback -> distiller run capability_observation -> CapabilityGraph` for missed and false-triggered capability feedback.

- Distiller/critic capability observations now feed the same graph path: `distiller run` auto-consumes operationalized `capability_observation` candidates when graph state already exists, while preserving no-graph no-op behavior.

- Hook/session closure preserves capability-offer provenance: `work close` and `hook session-end` hydrate the persisted ContextPack and copy `capability://...` offers into Receipt `skills.candidate` plus Transition `projections.skill`. This records what the agent was offered without claiming any skill was executed.
- Work closure also records used capability provenance: allowed recorded ActionIntent envelopes contribute their `capability` to Receipt `skills.used` and Transition `projections.skill`; denied or held actions never enter used skills because `RecordAction` refuses them.
- Capability feedback now feeds the graph explicitly: `work close` and `hook session-end` auto-run receipt consumption when graph state exists; `capability consume-receipts` remains the manual/replay command. Completed changed `skills.used` becomes `validated_by`; explicit `skills.missed` becomes `missed_when`; explicit `skills.false_triggered` becomes `false_triggered_by`; `skills.candidate` offers stay inert.

- Closure clients can now emit explicit feedback: `work close --missed ... --false-triggered ...` and `hook session-end` fixture fields write `skills.missed` / `skills.false_triggered` into emitted Receipts before automatic graph consumption.
- Imported skill-manifest offers now have executable full-loop proof: `capability import-skills` can seed graph, `hook user-prompt-submit` persists an offered capability in ContextPack, an allowed action records that capability as used, and `hook session-end` validates it back into the same CapabilityGraph.

- `compound event import-os` now adapts append-only OS-style event rows into typed `TraceEnvelope` files and `events/traces.ndjson` under `COMPOUND_HOME`; replay is idempotent and no memory claims are promoted.
- `compound distiller request --event-trace <trace_id>` now consumes those imported `TraceEnvelope` files as provider-neutral extraction source material with prior-state coverage, still without model calls or memory promotion.
- `compound distiller eval` now asserts event-trace memory candidates stay unpromoted under live policy and create no claims ledger.

- `compound distiller run --llm-response` now has command-level proof that model-shaped candidates citing imported trace evidence park under live policy, write memory candidate/adjudication records, and create no claims ledger.

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
| `capgraph` | canonical | capability graph: surface, observe, consume; normalized skill manifests and generated skill-manifest JSON land here as graph nodes/edges, never a parallel skill registry |
| `runtimebinding` | canonical | RuntimeBinding + ServiceContract bundle and doctor |
| `projection` | canonical | GeneratedViewObservation + ProjectionFreshness doctor + ProjectionRegenerationPlan review artifacts |
| `writeback` | canonical | OwnerWritebackGate + NoPromoteDecision: candidate/observation -> owner gate -> policy/no-promote ledger |
| `product` | canonical | Product registry + standalone/source-export ProductSignal adapter + strict signal doctor/no-promote path + joined product timeline over signals/hypotheses/transitions/open and closed source issues; signal/review/source-issue-review imports emit product Transition projections; doctor routes open feedback review debt through writeback gate |
| `routeevidence` | canonical | ExecutionRouteEvidence import/record/join path; metadata-only route decisions become Transition routing/cost projections, never task success |
| `arch` | canonical | architecture doctor |
| `authority` | importer | external source/path authority records imported through spine memory; path authority preserves entity identity across path/source moves |
| `decisions` | importer | DecisionLifecycle records imported/projected through spine memory; doctor routes lifecycle gaps through owner writeback; source-doctor verifies lifecycle source templates/docs; repair-plan emits owner-review artifacts; repair-apply applies reviewed lifecycle fields only on explicit write |
| `sourceobs` | importer | SourceObservation records map generated/source views into candidate inboxes plus no-promote decisions; observation only, never promotion |
| `tooltopology` | importer | Tool/MCP topology observations import normalized YAML or generated resolver JSON into CLI/MCP/adapter nodes and fallback recommendations in CapabilityGraph; offers only, never executes |
| `eventsource` | importer | append-only event streams import as typed TraceEnvelope evidence under runtime events; idempotent, no memory promotion |

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

Decision source hardening: `compound decisions import --policy <DEC.md>` and `--policy-dir <dir>` record policy markdown as evidence-backed `DecisionLifecycle`, preserve supersedes/replaced_by transitions, give each decision a stable `decision:<id>` entity, promote only accepted decisions with complete lifecycle proof, park incomplete or non-accepted lifecycle records, `decisions source-doctor --template <md> [--format-doc <md>]` catches templates/docs that would regenerate lifecycle debt, `decisions repair-plan --policy-dir <dir>` emits reviewable owner-source repair artifacts under `COMPOUND_HOME`, and `decisions repair-apply --plan <json> --write-reviewed` applies those fields only after review. The legacy policy directory env var routes through the same adapter, not the candidate-only parser.

| Family | Verbs | Status |
|---|---|---|
| Spine | `home`, `envelope`, `contract`, `orient`, `work`, `policy`, `hook`, `harness`, `transition`, `route import` | canonical; `orient` freezes scope-aware ContextPacks and includes surfaced CapabilityGraph offers when graph state exists, filtering harness-specific offers by `surfaces_with -> harness:*`; hooks persist normalized `HookEvent` envelopes to `events/<hook_event_id>.json` and `events/hook-events.ndjson`; `hook session-start` uses the same harness-filtered pack path and persists redacted `context/requests/<ctxreq_id>.json`, `context/packs/<ctx_id>.json`, plus exact `context/injections/<hook_event_id>.md`; `route import` writes external `ExecutionRouteEvidence` metadata plus an audit-only Transition projection |
| Memory | `memory candidate/adjudicate/recall/timeline/query/engine-doctor/engine-compare/doctor`; sidecars `compound-mock-engine`, `compound-memory-cli-engine` | canonical; `engine-doctor` proves one rented breadth engine leaves owned ledger bytes unchanged; `engine-compare` proves a configured sidecar hydrates the same claim ids as the owned lexical reference; `compound-memory-cli-engine` adapts an external memory CLI read-only as expansion context and returns only allowed indexed claim ids |
| Distillation | `distiller eval <case.json\|cases-dir>`; `distiller request --work-id <work_id>`; `distiller extract`; `distiller run [--llm-response]`; `distiller doctor [--writeback]`; `distiller prior-state` | canonical; `eval` proves request/response/adjudication fixtures offline before live model lanes, runs sorted `*-eval.json` corpus directories, and treats expected extractor rejections as first-class checks (`error_contains`) for malformed/adversarial model output; `request` builds provider-neutral LLM extraction packets from typed spine artifacts or a closed `COMPOUND_HOME` work chain (WorkEnvelope + ContextPack + Receipt + Transition + matching HookEvents) without calling model; `extract` is a rented OpenAI-compatible provider call gated by a passing eval corpus, validated through `LLMExtractor`, then handed to `routeevidence` for metadata-only `ExecutionRouteEvidence` + Transition routing/cost projection, with no provider config mutation and no direct promotion; route evidence alone is explicitly not task success; LLM-shaped responses are adapter input only and cannot carry promotion basis; duplicate memory outputs create idempotent `NoPromoteDecision` rows; prior-state reads current claims, typed inboxes, capability nodes, and no-promote rows; doctor routes method/policy debt through `OwnerWritebackGate` |
| Capability | `capability add/surface/observe/consume/consume-receipts/list/import-topology/import-skills` | canonical; topology import maps normalized topology YAML or generated resolver JSON into CapabilityGraph nodes + fallback edges; skill-manifest import maps normalized manifests or generated skill-manifest JSON into the same graph, records harness compatibility edges, and only offers surfaced nodes; close/session-end auto-consume receipts when graph state exists; manual receipt consumption replays the same idempotent path; completed changed `skills.used` becomes `validated_by`, explicit `skills.missed` becomes `missed_when`, explicit `skills.false_triggered` becomes `false_triggered_by`, offers stay inert |
| Runtime | `runtime doctor <runtime-binding.yaml> [--writeback]`; `runtime doctor --project-contract <project.yaml> [--writeback]` | canonical first slice + infra-os project-contract adapter; failing reports can enter work -> gate -> receipt/transition |
| Projection | `projection doctor <freshness.yaml> [--writeback] [--source-observe]`; `projection repair-plan <freshness.yaml>` | canonical first slice; stale/drifted observations can enter work -> gate -> receipt/transition and/or SourceObservation -> candidate/no-promote; repair-plan writes owner-review regeneration artifacts and never mutates generated views |
| Product | `product import <product.yaml>`; `product signal <signal.yaml\|source-export.yaml>`; `product signal-doctor <signal.yaml\|source-export.yaml> [--record]`; `product review <review.yaml>`; `product source-issue-review <review.yaml>`; `product timeline <product_id>`; `product doctor <product_id> [--writeback]` | canonical first product feedback-loop slice; imports product identity, deterministic claims, standalone metric-source signal evidence, source-export metric evidence, parked hypotheses; signal/review/source-issue-review imports emit product Transition projections; invalid signal source contracts can enter `NoPromoteDecision` through `signal-doctor --record`; timeline joins product signals, hypotheses, transition deltas, and open/closed source issues without declaring causality; pending hypothesis review or open product source issues can enter work -> gate -> receipt/transition; review results update the same parked hypothesis with evidence; source-issue reviews close source issues while preserving the original no-promote evidence |
| Writeback | `writeback gate <gate.json> --policy <policy-decision.json> [--record]`; `writeback no-promote --candidate C --basis B --evidence E1[,E2]` | canonical first slice |
| Importers | `authority import [registry.yaml]`; `authority path <path-authority.yaml>`; `authority path-registry <os-path-authority.yaml>`; `authority resolve <token>`; `decisions import --brain <markdown>`; `decisions import --policy <DEC.md>`; `decisions import --policy-dir <dir>`; `decisions doctor [--writeback]`; `decisions source-doctor --template <md> [--format-doc <md>]`; `decisions repair-plan --policy-dir <dir>`; `decisions repair-apply --plan <json> [--write-reviewed]`; `source observe <source-observation.yaml>` | canonical import/report surfaces; source/path authority imports normalized and OS-style path registries, resolving moved paths to stable entities while stale/retired roots become `known_path_*` claims, not `current_path`; brain and policy markdown decisions import as EvidenceItem + DecisionLifecycle; policy batch imports use the same evidence-backed adapter; decision lifecycle gaps can enter work -> gate -> receipt/transition, source format debt can be detected before new records are created, repair artifacts can be produced for owner review, and reviewed lifecycle fields apply only by explicit write; source observations enter inbox/no-promote without direct promotion |
| Events | `event import-os <events.ndjson> [--limit N] [--stream NAME]` | importer; adapts OS-style append-only rows into `TraceEnvelope` evidence under `COMPOUND_HOME/events`; replay skips existing traces; no claims ledger is written |
| Gen-1 tombstones | `distill`, `scope`, `promote`, `build`, `reconcile`, top-level `timeline`, top-level `recall`, `pack`, `ledger` | retired; exit `2` replacement hint |

Intentionally no alias layer. Alias layers keep obsolete surfaces alive and recreate fragmentation.

## Architecture Doctor Checks

- candidate-map integrity: every candidate kind has exactly one created inbox dir and consumer;
- package classification totality: every `internal/` directory is classified, and every classified package exists;
- public-doc leak gate: generic path/email leaks plus private leak-token list;
- convergence ratchet: `converge-pending` packages must not exceed budget, now fixed at `0`.

## Next Coverage Moves

Recently closed: source-specific policy markdown decisions now enter through `DecisionLifecycle` owner without flattening lifecycle transitions into loose claims; batch policy imports use the same evidence-backed adapter; incomplete accepted source records park and surface writeback debt instead of becoming ambient truth; lifecycle repair plans turn gaps into owner-review artifacts; reviewed repair application lets parity debt decrease on a reviewed source tree without weakening the promotion gate. Current hardening adds source-format doctor so repaired decision lifecycle debt cannot be recreated by stale templates/docs.

Next work is no longer memory-stack convergence. It is adapting existing operating-system functions into first-class Compound contracts without creating parallel owners:

- runtime/service ownership -> `RuntimeBinding` / `ServiceContract` first slice + infra-os project-contract adapter landed opt-in `--writeback` route through `OwnerWritebackGate`, receipt, transition; next hardening broader private parity fixtures owner-regeneration adapter;
- decisions -> `DecisionLifecycle` first slice landed; `decisions import --brain <markdown>` imports curated markdown decision pages as EvidenceItem + DecisionLifecycle; `decisions import --policy <DEC.md>` and `--policy-dir <dir>` import policy markdown through the same evidence-backed lifecycle adapter; accepted decisions require falsifier + review trigger before promotion; incomplete accepted records park; `decisions doctor --writeback` routes lifecycle gaps through `OwnerWritebackGate`, receipt, transition; `decisions source-doctor --template <md> [--format-doc <md>]` verifies templates/docs keep lifecycle fields visible; `decisions repair-plan --policy-dir <dir>` writes JSON + Markdown owner-review artifacts under `COMPOUND_HOME`; `decisions repair-apply --plan <json> --write-reviewed` applies reviewed lifecycle fields; next hardening is broader source parity/regression fixtures without weakening the promotion gate;
- generated views -> `GeneratedViewObservation` / `ProjectionFreshness` first slice landed hash-based doctor opt-in `--writeback` route through `OwnerWritebackGate`, receipt, transition; `projection doctor --source-observe` routes stale/drifted freshness into SourceObservation -> method_upgrade candidate -> `NoPromoteDecision`; `projection repair-plan` consumes gate + source-observation evidence into reviewable regeneration artifacts under `COMPOUND_HOME`; source observations map generated/source views into candidate inbox + `NoPromoteDecision`; distiller duplicate memory outputs route to `NoPromoteDecision`; next hardening private regeneration parity and tool/MCP topology observations;
- source documents -> source adapters emit evidence/candidates/no-promote without direct promotion;
- source-map / path-authority -> `authority path` imports normalized `PathAuthority`; `authority path-registry` adapts OS-style canonical/stale root registries into the same entity identifiers + authority claims; `authority resolve` proves old path/new path/source remote map to one entity while current authority remains status-aware;
- orientation scope -> project-aware context loading bridged with fixture repo markers; `compound orient` excludes governance context in product repos and includes it in control-plane repos, recording exclusions; it also consumes harness-compatible `CapabilityGraph` offers into ContextPack included refs without executing them; `hook session-start` now persists the redacted request, same pack, and exact injected markdown under `COMPOUND_HOME/context`;
- hook work admission -> `hook user-prompt-submit` persists redacted request + context pack + exact injection + proposed WorkEnvelope tied to `context_pack_id` when a work store is available; hook calls persist normalized HookEvents without raw prompt bodies; package proof closes that work through `session-end` and asserts Receipt/Transition retain the same context pack in ids and `before_refs`; `distiller request --work-id` can turn that closed runtime chain plus matching HookEvents into candidate-only extraction source material;
- product feedback -> `product import` registers product identity, promoted deterministic product claims, `ProductSignal` evidence rows, and parked `ProductHypothesis` candidates; `product signal` maps normalized signal docs and source-export analytics-shaped metric documents into the same `ProductSignal` ledger with EvidenceItem refs and a product Transition projection; `product signal-doctor --record` records invalid source-export shapes as no-promote rows instead of silently ignoring provider fields; `product timeline` joins signals, hypotheses, product transition deltas, and open/closed product-scoped source issues without declaring causality; `product doctor --writeback` routes pending hypothesis review and open product source issues through governed owner writeback; `product review` records a review EvidenceItem, updates the same hypothesis candidate result without promotion, and emits a product Transition projection; `product source-issue-review` records closure evidence, preserves the original no-promote issue, and removes the issue from open doctor/writeback debt;
- rented retrieval -> `MemoryEngine` protocol + `memory engine-doctor` prove lexical/reference-sidecar breadth engines leave ledger bytes unchanged; `memory engine-compare` compares a configured sidecar against the owned lexical reference so legacy/vector adapters must prove swap parity before adoption; first read-only external memory CLI sidecar landed behind the same protocol and uses the external store only as expansion context; next hardening direct legacy/vector adapter parity if it earns adoption;
- skills/tools/MCPs/hooks -> `CapabilityGraph` imports observations; `compound capability import-topology` imports normalized tool/MCP topology or generated resolver JSON into tool/MCP/adapter nodes and replacement/fallback recommendations; real generated resolver parity imported 33 nodes and 1 fallback edge without executing tools; `compound capability import-skills` imports normalized manifests or generated skill-manifest JSON into the same graph without auto-execution. Real generated skill-manifest parity imported 119 offer nodes and 37 harness compatibility edges; `compound orient` consumed the resulting `capability://procedure:app-store-visuals` and `capability://skill:app-store-screenshots` refs for a product screenshot task while harness filters kept Codex-only and Claude-only skill offers separate. `work close`/`hook session-end` now auto-feed receipts into the graph when graph state exists; `compound capability consume-receipts` replays the same path.

# Kernel Contracts (the lego seams)

Fragmentation happens when seams between pieces are fuzzy: change one and others break. The cure is
a small explicit contract — port + lifecycle + receipt — with implementations swapped behind it.
Defined before implementation so agents stop re-deriving them. Implement a contract only when a
vertical slice needs it (`../../roadmap/phases.md`).

> Public/private rule (`../../BOUNDARIES.md`): the *contract* and generic lifecycle are public. Instances
> carrying personal paths, credentials, accounts, or private bodies live in the private overlay
> (`docs_private/`, gitignored) or an out-of-repo adapter — never a tracked file.

## Envelope-spine contracts (first-order — the runtime grammar)

These are the typed objects every harness/hook/run flows through, hung off one root **`work_id`**. Full JSON
schemas: `../runtime.md`.

| Contract | Purpose | Lifecycle |
|---|---|---|
| **EffectiveContract** | the compiled "what applies right now" artifact every run references by `contract_version = sha256(...)`; kills global instruction bloat | compiled-per (harness, cwd, task) |
| **ContextRequest** | what a hook asks compound for | per hook event; `hook session-start` persists a redacted request under `COMPOUND_HOME/context/requests` with `intent_text_ref` as hash-only, never raw prompt |
| **ContextPack** | frozen context an agent consumes; records included AND **excluded** refs + reasons → enables replacement | immutable per work/session. `compound orient` appends surfaced `CapabilityGraph` offers as generated included refs when graph state exists, filtered by `surfaces_with -> harness:*` compatibility when present; offers are not execution. Hooks persist the pack and exact markdown injection under `COMPOUND_HOME/context`; session close copies capability offers into Receipt `skills.candidate` and Transition `projections.skill` as provenance, not usage. Fixture product repo excludes control-plane governance refs, fixture control-plane repo includes them. |
| **WorkEnvelope** | the one root identity for a unit of work (carries the JobUnit fields: desired_outcome, success_predicate, non_goals, risk + the id chain) | proposed → admitted → ready → claimed → running → verifying → completed \| held \| blocked \| failed \| cancelled. `hook user-prompt-submit` persists proposed work with `context_pack_id` when a work store is available. |
| **ActionIntent** | a proposed mutation, pre-policy (capability, target, mutation_class, reversibility, blast_radius, rollback_plan) | per proposed action |
| **PolicyDecision** | the governance verdict an adapter **enforces** (allow \| allow_with_obligations \| hold \| deny + obligations) | per ActionIntent |
| **StandingGrant** | a pre-authorized envelope (scope/budget/blast-radius/reversibility) so agents act inside it without paging, escalate on leaving | granted → expires/revoked |
| **HookEvent** | one normalized harness lifecycle event (capability, input_hash, side_effects, status, ids{work_id,context_pack_id}) | append-only; persisted as `events/<hook_event_id>.json` plus `events/hook-events.ndjson` when `COMPOUND_HOME` is available |
| **Receipt** | how a run is learned from — proportional to risk/blast-radius (state_delta, `delta_cause`, cost, skills, memory, `next_consumer`, `opens_branches[]`, `move_type`) | append-only; mined. `skills.used` is derived from allowed recorded ActionIntents; `skills.candidate` is derived from ContextPack capability offers; close/session-end auto-consume receipts into CapabilityGraph feedback when graph state already exists. Explicit feedback can be emitted by `work close --missed ... --false-triggered ...` or `hook session-end` fixture fields; unused candidates are never inferred as feedback. |
| **Transition** | the apex object joining projections (`../transition.md`) | append-only; emits second-order signals |

`work_id` is **the** anti-fragmentation object — one root id linking
intent→context→action→policy→run→receipt→transition replaces scattered packet/run/provider/receipt/slug ids.
Governance is enforced at adapters: **a mutating adapter rejects a mutation lacking a valid `PolicyDecision`**.

## Knowledge / domain contracts

| Contract | Purpose | Key fields / lifecycle |
|---|---|---|
| **Entity** | canonical identity that survives path/host moves | id, kind, aliases[], owners[], path_bindings[], source_bindings[], **host_bindings[]**, **deployment_bindings[]**, **credential_bindings[]** (private), evidence_refs[] · candidate → active → merged/retired |
| **RegistryEntry** | one capability/tool/model/product made legible & routable | id, kind, name, owner, scope, **world** (inside\|outside), capabilities[], constraints[], cost_model, risk_tier, trigger_conditions[], I/O contract, evidence_refs[], status, replacement_for |
| **TraceEnvelope** | what actually happened (the ground) | trace_id, source, actor, scope, work_id, times, tools/models_used[], files_touched[], cost{}, outputs[], evidence_refs[], risk_tier, receipt_id · append-only |
| **SourceObservation** | one adapter observation over an existing source or generated view | adapter_id, source_system, source_ref{path, kind, owner, source_class, revision}, generated_view{view_kind, generated_from[], generator, marker, freshness}, maps_to[], evidence_refs[] · append-only evidence/candidate input, never promotion by itself |
| **EvidenceItem** | immutable, citeable raw material (never truth alone) | id, source, occurred_at, body_ref, entity_ref · append-only |
| **Claim / MemoryDelta** | smallest durable statement; carries delta + temperature | claim_id, entity_id, predicate, value, confidence, status, **temperature**, source_trace_ids[], evidence_refs[], last_verified_at, expires_at, contradicts[], supersedes[] · observed → candidate → **rejected** \| parked \| promoted \| operationalized → superseded → retired |
| **Capability / Skill** | a routable affordance (lens/procedure/checker/tool-wrapper/taste/script) | + trigger_conditions[], compatible_harnesses[], **counter_skills[]**, **decay_rule**, **second_order_risks[]**, evidence_score{} · candidate → active → needs_review → deprecated → retired |
| **Tool / MCP** | an external action surface | capabilities[], limitations[], cli_vs_mcp, auth, cost, health, status (discovered→available→served→wired→evaluated→promoted→deprecated) |
| **Model / Provider** | a routed intelligence utility | provider, capabilities[], context_window, cost{in,out,cache}, latency, limits, lane |
| **Harness** | an epistemic environment (NOT just an executor) | context_injection_modes[], hook_events[], **strengths[]**, **blindspots[]**, **failure_signatures[]**, trace_quality, needs_compound_support[] |
| **ExecutionRouteEvidence** | one gateway route decision, imported as evidence (NOT task success) | request_id, work_id, transition_id, client_profile/protocol, selected_target, fallbacks[], rejected[]{target,reason}, usage{}, cost_micros, latency_ms, status, evidence_refs[]; canonical owner `internal/routeevidence`; `compound route import <json>` and local producers write `ledger/execution-route-evidence.jsonl` and join to a Transition `projections.routing`/`projections.cost` ref; metadata-only, no prompt/response bodies or secrets |
| **ProductSignal** | market observation (evidence, not truth) | product_id, surface, change_event, metric_movement, confidence, **confounders[]**, **interpretations[]{lens,claim}**, next_action. First slice: `product import` writes bundled signals; `product signal <signal.yaml\|source-export.yaml>` accepts normalized signal docs and source-export docs (`compound.product_signal_export.v0.1`) for ASC/RevenueCat/PostHog-shaped metrics, maps them into `ledger/product-signals.jsonl` with EvidenceItem refs, and emits Transition `projections.product[]`; `product signal-doctor <yaml> [--record]` strictly checks source shape and records invalid source contracts as `NoPromoteDecision`; `product timeline` joins valid signals, hypotheses, product transitions, and open/closed product-scoped source issues. |
| **ProductSourceIssueReview** | review/closure for invalid product signal source contracts | issue_id, product_id, result `resolved|waived|false_positive`, rationale, evidence_refs[]. `product source-issue-review <review.yaml>` records EvidenceItem, writes `ledger/product-source-issue-reviews.jsonl`, emits product Transition, and makes the issue no longer count as open doctor/writeback debt without deleting the original `NoPromoteDecision`. |
| **ProductHypothesis** | candidate explanation or next review after product/market signal | product_id, change_event, expected_effect, metrics[], observation_window, result, next_action, evidence_refs[]. First slice parks hypotheses in `inbox/product-hypotheses`; `product doctor --writeback` routes feedback review debt (unknown hypotheses or open product-scoped source issues) through `OwnerWritebackGate` Receipt/Transition; `product review <review.yaml>` records review evidence, updates the same candidate to supported/refuted/inconclusive, and emits Transition `projections.product[]`. It does **not** declare causality, promote truth, or mutate product surfaces. |
| **Product** | a market-control surface (NOT a repo summary) | product_id, **surfaces[]**, **channels[]**, positioning, **taste_profile**{examples,anti_examples,standards,red_lines,review_lenses}, privacy_posture, current_hypotheses[]. First slice registers the product entity and promotes deterministic product claims only. |
| **Decision** | a committed choice (the durable artifact) | choice, rationale, **falsifier**, **review_trigger**, evidence_refs[], status |
| **AutonomyEnvelope** | a trust gradient, not a boolean | allowed_without_approval[], requires_approval[], max_cost_usd, max_runtime, required_receipts, **branch_sandbox** |
| **MemoryEngine** | rented breadth index — **bridge/index, never authority** | protocol lives with spine memory (`internal/mem/protocol.go`); target port: `Query/Recall/PutProjection/Build/Health/ExplainResult`. A vector/fact store is one implementation; arrow is **compound → materialized objects → engine index**, never engine → truth → compound reads. Engine never owns promotion/visibility/supersession/source-class/privacy. First proof: `memory engine-doctor` hashes `COMPOUND_HOME/ledger` before/after a breadth query and fails if a rented engine mutates it. Swap proof: `memory engine-compare` compares a configured sidecar against the owned lexical reference on hydrated claim ids while keeping ledger bytes unchanged. When an adapter wraps an existing store, distinguish its **durable** parts (facts, source-metadata, page-versions — preserve) from **rebuildable** parts (chunks, embeddings, search — regenerate). |

## Distillation & capability-growth contracts (the compounding layer — P6 continuation + P8)

These turn traces into *adjudicated, versioned* knowledge and grow capability from evidence. They **extend**
the candidate loop and the `Claim` / `Capability` / `RegistryEntry` rows above — they do not replace them.
Defined here; implemented when a slice needs them (the candidate fan-out + as-of recall already exist in
`internal/mem`). Invariant throughout: **model extracts, code decides; no candidate reaches ambient recall
unadjudicated; distillation proposes candidates only.**

`LLMExtractionRequest` is the provider-neutral request packet. It carries bounded raw trace text,
prior-state coverage, source refs, typed summaries of ContextPack, WorkEnvelope, Receipt, explicit
`capability_feedback[]` derived from `Receipt.skills.missed` / `Receipt.skills.false_triggered`
(never `skills.candidate` offers), and extra spine envelopes. It can be assembled from explicit typed files or from a closed
`COMPOUND_HOME` work chain via `distiller request --work-id <work_id>`, including matching
HookEvents from `events/hook-events.ndjson`; it is prompt material only, not a model call and not
authority.

`LLMExtractionResponse` is the provider-neutral model-output adapter for TraceDistiller. It carries
`schema_version`, `trace_ref`, `source_refs[]`, and `candidates[]`; code normalizes it into `Trace`.
Unknown kind, missing confidence, malformed JSON, or model-supplied `promotion_basis` is invalid.
Missing candidate evidence inherits `source_refs[]` or `trace_ref`; model output still cannot promote
memory directly.

| Contract | Purpose | Key fields / invariant |
|---|---|---|
| **TraceDistiller** | turns spine artifacts (TraceEnvelope + Receipt + ContextPack + HookEvents + ExecutionRouteEvidence + raw refs) into a CandidateSet — extraction only, never canon | loads RetrievedPriorState first; each output cites `evidence_refs[]` + `distillation_policy_version` + a stable deterministic id (replay ⇒ same ids) |
| **RetrievedPriorState** | the canon the distiller compares against *before* extracting (distill-against-canon, not blind) | current claims by type; decision/status/preference/architecture/constraint claims; candidate inboxes for failure/taste/product/capability/eval/method/policy; capability nodes; no-promote decisions; current policy version |
| **CandidateSet** | the typed fan-out (all candidate-first) | MemoryCandidate · SkillCandidate · DecisionCandidate · FailurePattern · TasteObservation · ProductHypothesis · CapabilityObservation · RouteObservation · MethodUpgradeCandidate · DistillationPolicyPatchCandidate · EvalCaseCandidate |
| **DistillationPolicy** | the *versioned* method that produced a candidate — so a method change is legible, not silent prompt drift | id, version, lenses[], thresholds, prompt_refs; changed only via an adjudicated DistillationPolicyPatchCandidate |
| **DistillationCritique** | the critic verdict per candidate, before adjudication | new? · supersedes? · contradicts? · duplicate? · lower_quality_than_canon? · better_procedure? · become_skill_rule_eval? · park? · **exposes_method_failure?** |
| **EvalCase** | frozen request→response→expected-adjudication fixture; distiller's own regression corpus before live model lanes | schema_version, id, seed_traces[], request{trace_ref, raw, context_pack, work, receipt, extra_envelopes[]}, response, expect{source_refs[], prior_coverage{}, decisions[], current_claims, live_model_calls, error_contains}. Expected extractor errors are first-class eval outcomes; malformed/adversarial model output must fail with zero promoted claims and zero live model calls. Corpus directories execute sorted `*-eval.json` cases only, so response fixtures can live beside cases without becoming executable cases. |
| **EvalCorpusReport** | directory-level distiller gate before live provider lanes | dir, status, case_count, pass_count, fail_count, decision_count, current_claims, live_model_calls, reports[]. A live lane is not ready if corpus status fails or live_model_calls is nonzero. |
| **OpenAICompatibleExtraction** | rented model-lane adapter, not a router or authority source | consumes `LLMExtractionRequest`, requires a passing `EvalCorpusReport` before any HTTP call, posts to a caller-supplied OpenAI-compatible `/chat/completions` endpoint, validates returned `LLMExtractionResponse` through `LLMExtractor`, and reports provider usage through `internal/routeevidence`. It does not mutate provider/gateway config, does not promote memory, and emits metadata-only `ExecutionRouteEvidence` plus an audit-only Transition routing projection. |
| **AdjudicationDecision** | the durable verdict a candidate receives | candidate_ref, verdict (reject \| park \| promote \| operationalize \| retire), basis, evidence_refs[], policy_version, optional no_promote_id for duplicate anti-churn |
| **DistillerDoctorReport** | read model over distiller compounding state, never a writer | adjudication counts, memory verdicts, eval corpus size, method/policy debt, capability observations, duplicate/no-promote churn; method/policy debt can enter `OwnerWritebackGate` |
| **CapabilityGraph** | the typed graph over `Capability` / `RegistryEntry` nodes (P8) | node kinds (lens/procedure/script/tool/mcp/model_profile/harness/eval/workflow/adapter/doctrine/taste_standard) + edges (surfaces_with/requires/conflicts_with/specializes/fallback_for/validated_by/**missed_when**/**false_triggered_by**/retired_by); `ToolTopologyAdapter` imports normalized topology YAML or generated resolver JSON into tool/MCP/adapter nodes + replacement/fallback recommendations; `SkillManifestAdapter` imports normalized manifests or generated skill-manifest JSON into skill/procedure/lens nodes; receipt consumption mines completed, changed `Receipt.skills.used` into `validated_by` edges, explicit `Receipt.skills.missed` into `missed_when` context edges, and explicit `Receipt.skills.false_triggered` into `false_triggered_by` context edges while ignoring `skills.candidate` offers; `work close` / `hook session-end` trigger this automatically when graph state exists, and `capability consume-receipts` replays it manually; **auto-surface, never auto-execute** |

`SkillManifestAdapter` is the skill-import seam for this graph: it imports strict versioned skill/lens/procedure manifests and read-only generated skill-manifest JSON into `CapabilityGraph`, records compatible harnesses as graph edges, treats needs_review/deprecated/retired as non-surfacing, and never runs imported procedures. Generated skill views are normalized into the same manifest contract as evidence-backed offers; they do not become a second skill registry.

The self-improving loop is what makes this *compound*: the distiller's own misses/false-triggers and a
capability's `missed_when` / `false_triggered_by` edges become evidence that patches the next
DistillationPolicy and re-weights surfacing — repeated success becomes infrastructure, repeated failure
becomes a rule, **only after evidence** (Laws 7/8).

Operationalized `capability_observation` candidates are the distiller/critic feedback seam into CapabilityGraph. `distiller run` consumes them automatically only when graph state already exists; `capability consume` remains the manual replay/repair command. A distiller run never creates graph state by itself and never executes capabilities.

## Authority & governance contracts (the under-modeled organs — Axis B)

These are the **inside-world registry and governance** seams that plain `Claim`s under-model
once agents *act* on infra, decisions, and writeback. Generic and public — no private owner
names (the named OS→contract mapping lives in `docs_private/plan/os-coverage-matrix.md`).

| Contract | Purpose | Key fields / lifecycle |
|---|---|---|
| **RuntimeBinding** | where thing actually runs (so autonomy knows what is real/production/reversible) | service_id, owner_entity, host_binding, deployment_binding, data_bindings[], credential_refs[] (private), health_check_refs[], risk_tier, evidence_refs[], status: candidate\|active\|stale\|retired. First slice: `compound runtime doctor <bundle>`; infra-os adapter: `compound runtime doctor --project-contract <project.yaml>` |
| **ServiceContract** | promised interface/SLA of running service | service_id, interface_refs[], dependencies[], owner_entity, risk_tier, evidence_refs[], status. First slice pairs with RuntimeBinding in one checked bundle |
| **DecisionLifecycle** | makes `Decision` lifecycle typed auditable instead loose prose on claim | choice, rationale, **falsifier**, **review_trigger**, scope, owner, effective_from, **status: proposed\|accepted\|superseded\|deprecated\|rejected**, replaced_by, evidence_refs[]. Source adapters include `decisions import --brain <markdown>`, records `note` EvidenceItem before adjudicating lifecycle claim. A decision may promote only when accepted **and** lifecycle-required fields are present; incomplete accepted records park as writeback debt. `compound decisions doctor --writeback` routes lifecycle gaps through `OwnerWritebackGate`; `compound decisions source-doctor --template <md> [--format-doc <md>]` verifies source templates/docs keep lifecycle fields visible; `compound decisions repair-plan --policy-dir <dir>` emits reviewable repair artifacts under `COMPOUND_HOME`; `compound decisions repair-apply --plan <json> --write-reviewed` applies reviewed fields explicitly and separately from promotion |
| **OwnerWritebackGate** | candidate → owner target → gate → policy → receipt → transition (writeback is command-shaped, not doctrine) | candidate_ref, owner_target, gate_basis, policy_decision_ref, receipt_ref; a mutating writeback requires a valid `PolicyDecision` |
| **NoPromoteDecision** | an explicit lifecycle state — "seen, deliberately not promoted" — distinct from park/reject | candidate_ref, basis, expiry, evidence_refs[]; prevents the same candidate re-entering the loop as "new" |
| **ProjectionFreshness** | a read model/dashboard cannot silently drift from the ledger | projection_id, source_ledger_seq, built_at, staleness, coverage_verdict, status: fresh\|stale\|drifted. First slice: `compound projection doctor <freshness.yaml>` |
| **SourceAuthority / PathAuthority** | which source owns a fact, and identity that survives path/host moves | entity_ref, authoritative_source, path_bindings[], host_bindings[], precedence_rank, evidence_refs[]. First slice: `compound authority path <path-authority.yaml>` merges source/path bindings into the canonical entity registry and emits deterministic authority claims; `compound authority resolve <token>` proves old/new paths and source remotes resolve to one entity. |

DecisionLifecycle adapter note: `decisions import --policy <DEC.md>` and `decisions import --policy-dir <dir>` record policy markdown source evidence, preserve `supersedes` / `replaced_by` lifecycle transitions, promote only accepted decisions with complete lifecycle proof, and park incomplete or non-accepted decisions so they cannot become ambient truth by accident. Compatibility directory inputs must route through this same adapter, not a candidate-only parser. Each decision uses its own stable entity id (`decision:<id>`) so unrelated accepted decisions do not supersede one another. `decisions source-doctor --template <md> [--format-doc <md>]` prevents repaired lifecycle debt from regenerating in new source records; `decisions repair-plan --policy-dir <dir>` converts lifecycle gaps into owner-review artifacts; `decisions repair-apply --plan <json> --write-reviewed` applies reviewed fields but does not promote them.

### Generated views

`GeneratedViewObservation` is the contract for generated instruction/dashboard/registry
views. It records `view_ref`, `source_refs[]`, `output_hash`, `source_hashes[]`,
`generator`, `consumer`, `marker_status`, `freshness`, and the object family it maps
to. It may create `ProjectionFreshness`, evidence, or a parked/no_promote candidate.
It cannot promote truth by itself.

Build order (most likely to let fragmentation back in first): **OwnerWritebackGate/NoPromoteDecision** first slice landed; `projection doctor --writeback`, `runtime doctor --writeback`, and `decisions doctor --writeback` consume it for stale/drifted generated views, failing runtime reports, and decision lifecycle gaps. `distiller run` now records duplicate memory outputs as idempotent `NoPromoteDecision` rows instead of letting them re-enter as new work. `source observe` maps generated/source observations into candidate inboxes plus explicit no-promote rows; it observes existing owners and cannot promote truth. `capability import-topology` maps observed tool/MCP topology or generated resolver JSON into CapabilityGraph nodes and fallback edges; `capability import-skills` maps normalized manifests or generated skill-manifest JSON into CapabilityGraph offers; work/session closure auto-closes the runtime feedback loop when graph state exists, and `capability consume-receipts` replays the same idempotent path. None of these commands execute tools or skills.
Each is **defined here before implementation** so agents stop re-deriving them; each is built
only when a vertical slice needs it (`../../roadmap/phases.md`).

## Entity resolution — the precedence order (the root of anti-fragmentation)

`NotesApp` / `notes-app` / a bundle id / "that app with the metrics" must collapse to **one** entity:

```text
1. explicit compound entity id
2. registry alias
3. repo / app / product fingerprint (remote, bundle id, package name, deploy target)
4. scope-resolver match ABOVE a confidence threshold
5. otherwise → a NEW candidate entity, PARKED until reviewed
```

An **uncertain** match creates **parked evidence only — never a promoted claim.**

## Shared fields that bend behavior

- **`source_class`** (on the universal header) — `source | generated | evidence | review | runtime |
  private_overlay`. Prevents *retrieved ≠ relevant ≠ authoritative ≠ current*. Only some classes are ambient.
- **`second_order_risks[]`** — every affordance is an attractor; declare what it makes more likely and can
  fossilize/flatten (`../transition.md`). Selection is ensemble-aware, not lookup.
- **`temperature` (memory) / `move_type` (receipts)** — make Law 5 and Law 15 measurable. Canonical
  `move_type` enum: `outward_delta | memory_delta | rule_delta | control_plane_repair | audit_only`. A
  `control_plane_repair` only counts when it removes a duplicate owner, fixes a recurring failure, or creates
  runtime proof — otherwise `audit_only`, which does not count.

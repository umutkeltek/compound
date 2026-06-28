# Kernel Contracts (the lego seams)

Fragmentation happens when the seams between pieces are fuzzy — change one and others break. The cure is the
one already proven by `MemoryEngine` (`internal/engine/engine.go`): **every fuzzy subsystem becomes a small
explicit contract — a port + a lifecycle + a receipt — and implementations swap behind it.** Defined here
before implementation so agents stop re-deriving them. Implement a contract only when a vertical slice needs
it (`../../roadmap/phases.md`).

> Public/private rule (`../../BOUNDARIES.md`): the *contract* and generic lifecycle are public. Instances
> carrying personal paths, credentials, accounts, or private bodies live in the private overlay
> (`docs_private/`, gitignored) or an out-of-repo adapter — never a tracked file.

## Envelope-spine contracts (first-order — the runtime grammar)

These are the typed objects every harness/hook/run flows through, hung off one root **`work_id`**. Full JSON
schemas: `../runtime.md`.

| Contract | Purpose | Lifecycle |
|---|---|---|
| **EffectiveContract** | the compiled "what applies right now" artifact every run references by `contract_version = sha256(...)`; kills global instruction bloat | compiled-per (harness, cwd, task) |
| **ContextRequest** | what a hook asks compound for | per hook event |
| **ContextPack** | frozen context an agent consumes; records included AND **excluded** refs + reasons → enables replacement | immutable per work/session |
| **WorkEnvelope** | the one root identity for a unit of work (carries the JobUnit fields: desired_outcome, success_predicate, non_goals, risk + the id chain) | proposed → admitted → ready → claimed → running → verifying → completed \| held \| blocked \| failed \| cancelled |
| **ActionIntent** | a proposed mutation, pre-policy (capability, target, mutation_class, reversibility, blast_radius, rollback_plan) | per proposed action |
| **PolicyDecision** | the governance verdict an adapter **enforces** (allow \| allow_with_obligations \| hold \| deny + obligations) | per ActionIntent |
| **StandingGrant** | a pre-authorized envelope (scope/budget/blast-radius/reversibility) so agents act inside it without paging, escalate on leaving | granted → expires/revoked |
| **HookEvent** | one normalized harness lifecycle event (capability, input_hash, side_effects, status) | append-only |
| **Receipt** | how a run is learned from — proportional to risk/blast-radius (state_delta, `delta_cause`, cost, skills, memory, `next_consumer`, `opens_branches[]`, `move_type`) | append-only; mined |
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
| **EvidenceItem** | immutable, citeable raw material (never truth alone) | id, source, occurred_at, body_ref, entity_ref · append-only |
| **Claim / MemoryDelta** | smallest durable statement; carries delta + temperature | claim_id, entity_id, predicate, value, confidence, status, **temperature**, source_trace_ids[], evidence_refs[], last_verified_at, expires_at, contradicts[], supersedes[] · observed → candidate → **rejected** \| parked \| promoted \| operationalized → superseded → retired |
| **Capability / Skill** | a routable affordance (lens/procedure/checker/tool-wrapper/taste/script) | + trigger_conditions[], compatible_harnesses[], **counter_skills[]**, **decay_rule**, **second_order_risks[]**, evidence_score{} · candidate → active → needs_review → deprecated → retired |
| **Tool / MCP** | an external action surface | capabilities[], limitations[], cli_vs_mcp, auth, cost, health, status (discovered→available→served→wired→evaluated→promoted→deprecated) |
| **Model / Provider** | a routed intelligence utility | provider, capabilities[], context_window, cost{in,out,cache}, latency, limits, lane |
| **Harness** | an epistemic environment (NOT just an executor) | context_injection_modes[], hook_events[], **strengths[]**, **blindspots[]**, **failure_signatures[]**, trace_quality, needs_compound_support[] |
| **ExecutionRouteEvidence** | one gateway route decision, imported as evidence (NOT task success) | request_id, work_id, transition_id, client_profile/protocol, selected_target, fallbacks[], rejected[]{target,reason}, usage{}, cost_micros, latency_ms, status (metadata-only) |
| **ProductSignal** | a market observation (evidence, not truth) | product_id, surface, change_event, metric_movement, confidence, **confounders[]**, **interpretations[]{lens,claim}**, next_action |
| **Product** | a market-control surface (NOT a repo summary) | product_id, **surfaces[]**, **channels[]**, positioning, **taste_profile**{examples,anti_examples,standards,red_lines,review_lenses}, privacy_posture, current_hypotheses[] |
| **Hypothesis** | a testable product belief | change_event, expected_effect, metrics[], observation_window, result (unknown\|supported\|refuted\|inconclusive), next_action |
| **Decision** | a committed choice (the durable artifact) | choice, rationale, **falsifier**, **review_trigger**, evidence_refs[], status |
| **AutonomyEnvelope** | a trust gradient, not a boolean | allowed_without_approval[], requires_approval[], max_cost_usd, max_runtime, required_receipts, **branch_sandbox** |
| **MemoryEngine** | rented breadth index — a **bridge/index, never authority** | current: `Capabilities/Query/Recall/Put` (`internal/engine/engine.go`); target port: `Query/Recall/PutProjection/Build/Health/ExplainResult`. A legacy vector/fact store is one implementation; the arrow is **compound → materialized objects → engine index**, never engine → truth → compound reads. The engine never owns promotion/visibility/supersession/source-class/privacy. When an adapter wraps a legacy store, distinguish its **durable** parts (facts, source-metadata, page-versions — preserve) from its **rebuildable** parts (chunks, embeddings, search — regenerate). |

## Distillation & capability-growth contracts (the compounding layer — P6 continuation + P8)

These turn traces into *adjudicated, versioned* knowledge and grow capability from evidence. They **extend**
the candidate loop and the `Claim` / `Capability` / `RegistryEntry` rows above — they do not replace them.
Defined here; implemented when a slice needs them (the candidate fan-out + as-of recall already exist in
`internal/mem`). Invariant throughout: **model extracts, code decides; no candidate reaches ambient recall
unadjudicated; distillation proposes candidates only.**

| Contract | Purpose | Key fields / invariant |
|---|---|---|
| **TraceDistiller** | turns spine artifacts (TraceEnvelope + Receipt + ContextPack + HookEvents + ExecutionRouteEvidence + raw refs) into a CandidateSet — extraction only, never canon | loads RetrievedPriorState first; each output cites `evidence_refs[]` + `distillation_policy_version` + a stable deterministic id (replay ⇒ same ids) |
| **RetrievedPriorState** | the canon the distiller compares against *before* extracting (distill-against-canon, not blind) | active + superseded claims, open decisions, failure patterns, capability nodes, taste standards, hypotheses, prior false-pos/neg, current policy version |
| **CandidateSet** | the typed fan-out (all candidate-first) | MemoryCandidate · SkillCandidate · DecisionCandidate · FailurePattern · TasteObservation · ProductHypothesis · CapabilityObservation · RouteObservation · MethodUpgradeCandidate · DistillationPolicyPatchCandidate · EvalCaseCandidate |
| **DistillationPolicy** | the *versioned* method that produced a candidate — so a method change is legible, not silent prompt drift | id, version, lenses[], thresholds, prompt_refs; changed only via an adjudicated DistillationPolicyPatchCandidate |
| **DistillationCritique** | the critic verdict per candidate, before adjudication | new? · supersedes? · contradicts? · duplicate? · lower_quality_than_canon? · better_procedure? · become_skill_rule_eval? · park? · **exposes_method_failure?** |
| **EvalCase** | a frozen distiller input→expected-output, born from a distiller mistake — the distiller's own regression corpus | id, trace_ref, expected, actual, regression_for_policy_version |
| **AdjudicationDecision** | the durable verdict a candidate receives | candidate_ref, verdict (reject \| park \| promote \| operationalize \| retire), basis, evidence_refs[], policy_version |
| **CapabilityGraph** | the typed graph over `Capability` / `RegistryEntry` nodes (P8) | node kinds (lens/procedure/script/tool/mcp/model_profile/harness/eval/workflow/adapter/doctrine/taste_standard) + edges (surfaces_with/requires/conflicts_with/specializes/fallback_for/validated_by/**missed_when**/**false_triggered_by**/retired_by); **auto-surface, never auto-execute** |

The self-improving loop is what makes this *compound*: the distiller's own misses/false-triggers and a
capability's `missed_when` / `false_triggered_by` edges become evidence that patches the next
DistillationPolicy and re-weights surfacing — repeated success becomes infrastructure, repeated failure
becomes a rule, **only after evidence** (Laws 7/8).

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

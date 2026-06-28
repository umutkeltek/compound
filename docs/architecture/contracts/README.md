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
| **Receipt** | how a run is learned from — proportional to risk/blast-radius (state_delta, cost, skills, memory, next, `move_type`) | append-only; mined |
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

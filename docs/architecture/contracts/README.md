# Kernel Contracts (the lego seams)

Fragmentation happens when the seams between pieces are fuzzy — change one piece and others
break. The cure is the same one already proven by `MemoryEngine` (`internal/engine/engine.go`):
**every fuzzy subsystem becomes a small explicit contract — a port + a lifecycle + a receipt —
and implementations swap behind it.** Not a doc, not a prompt, not a vibe. A contract.

Each contract below names its **purpose**, **key fields**, **lifecycle**, and **public/private
boundary**. They are defined here before being implemented so agents stop re-deriving them.
Implement a contract only when a vertical slice needs it (see `../../roadmap/phases.md`).

> Public/private rule (see `../BOUNDARIES.md`): the *contract* and generic lifecycle are public.
> Any instance carrying personal paths, credentials, accounts, or private bodies lives in the
> private overlay (`docs_private/`, gitignored) or an out-of-repo private adapter.

| Contract | Purpose | Key fields | Lifecycle |
|---|---|---|---|
| **Entity** | canonical identity that survives path/alias moves | id, kind, aliases[], owners[], path_bindings[], source_bindings[], evidence_refs[] | candidate → active → merged/retired |
| **RegistryEntry** | one capability/tool/model/product/etc. made legible & routable | id, kind, name, owner, scope, capabilities[], constraints[], cost_model, risk_tier, trigger_conditions[], input/output_contract, evidence_refs[], status, replacement_for | candidate → active → deprecated → retired |
| **TraceEnvelope** | what actually happened (the ground / system reality) | trace_id, source, actor, scope, job_id, started/ended_at, inputs[], tools_used[], models_used[], files_touched[], cost{}, outputs[], evidence_refs[], risk_tier, receipt_id | append-only |
| **EvidenceItem** | immutable, citeable raw material (never truth alone) | id, source, occurred_at, body_ref, entity_ref | append-only |
| **Claim / MemoryDelta** | smallest durable statement; carries delta | claim_id, entity_id, predicate, value, confidence, status, temperature, source_trace_ids[], evidence_refs[], last_verified_at, expires_at, contradicts[], supersedes[] | observed → candidate → parked \| promoted \| operationalized → superseded → retired |
| **Transition** | a state change + its projections (the core primitive) | see `../transition.md` | append-only; may emit second-order signals |
| **JobUnit** | smallest verifiable unit of work | job_id, intent, scope, desired_outcome, success_predicate, non_goals[], risk_tier, allowed/forbidden_actions[], required_evidence[], budget{}, routing_constraints{} | queued → routed → executed → evidenced → closed |
| **Capability / Skill** | a routable affordance (lens/procedure/checker/tool-wrapper/taste/script) | skill_id, kind, source{origin,url,commit,author}, trigger_conditions[], required_tools[], compatible_harnesses[], compatible_models[], input/output_contract, risk_tier, **counter_skills[]**, **decay_rule**, **second_order_risks[]**, evidence_score{} | candidate → active → needs_review → deprecated → retired |
| **Tool / MCP** | an external action surface | id, kind, capabilities[], limitations[], cli_vs_mcp, auth_requirements[], cost_model, health | active \| degraded \| retired |
| **Model / Provider** | a routed intelligence utility | id, provider, capabilities[], context_window, cost{in,out,cache}, latency, limits, lane | active \| deprecated |
| **Harness** | an epistemic environment (NOT just an executor) | id, context_injection_modes[], hook_events[], discovery, **strengths[]**, **blindspots[]**, **failure_signatures[]**, trace_quality, needs_compound_support[] | active |
| **Receipt** | how the system learns from a run | receipt_id, run_id, surface, **move_type** (outward/memory/rule/control_plane_repair/audit_only), state_delta, evidence_refs[], repeated_failure_key, owner, cost{}, tier (0 heartbeat / 1 state / 2 learning / 3 governance) | append-only; mined |
| **ProductSignal** | a market observation (evidence, not truth) | signal_id, product_id, surface, change_event, metric_movement, confidence, **confounders[]**, **interpretations[]{lens,claim}**, next_action | append-only |
| **Hypothesis** | a testable product belief | hypothesis_id, product_id, change_event, expected_effect, metrics[], observation_window, result (unknown\|supported\|refuted\|inconclusive), next_action | open → resolved |
| **Decision** | a committed choice (the durable artifact) | decision_id, scope, choice, rationale, **falsifier**, **review_trigger**, evidence_refs[], status | open → reviewed → superseded |
| **AutonomyEnvelope** | a trust gradient, not a boolean | scope, allowed_without_approval[], requires_approval[], max_cost_usd, max_runtime, required_receipts, **branch_sandbox** | per-run |
| **MemoryEngine** | rented breadth index (already implemented) | `Capabilities/Query/Recall/Put` (see `internal/engine/engine.go`) | swappable view |

## Two new fields that most contracts share (and why)

- **`second_order_risks[]` / behavioral metadata** — every affordance is an attractor. Skills,
  registries, and routes must declare what they make *more likely* and what they can fossilize or
  flatten (`../transition.md`). Lookup-based selection is replaced by ensemble-aware selection.
- **`temperature` (memory) / `move_type` (receipts)** — make Law 5 (recall only what's relevant)
  and Law 15 (control plane earns its keep) *measurable* before any fancy meter exists.

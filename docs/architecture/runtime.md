# Runtime: the `.compound` home, the envelope spine, and hooks-as-clients

This is how a *running* compound instance works. Three parts: the owned runtime home, the typed envelope
spine, and the thin hook clients. Build this **before** memory ingestion (`../roadmap/phases.md`). One doc on
purpose — fragmenting "generated-state", "envelopes", and "hooks" into three would reproduce the disease.

`compound contract import-hooks --codex-hooks-json <hooks.json>` or `--hook-registration-ts <hook-registration.ts>`
installs proven generated hook rosters as `compound://config/installed-hooks`; after import, `contract compile`
and runtime ContextPacks read the Compound-owned manifest while generated sources remain evidence/rollback input.

`compound policy import-operator-state <operator-state.json>` installs the global autonomy ceiling
`compound://registries/operator-state` under `COMPOUND_HOME`; policy evaluation cites that authority and uses
it as a ceiling, not a bypass for public, spend, destructive, or security holds.

---

## 1. `COMPOUND_HOME` / `.compound` — the runtime body

The repo is **product source**. The running instance owns **state and durable information bodies**, under one
controlled home — never the public repo tree. The custody contract, edit/writeback flow, and restore gates are
owned by [`information-custody.md`](./information-custody.md).

```text
repo            = source code, schemas, docs, tests, fixtures
COMPOUND_HOME   = owned private runtime state + durable bodies (a typed habitat, not a folder pile)
private overlay = ingress bindings, credentials/secret refs, and operator-specific adapter configuration
```

Resolution order: `1) --home <path>  2) $COMPOUND_HOME  3) nearest ancestor .compound/manifest.yaml
4) ~/.compound`. Commands: `compound home init [--global|--local]`, `home doctor`, `home where`.

```text
$COMPOUND_HOME/
  manifest.yaml
  config/       sources/       artifacts/     ledger/        registries/
  inbox/        work/          events/        context/       reviews/
  projections/  indexes/       archives/      tmp/
```

These are the accepted end-state roots, not a claim that every producer has migrated. HabitatCatalog now owns the
closed set: init, placement, doctor, backup, compaction, and restore consume it; unknown top-level entries fail
doctor/backup. Existing feature-shaped roots are explicit non-canonical bridges with destinations and retirement
conditions, and producer path constructors cut over incrementally before bytes move. Cataloged behavior-bearing
exceptions under `projections/` survive backup and compaction until their producers reach the correct typed home;
new behavior-bearing cursors may not be placed there. The detailed responsibilities are in the custody contract.

`SourceManifest` storage follows the habitat meaning: canonical manifests live under
`sources/manifests/`, because they are source-family custody and provenance records, not inside-world registry
authority. `registries/source-manifests/` is an explicit compatibility bridge only. New capture and lifecycle
writes publish to the canonical path. Reads consult the bridge only when the canonical file is absent; a present
but corrupt canonical file fails closed. `manifest-doctor` reports bridge consumption as `degraded` with
`storage_location=pending`, so compatibility cannot look like completed topology migration.

`ExecutionRouteEvidence` records the requested route separately from the observed identity: `selected_target` is the requested lane/alias, while `resolved_provider`, `resolved_model`, `resolution_source`, and (for gateway resolution) `gateway_request_id` identify what actually served the call. Gateway aliases use `resolution_source=gateway_route`; direct OpenAI-compatible lanes may use the response body's exact `model` with `resolution_source=response_body`. Measurements and autonomy evidence MUST attribute to the observed provider/model, never the alias or provider route string.

Typed semantics (the layout *is* the guardrail): `sources/` custody records (including
`sources/manifests/`) · `artifacts/` exact bodies ·
`ledger/` durable meaning · `inbox/` untrusted candidates · `registries/` current typed authority ·
`projections/` rebuildable views · `indexes/` rented · `events/` raw · `work/` execution lifecycle ·
`context/` what agents actually consumed. `compound home doctor` asserts the cataloged root set and reports present
bridge roots; per-family semantic restore and real-consumer replay remain migration gates beyond byte inventory.

`internal/eventstore` is the sole physical-layout owner beneath `COMPOUND_HOME/events`. Its current loose-file
engine preserves the existing bytes and stream semantics behind one replaceable seam. Durable event references
use `compound://events/<id>`; the resolver also accepts bare IDs and historical `events/<id>.json` references,
but rejects traversal and physical segment/index paths. The lifecycle and compatibility streams remain distinct:
`events/work.jsonl`, `events/hook-events.ndjson`, and `events/traces.ndjson` retain their existing contracts while
their physical access stays inside the event-store seam.

---

## 2. The envelope spine (the typed grammar everything runs through)

Current evidence-autonomy status:

- `job_health` is the sole implemented class, not a live production
  consumer. No public path binds a running Director WorkID to a job-health
  Panel target, runs an attested non-fixture gateway, records a trusted judge
  row, and closes that same envelope with a genuine delta.
- Current Panel and arbitrary OpenAI-compatible calls record
  `recorded_via=generic`; public imports record `external_import`.
  `trusted_panel_runtime` is reserved for a future attested adapter and has
  no production caller. Missing legacy provenance is generic for resume,
  never trusted.
- New terminal events seal canonical Work and Receipt hashes. A deciding work
  has exactly one canonical Director trigger, and eligibility
  `generated_at` must equal the admission instant.
- Current `operator:*`/`human:*` checks prove lexical provenance, not
  authenticated identity. Until authenticated operator authority and an
  attested same-WorkID Panel feeder exist, production eligibility has no
  legitimate sample and remains fail closed.

Task meaning is an additive value on the existing spine, not a tenth envelope
or a second task identity. `EffectiveContract` and `ContextPack` may carry one
provider-neutral `TaskContract` (intent, mode, desired outcome, success
predicate, non-goals, proof medium); legacy v0.1 objects without it remain
valid. Dispatch preserves the frozen value and its originating `contract_id`
while CapabilityGraph results remain offers. `panel plan --work-id work_...`
binds a nonterminal parent before any model call, freezes that task contract
into the Panel run, and fixture-only lens/judge evidence records the same
`work_id`. A non-fixture caller and bound apply both fail closed until Work owns
a delegated operation lease spanning provider spend and target mutation; Panel
therefore cannot claim, advance, mutate, or close the parent in this slice. The
parent owner remains the sole terminal Receipt/Transition authority. This path
is still `recorded_via=generic`, not the attested
`trusted_panel_runtime` required for autonomous production eligibility.

Hung off one root `work_id`. The header matters more than any body — it prevents *retrieved ≠ relevant ≠
authoritative ≠ current*.

### Universal header (every envelope begins with this)
```json
{ "schema_version":"compound.<kind>.v0.1", "id":"kind_...", "created_at":"...",
  "producer":{"kind":"hook|cli|daemon|adapter|manual|harness","name":"compound","version":"..."},
  "ids":{"work_id":null,"run_id":null,"session_id":null,"transition_id":null,"context_pack_id":null,"contract_id":null},
  "scope":{"entity_id":null,"project_id":null,"product_id":null,"surface":null},
  "authority":{"source_class":"source|generated|evidence|review|runtime|private_overlay","owner":null,"source_revision":null,"status":"current|stale|disputed|superseded|unknown"},
  "privacy":"public|private|sensitive|secret", "risk":"tier0|tier1|tier2|tier3",
  "evidence_refs":[], "uncertainty":{"level":"known|inferred|suspected|unknown","reason":null} }
```

| Envelope | Role | Key body fields |
|---|---|---|
| **EffectiveContract** | the compiled "what applies right now" artifact every run references by `contract_version = sha256(...)` | active_instructions[], active_hooks[], hook_roster[], hook_readiness[], active_decisions[], memory_policy{ambient/explicit_only/forbidden}, capability_policy{tools_allowed/on_demand/mcps_not_loaded_by_default}, mutation_policy, source_revisions[], unknowns[], conflicts[] |
| **ContextRequest** | what a hook asks compound for | intent_text_ref (hash/redacted), harness, cwd, task_guess, requested_mode (observe\|plan\|execute\|review), constraints{max_chars, allow_private_memory:false, allow_raw_corpus:false, allow_provisional_reviews:false} |
| **ContextPack** | what an agent consumes (frozen, replaceable) | summary_md, included[]{ref,source_class,reason,confidence,status}; surfaced capabilities and matched InstructionRules appear as `capability://...` generated refs when present, filtered by harness compatibility edges when present, carrying read/route/boundary constraints, **not execution**; runtime memory appears as relevance-gated `claim://...` refs in active_memory[] only after ledger visibility/scope policy, **not from a rented index**; volatile `repo:*` source-control snapshot claims stay queryable memory, not ambient context, because live adapters/policy gates provide fresh source-control evidence; recorded `ProjectionFreshness` context appears as `projection://...` generated refs filtered by repo kind, with stale/drifted status preserved as context evidence, **not authority or regeneration**; close records capability offers in Receipt `skills.candidate` and Transition `projections.skill` as provenance, not use; **excluded[]{ref,reason}**, active_instructions[], active_hooks[], hook_roster[], hook_readiness[], active_decisions[], active_memory[], source_revisions[], required_checks[], avoid[] |
| **WorkEnvelope** | the one root identity for a unit of work | work_id, intent_id, contract_id, owner, canonical_surface, state (proposed→admitted→ready→claimed→running→verifying→completed\|held\|blocked\|failed\|cancelled), desired_outcome, success_predicate, non_goals[], risk, context_pack_id, route_decision_id, policy_decision_id, run_ids[], artifact_ids[], verification_ids[], memory_candidate_ids[], optional git_delivery{target_worktree_root,repository_fingerprint,target_local_ref,expected_base_sha,publication_mode,publication_ref}. `compound work create-plan` records proposal-only admission policy readiness under `COMPOUND_HOME/create-plans` and its generated create command carries the planned `--work-id` plus planned envelope fields; `compound work create [--work-id id]` enforces that policy path before writing work/action/policy/event state, preserving the planned root id when supplied. Markdown work packets enter here through `work import-packet`, keyed by packet hash as proposed source-backed work through the same admission gate, not as proof. `compound workbench [--actor A] [--active-packet-surface S] [--source-packet packet.md] [--packet-dir D]` is the read-only board over open work, held/blocked review, terminal receipt/transition proof, optional source-fresh `active_packet_proof`, offered packet files under `offered_packets`, observed run evidence, recent dispatch-step-run summaries, and `dispatch_next` command suggestion/wait reason for that actor; it never dispatches or mutates. `compound workbench --operator` is an additive bounded human operator view over that same read model and review queue: it renders highest-priority Needs you / Working / Ready missions from existing WorkEnvelope/proof/review/action-context fields, groups duplicate rows, caps each visible stage with `--operator-limit` (default 3; terminal proof remains bounded by `--recent`), hides raw IDs/proof dumps unless `--advanced` or `--operator-advanced` is supplied, preserves the default human output, and leaves `--json` output unchanged even when paired with `--operator`. `compound work active-packet-proof [--surface S] [--source-packet packet.md]` is the read-only active packet proof projection: it reports `ok` only when one canonical-surface WorkEnvelope has context pack, receipt, transition proof, and, when source is supplied, matching packet evidence ref; served readers should call it instead of scanning `COMPOUND_HOME`. `compound work claim-plan` records proposal-only claim policy readiness under `COMPOUND_HOME/claim-plans`; `compound work claim` enforces that policy path and records the claim action/policy envelopes before writing the lease. `compound work advance-plan` records proposal-only lifecycle transition readiness under `COMPOUND_HOME/advance-plans`; `compound work advance` enforces that policy path before changing state or attaching run/artifact/verification refs. `compound work close-plan` records proposal-only terminal close readiness under `COMPOUND_HOME/close-plans`; `compound work close` enforces that policy path before emitting close action/policy, Receipt, Transition, and terminal work link. `compound work dispatch-plan` records a proposal-only next-claim plan under `COMPOUND_HOME/dispatch-plans`; `--source-packet <packet.md>` is a read-only packet proof preview and writes no WorkEnvelope state. `compound work dispatch-claim` records the dispatch plan/result and claims at most one policy-allowed ready work item through the same claim path; source-packet flags on this mutating path import packet-hash WorkEnvelope state first, then claim through policy. It does not execute, advance, close, or promote memory. |
Git delivery note: source-controlled work created or admitted through current commands must declare the complete envelope-owned delivery contract: canonical target worktree root, derived repository fingerprint, exact `refs/heads/*` target, full expected-base SHA, and either `publication_mode=required` with one bound `refs/remotes/*` ref or explicit `local_only`. A nonterminal legacy envelope with no `git_delivery` may bind that complete contract exactly once through a same-state `work advance`; the actor must carry operator provenance, a matching legacy `expected_base_sha` is cleared, and rebinding or combining the bind with a lifecycle transition fails closed. A pre-fingerprint `compound.work.v0.1` envelope whose other delivery fields are complete remains readable and re-marshals to the same bytes/hash, but it is execution-ineligible: Workbench exposes repository-identity migration, while claim, dispatch, worker-prompt creation, and ordinary advance fail closed. Only an operator-provenance same-state advance declaring the exact five persisted delivery fields may derive and backfill the missing fingerprint; field drift, executor migration, terminal migration, and a second backfill are rejected. These are governed migrations through the existing advance journal, not a second repair system. `work land-check` treats caller repo/ref values only as assertions and evaluates the attached final `commit:<full-sha>` against the envelope authority: repository identity, base lineage, clean target, local containment, and required publication containment. Only a successful current `compound.work_land_check.v0.4` report is attached to that WorkEnvelope as `land-check:*` close evidence; v0.3 predates sealed repository identity, remains historical/stale evidence, and requires a fresh v0.4 check rather than being mislabeled as tampered. The publication observation is the bound local remote-tracking ref after push/fetch, not independent network attestation. Any completed close carrying `commit:*` through artifacts, direct proof refs, or an action-plan receipt draft fails without `git_delivery`. For non-audit moves, an explicit absolute `target_surface` or a valid persisted mutating `ActionIntent.target.path` inside a Git worktree also requires the contract even when a caller omits `commit:*`; missing, invalid, or mismatched `act_*` evidence fails loudly. ContextPack scope and `canonical_surface` are orientation, not mutation evidence, so they do not manufacture a delivery hold by themselves. `workbench`, `close-plan`, dispatch-close, and `close` re-evaluate current delivery state; local-only landing remains `awaiting_publication` when publication is required, stale/dirty proof is not advertised as closeable, and terminal work retains its terminal action instead of becoming `ready_to_close` again.
Repository identity binding note: `target_worktree_root` is a locator, never repository identity. On create or governed legacy binding, Compound derives `repository_fingerprint` from the Git object format, the lineage roots reachable from the bound expected-base commit, and the exact trimmed origin; any caller-supplied value is overwritten. The fingerprint is carried through action identity, Workbench and dispatch projections, worker prompts, land-check reports, and sealed recorded proof, then re-derived before every authoritative delivery check. Ordinary descendant commits remain valid, while a different lineage, changed origin, or same-path repository replacement fails closed and requires governed replacement rather than silent rebinding. Close has a deliberately narrower atomic guarantee: after preparing native locks for the exact target and required publication refs, Compound revalidates repository identity one final time before writing any terminal envelope. The Git transaction keeps those refs fixed through terminal event persistence, but cannot lock repository config, origin identity, or object roots; a repository-identity mutation after that final check is outside the atomic ref guarantee and is detected by the next authoritative validation rather than retroactively invalidating an already-persisted close.

Schema-evolution invariant: a field added to a hash-bound envelope under an unchanged schema must preserve the omitted legacy byte shape (normally `omitempty`) and ship with a frozen pre-field JSON/hash round-trip fixture. A field added to digest-covered sealed content requires a schema-version bump plus a frozen previous-version fixture; validation classifies the schema before applying the current digest codec. Historical proofs remain identifiable evidence, never current authority and never a false tamper accusation. Tests must pin both the previous payload and the current schema literal so a future field addition cannot silently redefine an already-issued version.

Work lifecycle transaction note: authoritative create, claim, advance, action-record, auto-admit, and close writers serialize in the fixed lock order `autonomy-admission -> work-event` and strictly validate the complete shared `events/work.jsonl` before changing any WorkEnvelope, lease, ActionIntent, PolicyDecision, Receipt, or Transition byte. A corrupt or incomplete event tail therefore fails closed before those side effects. Advance additionally stages the next envelope at `work/work/<work_id>.json.advance-pending` before publishing its hash-bound event. The marker is a crash-recovery journal, not disposable scratch: backup retains it, compaction and operational-history archiving do not remove it, and a retry of the exact `work advance` or `AdmitAuto` transition validates the old/new work hashes plus the attached action and policy before finalizing without a second event. A marker with no matching durable event is safely discarded by that retry; an invalid, conflicting, or multiply matched marker remains in place and fails closed for operator inspection. `home doctor` does not auto-repair or delete this journal. The operator response is to preserve the marker and event/envelope evidence, retry the exact transition once, and investigate or restore through the home owner if validation still fails—never delete `.advance-pending` by hand.
Create-plan integrity note: `compound.work_create_plan.v0.2` derives its id from the canonical admission payload, including normalized `action_context`, and admission requires both that digest and the `create-plans/<id>.json` path identity. Admission writes a content-addressed `work/action-contexts` snapshot even when context is absent; Workbench and dispatch consume that immutable snapshot, while legacy `v0.1` previews remain visible only as blocked re-plan-or-retire debt.
Evidence-autonomy note: the sole implemented class is `job_health`; it remains bench-only and has no live production consumer. `compound director propose --apply` remains a single synchronous proposal command; it may materialize a job-health create plan as `proposed`, then ask `AdmitAuto` to cross only to `admitted`. A legacy/broad `director-apply` grant authorizes filing but cannot establish eligibility. The separate decision-record grant must carry exact `auto_admit_class`, `min_terminal_samples`, `min_window_days`, and `max_failures` values. `artifacts/autonomy-eligibility/<id>.json` seals the latest deterministic sample window: unique terminal WorkEnvelopes, Receipt outcome/content hashes, genuine non-route state deltas, exact work-bound deciding route-row hashes and request identity, current route-plan id, router YAML revision, requested route id, and actual resolved provider/model mapped to a current eligible capability claim. Missing identity, route aliases, unknown claim levels/tiers, and free or supervised resolved lanes fail closed. `AdmitAuto` replays that artifact against live sources and the exact active grant inside the shared autonomy-admission advisory lock; canonical halt, operational-decision, editable-surface, operator-state, standing-grant, router, route, work/archive, and lifecycle writers take the same lock, and concurrent attempts commit once. `grant`, `freeze`, and `ruling` promotions are reserved to the operator-provenance policy-decision importer; ordinary policy/doctrine imports remain evidence-only. Runtime controls fold from protected raw decision history so generic same-ID MVCC supersession or review edges cannot hide them; the narrow pre-stamp operator-record shape remains supported for existing broad grants/freezes. Generic memory adjudication rejects copied stamps, reserved operational kinds, and attempts to occupy a protected decision slot, so extracted meaning remains evidence. Archive/restore is serialized with admission, and eligibility refuses an unindexed terminal work file left by a move/index crash gap. Stale create plans cannot overwrite an already-created/admitted work id. The halt projection is mutated only through the locked `compound policy autonomy-halt` command; direct halt YAML edits are unsupported. Manual `policy.yaml` or editable-surface YAML edits while autonomy is available are also unsupported: engage the global halt, edit and validate, then clear the halt through its command. New evidence, revocation, duplicate route/Receipt identity, router drift, tamper, route-only proof, or a non-eligible lane fails closed. On success, the WorkEnvelope and proposed→admitted event retain the grant/claim, eligibility, threshold, Receipt, route-plan, route-evidence, resolved provider/model, and router-revision refs. Halt, editable-surface, freeze, and the compiled operator-only risk frontier run first. No class grant is created or enabled by the code path itself.
Atomic publication note: route-ledger and operational-archive-index writers hold the shared autonomy-admission lock, close writable descriptors, prepare and sync a rooted sibling, then publish through a Darwin/Linux atomic name exchange (or a no-replace hard link when the canonical file is absent). The displaced inode is validated after the exchange and before commit; identity drift makes the transaction fail closed with both complete names retained for manual repair. This contract covers every supported Compound writer and concurrent path mutation completed before the exchange. A same-user process that bypasses the admission lock and keeps writing through a descriptor opened before exchange is hostile out-of-contract corruption: Darwin and Linux provide no portable mandatory lock or directory-entry CAS that can revoke such a descriptor.
`compound work dispatch-advance` records dispatch-start results under `COMPOUND_HOME/dispatch-advances` and advances at most one actor-claimed work item to `running` through the policy-backed `work advance` path; it does not execute task commands, close work, or promote memory.
`compound workbench --capability-task <task> [--capability-harness <harness>]` composes that same read-only workbench with the existing CapabilityGraph surface under `capability_surface`, defaulting the harness to the actor; it offers capabilities only and still does not dispatch, execute tools, install skills, activate hooks/MCP/plugins, or mutate graph/work state.
`compound work dispatch-close-plan` records proof-gated close-readiness results under `COMPOUND_HOME/dispatch-close-plans` and records the selected `verifying` work item's normal `ClosePlan` under `COMPOUND_HOME/close-plans`; it requires artifact, verification, or observed-run evidence and does not close work, emit Receipt/Transition proof, execute task commands, or promote memory.
`compound work dispatch-close` consumes proof-gated close readiness and closes at most one selected `verifying` work item through the existing `work close` Receipt/Transition chain; missing proof or policy hold records a no-op result under `COMPOUND_HOME/dispatch-closes` without mutating work.

Packet imports may carry optional `Proof-medium: web-ui | ios | cli | api | none`, stored on WorkEnvelope as `proof_medium`. Dispatch claim/step emits a worker prompt with a `LOAD THESE SKILLS` block built from the existing CapabilityGraph/ContextPack offers for that task and harness; this is an offer surface, not a second resolver and not execution. `work close` enforces packet proof media only for completed outcomes: `web-ui` requires screenshot plus design-review proof refs, `ios` requires screenshot proof, and `cli`/`api` require command-output or test proof refs. Receipts record offered capability refs in `skills.capabilities_offered` alongside candidate/used/missed/false-triggered fields so offer-to-use feedback remains measurable.
`compound work dispatch-step` records one safe lifecycle tick under `COMPOUND_HOME/dispatch-steps`, delegating to existing dispatch-close, dispatch-advance, or dispatch-claim in that priority order; source-packet flags are passed only through the existing dispatch-claim producer path, so packet import + claim remains governed by the normal packet adapter and claim policy. Recorded step results include pre-step `counts`, post-step `post_counts`, `next_action` from the post-step workbench item or no-op reason, plus `can_continue` / `continue_command` when another safe tick is available. When a step cannot continue safely, the result records `wait_reason` and `blocked_on` so callers can distinguish no-open-work, review, run-evidence, close-proof, and actor-claim waits without re-reading the whole workbench. `--until-wait [--max-steps N]` records an aggregate under `COMPOUND_HOME/dispatch-step-runs` while repeatedly invoking the same one-step path until a wait reason or max-step boundary; source-packet producer flags are cleared after the first step so continuation never re-imports the same packet. `workbench` surfaces those aggregate run summaries read-only as `dispatch_step_runs[]` with count, status, wait reason, step actions, work ids, and artifact path. It does not execute task commands or synthesize run observations.
`compound work dispatch-step --source-packet <packet.md>` reports `wait_reason=source_packet_terminal` when the packet hash already maps to a terminal WorkEnvelope, preserving the existing work/source proof and avoiding a misleading generic no-open-work stop.

| **ActionIntent** | a proposed mutation, pre-policy | actor, capability, action, target{kind,id,path}, mutation_class (read\|local_reversible\|external_side_effect\|public\|destructive\|financial\|security), reversibility, blast_radius, estimated_cost{}, preview, rollback_plan |
| **PolicyDecision** | the governance verdict an adapter enforces | outcome (allow\|allow_with_obligations\|hold\|deny), reasons[], obligations[] (write_receipt, include_rollback_ref, run_verification), standing_grant_id, expires_at; policy may consume runtime evidence such as source-control status and placement guard context, but evidence can only raise caution or add provenance, never relax a decision |
| **HookEvent** | one normalized harness lifecycle event (**no raw prompt stored by default** — only `input_hash`) | harness, hook_name, hook_capability (context_injection\|policy_gate\|telemetry\|receipt), ids{work_id,context_pack_id}, input_hash, side_effects[], output_ref, duration_ms, status (ok\|fail_open\|blocked\|error); persisted to `events/<hook_event_id>.json` and `events/hook-events.ndjson` when `COMPOUND_HOME` is available |
| **Receipt** | how a run is learned from (proportional to risk/blast-radius) | outcome, state_delta{changed,summary,files_changed[],artifacts[],verification[]}, cost{model_calls[],tokens,usd_actual,usd_estimated,human_minutes}, skills{used/missed/false_triggered/candidate}; `used` comes from allowed recorded ActionIntents and can validate CapabilityGraph nodes when the receipt is completed+changed; completed proof-bearing non-`audit_only` closes infer `changed=true` so omitted flags do not hide landed deltas from Workbench or receipt mining. Explicit `missed` refs add `missed_when` graph edges using the receipt summary as context, explicit `false_triggered` refs add `false_triggered_by` edges for the same context, and `candidate` comes from ContextPack offers and remains inert. `work close` / `hook session-end` auto-consume receipt feedback when graph state exists; `capability consume-receipts` replays it. `work close --missed ... --false-triggered ...` and `hook session-end` fixture fields are the explicit closure inputs for missed/false-triggered feedback. memory{candidates/promoted/parked}, next{suggested_work[],review_trigger} |
| **Transition** | the apex object joining projections (see `transition.md`) | before_refs[], action_refs[], after_refs[], state_delta, projections{memory,skill,product,cost,risk,taste,routing,human_attention}, second_order{future_behavior_changed,should_harden,should_create_rule,should_decay,revisit_at} |

`compound work observe-run <work_id> --trace <trace_id>` is the run-observation seam between `running` and `verifying`: it reads an existing `TraceEnvelope` from `COMPOUND_HOME/events`, requires the trace `ids.work_id` to match when present, records the observation under `COMPOUND_HOME/run-observations`, and delegates the state change to policy-backed `work advance` with the trace attached as a verification ref. `compound workbench` surfaces those observation artifacts as read-only `run_observations[]` on matching items, and `compound work close-plan` surfaces the same verification/observation evidence while generating an explicit `work close --proof ...` proposal. These commands do not execute task commands, close work, emit Receipt/Transition proof, promote memory, or claim task success.

`work_id` is **the** anti-fragmentation object: a control-spine fossil scatters identity across packet-id /
run-id / provider-call-id / receipt-path / memory-slug / dashboard-card. One root id linking
intent→context→action→policy→run→receipt→transition is the cure. Governance is enforced here: **a mutating
adapter rejects a mutation that lacks a valid `PolicyDecision`** — prompt-only "approval" is not enough.
`StandingGrant`s pre-authorize scoped envelopes (`target_path`, `max_usd`, `max_tokens`, `blast_radius`,
`reversibility`) so agents act without being paged only while inside the grant; leaving the envelope escalates
to `hold`/blocked through the same policy gate.

`PlacementGuard` is policy context, not a second hook path: PreToolUse may pass owner-placement evidence into
`PolicyDecision` with staged `observe -> warn -> block` behavior. Only `confidence:block` can hard-hold;
`OPEN_WRITE` and `PLACEMENT_OVERRIDE=1` fail open, unknown modes fail open as observe, and
the native Codex adapter returns `permissionDecision=deny` for a hold. `compound hook pre-tool-use --enforce
<fixture.json>` returns the same gate exit code for harnesses or adapters that cannot block natively.
`compound policy import-placement-rules <placement-rules.json>` installs generated owner placement rules
as `compound://registries/placement-rules` under `COMPOUND_HOME`; the source view is evidence/rollback
input after import, while PreToolUse reads the Compound-owned registry.

A dirty-repository mutation can proceed only through an exact recorded capability, not a general cleanup
acknowledgement. The first blocked PreToolUse persists its ActionIntents with one session-bound,
content-free mutation-input digest shared by the complete target batch. `compound authority dirty-plan
<repo> --work-id <owner-work> --action-intent <id>...` loads those persisted actions and derives the
action work id, digest, capability, action, mutation class, and canonical target set itself. The owner work
id is provenance and must still be `claimed`, `running`, `verifying`, or `held`; executable identity is the
live action work id plus mutation digest, exact repository/root/remote/full HEAD/dirty-state fingerprint,
and exact target set. After an `authorize_mutation` review is recorded, PreToolUse attaches the capability
to every action in the batch or none. A partial target set, another session, changed bytes/index state,
tampered plan, inactive owner work, or legacy count-only review keeps the normal dirty hold.

---

## 3. Hooks are clients, not brains

> Hooks are clients. compound is authority.

Hook event names are adapter vocabulary, not semantic authority. The stable input is a lifecycle intention such
as `prepare_context`, `guard_mutation`, `normalize_artifact`, `observe_outcome`, `preserve_before_loss`,
`restore_after_resume`, or `close_with_proof`. The existing hook-plan owner must compile those intentions from
the frozen TaskContract/EffectiveContract plus risk, privacy, and context needs against a revisioned harness
capability attestation. Its HookPlan output is a harness-native projection with one explicit disposition per
intention: `native`, `fallback`, `degraded`, or `unsupported`. This replaces HookPlan-as-authority; it does not
add a peer lifecycle or hook system. The full ownership and compaction/resume flow is in
[`information-custody.md` §5](./information-custody.md#5-recall-context-injection-and-hooks).

A plan is immutable for the work/session and cites the harness family, surface, version, feature/trust state,
config scope, handler revision, and fallback revision it was compiled against. Installation observation is
separate evidence. “Installed” therefore cannot imply “ready,” and an unknown harness cannot inherit optimistic
capabilities from a familiar one. On delegation, resume, or harness replacement, Compound restores the owned
working object and remaining ceilings, then compiles a new plan for the new executor.

Hooks may: **request** context · **freeze** selected context into a ContextPack · **guard** mutations (where
the harness supports blocking) · **capture** evidence/receipts · **emit** typed events. Hooks may **not**:
deep synthesis · canonical memory promotion · tool/model routing policy · product judgment · global truth
arbitration. (Hooks sit at high-gravity points — a tiny SessionStart nudge bends the whole session — so keep
them thin.)

Commands: `compound hook {session-start, user-prompt-submit, pre-tool-use, post-tool-use, session-end, stop}`.
`compound contract hooks-authority` reports `hook_readiness[]` from installed-hook authority, checked fixture coverage, and live-adapter support, plus `pre_tool_use_readiness[]` from installed hooks and harness native-block facts. With `--observe-live`, it exposes desired `hook_plan`, read-only `installation_observation`, and deterministic `reconciliation` separately; observed extras remain evidence and never enter desired authority. `compound hook pre-tool-use --enforce <fixture.json>` returns the same gate exit codes as `policy evaluate` for non-blocking adapter fallbacks.
Reconciliation reports an active command match as `registered`; it reports `ready` only when desired and
observed handler-content revisions also match. Invocation arguments have a separate privacy-safe digest, so
argument drift remains detectable without treating configuration bytes as handler-content proof.
Hook-plan reconciliation also carries `preserved_bindings` for exact operator-owned declarations outside the
managed plan. Those rows remain `external_preserved`: they are reserved before a managed fallback can claim the
same observed slot, survive narrower imports, and never enter active hooks or readiness. A concrete handler whose
content cannot be attested (including a symlink) retains the fail-loud `unavailable:concrete-handler` sentinel;
authority/readiness reports it as `handler_unavailable`, excludes it from EffectiveContract and ContextPack active
hooks, and requires restoration plus re-import before mutation. Opaque `handler:` and `hook://` identities make no
local content claim and retain their registered/readiness semantics. These fields and statuses are additive v0.1
extensions; the schema version changes only if strict-consumer evidence proves additive compatibility insufficient.
Each import records incoming hook count, the exact resulting authority revision, and the prior authority revision,
so identical source bytes cannot collapse distinct preserved-merge results into one provenance record.
Each: `stdin JSON → normalized HookEvent persisted under events/ → compound service → typed object written under .compound → small
harness-compatible output`. The lessons from a mature hook system map cleanly:

```text
SessionStart   → ContextRequest + EffectiveContract + ContextPack + context/injections/<hook_event_id>.md   (returns a compact orientation summary and persists exact injected text plus inclusion/exclusion provenance, NOT a universe dump)
UserPromptSubmit → ContextRequest + ContextPack + proposed WorkEnvelope (persists proposed work tied to context_pack_id when COMPOUND_HOME is available)
PreToolUse     → ActionIntent + PolicyDecision (+ held WorkEnvelope review or exact batch-bound capability for dirty source-control holds)
                                                                  (returns allow | allow_with_obligations | hold | deny)
PostToolUse    → ToolEvent + ArtifactRef + VerificationCandidate
Stop           → Receipt + Transition + MemoryCandidate/SkillCandidate/FailurePattern/RouteObservation
                                                                  (Stop may inject end-of-turn feedback; capability: StopFeedback)
SessionEnd     → same receipt family, telemetry-only — SessionEnd can never inject context,
                                                                  and some harnesses have no SessionEnd at all (capability: SessionEndReceipt)
```

For Codex, the live `Stop` adapter is also the stuck/resume consumer. It binds the thread and turn to one
open WorkEnvelope, extracts only a terminal blocking question from the existing transcript reader, and may
return `decision: "block"` with a cited continuation only when current operator-provenanced standing law and
policy both allow it. The continuation is recorded on the same `work_id` as ActionIntent → PolicyDecision →
Receipt → Transition without closing the parent work. Risk-floor, novel, stale, or unbound asks return no
continuation and remain paused as an existing DecisionBrief-backed review item.

**Capability-aware by harness — not fantasy-uniform.** Current Claude Code and Codex releases support blocking
PreToolUse hooks; Codex also layers `AGENTS.md` + skills through progressive disclosure (it sees skill
name/desc/path and loads full SKILL.md on demand; the initial list is capped). So a
`HarnessRegistry` records what each harness actually supports (`session_start_context`,
`pre_tool_use_block`, `session_end_receipt`, `native_skills`, `mcp`, …) and its `limitations[]` (long-session
drift, fail-open risk, instruction cap, skill truncation, cwd-dependent guidance). Native blocking is claimed
only when both the harness capability and an installed trusted write-scoped hook are proven. External maps enter as normalized `compound.harness_registry.v0.1` manifests, while OS-style entrypoint registries enter through `compound harness observe --os-entrypoints <file>` with only session-start context inferred; observation is evidence/no-promote, not hook installation. **No live hook install before the fixture loop passes.**

---

## 4. Instructions: tiny + compiled

A served `AGENTS.md` / `CLAUDE.md` (what a running compound instance gives another repo's agents) is a thin
entrypoint, not the doctrine:

```md
You are a client of compound.
Before non-trivial work: obtain or create a WorkEnvelope; load the active EffectiveContract + ContextPack
(do not infer canon from this file or cwd alone). Mutations require ActionIntent → PolicyDecision. Close
with a Receipt (state_delta + evidence) and a Transition. Retrieved evidence is not canon. Generated state
goes under COMPOUND_HOME, not source. Proof is a state delta, not a report.
```

Everything else loads **by scope** from the EffectiveContract + ContextPack + Skills + Policy. (compound's
*own* repo `AGENTS.md`/`CLAUDE.md` is the build guide — fuller — because agents here are *building* compound,
not consuming a running instance.)

---

## The first runtime slice (boring on purpose)
```text
compound home init --local
compound contract compile --harness claude-code --cwd .
compound orient --task "audit generated-state layout"
compound work create --intent "audit generated-state layout"
compound policy evaluate fixtures/action-intent/local-doc-write.json
compound policy import-placement-rules fixtures/policy/placement-rules.json
compound policy placement-authority
compound policy import-operator-state fixtures/policy/operator-state-l1.json
compound policy operator-authority
compound receipt create  fixtures/receipt/state-delta.json
compound transition create fixtures/transition/basic.json
compound transition show <id>
```
Proof: one stable `work_id` links context→action→policy→receipt→transition; **all** generated files under
`.compound`; repo source unchanged except intentional source/test files. No legacy index, no model gateway, no memory
ingestion yet — they all need this spine first.

Home durability is part of the runtime contract:

```text
compound home backup [--target archive.tar.gz]
compound home verify [archive.tar.gz]
compound home restore archive.tar.gz --target <empty-home-path>
compound home archive-history [--dry-run] [--kind work|lease|review|comparison]
compound home restore-history (--all|--id ID...)
compound home export --public-fixture [--target fixture.tar.gz]
compound home compact [--dry-run] [--rebuildable]
```

`backup` stores catalog-classified durable owned state and intentionally excludes transient or
rebuildable paths, while preserving behavior-bearing exceptions even when their historical parent is named
`projections`. `compact --rebuildable` consults the same classification and cannot remove those exceptions.
`export --public-fixture` emits only sanitized layout/manifest
metadata, not private events, ledgers, context packs, or work records.
`archive-history` moves terminal operational history out of hot surfaces into
`COMPOUND_HOME/archives/operational/` with an append-only index, without deleting
it. It archives completed/failed/cancelled WorkEnvelopes, stale
`work/leases/*.json` entries whose work is terminal/archived/missing, terminal
owner-review artifacts and sidecars, and judged comparison candidates. `home
doctor` reports operational hygiene (`hygiene:no-terminal-work-hot`,
`hygiene:no-stale-leases`) without making hygiene dirty state fatal, because
`archive-history` is the repair path. `workbench` and `reviews` exclude archived
entries by default; `--include-archived` restores query visibility, and
`restore-history` moves selected entries back to original hot paths.

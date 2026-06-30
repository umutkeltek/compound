# Runtime: the `.compound` home, the envelope spine, and hooks-as-clients

This is how a *running* compound instance works. Three parts: the generated-state home, the typed envelope
spine, and the thin hook clients. Build this **before** memory ingestion (`../roadmap/phases.md`). One doc on
purpose — fragmenting "generated-state", "envelopes", and "hooks" into three would reproduce the disease.

---

## 1. `COMPOUND_HOME` / `.compound` — the runtime body

The repo is **source**. The running instance owns **state**, under one controlled home — never the repo tree.

```text
repo            = source code, schemas, docs, tests, fixtures
COMPOUND_HOME   = generated runtime state (a typed habitat, not a folder pile)
private overlay = real paths, credentials, accounts, private corpus, taste examples (dogfood data)
```

Resolution order: `1) --home <path>  2) $COMPOUND_HOME  3) nearest ancestor .compound/manifest.yaml
4) ~/.compound`. Commands: `compound home init [--global|--local]`, `home doctor`, `home where`.

```text
$COMPOUND_HOME/
  manifest.yaml
  lock/            installed-hooks.lock.json, effective-contract.lock.json
  config/          core.yaml; overlays/{private,local}.yaml; harnesses/*.yaml; adapters/*.yaml
  contracts/       effective/<date>/<id>.json + latest.json ; schemas/
  events/          hook-events.ndjson ; <hook_event_id>.json ; <transition_id>.json   (raw nervous system + emitted envelopes, including route-evidence projections)
  work/            current.sqlite ; events.ndjson ; work/<id>.json ; runs/<id>.json ; leases/<id>.json ; checkpoints/   (execution identity)
  context/         requests/<id>.json ; packs/<id>.json ; injections/<hook_event_id>.md (EXACT injected text)
  ledger/          {evidence,entities,claims,positions,patterns,edges,decisions,transitions,execution-route-evidence}.jsonl (durable truth; route evidence is metadata, not task success)
  inbox/           memory-candidates/ skill-candidates/ product-signals/ capability-observations/
                   failure-patterns/ taste-observations/ route-observations/ contradictions/ parked/ rejected/  (untrusted)
  registries/      entities/ projects/ products/ skills/ tools/ mcps/ models/ providers/ harnesses/ hosts/ deployments/ sources/ adapters/
  artifacts/       sha256/ab/cd/<hash> ; refs/<id>.json ; retention/policy.yaml   (content-addressed evidence)
  indexes/         local/ vector/ fts/ embeddings/ + build-manifest.json                    (rented/search/cache)
  projections/     context/ dashboard/ context-summaries/ packs/ memory-timelines/ product-timelines/ capability-graph/ cost/ markdown/  (rebuildable views)
  reviews/         adjudication/ route-remorse/ memory-doctor/ public-private-boundary/
  exports/         public-fixtures/ private-backups/ handoff-packets/
  tmp/
```

Typed semantics (the layout *is* the guardrail): `ledger/` durable append-only · `inbox/` untrusted
candidates · `projections/` rebuildable views · `indexes/` rented · `artifacts/` content-addressed ·
`events/` raw · `work/` execution lifecycle · `context/` what agents actually consumed. `compound home
doctor` asserts: generated state inside home, **not** in repo source.

---

## 2. The envelope spine (the typed grammar everything runs through)

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
| **EffectiveContract** | the compiled "what applies right now" artifact every run references by `contract_version = sha256(...)` | active_instructions[], active_hooks[], active_decisions[], memory_policy{ambient/explicit_only/forbidden}, capability_policy{tools_allowed/on_demand/mcps_not_loaded_by_default}, mutation_policy, source_revisions[], unknowns[], conflicts[] |
| **ContextRequest** | what a hook asks compound for | intent_text_ref (hash/redacted), harness, cwd, task_guess, requested_mode (observe\|plan\|execute\|review), constraints{max_chars, allow_private_memory:false, allow_raw_corpus:false, allow_provisional_reviews:false} |
| **ContextPack** | what an agent consumes (frozen, replaceable) | summary_md, included[]{ref,source_class,reason,confidence,status}; surfaced capabilities appear as `capability://...` generated refs when present, filtered by harness compatibility edges when present, **not execution**; runtime memory appears as relevance-gated `claim://...` refs in active_memory[] only after ledger visibility/scope policy, **not from a rented index**; recorded `ProjectionFreshness` context appears as `projection://...` generated refs filtered by repo kind, with stale/drifted status preserved as context evidence, **not authority or regeneration**; close records capability offers in Receipt `skills.candidate` and Transition `projections.skill` as provenance, not use; **excluded[]{ref,reason}**, active_instructions[], active_decisions[], active_memory[], required_checks[], avoid[] |
| **WorkEnvelope** | the one root identity for a unit of work | work_id, intent_id, contract_id, owner, canonical_surface, state (proposed→admitted→ready→claimed→running→verifying→completed\|held\|blocked\|failed\|cancelled), desired_outcome, success_predicate, non_goals[], risk, context_pack_id, route_decision_id, policy_decision_id, run_ids[], artifact_ids[], verification_ids[], memory_candidate_ids[]. Markdown work packets enter here through `work import-packet`, keyed by packet hash as proposed source-backed work, not as proof. |
| **ActionIntent** | a proposed mutation, pre-policy | actor, capability, action, target{kind,id,path}, mutation_class (read\|local_reversible\|external_side_effect\|public\|destructive\|financial\|security), reversibility, blast_radius, estimated_cost{}, preview, rollback_plan |
| **PolicyDecision** | the governance verdict an adapter enforces | outcome (allow\|allow_with_obligations\|hold\|deny), reasons[], obligations[] (write_receipt, include_rollback_ref, run_verification), standing_grant_id, expires_at; policy may consume runtime evidence such as source-control status, but evidence can only raise caution or add provenance, never relax a decision |
| **HookEvent** | one normalized harness lifecycle event (**no raw prompt stored by default** — only `input_hash`) | harness, hook_name, hook_capability (context_injection\|policy_gate\|telemetry\|receipt), ids{work_id,context_pack_id}, input_hash, side_effects[], output_ref, duration_ms, status (ok\|fail_open\|blocked\|error); persisted to `events/<hook_event_id>.json` and `events/hook-events.ndjson` when `COMPOUND_HOME` is available |
| **Receipt** | how a run is learned from (proportional to risk/blast-radius) | outcome, state_delta{changed,summary,files_changed[],artifacts[],verification[]}, cost{model_calls[],tokens,usd_actual,usd_estimated,human_minutes}, skills{used/missed/false_triggered/candidate}; `used` comes from allowed recorded ActionIntents and can validate CapabilityGraph nodes when the receipt is completed+changed, explicit `missed` refs add `missed_when` graph edges using the receipt summary as context, explicit `false_triggered` refs add `false_triggered_by` edges for the same context, and `candidate` comes from ContextPack offers and remains inert. `work close` / `hook session-end` auto-consume receipt feedback when graph state exists; `capability consume-receipts` replays it. `work close --missed ... --false-triggered ...` and `hook session-end` fixture fields are the explicit closure inputs for missed/false-triggered feedback. memory{candidates/promoted/parked}, next{suggested_work[],review_trigger} |
| **Transition** | the apex object joining projections (see `transition.md`) | before_refs[], action_refs[], after_refs[], state_delta, projections{memory,skill,product,cost,risk,taste,routing,human_attention}, second_order{future_behavior_changed,should_harden,should_create_rule,should_decay,revisit_at} |

`work_id` is **the** anti-fragmentation object: a control-spine fossil scatters identity across packet-id /
run-id / provider-call-id / receipt-path / memory-slug / dashboard-card. One root id linking
intent→context→action→policy→run→receipt→transition is the cure. Governance is enforced here: **a mutating
adapter rejects a mutation that lacks a valid `PolicyDecision`** — prompt-only "approval" is not enough.
`StandingGrant`s pre-authorize scoped envelopes (`target_path`, `max_usd`, `max_tokens`, `blast_radius`,
`reversibility`) so agents act without being paged only while inside the grant; leaving the envelope escalates
to `hold`/blocked through the same policy gate.

---

## 3. Hooks are clients, not brains

> Hooks are clients. compound is authority.

Hooks may: **request** context · **freeze** selected context into a ContextPack · **guard** mutations (where
the harness supports blocking) · **capture** evidence/receipts · **emit** typed events. Hooks may **not**:
deep synthesis · canonical memory promotion · tool/model routing policy · product judgment · global truth
arbitration. (Hooks sit at high-gravity points — a tiny SessionStart nudge bends the whole session — so keep
them thin.)

Commands: `compound hook {session-start, user-prompt-submit, pre-tool-use, post-tool-use, session-end, stop}`.
`compound hook pre-tool-use --enforce <fixture.json>` returns the same gate exit codes as `policy evaluate`, so wrapper adapters can fail closed even when a harness only reports `fail_open`.
Each: `stdin JSON → normalized HookEvent persisted under events/ → compound service → typed object written under .compound → small
harness-compatible output`. The lessons from a mature hook system map cleanly:

```text
SessionStart   → ContextRequest + EffectiveContract + ContextPack + context/injections/<hook_event_id>.md   (returns a compact orientation summary and persists exact injected text, NOT a universe dump)
UserPromptSubmit → ContextRequest + ContextPack + proposed WorkEnvelope (persists proposed work tied to context_pack_id when COMPOUND_HOME is available)
PreToolUse     → ActionIntent + PolicyDecision (+ held WorkEnvelope review for dirty source-control holds)
                                                                  (returns allow | allow_with_obligations | hold | deny)
PostToolUse    → ToolEvent + ArtifactRef + VerificationCandidate
SessionEnd/Stop → Receipt + Transition + MemoryCandidate/SkillCandidate/FailurePattern/RouteObservation
```

**Capability-aware by harness — not fantasy-uniform.** Claude Code supports rich lifecycle hooks (incl.
PreToolUse blocking); Codex's verifiable surfaces are layered `AGENTS.md` + skills (progressive disclosure:
it sees skill name/desc/path and loads full SKILL.md on demand; the initial list is capped). So a
`HarnessRegistry` records what each harness actually supports (`session_start_context`,
`pre_tool_use_block`, `session_end_receipt`, `native_skills`, `mcp`, …) and its `limitations[]` (long-session
drift, fail-open risk, instruction cap, skill truncation, cwd-dependent guidance). Do not assume Codex can
block a tool call unless locally verified — enforce via wrapper/adapter + the compound CLI. External maps enter as normalized `compound.harness_registry.v0.1` manifests, while OS-style entrypoint registries enter through `compound harness observe --os-entrypoints <file>` with only session-start context inferred; observation is evidence/no-promote, not hook installation. **No live hook install before the fixture loop passes.**

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
compound home export --public-fixture [--target fixture.tar.gz]
compound home compact [--dry-run] [--rebuildable]
```

`backup` stores durable owned state and intentionally excludes transient or
rebuildable roots. `export --public-fixture` emits only sanitized layout/manifest
metadata, not private events, ledgers, context packs, or work records.

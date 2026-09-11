# The Execution Plane (model-traffic gateway)

compound is the **control / meaning plane**. Model/provider traffic belongs to a separate
**execution / data plane** — an AI provider gateway that receives OpenAI/Anthropic-compatible
requests, routes them by policy/health/cost/latency/quota/credentials, returns the response in the
caller's wire format, and emits an inspectable route decision. They are **siblings**, not nested.

> compound stays vendor-neutral about the gateway (rent, don't hardcode). The operator's reference
> implementation and its specific lanes/ports live in the private overlay, not here.

## The split (the same doctrine, a different medium)

```text
compound  doctrine:  own the meaning, rent the index.
gateway   doctrine:  own the route DECISION, rent the provider/account/model.

compound asks:  What is this job? What matters? What memory/skill/product context applies?
                What future behavior should change?   (route POLICY)
gateway asks:   Given this request shape and policy, which provider/model/account carries it?
                What did it cost? What failed? What was rejected?   (route DECISION + enforcement)
```

If compound absorbs provider routing it becomes bloated and fragile. If the gateway absorbs
memory/product/skill/taste it becomes a generic router pretending to understand the work. Both wrong.

The interactive harness boundary follows the same split:

- **Harness posture** is Compound-owned meaning: permissions, hooks, task intent, work identity, and policy.
- **Inference transport** is gateway-owned execution: proxy/tap/native path, provider/account routing, capture,
  retries, and route evidence.
- A **harness-transport adapter** is the thin composition edge. It resolves posture from Compound, passes only
  explicit launch inputs to the transport, and can be deleted without moving either authority. The gateway must
  not read Compound policy files, and Compound must not proxy model traffic.

## The execution handshake

A harness is not a model; a provider is not a transport; native inference effort is not orchestration; a route
alias is not an observed model identity. Compound stores these axes independently and never derives one from
another.

`ExecutionProfile` is the Compound-owned semantic request. It carries role/capability class, harness requirements,
privacy/risk, semantic effort, optional native-effort constraint, orchestration policy, candidate bounds, tools,
permissions, and request/token/time/cost ceilings. It is a plan, not evidence that a call occurred. The read-only
`compound route execution-profile` resolver proves profile coherence without contacting a provider or changing
gateway/harness configuration.

Experimental `compound work delegate` can select an existing profile with `--execution-profile`, but its mutation
surface is disabled by default and unsupported in v0.1; `work advance --execute` is the supported governed path. Optional
role/capability/harness/transport/route/model/effort/orchestration/privacy selectors narrow that profile and are
refused when no profile is selected. `--launch-profile` selects the executor owner's concrete launch/account-policy
profile and is likewise invalid without an execution profile. The existing `--harness` flag still selects the exact
Compound model-router `LaneTuple`. The route-evidence owner resolves one bounded canonical execution-plan snapshot
and attests compatibility against the selected harness, lane, model ID, provider route, and transport evidence;
`internal/work` does not maintain harness-family, model, route-alias, or executor-transport tables.

`LaneTuple` resolution deliberately leaves transport empty because the lane projection declares it executor-owned.
When an explicit launch profile is present, route-evidence may defer only the requested executor-owned gateway
transport while the remaining tuple is exact. The CLI then asks the executor owner for the full read-only profile
plan. Compound accepts only the owner's exact `launch-profile-plan@1` consumer projection, with current
generated artifacts, `segmented_full_wire` capture, an opaque nonempty client profile, and exact
harness-family/provider-lane/effort compatibility. A registered lane such as a Claude-family token lane remains
distinct from the owner's `claude-code` client family: the route-evidence registry owns that family mapping.
Likewise, the selected tuple's plan identity and provider route class remain distinct from the owner's concrete
model and route. `provider_route_kind` declares the one owner axis the selected provider route must match:
`provider_class` compares exactly with the owner-attested `provider_lane`, while `direct_model` compares exactly
with the owner-attested concrete `route`. Compound never infers the kind from `/`, the requested model, or an
owner alias. Both values compare case-sensitively after trimming surrounding whitespace. The owner's concrete model
and route are sealed from the plan rather than inferred from or equated to tuple aliases. The owner's exact
`gateway|tap|headroom` transport is sealed beside the selected gateway transport family; it is never inferred from
a route name.

Legacy model-router manifests may omit `provider_route_kind` and continue to import and serve ordinary delegation
without an owner launch profile. Selecting `--launch-profile` requires one of the two explicit values; a missing or
unknown kind refuses before profile resolution, readiness, capacity, RunAttempt mint, or process creation. The
model-router tuple is resolved again immediately before mint, and the selected route plus kind are persisted
independently on the spawned binding so recovery cannot accept a coherently resealed launch plan on another axis.
New launch bindings pair `compound.work_delegate_event.v0.2` with
`compound.delegate_launch_profile_plan.v0.2`. Recovery still reads paired v0.1 records created before the route-kind
field existed, without inventing independent route facts; mixed-version or downgraded current records fail closed.
Missing client profiles, drifted materializations, non-capturing or unknown transports, or changed authority
revisions fail at the same boundary.

Admission validates both snapshots' size, JSON, schema, required identity, and exact attestations. Typed
execution-profile and launch-profile readiness proofs carry those snapshots plus references and digests; both are
hashed into the canonical readiness `snapshot_revision` and persisted on the delegate binding. The exact
RunAttempt stores the execution plan ID and requested client-harness/transport/native-effort fields plus the launch
profile ID, owner revision, and opaque client-profile ID before spawn—not either full plan and never an account ID
or credential. The supervised tmux runner installs `work_id`, `ratt_*`, execution-plan identity, launch-profile
identity, and opaque client-profile identity as session-environment argv before respawn, without shell
interpolation. Observed model/provider fields remain empty until route evidence attests them. Gateway
account/credential selection remains gateway-owned and is not stored here. The lane-selected executable must match one
healthy owner-published wrapper artifact. Its path is used only for the spawn and RunAttempt command, never sealed into
the public-safe profile snapshot; that snapshot is persisted on the binding so status, steer, attach, sweep, and retry
recovery revalidate the same identity. Delegation without
`--launch-profile` retains its legacy behavior, so regular native Claude Code and existing tuple-backed lanes remain
available while their authority is migrated to publish an explicit route kind.

Profile compatibility binds the selected canonical tuple rather than trusting selectors in isolation. After
trimming surrounding whitespace, opaque model IDs, provider routes/route aliases, and transport IDs compare
exactly and case-sensitively; hyphens and underscores are not interchangeable. A nonempty model constraint must
equal the selected model ID; a nonempty route alias must equal the selected provider route or selected route
identity; and transport is compatible only through the explicit owner-plan handoff above.

Harness and transport are independent selectors: for example, Claude Code may run over native transport or an
observed gateway transport without becoming a different harness. Selecting a transport does not let Compound
claim the provider/model that actually ran; those remain `unknown_until_receipt`.

The gateway publishes a revisioned, non-secret conformance view: protocols, route aliases, provider/model
resolution rules, supported native effort per adapter/protocol, cost/account classes, and catalog/adapter revision.
Compound admits a profile only against a specific conformance revision. The gateway then returns observed route
evidence bound to that request and revision:

```text
Compound: requested profile + PolicyDecision + reserved ceilings
  → gateway: admitted route envelope + conformance revision
  → harness/adapter: requested native controls
  → gateway: resolved provider/model/account class + effective controls + attempts/fallbacks
  → Compound: ExecutionRouteEvidence + Receipt cost/outcome reconciliation
```

Requested and effective values are both durable. A gateway may translate a semantic effort into a different
harness-native value only when the profile allows that translation; the evidence records requested value,
effective value, disposition, and reason. Unsupported explicit combinations fail before provider contact. Alias or
adapter changes create a new revision and never rewrite historical plans.

Children receive new profiles. They inherit ceilings—privacy, targets, tools, mutation classes, and remaining
budget—but not provider, model, transport, account, native effort, or orchestration. Independent review is checked
against observed provider/model identity, not different selector labels.

Failure identity is part of the interface. Do not flatten these into `no eligible target` or an untyped 400:

| Failure class | Meaning / next branch |
|---|---|
| `unsupported_combination` | selected harness/transport/model/effort cannot faithfully apply the request; choose an allowed translation or another profile |
| `no_capable_target` | no candidate satisfies declared capabilities; change constraints or capability evidence |
| `no_account_or_quota` | a capable route exists but no admitted account/capacity is available; wait, reserve, or select another economics class |
| `circuit_open` | route is temporarily unhealthy; obey bounded retry/fallback policy |
| `provider_rejected` | provider received and rejected the request; preserve provider status/reason without relabeling it as capability absence |
| `transport_failed` | codec, relay, network, or persistence path failed before a trustworthy provider result |
| `unknown_effective_parameters` | execution may have happened but the adapter cannot attest what actually ran; evidence remains non-deciding |

The rollout order is conformance before enforcement: first prove the gateway's typed lane and requested/effective
receipt; then let Compound profiles authorize one shadow consumer; then enforce one bounded real consumer. A good
resolver or gateway doctor without that consumer is bench-only.

## Execution selection: what will run, what did run, and why they differ

Every launch is a tuple — launch profile (the lane), harness family, provider route, resolved model id,
requested effort — and Compound stores that tuple at two levels with two different lifetimes:

| Level | Type | Lifetime | Owner |
|---|---|---|---|
| Work-level saved selection | `envelope.ExecutionSelection` on `WorkEnvelope.execution_selection` | mutable by explicit save only; every save bumps `revision` and records a lifecycle event | `internal/work` |
| Attempt-level snapshot | `runattempt.ExecutionSelectionSnapshot` on `RunAttempt.execution_selection` | stamped once before the subprocess starts; append-only, never rewritten | `internal/runattempt` + the launch wire |

The saved selection is why "open work, pick a combination, exit, reopen" yields the same combination: the
choice lives on the envelope, not in the launcher, the route plan, or a preset. A renamed model alias or an
edited preset changes nothing that is already saved — the selection carries the resolved model id and the
owner profile revision it was made against, and a launch whose observed profile revision differs reports
`selection_drift` on the attempt instead of silently applying the new meaning.

**Precedence at launch, highest first.** (1) An explicit one-run override on the launch call (`--harness`,
`--launch-profile`) applies to that run only; the attempt records `source: override` and the saved selection is
untouched. (2) The work's saved selection (`source: saved`, with the `selection_revision` consumed). (3) The
project/profile default — for unattended work the route plan for the capability (`source: project_default`).
(4) The system default (`source: system_default`). Only an explicit save (`ExecutionSelection.Source =
explicit`) replaces the saved selection; a one-run override never does.

**Requested is not applied.** Effort is recorded three ways on the attempt: `requested_effort` (semantic),
`applied_effort` (the harness-native value the adapter actually passed, possibly a documented translation), and
`effort_evidence` (`argv:<flag>`, `env:<VAR>`, `settings:<path>`, `receipt:<ref>`, `unsupported:<why>`,
`unobservable:<why>`). A harness with no effort control records `unsupported` and an empty applied value; a
label that merely travelled through a wrapper records `unobservable`. The validator refuses an applied value
under either. Nothing here claims a provider received the value — that remains route evidence
(`unknown_until_receipt`).

**Sessions and lineage.** `native_session_id` plus `native_session_evidence` (`pinned:<flag>`, `derived:<path>`,
`unsupported:<why>`) say which harness session an attempt is, so a resume targets THAT session and never "the
most recent one". `continuation_of` names the predecessor attempt or `native:<id>` a launch resumed. A
cross-harness continuation keeps the Compound work lineage (work id, predecessor attempt, context pack) and
records the new harness on a new attempt; it never pretends one harness's session format was resumed by
another. Intentional changes of model, effort, or harness are new attempts with a new snapshot and, when saved,
a new selection revision with its lifecycle event — never an edit of a historical attempt.

**What a reader sees.** `compound work show <id>` prints the saved selection (what will run); each attached
attempt prints its snapshot (what did run, from which source, with which evidence); a difference between the two
is visible as `source: override` or `selection_drift` on the attempt. No launcher script has to be read.

## Route evidence is a projection, not a verdict

> A gateway `RouteDecision` is **execution-route evidence, NOT proof of task success.**

Recorder provenance is assigned at ingress, not accepted from payloads.
Public imports are `external_import`; ordinary producers, including current
Panel/OpenAI-compatible callers, are `generic`; missing historical values
normalize to generic for resume. The reserved `trusted_panel_runtime` value
has no production caller until an attested adapter exists. Imported, generic,
fixture, and legacy rows remain reviewable metadata but cannot decide
autonomy or shadow a differently bound Panel request id.

Panel call reservations and completions under
`panel-runs/attempts/<run-digest>/` are durable recovery evidence owned by
`internal/panel`. A resumed run validates every persisted call reservation
and completion before another model call; unresolved reservations remain
`unknown_spend` and fail closed rather than replaying earlier lenses. Keep
attempts with their run until a Panel-owned recovery or retirement action can
disposition them. Generic home cleanup and doctors do not delete or repair
this evidence, and no automatic retry may infer that an unresolved call was
free.

It becomes one projection inside a compound `Transition` (the cost/routing projection), joined with
the harness receipt and the real state delta:

```text
gateway trace alone   = a model-traffic fact
harness receipt alone = a work claim
compound Transition   = experience (the join is where compounding happens)
```

compound imports route decisions as `ExecutionRouteEvidence` (see `contracts/README.md`): selected
target, rejected candidates + reasons, model/provider/account, cost, latency, status — **metadata
only, no prompt/response/secrets.** Task success still requires a state delta (tests passed, file
changed, PR opened, product signal moved, or an explicit blocker).

External gateways use `compound route import <execution-route-evidence.json>`; provider-call NDJSON
ledgers use `compound route import-provider-ledger <provider-calls.ndjson> [--limit N|--tail N]`.
Both paths write metadata-only evidence rows and emit the same audit-only Transition routing/cost
projection. `compound route cost-doctor <provider-calls.ndjson> [--record]` records missing
cost/metadata coverage as a parked route observation plus no-promote decision, not billing truth or
policy. The provider-ledger adapter copies route/cost fields and drops source-specific row payloads
instead of importing prompts, responses, paths, or provider secrets.

Local `compound distiller extract` follows the same rule: successful extraction writes metadata-only
`ExecutionRouteEvidence` and an audit-only Transition whose routing/cost projections point at that
evidence; it still does not prove the extracted candidates are true or promoted.

Compound-owned route policy authority is separate from route execution evidence. The authoring door is
`route promotion-apply`: it revalidates a current `review_required` promote/demote artifact immediately
before mutation, mints an ActionIntent bound to the before and exact proposed authority revisions,
requires an allowing PolicyDecision, changes one claim through the act seam, and records the receipt,
transition, pre/post images, and literal `promotion-revert` command. Revert re-plans from that applied
record and restores only the canonical content-addressed pre-image; exported plan structs and applied
record bodies are untrusted input. `promotion-record` remains hold-only and never mutates authority.

`compound route import-model-capabilities <model-capabilities.yaml>` remains an additive
migration/backfill bridge into `compound://registries/model-router`; it is not the steady-state
authoring route. The compatibility direction is Compound outward: `route model-capabilities --yaml`
renders the deterministic OS view, while `--check-view FILE` compares every manifest field and exits
nonzero on semantic drift. The external generator consumes that projection instead of maintaining a
second meaning owner. `compound route plan --capability-class <class> [--privacy-sensitive]` only
renders a planned lane chain with `lane_justification`: it does not call models, rank live providers,
mutate gateway config, or certify task success. Privacy-sensitive plans filter to `local-batch` lanes;
free judge/critique lanes are tripwires, not verdict gates.

Lane resolution is typed independently of its execution address. `provider_route` says how to call
the lane; `resolution.mode=adaptive` means the requested alias must be resolved by gateway evidence,
while `resolution.mode=direct` declares one or more exact provider/model response identities. A
legacy lane with no resolution block may still be planned and called, but it cannot contribute a
deciding autonomy sample. This permits an incremental registry migration without inventing local
model identities or silently disabling existing route consumers.

Harness execution consumes the same authority instead of maintaining a second wrapper map. Exact
model id, alias, then harness id form the resolution precedence; ambiguity fails closed. A declared
`capacity_source` binds quota evidence to its own provider ref (for example
`codexbar://provider/qwencloud`) even when the execution route has another name. Inline governed
execution and experimental delegated tmux sessions both resolve this tuple before work moves or a RunAttempt is
minted. Delegated activation additionally requires the explicit process-local
`--experimental-delegate-mutations` gate. A raw command override is not executable authority: delegated activation refuses it
until an authority can attest that exact executable/profile. The delegated argv policy is also
fail-closed: the current tuple-backed policy admits no argv or one non-flag positional prompt; it
refuses flag-shaped or multi-token argv, so permission/profile flags cannot borrow the wrapper's
attestation. RunAttempt records requested tuple/provider separately from observed
`model_id`/`provider_route`, which remain unknown until ExecutionRouteEvidence supplies them.

After the work and lease holder are validated, delegated activation consumes a read-only
harness-readiness check at that boundary. Permission authority and applied posture must agree; the
permission owner hashes the exact target bytes it validated, and the authority revision is re-read
after posture validation so neither unvalidated replacement bytes nor a mixed-generation check can
be attested.
The installed-hooks declaration must expose one exact native-blocking, all-tool/mutation-covering
`PreToolUse` binding whose matcher equals the current canonical harness matcher (`*` today), keeping
every shell, browser, connector, messaging, secret, install, deploy, and newly added tool behind the
action monitor. Read-only live reconciliation must still match that binding's id, matcher, invocation
revision, and handler revision—not merely some ready `PreToolUse` row. The current, non-degraded
InstructionPack must
agree with current policy/hook authorities, while the WorkEnvelope's ContextPack selects its
immutable owned EffectiveContract artifact by `contract_id`; rotation of the unrelated
`contracts/effective/latest.json` projection cannot invalidate older work.

Failure returns structured `readiness_unmet` evidence before capacity admission, event persistence,
tmux creation, or a `harness_execution` RunAttempt mint. A missing readiness checker fails closed.
The admitted evidence rows plus the full lane-capacity verdict are canonicalized into one
`snapshot_revision`. Immediately before mint, the lifecycle-admission lock re-reads the exact WorkEnvelope hash
and lease, re-resolves the execution profile, revalidates the compatibility attestation against the exact selected
harness/lane/model/provider-route/transport tuple and canonical plan identity, re-runs harness readiness and
capacity with the same captured inputs, and requires the same snapshot revision. Any changed authority or capacity
returns `authority_changed` with no mint, refusal-event persistence, or spawn. A successful activation carries the
revision-bearing snapshot, including the typed canonical execution-profile proof, in its returned result and persisted
`work.delegate.spawned` binding. Recovery revalidates the plan projection, proof body, reference, digest, and
aggregate readiness revision, so it can name what admitted the run without trusting mutable current
configuration.

**Teardown reaps what activation created.** A delegate leaves two artifacts that outlive its
process: the tmux session and the `git worktree lock` over its workspace. Every terminal path
(`DelegateSweep`, and the spawn-failure finalizer) releases both, and the sweep outcome always
reports what happened to the lock (`released` / `not_locked` / `held_by_live_attempt` /
`no_workspace_evidence` / `reaper_absent`) so "nothing released" stays legible instead of silent.
Ownership is the constraint, not convenience: the workspace comes from the terminalized RunAttempt's
own record, the path must actually be locked, and no other non-terminal RunAttempt may name that
workspace. On the session side the same principle applies — a delegate re-spawning over its own
recorded exit reaps only a DEAD pane under that name; a LIVE pane is the ownership uncertainty
`spawn_ambiguous` exists for and is never touched. Lock reaping is reported, never fatal: an
advisory lock that cannot be released must not strand a terminal work item. What teardown could not
reap is surfaced by `compound doctor`, which joins locked delegate workspaces against their
RunAttempt status and flags every lock whose owning delegates are all terminal; doctor reports and
never releases.

## Second-order effects (design against these)

- **Cheap-mediocre drift.** If the router optimizes cost/latency without *outcome* feedback, the
  system gets cheaper but worse. Antidote = **routing remorse**: sample cheap-route outputs against a
  stronger judge; promote a cheap route only after outcome evidence; auto-escalate for canonical
  memory, product positioning, taste, irreversible actions, or high-risk code.
- **Credentials become infrastructure.** Centralizing provider/account selection means agents stop
  carrying provider truth — but gateway config drift can drift *all* traffic. Treat gateway config
  changes as governed infrastructure that emits compound evidence.
- **Autonomy gets a real budget boundary.** Autonomy = a semantic gate (compound: risk tier, allowed
  providers, privacy class, budget) **+** a runtime gate (gateway: cost ceilings, quotas, provider
  restrictions, concurrency) **+** a state receipt after. A Tier-1 job cannot accidentally overspend
  or use a forbidden provider.
- **Native-direct vs ingress vs relay.** A client may talk directly to a vendor (gateway can only
  import metadata), through gateway ingress (full route/usage trace), or via a native relay (gateway
  leases native auth). Native relay is the hardest mode — keep it behind a conformance/fixture gate
  and fail-closed; integrate **read-only/observation first → controlled ingress → relay canary**.
- **Multimodal is not free.** A text+tools gateway should reject multimodal at ingress (not silently
  drop it). Route visual/taste work (screenshots, UI QA, image review) to a vision-capable harness or
  a manual lane, and record the text/judge calls around it — do **not** push visual work through a
  text-shaped pipe and quietly degrade product quality.

## What the gateway must NOT become

Not the source of truth (eval evidence is preview until compound promotes it for a scope); not the
whole `Harness` registry (it contributes facts; compound owns the registry); not a cheapness-only
optimizer (cost is one dimension among taste/judgment/outcome/privacy/latency/reliability); not
another dashboard tax. Gateway work counts only if it reduces provider/model ambiguity, creates
usable route evidence, enforces a real cost/risk gate, removes duplicate routing config, or enables a
real product/system loop.

The execution-plane construction track is in `../roadmap/phases.md` (P10 — execution route evidence).

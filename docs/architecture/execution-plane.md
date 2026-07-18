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

Harness and transport are independent selectors: for example, Claude Code may run over native transport or a
Switchback-compatible transport without becoming a different harness. Selecting Switchback does not let
Compound claim the provider/model that actually ran; those remain `unknown_until_receipt`.

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

Compound-owned route policy authority is separate from route execution evidence. `compound route
import-model-capabilities <model-capabilities.yaml>` imports generated model-router capability claims
and `routing_defaults` into `compound://registries/model-router`, records source authority, and leaves
the generated source view as rollback/evidence input. `compound route plan --capability-class <class>
[--privacy-sensitive]` only renders a planned lane chain with `lane_justification`: it does not call
models, rank live providers, mutate gateway config, or certify task success. Privacy-sensitive plans
filter to `local-batch` lanes; free judge/critique lanes are tripwires, not verdict gates.

Lane resolution is typed independently of its execution address. `provider_route` says how to call
the lane; `resolution.mode=adaptive` means the requested alias must be resolved by gateway evidence,
while `resolution.mode=direct` declares one or more exact provider/model response identities. A
legacy lane with no resolution block may still be planned and called, but it cannot contribute a
deciding autonomy sample. This permits an incremental registry migration without inventing local
model identities or silently disabling existing route consumers.

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

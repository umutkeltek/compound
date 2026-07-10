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

## Route evidence is a projection, not a verdict

> A gateway `RouteDecision` is **execution-route evidence, NOT proof of task success.**

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

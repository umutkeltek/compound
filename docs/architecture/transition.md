# The Transition Primitive + Second-Order Design

Compound's core object is not `MemoryRecord`. It is `Transition`.

A system **compounds** only when the output of today's run modifies the affordance landscape
of tomorrow's run. So the object the kernel optimizes is the state change itself:

```text
State_t  →  Action_t  →  Receipt_t  →  State_t+1
```

`State_t` = identity graph + memory graph + capability graph + tool/model/harness graph +
product/market signals + cost gradients + risk gates + taste standards + operator attention +
system trust + open loops + dead surfaces.

## The Transition object

A `Transition` is a state change plus its **projections** into multiple representational spaces.
The anonymous `Header` field supplies the universal envelope keys and flattens them into JSON; in
particular, `ids.work_id` is the root link from intent through transition and
`ids.transition_id` carries the transition identifier. The direct struct-field contract is:

| Go field | Go type | JSON tag |
|---|---|---|
| `Header` | `Header` | `-` |
| `SourceTraceID` | `string` | `json:"source_trace_id,omitempty"` |
| `BeforeRefs` | `[]string` | `json:"before_refs"` |
| `ActionRefs` | `[]string` | `json:"action_refs"` |
| `AfterRefs` | `[]string` | `json:"after_refs"` |
| `StateDelta` | `string` | `json:"state_delta,omitempty"` |
| `Projections` | `Projections` | `json:"projections"` |
| `SecondOrder` | `SecondOrder` | `json:"second_order"` |
| `Cause` | `*TransitionCause` | `json:"cause,omitempty"` |
| `OpensBranches` | `[]BranchRef` | `json:"opens_branches,omitempty"` |

The Transition-specific JSON body has this shape (the flattened `Header` keys precede it in a full
envelope):

```json
{
  "source_trace_id": "trace_...",
  "before_refs": ["claim_...", "metric_..."],
  "action_refs": ["run_..."],
  "after_refs": ["ev_...", "artifact_..."],
  "state_delta": "...",
  "projections": {
    "memory": ["claim_candidate_..."],
    "skill": ["capability://skill_or_procedure_offered"],
    "product": ["hypothesis_..."],
    "cost": ["cost_record_..."],
    "risk": ["tier_..."],
    "taste": ["taste_observation_..."],
    "routing": ["route_decision_..."],
    "human_attention": ["approval_needed_..."]
  },
  "second_order": {
    "future_behavior_changed": true,
    "should_harden": false,
    "should_create_rule": false,
    "should_decay": false
  },
  "cause": {
    "delta_cause": "accept",
    "caused_by_refs": ["work_..."],
    "next_consumer": "memory-review"
  },
  "opens_branches": [
    { "branch": "branch-lifecycle", "owner": "workbench", "kind": "opens" }
  ]
}
```
`runtime.md` and `contracts/README.md` reference this canonical shape. `source_trace_id` identifies
an originating raw trace when one exists.

It forces three questions on every run: **What changed? Across which spaces? What future
behavior should this affect?**

`projections.skill` may include capability refs from two sources: used capabilities from allowed recorded ActionIntents, and `capability://...` offers from the ContextPack. Offers alone are not proof the skill executed; actual mutation still needs ActionIntent, PolicyDecision, run/artifact refs, and Receipt state delta. Close/session-end automatically feeds the emitted receipt into CapabilityGraph when graph state exists; used nodes validate only from completed changed receipts, explicit `skills.missed` refs add `missed_when` graph context, and explicit `skills.false_triggered` refs add `false_triggered_by` graph context.

## The medium spaces (projections)

Do not collapse everything into one schema. The ledger preserves evidence; the system needs
*projections*. Same event, many projections.

```text
1. Identity space         entities, aliases, owners, paths, source bindings
2. Evidence / time space  traces, receipts, sessions, before/after states
3. Semantic / vector      fuzzy similarity, discovery, analogy, broad recall  (RENTED engine)
4. Graph space            skills, tools, models, products, decisions, claims, substitutions
5. Workflow space         jobs, DAGs, gates, scripts, handoffs, retries, escalations
6. Cost / latency space   tokens, sub/API/local routing, caching, provider limits, runtime
7. Risk / policy space    reversible vs irreversible, public/private, spend/destructive
8. Taste space            examples, anti-examples, aesthetic standards, product judgment
9. Product / market space analytics, experiments, pricing, paywalls, ASO, support, distribution
10. Human-attention space synthesis debt, interruption cost, trust, focus
```

### The central invariant (this is what "coherent tensors across media" means)

> **Every projection MUST preserve:** `entity_id, scope, evidence_ref, time, actor,
> uncertainty, risk, cost, state_delta`.

Coherence is not one schema. Coherence is the ability to transform between spaces without
losing those nine fields. If a projection drops one, fragmentation enters through a side door.

## Relationship to the ledger

`Transition` is a higher-order object over the existing `Evidence → Claim → Position → Pattern`
model. Projections are typed, citeable views; they do not create a second truth lifecycle.
Engines (space 3) remain rented and rebuildable. The ledger stays authority.

---

# Second-order design discipline

No field is neutral. A memory schema, a goal prompt, a skill trigger, a cost rule, a trace
format, even a naming convention changes the probability distribution of future actions. The
architecture is judged not by "does it have X?" but by **"when alive, what does each layer make
more likely tomorrow?"**

| Layer | Naïve view | Second-order reality | Primary failure to design against |
|---|---|---|---|
| Memory | storage | **gravity** — recalled claims bend future behavior | promote a weak claim → system manufactures coherence around a false attractor |
| Registries | inventory | **affordance attractors** — entries pull behavior toward them | add many skills → over-trigger → context bloat → taste flattens |
| Routing | cost optimization | **epistemic shaping** — chooses what *kind* of mind touches a problem | cheap-by-default becomes a silent worldview; quality degrades invisibly |
| Receipts | proof / audit | **training data** for the next system | agents optimize for receipt completion → bureaucracy → trust drops |
| Product feedback | analytics | **reality pushing back** | over-credit the last change → imitate the wrong pattern → damage product identity |
| Taste | preference | **policy surface** | optimize visible metrics + tests pass → competent mediocrity, "less yours" |
| Autonomy | permissions | **trust gradient** | punish risky proposals → only trivial safe moves → compounding dies |
| Harnesses | executors | **epistemic environments** | a workflow that worked in one harness silently breaks in another |

## Memory temperature (not just park/promote)

```text
ambient       safe to inject without asking
contextual    load only under matching scope/intent
candidate     visible to reviewers/distillers, not normal agents
volatile      useful now, expires unless reinforced
private       never ambient
operational   directly changes routing/tools/procedures
retired       kept for history, blocked from instruction
```

Every memory promotion is a controlled state mutation, not archival.

## Acceptance tests (a slice is "alive," not "archived," only if it survives these)

1. **Perturbation** — change one memory claim → only relevant scopes change recall; system
   explains why it mattered. (catches: ambient gravity too strong)
2. **Path-move** — move a project's folder → identity stable, old path retired binding, new path
   active. (catches: path-dependent identity fragmentation)
3. **Skill-misfire** — run a visual/UI task without the vision/taste skill → receipt detects the
   likely missing skill; future trigger probability rises. (catches: lazy-but-looks-successful)
4. **Counterfactual routing** — same job to small/frontier/script/human → explains tradeoff,
   records "routing remorse" if cheap underperforms. (catches: silent quality degradation)
5. **Product confounder** — a metric moves alongside a traffic spike → no causal claim; competing
   interpretations + review trigger. (catches: metric worship)
6. **Registry-attractor** — add an external skill → enters `candidate`, needs evidence before
   ambient triggering, does not bloat context. (catches: registry as noise factory)
7. **Control-plane tax** — a dashboard/readme/coherence change does NOT count unless it removes a
   duplicate owner, fixes a recurring failure, or creates runtime proof. (catches: map = work)
8. **Public/private contamination** — building a feature keeps generic contracts in public core
   and all personal paths/credentials/accounts in the private overlay. (catches: "private OS with
   prettier nouns")

## The evaluation rule

Every non-trivial run classifies itself: `outward_delta | memory_delta | rule_delta |
control_plane_repair | audit_only`. A receipt only matters if it becomes searchable experience
or subtractive pressure. The one question every receipt answers: **"Did this run alter the
future?"** If not, it is telemetry, not compounding.

## Future-branch semantics (second-order, operationalized)

Second-order asks "does this make X likelier tomorrow?" The operational form, learned under
execution pressure: a placed artifact compounds *nothing* if it never changes recall, routing,
action, evaluation, or pruning. Owner placement prevents fragmentation; it does not guarantee
compounding. So a `Transition`/`Receipt` declares the branch it opens and who consumes it:

- `opens_branches[]` — `{branch, owner, kind}`, kind ∈ `opens | blocks | supersedes | validates |
  retires | routes_to`. **SHIPPED (2026-07-24, work_18f8ce768471):** validated schema on
  `Transition` (`envelope.BranchRef`), the `compound work close --opens-branch slug:owner:kind`
  write door, and the consumer that un-deferred it — `internal/work` `BranchCoverage`, the
  deterministic read model over `events/tr_*.json` surfaced as `branch_debt` in
  `compound workbench`. Coverage is explicit-only: only `validates | supersedes | retires` on the
  same slug resolve a branch; a repeated `opens` is an expansion that keeps the original
  `opened_at`; covering an unopened slug is a dangling-cover integrity finding.
  Count semantics in `branch_debt.counts`: `opened`/`covered` are cumulative; `open` is the
  snapshot (`opened - covered`) — that is the debt numerator; `stale` is the subset of `open`
  past the 7-day window; `unverifiable_changed` is the count of transitions claiming
  `future_behavior_changed` with no branch and no `next_consumer` — the evaluation rule's first
  teeth. `blocks |
  routes_to` are neutral records. An open branch past the 7-day window surfaces as STALE debt —
  advisory visibility pressure, never auto-closed (humans stay on irreversible decisions).
  Transitions claiming `future_behavior_changed` with no branch and no `next_consumer` are counted
  as unverifiable claims — the evaluation rule's first teeth.
- `next_consumer` — who is expected to act next. No named consumer ⇒ `audit_only` until one exists.
- `delta_cause` — the causal *class* of a source-truth change (`repair | retire | accept |
  invalidate`), carried with the new value so a future agent chooses repair/retirement/acceptance
  instead of re-litigating the old value. A bare value change is ambiguous; the cause is the branch.

**Branch coverage — open or it bloats.** An opened branch is an open loop and must move through
`opened → expanded → resolved → retired`. Presence ≠ coverage: an upstream artifact must not keep
anyone on an earlier branch once downstream evidence already covers it — surfaces compute branch
*coverage*, not artifact *presence*. Every opened branch becomes an owner-bounded step, is explicitly
deferred, or is subtracted; nothing sits unclassified. **Subtraction is a branch operation:** each
retirement records the future wrong move it prevents (`prevents_branch`).

**Brief before mutate.** When evidence is too broad to act on, insert a *transition brief* that
converts the queue into decision classes and names the blocked moves — it narrows branch entropy
*without* mutating. A brief is permission to think, not to mutate; stateful action still requires a
later `ActionIntent` + `PolicyDecision`. This keeps `orient`/ContextPack (read) cleanly separate from
`ActionIntent`/policy (write).

**Anti-bloat pipeline.** An observation/evidence artifact is live only while its branch is open. Once
promoted into a durable contract/check/decision — or subtracted — its workbench entry is marked
`promoted → <destination>` and archived, so observation logs stay thin (consumed entries leave them).
This is the archive half of the compounding-knowledge loop (`../roadmap/phases.md`).

**Harness co-evolution (shipped subset).** `Transition.Cause` (`internal/envelope`) ships a proven
subset of the above: `delta_cause` + `caused_by_refs` (≥1, validated only when `Cause != nil`) +
`next_consumer` (empty ⇒ `audit_only`). `opens_branches[]` is ALSO shipped (see the branch entry
above); `prevents_branch` stays deferred — dead schema without a consumer. The `harness
co-evolution` slice wires Cause end to end: harness-canon files
are a source-manifest family; `compound source canon-observe` (Importer `sourceobs.ObserveCanonDrift`)
re-hashes them, parks one no-promote `CapabilityObs` candidate per drifted file, and the ObjectFamily
`capgraph.RecordCanonEvolution` mints ONE cause-carrying `Transition` projected onto the touched
capability nodes as `capability://<node-id>` in the flat `Projections.Skill` (no new `ProjectionKind`).
Two read surfaces: the manifest json (baseline of record) and `events/tr_*.json` filtered by the
`capability://<node>` projection.

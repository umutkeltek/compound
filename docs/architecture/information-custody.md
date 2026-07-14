# Information Custody: one physical home, one meaning owner

This document owns the **information-custody slice** of the architecture. It is not a second object model:
the envelope spine remains the runtime grammar, and the contracts remain the object model. This document
answers a narrower question that those documents previously left ambiguous: *where are the durable bodies,
who may interpret them, how do users edit and restore them, and what must be proven before a legacy owner can
retire?* ADR 0002 records the decision.

## 1. Four contracts that must never collapse

Calling all four "memory" recreates the fragmentation Compound is meant to remove.

| Contract | Owns | Does not own |
|---|---|---|
| **Physical custody** | exact recoverable bodies and source versions inside `COMPOUND_HOME`, with hashes, provenance, privacy, retention, and restore metadata | truth, relevance, promotion, or currentness |
| **Semantic authority** | entity identity, evidence, claims, positions, decisions, policy, registries, supersession, visibility, and retirement | source rendering or retrieval ranking |
| **Projection** | a rebuildable consumer view such as Markdown, Obsidian, a dashboard, a harness file, or a compact timeline | canonical bytes or an independent write lifecycle |
| **Index** | disposable FTS/vector/graph/fact acceleration over Compound-owned refs | canonical IDs, ambient eligibility, currentness, or truth |

An owned source body may contain a false or superseded statement. A promoted claim may cite several body
versions. A fresh Markdown projection may still display a disputed claim. A perfect vector hit may be excluded
by privacy or currentness policy. The separate contracts make those cases ordinary instead of exceptional.

**Physical ownership invariant:** a source family is not in Compound custody until an empty-home or isolated
restore can reproduce its declared body and required metadata while the original source is unavailable.
Inventory, a path, a hash, a database row count, or a search result is not custody.

## 2. The runtime habitat

The end-state top-level habitat is intentionally small:

```text
$COMPOUND_HOME/
  manifest.yaml
  config/       sources/       artifacts/     ledger/        registries/
  inbox/        work/          events/        context/       reviews/
  projections/  indexes/       archives/      tmp/
```

| Root | Durable responsibility |
|---|---|
| `config/` | explicit operator settings, policy inputs, adapter configuration, schema/contract locks, and secret references; never learned guesses |
| `sources/` | source-family records, capture generations, stable source identities, connector checkpoints, and custody manifests |
| `artifacts/` | immutable content-addressed bodies and large evidence; a body ref survives source-path and index changes |
| `ledger/` | append-only evidence, claims, positions, decisions, transitions, and route evidence |
| `registries/` | current inside-world and outside-world authority: entities, projects, services, capabilities, policies, deployments, adapters, and source bindings |
| `inbox/` | untrusted observations and candidates awaiting reject, park, promote, or operationalize |
| `work/` | WorkEnvelope lifecycle, leases, checkpoints, runs, and delivery proof |
| `events/` | normalized raw runtime/hook/tool events; evidence, not automatically meaning |
| `context/` | ContextRequests, frozen ContextPacks, inclusion/exclusion reasons, and exact injected text |
| `reviews/` | adjudication, owner writeback, contradiction, policy, and retirement review artifacts |
| `projections/` | rebuildable Markdown, dashboard, harness, timeline, and export views |
| `indexes/` | disposable local or remote retrieval state and build manifests |
| `archives/` | sealed local snapshots, import/export bundles, retention manifests, and rollback material |
| `tmp/` | restart-safe scratch only; no fact or cursor whose loss changes behavior |

The shared **HabitatCatalog** now declares every observed top-level root and drives home scaffolding, placement's
canonical allow-set, doctor inventory, backup inclusion, compaction, and restore validation. An unknown top-level
directory or file makes doctor and backup fail. Transitional feature-shaped roots remain explicit `bridge`
entries: each names its canonical destination and retirement condition, is preserved by backup, and is excluded
from canonical placement so new writes still receive a redirect. This is inventory/control ownership, not a claim
that the layout migration is complete; path constructors move behind the catalog one producer at a time before
their bridges retire. Behavior-bearing historical files under `projections/` are catalog exceptions: backup keeps
them and compaction refuses them until their producers cut over to their true typed homes. Connector offsets belong
with the source adapter, work cursors with work, and schema/contract locks with config.

## 3. Information placement grammar

| Information encountered | Physical body | Meaning owner | User-facing form |
|---|---|---|---|
| authored note, policy, document, or attachment | immutable artifact plus versioned SourceManifest | evidence/candidate/claim or the relevant typed registry after adjudication | Markdown/Obsidian projection |
| conversation or imported corpus page | transcript/export artifact plus event/source records | observations first; promoted claims only after adjudication | conversation/history projection with drilldown |
| explicit user preference | source evidence plus versioned config mutation receipt | scoped `PreferenceSetting` in config/registry authority | settings screen and explain view |
| inferred preference | observation artifact if needed | candidate or claim; never silently a setting or policy | review suggestion |
| one-task instruction | ContextRequest/TaskContract body | frozen TaskContract on the WorkEnvelope | task view; expires with work |
| approval, permission, or prohibition | decision evidence | Policy/StandingGrant authority | policy review and audit trail |
| project, service, host, source, or tool fact | source evidence | typed registry entry with status and provenance | topology/dashboard projection |
| deployed location or version | runtime observation/heartbeat evidence | `RuntimeBinding` with environment, artifact/config revisions, currentness, and health refs | deployment status card |
| procedure, skill, model, harness, or adapter | source/evaluation evidence | CapabilityGraph plus lifecycle and evaluation edges | task-scoped capability offer |
| action, model call, cost, or result | event and provider/gateway evidence | Work/Receipt/Transition and ExecutionRouteEvidence | activity, cost, and “why” views |
| vector, FTS, graph, fact row, or embedding | rebuildable index bytes | no meaning authority | search accelerator only |

Two sharp boundaries follow:

- An explicit preference may become config through a governed write. A learned preference stays a claim until
  the operator accepts it. An enforceable rule is policy, not a “strong preference.”
- A deployment is not a prose fact. It is a temporal `RuntimeBinding`; the old binding becomes stale or
  retired when a new one is evidenced. A dashboard cannot make it current by rendering it.

## 4. Source custody and meaning flow

```text
register source identity
  → capture exact version into artifacts
  → verify body/hash/metadata without the original
  → record SourceManifest lineage
  → emit EvidenceItem / SourceObservation
  → resolve entity (uncertain means parked)
  → reject | park | promote | operationalize
  → Claim / Position / Decision / RegistryEntry
  → ContextPack, projection, and index
```

Capture and promotion are independent transactions. Failure after capture leaves owned evidence, not ambient
truth. Failure after an observation leaves a candidate, not a half-written registry. A recapture of the same
stable source identity creates a new version and explicit supersession; it does not replace history. Identical
bodies deduplicate. A path move must not create a new identity once an explicit source ID or entity binding is
known.

`source_in_place` remains readable only as a migration bridge. `owned_snapshot` means Compound can serve the
body from its artifact store. A database export manifest that lists rows or hashes but does not custody each
declared recoverable body remains `custody_complete=false`. Database-native dumps, Git history, directories,
symlinks, xattrs, ACLs, and compound document packages require source-family adapters; a regular-file snapshot
must not overclaim those semantics.

The first database-export custody adapter uses `manifest-create --from-export ...
--dump ...` accepts a dump only when its streamed SHA-256 equals the export's exact `dump_ref`, then records
`dump_body_ref` and `dump_bytes` at family scope. A mismatch publishes no artifact. `manifest-doctor` keeps an
inventory-only export pending and fails a missing, corrupt, wrong-ref, or wrong-size owned dump;
`manifest-materialize-dump` streams the private dump back with mode `0600` without requiring the original.
This proves dump-byte custody only. Database restore, schema/application validity, real-consumer replay,
negative control, write cutover, quiet window, and the operator retirement act remain separate proofs.

The current generic v0.2 SourceManifest slice owns regular-file bytes, media type, byte count, permission mode,
stable inferred source key, and version lineage. Its inferred key uses explicit entity refs when available and
otherwise path identity. Explicit source IDs and path-move reconciliation are required before bulk migration.

## 5. Recall, context injection, and hooks

An index may return candidate refs; Compound re-resolves them against ledger identity, visibility, currentness,
scope, privacy, and supersession before inclusion. The index result itself never enters ambient context.

```text
task + live scope
  → ContextRequest
  → EffectiveContract
  → ledger/registry retrieval (index-assisted, authority-checked)
  → ContextPack {included + reason, excluded + reason, source revisions}
  → exact injected bytes persisted
  → harness consumes
  → Receipt cites context_pack_id and resulting state delta
```

This makes “why did it know that?” answerable from a stable chain: injected text → ContextPack entry →
claim/position → evidence → source version/body. It also makes “why did it not know that?” answerable from
`excluded[]`, freshness, privacy, uncertainty, or capability/readiness evidence.

Hooks remain thin clients:

- Session start and prompt hooks request/freeze context; they do not maintain a private memory cache.
- Pre-tool hooks turn intended mutations into `ActionIntent → PolicyDecision`.
- Post-tool hooks record normalized evidence and verification candidates.
- Stop/session-end hooks record outcome evidence on the same `work_id` where the harness supports them.
- Missing, stale, or fail-open hook capability is recorded as readiness evidence, never hidden by a generic
  “installed” flag.

## 6. Markdown, Obsidian, dashboard, and settings clients

Markdown and Obsidian remain first-class **interaction formats**, not alternate authorities. A projected note
carries stable object IDs, source/ledger revision, projection revision, and freshness metadata. Regeneration is
safe because the authoritative objects and owned source bodies live elsewhere.

An edit follows a governed round trip:

```text
projection edit
  → capture edited bytes as evidence
  → compare projected base revision with current authority
  → conflict, reject, park, or create typed writeback proposal
  → ActionIntent → PolicyDecision → owner write
  → Receipt + Transition
  → regenerate projection
```

Direct ledger mutation from a file watcher is forbidden. Concurrent authority drift creates a reviewable
conflict; it never uses last-write-wins. Attachments enter artifact custody. Deleting a projected note requests
retirement; it does not erase shared evidence or claims.

The ordinary dashboard is a bounded projection over the same owners. It should answer, without exposing the
full kernel by default:

- **What changed?** state delta, responsible work, before/after, and undo/rollback availability.
- **Why did it know or do that?** context, evidence, policy, capability, execution, and receipt drilldown.
- **Where is it deployed?** current RuntimeBinding plus freshness and health.
- **What needs me?** held policy, contradiction, writeback, restore, or retirement gates.
- **Is my information safe?** custody coverage, source drift, backup age, last isolated restore, index health.

Buttons that mutate settings, memory, policies, projections, or deployments are clients of the same action and
policy path. A dashboard never grows a hidden write API.

## 7. Compound and execution-gateway boundary

A harness is not a model, a model is not a provider, native inference effort is not orchestration, and an alias
is not an observed identity. Compound stores those axes independently.

Compound owns the semantic request: task/job class, agent role, harness requirements, privacy/risk, semantic
effort, orchestration policy, budget ceilings, capability constraints, standing grants, and the WorkEnvelope.
The execution gateway owns live provider/model/account availability, transport, codec compatibility, retries,
circuits, fallback execution, provider catalog revision, and exact resolution. The handshake is:

```text
Compound ExecutionProfile + budget + policy
  → execution gateway revision-bound transport request
  → resolved provider/model/native effort/effective parameters + gateway request id
  → ExecutionRouteEvidence
  → Receipt/Transition joined to task outcome
```

Historical evidence stores both requested alias/profile revision and observed provider/model. Alias drift must
not rewrite history. An explicitly requested unsupported native effort fails or records an allowed translation;
it never silently changes effort. `ultra`, `xhigh`, and orchestration policies are not interchangeable.

The rollout is deliberately A then B. In A, the gateway owns and validates self-contained lane definitions and
provider/account reality. In B, Compound's `ExecutionProfile` becomes semantic authority and the gateway lowers
it into an executable lane. A specifically named interactive driver may choose the highest native effort;
that effort is never the global default, is not inherited by child agents, mining, or routine work, and every
child operation receives its own bounded profile and receipt requirement. “Route accepted” remains route
evidence, not task success.

Required hardening before B is complete: validate aliases against a revisioned gateway catalog, preserve the
full requested/effective execution tuple in Panel and receipts, make the highest native effort reachable only
through explicit profile semantics, accept valid cross-model harness combinations, and distinguish
no-account/circuit-open from no-capable-target failures. Those are open integration gates, not reasons to
duplicate the gateway inside Compound.

## 8. Backup, restore, multi-device, retention, and deletion

A backup is successful only when it restores behavior, not merely bytes.

- A sealed backup includes manifest/catalog revision, config, sources, artifacts, ledger, registries, work,
  events, context, and reviews. Rebuildable projections/indexes may be omitted only when rebuild is proven.
- Private backups are encrypted; encryption keys/recipients are not stored in the same archive. At least one
  verified copy lives outside `COMPOUND_HOME` and outside the source disk.
- Restore targets an empty isolated home, verifies hashes/schema/refs, rebuilds projections/indexes, replays a
  real consumer, and runs a negative control with legacy sources unavailable.
- A new-machine flow restores identity and settings before enabling writers or hooks. Provider credentials are
  rebound through secret references, not copied into public or plaintext config.
- One authoritative home has one fenced writer at a time. Multi-device replication is log/object replication
  or sealed-backup transfer; live SQLite/JSONL folders are never merged by generic file sync. A writer handoff
  requires epoch/lease proof. Divergent offline writes re-enter as evidence/candidates, not file merges.
- Retention is policy by source/body/privacy class. Content-addressed bodies shared by live refs cannot be
  removed until every reference and backup-retention obligation is satisfied. Deletion emits policy, receipt,
  tombstone, and deletion-certificate evidence. Retirement never implies deletion.

Replication, encrypted backup policy, restore rehearsals, and reference-aware deletion are target contracts
until a vertical slice proves them. No legacy writer or store retires based on this document alone.

## 9. Custody and retirement state machine

```text
registered
  → capturing
  → owned_snapshot
  → verified
  → compound_primary_writer
  → legacy_frozen
  → quiet_window_proven
  → archived
  → retired
```

Every arrow needs evidence. The gates are:

1. inventory all information classes, bodies, identities, writers, readers, jobs, hooks, projections, and
   unknowns;
2. capture exact bodies and source-family metadata into Compound custody;
3. perform an isolated restore with the originals unavailable;
4. shadow semantic lookup and write behavior against frozen fixtures and real traffic;
5. replay each real consumer through Compound;
6. disable the old path in an isolated negative control and reject silent fallback/false green;
7. cut over governed writes, then freeze the old writer;
8. observe a measured quiet window for unexplained legacy reads/writes;
9. archive with a verified rollback procedure;
10. request an explicit operator decision for service shutdown, destructive retirement, or deletion.

Brain-like Markdown, database retrieval stores, and control-plane repositories each need their own rows because
their bodies, writers, and consumers differ. A single “OS absorbed” status cannot retire all three. The end
state is subtraction: Compound owns durable bodies and meaning; Markdown is a client, indexes are rented,
the gateway executes, and legacy systems are evidence/archives or gone.

# Source Adapters And Generated Views

Source adapters let a running Compound instance learn from existing systems without
turning those systems into permanent peer authorities.

The hard case is generated files: agent instructions, dashboard JSON, rendered
registries, hook configs, skill manifests, and other read models. They are useful
evidence, but they are not canon by themselves.

```text
external source or generated view
  -> SourceObservation
  -> EvidenceItem / Candidate / ProjectionFreshness
  -> AdjudicationDecision
  -> ledger object or NoPromoteDecision
  -> optional OwnerWritebackGate
```

## Rule

```text
Adapters observe. They do not promote.
Generated views prove freshness or drift. They do not prove truth.
Writeback requires an OwnerWritebackGate plus PolicyDecision.
```

This is the difference between using an existing operating system as a dogfood
source and rebuilding its fragmentation inside Compound.

## SourceObservation

Every adapter emits observations with enough metadata to answer: what was read,
what kind of surface it was, who owns it, what it was generated from, what changed,
and what Compound object family it may affect.

Minimum shape:

```json
{
  "schema_version": "compound.source_observation.v0.1",
  "id": "srcobs_...",
  "adapter_id": "operator-os.generated-views",
  "source_system": "operator-os",
  "observed_at": "2026-06-29T00:00:00Z",
  "source_ref": {
    "path": "operator-os/spine/views/skill-manifest.json",
    "kind": "generated_view",
    "owner": "spine",
    "source_class": "generated",
    "revision": "sha256:..."
  },
  "generated_view": {
    "view_kind": "skill_manifest",
    "consumer": "agent harnesses",
    "generated_from": [
      {
        "path": "operator-os/spine/skill-manifest.ts",
        "revision": "sha256:..."
      }
    ],
    "generator": "bun operator-os/spine/generate.ts",
    "marker": "generated",
    "freshness": "fresh",
    "drift_reason": null
  },
  "maps_to": [
    {
      "object_family": "capability",
      "candidate_kind": "capability_observation",
      "entity_hint": "skill-manifest",
      "promote_allowed": false
    }
  ],
  "evidence_refs": ["artifact_sha256_..."]
}
```

`promote_allowed: false` is intentional for generated views. A generated file can
support a candidate or freshness verdict, but the promotion basis must come from a
source owner, decision record, runtime proof, explicit confirmation, or trusted
deterministic import.

## GeneratedViewObservation

Generated views get one extra interpretation layer:

```text
view exists
view has marker
view was built from declared sources
view hash matches current generator output
view consumer is known
view is fresh | stale | drifted | unknown
view maps to one object family
```

The output is `ProjectionFreshness`, not canon. If a dashboard says a service is
healthy, Compound records that the dashboard projection said so at a time and
revision. Runtime truth still needs a runtime source or ServiceContract evidence.

## Adapter Port

The public seam should stay small:

```text
DiscoverSources(root, config) -> []SourceRef
Observe(ref) -> SourceObservation
Map(obs) -> CandidateSet + ProjectionFreshness
Explain(obs) -> authority chain
```

Private adapters can carry local paths and account-specific logic. Public adapters
must use synthetic fixtures and relative paths.

## Operating-System Dogfood Mapping

An existing operating system should enter Compound through observations, not file
copies. The mapping is by surface *kind*, not by any specific repo layout:

| Operating-system surface | Compound interpretation |
|---|---|
| generated spine views (`spine/views/*`) | `GeneratedViewObservation` + `ProjectionFreshness` |
| dashboards / read models | `ProjectionFreshness` through dashboard read-model adapter; card/source coverage evidence, never authority |
| event streams (`events/*.ndjson`) | `TraceEnvelope` / event evidence |
| parked memory candidates -> curated knowledge markdown | `MeaningWritePlan` review artifacts under `COMPOUND_HOME`; no direct source mutation or memory promotion |
| append-only ledgers (`ledger/*.ndjson`) | receipt / route / cost evidence |
| contract files | source material for contract candidates |
| source-control snapshots | `SourceAuthority` status claims via read-only git inspection; branch/head/ahead/behind/dirty counts only, no cleanup/staging/push authority; `PreToolUse` may consume the same observation to hold dirty-repo mutations |
| registries | `RegistryEntry` candidates |
| tool resolver / MCP catalogs | `ToolTopologyAdapter` -> `CapabilityGraph` tool/mcp/adapter nodes + fallback edges from normalized topology YAML or generated resolver JSON; imported topology source also records `SourceObservation -> capability_observation candidate -> NoPromoteDecision` |
| skill manifests / external skill catalogs | `SkillManifestAdapter` -> `CapabilityGraph` skill/lens/procedure nodes + graph edges from normalized manifests or generated skill-manifest JSON; imported manifest source also records `SourceObservation -> capability_observation candidate -> NoPromoteDecision`; offers only, never execution |
| work packets | WorkEnvelope / Receipt / Transition evidence |
| runtime / service contracts | `RuntimeBinding` / `ServiceContract` candidates; failing doctor reports can emit gated runtime repair-plan artifacts without owner mutation |
| product project/release contracts | `ProductContractAdapter` -> existing `Product` registry bundle; deterministic identity/surface/channel/privacy claims only, never analytics signals or causality |
| decision records | `DecisionLifecycle` candidates; brain narrative markdown import records note evidence, derives stable ids from page metadata/heading when needed, extracts explicit verdict/review-trigger text, parks incomplete lifecycle proof, and emits reviewable lifecycle repair artifacts through the same repair-plan path; policy markdown source import records source evidence, supports single-file and directory batch import through the same adapter, preserves supersedes/replaced_by lifecycle transitions, gives each decision a stable entity, parks incomplete or non-accepted decisions, verifies source templates/docs contain lifecycle fields, emits reviewable lifecycle repair artifacts, and applies reviewed lifecycle fields only on explicit write |
| knowledge-base pages | Evidence / Claim / Position candidates |
| product analytics exports | `ProductSignalExport`, App Store Connect datapoints snapshot rows, and explicit-window App Store Connect sales-series rows -> `ProductSignal` evidence + product `Transition` projection, never causality; ASC datapoints are neutral provider observations (before=after), while ASC sales-series requires explicit before/after ranges and a change event; unknown/unsupported source fields fail strict doctor and may record `NoPromoteDecision`; source-issue review records closure evidence while preserving the original no-promote row; product timeline reads valid signals plus open/closed product-scoped source issues together |
| a retrieval / embedding store | retrieval engine or source index, never authority |

A concrete mapping for a specific operating system — real repo paths and named
tools — belongs in that operator's private overlay (`docs_private/`), never in
this public doc. Public adapters use synthetic fixtures and relative paths.

## First Acceptance Test

The first implementation should be deliberately narrow:

```text
one generated operating-system view
 -> ProjectionFreshness
 -> SourceObservation
 -> method_upgrade candidate no_promote
 -> ProjectionRegenerationPlan review artifact
 -> source doctor reports fresh/stale/drifted

one tool resolver view
 -> ToolTopologyAdapter
 -> CapabilityGraph offers
 -> SourceObservation
 -> capability_observation candidate no_promote

one skill manifest view
 -> SkillManifestAdapter
 -> CapabilityGraph offers
 -> SourceObservation
 -> capability_observation candidate no_promote
```

This proves Compound can learn from generated files without making them new truth
owners.

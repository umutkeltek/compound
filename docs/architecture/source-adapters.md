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
    "path": "umut-os/spine/views/skill-manifest.json",
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
        "path": "umut-os/spine/skill-manifest.ts",
        "revision": "sha256:..."
      }
    ],
    "generator": "bun umut-os/spine/generate.ts",
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

The current operating system should enter Compound through observations, not file
copies:

| Operating-system surface | Compound interpretation |
|---|---|
| `umut-os/spine/views/*` | `GeneratedViewObservation` + `ProjectionFreshness` |
| `umut-os/dashboard/*` | projection evidence, never authority |
| `umut-os/events/*.ndjson` | `TraceEnvelope` / event evidence |
| `umut-os/ledger/*.ndjson` | receipt / route / cost evidence |
| `umut-os/contracts/*` | source material for contract candidates |
| `umut-os/registry/*` | `RegistryEntry` candidates |
| `umut-os/packets/*` | WorkEnvelope / Receipt / Transition evidence |
| `infra-os` runtime contracts | `RuntimeBinding` / `ServiceContract` candidates |
| `decisions` | `DecisionLifecycle` candidates |
| `brain` pages | Evidence / Claim / Position candidates |
| `gbrain` | retrieval engine or source index, never authority |

## First Acceptance Test

The first implementation should be deliberately narrow:

```text
one generated operating-system view
  -> SourceObservation
  -> artifact hash
  -> ProjectionFreshness
  -> capability or authority candidate parked/no_promote
  -> source doctor reports fresh/stale/drifted
```

This proves Compound can learn from generated files without making them new truth
owners.

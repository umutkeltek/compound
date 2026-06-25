# Compound Architecture

Compound is a local, adaptive information kernel. Its job is to make information
placement explicit, evidence-backed, and easy to query without turning the system
into a scattered microservice estate.

## Architectural Thesis

Compound is a **local monolith with deep internal modules**. Agents and users
should see one coherent information system:

```text
source material
-> evidence
-> candidate claims
-> promotion / adjudication
-> positions / patterns
-> context packs
```

The implementation can have packages, adapters, and optional engines, but those
are internal seams. They must not become separate authority centers.

## Why Not Microservices

The reference-system failure mode was clear: many useful surfaces become hard to
operate when every tool has a different root, cwd assumption, generated view, and
authority interpretation.

Compound removes that tax by giving agents one stable information interface:

```text
compound recall ...
compound pack ...
compound promote ...
compound build ...
compound reconcile ...
```

Adapters can read files where they already live, but Compound owns the lifecycle
of derived knowledge.

## Deep Modules

| Module | Seam | What It Hides |
|---|---|---|
| `memory` | object types | evidence/claim/position vocabulary and lifecycle states |
| `ledger` | append/read JSONL by kind | local file placement and append-only persistence |
| `scope` | session -> scope packet | cwd-independent attribution and weighted signals |
| `recall` | query -> plan/result | hard filters, promotion visibility, engine breadth |
| `authority` | domain source -> entities/claims | external authority source shape |
| `decisions` | decision source -> claims | decision source formats and status mapping |
| `reconcile` | claims -> supersession edges | alive-memory adjudication |

Deletion test: removing any module should leave a module-shaped hole behind a
narrow seam. It should not force callers to learn many file formats or paths.

## Information Placement

Compound supports two placement modes.

### Compound-Owned

Compound owns canonical ledger objects:

- `evidence`
- `entities`
- `claims`
- `positions`
- `patterns`
- `edges`
- `scope_assignments`

These live under one ledger root as append-only JSONL files.

### Source-In-Place

External sources stay where they belong:

- repository files;
- notes;
- decision documents;
- runtime contracts;
- issue trackers;
- transcripts.

Compound stores durable identifiers, evidence references, claims, positions, and
citations. It does not need to copy every source into one folder to make it
findable.

## Cwd Independence

Compound must not infer truth from the current working directory. Cwd is a weak
operational hint, not placement authority.

Correct attribution comes from:

1. explicit identifiers;
2. registered entity aliases;
3. content signals;
4. decision-turn signals;
5. source metadata.

If attribution is unclear, Compound returns `unresolved` or `multi_scope` instead
of guessing.

## Extensibility

Domains extend Compound through adapters and entity/claim vocabularies, not by
forking the core.

An adapter may define:

- source readers;
- entity seeds;
- path canonicalization;
- visibility handling before recall;
- positions that represent current stance for a domain.

Adapters may not redefine core lifecycle semantics.

## Public Core And Private Overlay

The open-source core should contain:

- core object model;
- local ledger;
- scope resolver;
- recall planner;
- promotion and reconciliation lifecycle;
- context-pack assembly;
- synthetic fixtures;
- adapter interfaces and safe sample adapters.

Private overlays should contain:

- personal memory;
- private project registries;
- private decisions;
- credentials;
- local paths;
- private retrieval engine configuration;
- organization-specific policies.

The public core should be useful without a private overlay. A private overlay can
be the demanding dogfood adapter, but it is not the product.

## Immediate Proof Standard

Before adding new layers, Compound should prove:

```text
synthetic session
-> scope packet
-> evidence item
-> candidate claim
-> promoted claim
-> position
-> context pack
-> supersession
-> updated context pack
```

No model calls are required for this proof. The point is to prove placement,
lifecycle, and findability.

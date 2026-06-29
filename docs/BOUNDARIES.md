# Compound Boundaries

Compound's public architecture is a general information system. It must not ship
private data, private paths, or private operating doctrine.

## Boundary Rule

```text
core lifecycle is public
domain adapters may be public or private
user data is private
```

## Public Core

Public Compound may include:

- Evidence, Entity, Claim, Position, Pattern, and Edge types;
- append-only ledger;
- scope resolver;
- recall planner;
- promotion gate;
- reconciliation and supersession logic;
- context-pack construction;
- synthetic examples;
- safe file/import adapters.

## Public Domain Adapters

A public adapter is acceptable when it contains no private data and no private
account assumptions.

Examples:

- generic markdown notes adapter;
- generic git repository adapter;
- generic JSONL transcript adapter;
- generic issue tracker adapter with fake fixtures.

## Private Overlay

Private overlays include:

- private notes and transcripts;
- private project registries;
- private infrastructure data;
- private decision ledgers;
- personal doctrine;
- credentials and account configuration;
- local retrieval engine configuration;
- organization-specific policies.

Private overlays can use the same core interfaces, but they must never be tracked or
shipped. They live either outside the public repository, or in a gitignored in-tree
directory (`docs_private/`) that is never committed. A gitignored in-tree overlay is
convenient but reachable by `git add -f` or a removed ignore rule, so the public/private
contamination check plus a redact scan gate every commit.

## Adapter Contract

Adapters translate external sources into objects:

```text
external source -> Evidence / Entity / Claim / Edge
```

Generated-file adapters translate generated views into observations first:

```text
generated view -> SourceObservation -> ProjectionFreshness / parked candidate
```

A generated view can prove freshness or drift. It cannot prove truth by itself.

Adapters can also render core objects back to source:

```text
Position / ContextPack -> markdown / JSON / CLI output
```

Adapters must not:

- create a second truth lifecycle;
- bypass promotion rules;
- rely on cwd as authority;
- leak private source bodies into public fixtures;
- require a particular user's directory layout.

## One Query Path

Agents should not need to know whether a fact came from a note, repository,
imported authority file, or runtime contract. They should ask Compound:

```text
compound recall "<query>"
compound pack "<topic-or-entity>"
compound authority <entity>
```

Compound decides which placed objects and source-in-place citations apply.

## Public Fixture Rule

Every public behavior must be demonstrable with synthetic fixtures:

- no real names;
- no local user paths;
- no private repo names;
- no credentials;
- no private transcripts.

The private reference system can prove stress realism, but public tests prove
portability.

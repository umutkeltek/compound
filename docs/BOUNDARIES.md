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

## Private Runtime Data And Overlay

Private runtime data includes:

- private notes, transcripts, project/infra data, decisions, and personal doctrine custodied inside a running
  `COMPOUND_HOME`.

Private overlays configure ingress and external access:

- credentials and account configuration;
- local retrieval engine configuration;
- organization-specific policies.

Private runtime data and overlays use the same core interfaces, but they must never be tracked or
shipped. Overlay inputs live either outside the public repository, or in a gitignored in-tree
directory (`docs_private/`) that is never committed. A gitignored in-tree overlay is
convenient but reachable by `git add -f` or a removed ignore rule, so the public/private
contamination check plus a redact scan gate every commit.

CI (`.github/workflows/ci.yml`) enforces a portable second line on every push and pull request: the
fresh-clone-portable subset of `compound proof-check` (run via `proof-check --ci`, single-sourced
from `cmd/compound/proof_check.go` — go test/vet/build, gofmt, `git diff --check`, `architecture
doctor --strict` including the armed doc-leak gate, and `memory doctor`) plus a `gitleaks` secret
scan (config `.gitleaks.toml`). The armed doc-leak gate enforces generic identity-leak classes
(non-RFC2606 email shapes; boundary-guarded absolute /Users|/home path shapes) over every tracked
text file — root files, docs, skills, and source alike — with no operator overlay required. The
`redact` public-push floor is the richer, local gate (it also detects PII, private paths, and
private-tool names a generic scanner misses) and remains required before a public push; the CI
gitleaks step complements it because `redact` is a local tool not installed on a CI runner.

## Adapter Contract

Adapters translate external sources into objects:

```text
external source -> Evidence / Entity / Claim / Edge
```

Generated-file adapters translate generated views into observations first:

```text
generated view -> ProjectionFreshness -> SourceObservation / NoPromoteDecision -> ProjectionRegenerationPlan
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
compound memory recall "<query>"
compound memory query "<topic-or-entity>"
compound authority <entity>
```

Compound decides which owned objects and source-version citations apply. An external path may remain
migration/rollback provenance, but it is not completed physical custody.

## Public Fixture Rule

Every public behavior must be demonstrable with synthetic fixtures:

- no real names;
- no local user paths;
- no private repo names;
- no credentials;
- no private transcripts.

The private reference system can prove stress realism, but public tests prove
portability.

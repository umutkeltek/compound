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

Routine CI (`.woodpecker.yml`) runs on the owned Woodpecker agent for scheduled/manual builds (and
push/pull-request events when a forge webhook is reachable), so ordinary verification does not
consume GitHub-hosted Actions minutes. Stable release tags use the separate, commit-pinned
`.github/workflows/release.yml`: a read-only Linux job builds the candidate twice, read-only
`ubuntu-24.04` → `linux_amd64` and `macos-15` → `darwin_arm64` jobs clean-install the exact
candidate, and only the final attestation and draft-publication job receives write authority. Routine CI
enforces the fresh-clone-portable subset of `compound proof-check`
(run via `proof-check --ci`, single-sourced from `cmd/compound/proof_check.go` — go test/vet/build,
gofmt, `git diff --check`, `architecture doctor --strict` including the armed doc-leak gate, and
`memory doctor`) plus the console subset (`proof-check --ci-console`) and a `gitleaks` secret scan
(config `.gitleaks.toml`). The Go step mounts repository-scoped named build and module caches; test
results remain uncached (`go test -count=1`), the source checkout and `COMPOUND_HOME` stay outside
those volumes, and another repository must use different cache names. The armed doc-leak gate enforces generic identity-leak classes
(non-RFC2606 email shapes; boundary-guarded absolute /Users|/home path shapes) over every tracked
text file — root files, docs, skills, and source alike — with no operator overlay required. The
`redact` public-push floor is the richer, local gate (it also detects PII, private paths, and
private-tool names a generic scanner misses) and remains required before a public push; the
Woodpecker gitleaks step complements it because `redact` is a local tool not installed on the CI
runner.

The release workflow separately proves the exact stable tag resolves to the workflow SHA, that SHA
is contained in freshly fetched remote `main`, the canonical `proof-check --ci` gates pass, a real
`release export-public` succeeds, and the pinned gitleaks scan is clean before release artifacts are
built. A rerun can resume only an unpublished draft with the exact tag, full-SHA target, and sealed
nine-file inventory; published or mismatched releases fail closed. Tag immutability, protected-main
rules, and activation of a private vulnerability-reporting channel remain repository-administrator
pre-tag gates. The workflow documents and verifies those prerequisites but does not pretend it can
activate external repository policy.

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

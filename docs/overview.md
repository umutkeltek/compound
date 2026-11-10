# Compound overview for contributors

This page explains why Compound exists, how it is built, where its state lives, and the command
map. The [README](../README.md) is the short version for a first-time reader.
[Getting started](getting-started.md) is the install guide. Build, test and landing rules are in
[CONTRIBUTING.md](../CONTRIBUTING.md).

## Why it exists

Four problems keep coming back when agents do real work:

- **Rediscovery.** An agent spends rounds working out what the system already knew, because
  nothing handed it that knowledge when it needed it.
- **"Landed" is not "working".** A change merges, a status note says done, and the behavior never
  changed, because nobody ran the real flow.
- **Silent schedules.** A scheduled job exits 0 while broken, and nothing notices that its effect
  never appeared.
- **Decisions as homework.** Every judgment reaches you as a command to decode or a wall of
  context, instead of one plain question with one action.

Compound treats each as a loop. Evidence comes in, a receipt goes out, and the next run can see
what changed. When nothing changed, Compound records that gap too.

## How it works

Five places, each with one job:

- **The door.** Every task, observation or model output arrives as a typed envelope and is
  validated before it can matter.
- **The ledger.** Meaning has one home. Nothing becomes shared truth without a recorded verdict,
  and a replacement is a new record, not an edit.
- **The edge.** Models, harnesses and engines are rented. They propose, extract and execute; they
  never own truth, promotion or policy.
- **The gate.** Every change asks policy first. Destructive and irreversible actions stay with you
  by construction.
- **The receipt.** After an action, a receipt records what ran, and a transition records what it
  means for future behavior.

The typed objects behind those places, for contract consumers:

```text
ContextRequest
-> ContextPack        the briefing: what the agent is told, and what was left out
-> WorkEnvelope       the job: one stable identity for one unit of work
-> ActionIntent       what an executor wants to do
-> PolicyDecision     the gate's answer: allow, deny, or hold
-> Receipt            proof the job ran: what was touched, what came out
-> Transition         the ledger entry: state A -> B because of this work
-> candidates / adjudication / projections
```

Memory is one object family on this spine, next to decisions, capabilities, products, route
evidence, runtime bindings and policy.

Design rules: look before acting; keep evidence, not truth; serve only the context a task needs;
keep people on irreversible decisions through standing limits, not constant prompts; prove a change
by running the flow, not by trusting the report; remove what nothing uses.

## Try the work lifecycle without a model

This walks one job through the governed lifecycle with a local command instead of a model. You
need Git, Go 1.27.1 or newer and Python 3. Run it from the root of a clone of this repository:

```sh
export COMPOUND_TRY="$(mktemp -d)" && export COMPOUND_HOME="$COMPOUND_TRY/home"  # throwaway home
go build -o "$COMPOUND_TRY/compound" ./cmd/compound && export PATH="$COMPOUND_TRY:$PATH"
compound home init && mkdir "$COMPOUND_TRY/project" && cd "$COMPOUND_TRY/project"
CTX=$(compound orient --harness claude-code --cwd . --task "say hello" --json \
  | python3 -c 'import json, sys; print(json.load(sys.stdin)["id"])')
W=$(compound work create --owner first-run --predicate "prints hello and keeps the proof" \
  --context-pack "$CTX" --state ready --by operator:you | awk '/^created/ {print $2}')
compound work claim "$W" --by agent:you:session-1
compound work advance "$W" --to running --by agent:you:session-1 --execute \
  --harness os-harness-entrypoints --workspace "$PWD" -- echo "hello from compound"
compound work close "$W" --by agent:you:session-1 --outcome completed --move-type audit_only \
  --summary "first run left inspectable evidence" | head -1
compound workbench
```

You made a briefing, one job, a governed run with a saved transcript, and a receipt. No model ran,
no harness setting changed, and your real `~/.compound` was not touched.
[Getting started, step 5](getting-started.md#5-walk-one-job-through-by-hand) explains each command.

## Day-to-day details

1. **Check the install and home.** `compound version`, then `compound home doctor`. Keep runtime
   data outside your project checkout.
2. **Connect your harness.** `compound init` wires Claude Code, Codex, OpenCode and oh-my-pi when it
   finds them. [Harness wiring](harness-wiring.md) covers each one and the isolated executor
   profiles. Any other harness needs an enrolled, supported adapter, not just a name in a command.
3. **Give your agent one goal and its limits.** Ask it to check existing work, keep the chosen
   executor and use the governed work lifecycle. You should not need to copy IDs between sessions.
4. **Look at the result.** Use `compound receipt last`, `compound workbench`,
   `compound coordination` or the optional [dashboard](console.md). A successful run is separate
   from verification and acceptance.

For a job you already claimed with a saved executor selection, this resumes your one held job:

```sh
compound work continue --selected --actor <your-existing-actor> --json
```

It does not create or claim work, pick an arbitrary job, or grant new permission. More than one held
job, an unsettled run or missing authority stops it with an explicit reason.
[Daily use](getting-started.md#6-daily-use) covers resuming and verification.

**Dashboard.** Prebuilt archives contain the CLI and engine, not the dashboard. The dashboard needs
a matching source checkout, Bun and Node 20+. Its supported surface is local and read-only, and it
does not replace the harness's governed execution path. See the [console guide](console.md).

**Semantic memory** (search over your docs): [Memory quickstart](memory-quickstart.md).

**Product workflows** from discovery and UI/UX through release, distribution, retention and skill
improvement: the [public lifecycle skills](../skills/product-pipeline/README.md), with
[pinned source credits](../skills/product-pipeline/SOURCES.md). They need no private overlay or bulk
upstream install. An offer is not unattended execution and not proof of a business outcome.

**Contract consumers:**

```bash
go run ./cmd/compound schema list
go run ./cmd/compound schema export context_pack
go run ./cmd/compound envelope validate fixtures/envelopes/*.json
```

The last command exits non-zero on purpose: `fixtures/envelopes/work.invalid.json` is malformed by
design, so a correct run reports it `INVALID`. Every other fixture must report `ok`.

## Where state lives

The repository is source: code, schemas, tests, docs and fixtures. `COMPOUND_HOME` is the runtime
body: ledgers, events, work, context packs, candidate inboxes, registries, artifacts, reviews,
exports, and indexes and projections that can be rebuilt.

```text
--home -> --local (for commands that support it) -> COMPOUND_HOME -> ~/.compound
```

Ancestor discovery (the nearest `.compound/manifest.yaml`) survives only as a read-only
compatibility API. Normal home resolution never treats an ancestor marker as authority. Private
paths, credentials, accounts, personal corpora and real operator data belong in a private Compound
home, never in tracked public files.

```bash
compound home backup [--target archive.tar.gz]
compound home export --public-fixture [--target fixture.tar.gz]
compound home verify [archive.tar.gz]
compound home restore archive.tar.gz --target <new-absent-home-path>
compound home compact [--dry-run] [--rebuildable]
```

- Atomic `home restore` publication works on Darwin and Linux; other platforms stop before
  publishing the staged home.
- Restore accepts Compound backup archives only; a public fixture is not a recovery artifact.
  Verification finishes before any staging directory exists. If a later step fails, Compound
  reports it and keeps the empty `0700` staging directory for you to inspect.
- A home root is private (`0700`). Diagnose or repair an older one with
  `compound home private-custody repair [--dry-run]`.
- Backups keep durable evidence, work and config, and skip transient or rebuildable folders such as
  `tmp`, `lock`, `exports`, `indexes` and `projections`. A public fixture export holds only
  sanitized layout and manifest metadata.

The full backup, restore, upgrade and rollback procedure is in [Recovery](operations/recovery.md).

## Commands

| Area | Commands | Notes |
|---|---|---|
| Setup | `init`, `hook install`, `hook uninstall`, `doctor`, `receipt last` | `init --check` reports one line per harness and changes nothing |
| Runtime and contracts | `home`, `envelope`, `schema`, `contract compile`, `orient` | `orient` builds a ContextPack with what was included and excluded, and why |
| Work | `work`, `workbench`, `transition`, `reviews`, `coordination` | `workbench` and `reviews` are read-only; they never dispatch or apply |
| Policy | `policy evaluate`, `authority`, `writeback` | `policy evaluate` turns an ActionIntent into a PolicyDecision; dirty-tree review needs an operator verdict |
| Proof | `proof-check`, `architecture doctor --strict`, `memory doctor` | see [Proof-check and the land-lease](architecture/proof-check.md) |
| Meaning | `memory`, `knowledge`, `synthesis`, `distiller`, `decisions`, `source observe`, `event import-os` | imports become evidence and candidates; nothing is promoted on model confidence alone |
| Adaptive | `capability`, `panel`, `local-models advise`, `route`, `runtime doctor`, `product`, `projection` | `capability` offers never run skills; `local-models advise` never changes provider runtime; `panel` applies only through governed proof |

Run `compound` with no arguments for the full usage list.

## Supported scope in detail

The public alpha is a scoped, file-backed, single-user development system:

- File backend only. The experimental Postgres adapter was removed.
- One trusted OS user on local disk.
- Explicit home selection. An ancestor `.compound` folder never selects your home; use `--home`,
  `COMPOUND_HOME` or an explicit local selector.
- Interactive hooks may observe. Any enforcement claim needs behavioral proof for that exact launch
  profile.
- The dashboard is projection-only (read-only) by default, and direct private reads are not
  available. Its mutation extension needs an explicit home and an out-of-band capability, and it is
  not supported.

Unsupported: Postgres-backed storage, shared homes, server or multi-tenant use, a remote or proxied
dashboard, and arbitrary private embedding endpoints. The full contract, including the gaps that
are still open, is in [docs/security/supported-scope.md](security/supported-scope.md). See also the
[support policy](../SUPPORT.md), [security policy](../SECURITY.md),
[recovery procedure](operations/recovery.md),
[second-opinion snapshots](operations/second-opinion-snapshot.md) and the
[changelog](../CHANGELOG.md).

## Release and CI

Hosted GitHub Actions are disabled in this repository. `.woodpecker.yml` runs CI and gated release
builds. Each release is built, drilled, signed and uploaded as a draft; only the maintainer
publishes it. The [release procedure](release.md) has every step.

A change reaches `main` after a scoped presubmit (build, vet, format, strict doctor, tests of the
affected packages, redact scan). The full proof battery then runs on the landed commit; changes to
the proof, landing and policy core run it before landing. CI runs are fresh-machine evidence, not
the canonical proof.

## Status

`main` is a development branch. Supported builds are tagged releases. `v0.1.0` was the first;
`v0.1.1` is the first with prebuilt binaries. The maintainer uses Compound daily against a private
reference setup. This repository is the generic core, not that private data. Governed runs and
their evidence can be inspected with `compound workbench` and `compound work proof-chain-doctor`.
Automatic skill lifecycle and business-workflow orchestration are still in development and are not
part of the public surface. The older Gen-1 memory packages are gone from `internal/`, and their
top-level commands are tombstones that refuse rather than aliases.

Health gates:

- `compound architecture doctor --strict` checks package classification, public-doc leaks,
  candidate-map integrity, contract guards, and a convergence budget of `0`.
- `compound memory doctor` checks capture, promotion, retrieval, authority and optional engine sync.

Public behavior must be proven with leak-free fixtures and explicit runtime inputs.

## Architecture reading list

| Guide | For |
|---|---|
| [Supported scope](security/supported-scope.md) | What the public alpha supports, and where its security boundary is |
| [Getting started](getting-started.md) | Install, wire your harness, walk one job through by hand |
| [Harness wiring](harness-wiring.md) | Claude Code, Codex, OpenCode, oh-my-pi and other CLI harnesses |
| [Memory quickstart](memory-quickstart.md) | Semantic search over your docs |
| [Console](console.md) | The local dashboard |
| [Contributing](../CONTRIBUTING.md) | Build, test, architecture rules and landing |

Architecture, in reading order: [LAYERS](LAYERS.md), [status](architecture/status.md),
[epistemics](architecture/epistemics.md), [runtime](architecture/runtime.md),
[contracts](architecture/contracts/README.md), [memory architecture](memory-architecture.md),
[source adapters](architecture/source-adapters.md) and
[legacy system retirement](operations/legacy-system-retirement.md). Every package under `internal/`
belongs to exactly one layer of `docs/LAYERS.md`, and `compound architecture doctor --strict`
enforces it. Importer packages (`authority`, `decisions`, `eventsource`, `knowledge`, `sourceobs`,
`tooltopology`) adapt existing systems without becoming new owners.

Search engines, vector indexes, model providers, harnesses, MCP servers, browser tools, analytics
APIs and sidecars are replaceable edges. They may widen retrieval or do work; they never own
promotion, visibility, replacement, policy or truth. The ledger is the authority; engines are views
that can be rebuilt.

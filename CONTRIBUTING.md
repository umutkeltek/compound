# Contributing to Compound

This guide covers how to build Compound, test it, and send a change.

## What Compound is

Compound gives coding agents a memory that lasts between sessions. It keeps goals, decisions, work
items and receipts in local files. A receipt is the record of what a session or job did. Each new
session starts from that record instead of from nothing. The [README](./README.md) is the short
version. [`docs/overview.md`](./docs/overview.md) explains how it is built, and
[`docs/LAYERS.md`](./docs/LAYERS.md) is the canonical architecture.

## Prerequisites

- **Go 1.27.1 or newer**, the version in `go.mod`; `go version` must print at least that. If `go version` prints an older one (Debian 13 packages
  Go 1.24), install from [go.dev/dl](https://go.dev/dl/) instead.
- **Git**, **Python 3** and **make**.
- Optional: **Bun** for dashboard work, **Ollama** for semantic-memory tests.

## Build

```sh
make install          # builds the binaries, creates ~/.compound, runs the health checks
# or by hand, then create a Compound home before any other command:
go build ./cmd/compound
COMPOUND_HOME="$HOME/.compound" ./compound home init
```

## Test

```sh
# Fast feedback while you work (not certifying):
go run ./cmd/compound proof-check --dev --base main

# The full Go suite in a fresh run (not certifying):
go run ./scripts/testbattery test

# One package:
go test ./internal/work/ -count=1 -timeout 5m

# Architecture doctor (package rules, doc-leak gate, contract guards):
go run ./cmd/compound architecture doctor --strict

# Full proof bundle (tests, vet, build, gofmt, doctors):
PRE_LAND_BASE_SHA=$(git rev-parse --verify "main^{commit}")
go run ./cmd/compound proof-check --base "$PRE_LAND_BASE_SHA" \
  --work <claimed-work-id> --actor <lease-holder> \
  --out <absolute-report.json>
```

`--dev` runs `go test -short` across the repository with Go's test cache. Unchanged packages reuse
their last pass, and a package reruns when its source, dependencies or test inputs change. It also
checks formatting, the diff, strict architecture, redaction of changed files, and the dashboard's
TypeScript when relevant. It is iteration feedback only and does not count as landing proof.

Some packages run slowly. `internal/work`, `internal/authority` and `internal/eventstore` take
2 to 5 minutes each; use `-p 2` to avoid CPU contention timeouts. `cmd/compound` takes longer than
Go's default 10-minute test timeout, so give it `-timeout 45m`.

Run the full bundle as its own command, without a pipe, because a pipe can hide its exit code.
Read its report from the `--out` file.
Every gate must finish `ok`. A skipped gate is a diagnostic, not proof. How the bundle seals the
commit it tested is in [Proof-check and the land-lease](docs/architecture/proof-check.md).

## Code style

- **gofmt** is enforced. Run `gofmt -w .` before you commit.
- **go vet** must pass: `go vet ./...`.
- No personal paths, credentials or private data in tracked files. `architecture doctor --strict`
  enforces this with its doc-leak gate.
- Tests never compare the wall clock against fixed fixture dates. Inject the clock, or set fixture
  timestamps at runtime.

## Architecture rules

`compound architecture doctor --strict` enforces these, and CI fails when one breaks.

1. **Every `internal/` package is classified** in `internal/arch/arch.go` (the `Classification`
   map) and documented in `docs/architecture/status.md`. A new package needs a class (spine,
   object-family, leaf or importer) and a mint-home entry in `internal/arch/mint.go`.
2. **One meaning owner.** Compound owns meaning. Engines, models and harnesses are rented. Never
   add a package that becomes a second authority.
3. **Envelope first.** Work flows through one spine: Intent, EffectiveContract, ContextPack (the
   briefing), WorkEnvelope (the job), ActionIntent, PolicyDecision, Receipt, Transition. New
   features plug into this spine, not around it.
4. **Candidates first.** Incoming information is judged (reject, park, promote or operationalize)
   before it can become authority. It is never written straight to authority.
5. **Subtract by default.** A new object family names the old surface it retires. A parallel path
   that leaves the old one in place fails the subtraction check.
6. **The public core stays clean.** No private paths, personal data or operator-specific names in
   tracked files. `docs_private/` is gitignored.

## Package layout

```text
cmd/compound/             CLI boundary (one binary)
internal/                 All implementation packages
  arch/                   Architecture doctor (enforced rules)
  contract/               EffectiveContract + ContextPack
  envelope/               Typed runtime grammar
  home/                   COMPOUND_HOME runtime body
  hook/                   Lifecycle hook runner
  mem/                    Memory object family
  policy/                 ActionIntent → PolicyDecision
  work/                   WorkEnvelope lifecycle
  ...                     (see docs/architecture/status.md for all)
docs/                     Architecture, guides, roadmap
fixtures/                 Synthetic test data (no personal data)
console/                  Next.js operator console (optional)
skills/                   Agent ceremony definitions
```

## Sending a change

1. **Send a pull request against `main`.** For anything beyond a small fix, open an issue first.
2. **Write tests that would fail if the depth were wrong.** A test should catch a real failure mode,
   not only cover the happy path.
3. **Iterate with `proof-check --dev`**, then run the full bundle from the Test section.
4. **One logical change per pull request.** If the change teaches something about the architecture,
   update the docs, contracts and tests in the same change.
5. **CI runs subsets on a fresh clone, not the certifying battery.** Hosted GitHub Actions are
   disabled here; `.woodpecker.yml` runs CI. The files under `.github/workflows/` are inactive and
   kept for tests. See the [release procedure](docs/release.md).

### Proof rules for landing

These apply when a maintainer lands a change on `main`. The landing steps are in
[`skills/compound-land/SKILL.md`](skills/compound-land/SKILL.md).

- A scoped presubmit gates the fast-forward:
  `go run ./cmd/compound proof-check --presubmit --base <full-sha> --out <file>`. The full bundle
  then runs once on the landed commit.
- Capture `PRE_LAND_BASE_SHA` once, before the land, and reuse that exact SHA for the run on the
  landed commit. Never capture `main^{commit}` again after landing.
- A rebase, replay, or new HEAD invalidates prior proof and requires a fresh battery.
- For source-controlled work, a committed stored successful proof is required: `land-check`
  candidate tip, current attached commit candidate, and stored proof HEAD must match; close refuses
  a missing, mismatched, or rebased proof.

## Good contributions

- A test that catches a failure mode nobody had considered.
- A doc that makes a concept click for someone new.
- A fixture that proves an envelope shape works.
- A bug fix with a regression test.
- A retirement: removing a scattered surface that a spine object now owns.

## What to avoid

- A new service or authority center. Compound is one program with deep modules, not microservices.
- Building ahead of a slice that needs it. Prove one vertical slice with a command and fixtures,
  then retire the parallel handling.
- Storing model output as truth. The model extracts; code decides.
- Governance in a prompt. Policy is enforced at the adapters, not requested in a prompt.

## Questions and security reports

Open an issue for questions. [`docs/ARCHITECTURE.md`](./docs/ARCHITECTURE.md) gives the reading
order for the architecture docs. Report a possible vulnerability privately, as
[`SECURITY.md`](./SECURITY.md) describes, never in a public issue.

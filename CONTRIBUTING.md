# Contributing to Compound

Thank you for your interest in contributing to Compound. This guide covers
how to build, test, and submit changes.

## What Compound is

Compound is a local-first, agent-agnostic **meaning + execution substrate**.
It makes agent work accumulate: every session leaves claims, receipts, and
lenses behind, and the next session starts smarter. Read the
[README](./README.md) for the full thesis and
[`docs/LAYERS.md`](./docs/LAYERS.md) for the canonical architecture.

## Prerequisites

- **Go 1.22+**
- **Git**
- Optional: **Bun** (for console work), **Ollama** (for semantic memory tests)

## Build

```sh
make install          # builds binary, inits home, runs doctor
# or manually:
go build ./cmd/compound
```

## Test

```sh
# Fast repeatable development feedback (non-certifying):
go run ./cmd/compound proof-check --dev --base main

# Standalone fresh Go suite (non-certifying; canonical bundle below):
go test -count=1 -timeout=40m ./...

# Single package:
go test ./internal/work/ -count=1 -timeout 5m

# Architecture doctor (structural integrity):
go run ./cmd/compound architecture doctor --strict

# Full proof bundle (tests + vet + build + gofmt + doctors):
go run ./cmd/compound proof-check --work <claimed-work-id> --actor <lease-holder> \
  --out <absolute-report.json>
```

Run the full profile unpiped from a real terminal/PTY. All canonical gates must
finish `ok`—a skipped gate is diagnostic evidence, not certifying proof.

**Note:** some packages (`internal/work`, `internal/authority`,
`internal/eventstore`) have slow tests (2-5 min each). Use `-p 2` to limit
parallelism and avoid CPU contention timeouts.

## Code style

- **gofmt** is enforced. Run `gofmt -w .` before committing.
- **go vet** must pass: `go vet ./...`
- No personal paths, credentials, or private data in tracked files. This is
  enforced by `architecture doctor --strict` (doc-leak gate).
- Tests never compare wall-clock time against fixed fixture dates. Inject the
  clock or freshen fixture timestamps at runtime.

## Architecture rules

These are enforced by `compound architecture doctor --strict` and will fail CI:

1. **Every `internal/` package must be classified** in `internal/arch/arch.go`
   (`Classification` map) and documented in `docs/architecture/status.md`.
   New packages need a classification (spine / object-family / leaf / importer)
   and a mint home registration in `internal/arch/mint.go`.

2. **One meaning owner.** Compound owns meaning; engines, models, and harnesses
   are rented edges. Never add a package that becomes a second authority.

3. **Envelope-first.** The spine is: Intent → EffectiveContract → ContextPack →
   WorkEnvelope → ActionIntent → PolicyDecision → Receipt → Transition. New
   features plug into this spine, not around it.

4. **Candidate-first.** Incoming information is adjudicated
   (reject / park / promote / operationalize), never written straight to
   authority.

5. **Subtract by default.** Every new object family must name what old surface
   it retires. Adding a parallel path without retiring the old one fails the
   subtraction check.

6. **Public core stays clean.** No private paths, personal data, or
   operator-specific names in tracked files. `docs_private/` is gitignored.

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

## Submitting changes

1. **Fork and branch** from `main`.
2. **Write tests** that would fail if the depth were wrong. Tests are how the
   system understands its own depth — not happy-path coverage.
3. **Use the fast development profile while iterating**, then run the canonical
   proof bundle before pushing:
   ```sh
   go run ./cmd/compound proof-check --dev --base main
   ```
   `--dev` runs repository-wide `go test -short` with Go's content-addressed
   cache: unchanged package passes are reused, while packages rerun when their
   source, dependencies, or observed test inputs change. It also checks formatting,
   the diff, strict architecture, changed-file redaction, and console TypeScript
   when relevant. This is iteration feedback only: it does not write canonical
   installed-runtime proof evidence or satisfy pre-land or post-land proof.

   ```sh
   go run ./cmd/compound proof-check --work <claimed-work-id> --actor <lease-holder> \
     --out <absolute-report.json>
   ```
   The command atomically replaces `--out` only after the complete report is
   synced; admission or transport refusal never creates or changes the target.
4. **One logical change per PR.** If your change teaches something about the
   architecture, update the docs/contracts/tests in the same change.
5. **CI runs automatically** on push/PR: `proof-check --ci` (Go gates) +
   `proof-check --ci-console` (TypeScript gates) + gitleaks (secret scan).

## What makes a good contribution

- A test that catches a failure mode nobody had considered
- A doc that makes a concept click for someone new
- A fixture that proves an envelope shape works
- A bug fix with a regression test
- A retirement: removing a scattered surface that a spine object now owns

## What to avoid

- Adding a new service or authority center (Compound is a monolith with deep
  modules, not microservices)
- Building ahead of a slice that needs it (one vertical slice, proven with a
  command + fixtures, then retire the parallel handling)
- Storing model output as truth (model extracts; code decides)
- Prompt-level governance (policy is enforced at adapters, not asked in a
  prompt)

## Questions?

Open an issue. Compound's architecture docs are thorough — start with
[`docs/ARCHITECTURE.md`](./docs/ARCHITECTURE.md) for the reading order.

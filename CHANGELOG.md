# Changelog

All notable changes are recorded here. Compound follows semantic versioning
for tagged releases, subject to the public-alpha compatibility policy in
[`SUPPORT.md`](SUPPORT.md).

## [Unreleased]

### Added

- `compound init` wires OpenCode and oh-my-pi as well as Claude Code and Codex. OpenCode gets one
  Compound-owned plugin file at `~/.config/opencode/plugins/compound.ts`, which OpenCode loads on
  its own; oh-my-pi gets its hook file at `~/.omp/agent/hooks/pre/compound.ts`. Both are refreshed
  in place when Compound owns them and never overwritten when someone else wrote them.
  `compound hook install|uninstall --harness opencode|oh-my-pi` work by name, and `init --check`
  reports all four harnesses. The `init` footer names the harness it just wired and says when a
  harness writes no session receipt (OpenCode has no session-end callback).
- `compound doctor` lists OpenCode and oh-my-pi in its hooks section when their config folder
  exists, and warns when an installed harness is not wired.

- `compound orient --brief` prints a plain-text brief of at most 6,000 bytes instead of the JSON pack (57-77 KB measured): the checkout's own facts from an optional `state/repo-briefs.json` projection, up to five task-matched memory lines, up to five capability offers with what to read or invoke, and the command for the full pack. Outside Compound's own repo it leaves out Compound's internal state. The orient instruction line and the session-start pointer now say `--brief`.
### Removed

- `compound game`, `compound terminal`, `compound behavioral-lens`, `compound executor-lens`,
  `compound lens-modulate` and `compound intent`, with their packages. No script, skill,
  console page or scheduled job called them.
- The nine Gen-1 tombstone commands (`distill`, `scope`, `promote`, `build`, `reconcile`,
  `timeline`, `recall`, `pack`, `ledger`). They printed a pointer and exited 2; they are now
  unknown commands. Use `compound memory ...` and `compound distiller run`.
- The `sessions job plan|review|apply` code and the `credential exec|bind|list` code. Neither
  was ever reachable from the CLI.
- The experimental Postgres+pgvector store: the `compound store` command, `internal/store` and
  `internal/persist/pg`, and five module dependencies (pgx, pgvector-go, puddle, pgpassfile,
  pgservicefile). It was fenced off, unsupported, and had no caller. File is the only backend.
  `COMPOUND_STORE=postgres`, any other non-file value, and a leftover `COMPOUND_DSN` or other
  variable of that adapter are still refused instead of being served from files, and the
  refusal never prints the rejected value. ADR-0005 is marked superseded.

### Fixed

- `compound home doctor` in text mode now exits 1 whenever it prints a failing verdict. A global
  home inside a Git checkout printed the failing verdict and still exited 0, while `--json`
  exited 1. Both installers run this check before they publish, so an install into such a home
  is now refused instead of completing with a failing verdict.
- `compound home compact`, `home archive-history` and `home restore-history` refuse an unknown
  flag with exit 2. A mistyped `--dry-run` used to be ignored, so the command ran for real.
- The top-level help no longer says that `retention` never reclaims or that `presence` writes
  nothing: `retention apply`, `retention empty-trash` and `presence announce` are governed writes.
- Parity fixtures enforce the stated limits on `orient_expectations` (negative ceilings, canary
  and private-ref counts and sizes); the check existed and was never called.
- The console accepts every work id the CLI accepts. The trace view and the advance, continue
  and dispatch actions refused any id with an underscore, a capital letter, a dot, a dash or a
  colon, which is most named work; they now share one check with the CLI's rule.
- `compound architecture doctor` run from a linked worktree applies the operator's private
  leak-token overlays from the main checkout. A linked worktree never carries them, so the
  private-name pass was skipped before a landing and failed on main after it.

### Documentation

- `docs/getting-started.md` is now a setup guide, not release engineering. It covers
  prerequisites, install, first run, wiring all four harnesses, a first session, the step-by-step
  lifecycle demonstration and daily use. The release drill, installer journal internals, source
  install locks and pre-tag gates were cut; [`docs/release.md`](docs/release.md) covers how
  releases are built.
- The prebuilt and source install blocks now run in their own `sh`, so a failed check no longer
  closes your terminal. The source block also works from zsh: zsh had read `$TAG:r` in
  `refs/tags/$TAG:refs/tags/$TAG` as a modifier and fetched a tag that does not exist. The prebuilt
  block downloads only the files the installer reads.
- The prerequisites say where to get Go 1.27.1 or newer (go.dev/dl), since the Go that Debian 13
  packages (1.24) is older.
- The lifecycle demonstration saves each ID in a shell variable (`CTX`, `WORK`, `TRANSITION`) with
  a one-line `python3` reader. It names the field to copy (`.id`), offers a throwaway home, and marks
  every placeholder.
- The README, `SUPPORT.md`, `docs/overview.md` and the getting-started guide now say that
  `compound init` wires Claude Code, Codex, OpenCode and oh-my-pi. Each page names the file `init`
  writes for each harness, notes that OpenCode writes no session receipt, and gives the message
  `init` prints when it finds no harness. The stale line "OpenCode is observed only" is gone.
- `SECURITY.md`, `SUPPORT.md` and `CONTRIBUTING.md` are shorter and use plain words. CI and release
  details moved behind a pointer to `docs/release.md`. `docs/overview.md` no longer says prebuilt
  binaries are missing.

## [0.1.1] - 2026-11-07

Setup in one command, and the first release with prebuilt, signed binaries for
macOS (Apple silicon) and Linux (x86-64).

### Added

- `compound init`: the one setup command after `make install`. It creates the Compound home if it
  is missing, looks for Claude Code and Codex on this machine, wires the hooks of each one it
  finds, and prints one line per harness: wired, already wired, or not found. When `~/.local/bin`
  is not on PATH it prints the exact line for your shell; it never edits shell profiles.
  `compound init --check` only reports and changes nothing.
- `compound hook uninstall`: the reverse of `hook install`. It removes only Compound's hook
  entries, leaves every other setting and hook, and records the removal.
- `compound receipt last`: the newest receipt, meaning what the last session (or closed work
  item) did and where its transcript is.

### Changed

- `compound hook install --scope project` now wires the current project (its own `.claude`
  local settings file). Before, it wrote to the home folder unless `--base` was also given. `hook install` now refuses an unknown flag instead of ignoring it.
- `compound doctor` lists only the harnesses this machine has. It no longer lists provider
  profiles and private lanes that a fresh machine never had.
- Session start no longer opens with "Door map: none produced yet" when no door map exists.
- `make install` now names `compound init` as the next step, and the README first run uses it.
- The README is now a short page for a first-time reader: a six-line first run
  from the release tag, how it works, day-to-day use and the supported scope.
  The maintainer detail it held moved unchanged to `docs/overview.md`.
  `SUPPORT.md` and `SECURITY.md` now name the published `v0.1.0` tag.

## [0.1.0] - 2026-11-07

First tagged public-alpha release: a file-backed, single-user developer preview.

### Added

- Plain-language review cards for every needs_you item: a cardless core
  review artifact now gets a derived operator card stating what the record
  is, why it sits in front of the operator, and one concrete action —
  composed from the artifact's own payload (title clause, status, blocked
  reason, next action) by `reviewartifact.DerivedOperatorCard`, gated by
  the operator-card grammar and a new shared deterministic eighth-grade
  readability ceiling (`reviewartifact.GradeLevel` /
  `narrate.CheckScriptReadability`). The daily digest letter renders each
  needs_you group's card and self-gates on the same ceiling, degrading a
  too-dense card to a plain count-and-reply line rather than shipping
  jargon.
- A file-backed, single-user public-alpha support and security contract.
- A fail-closed vulnerability-disclosure gate, compatibility policy, and
  recovery procedure. Private reporting remains a pre-tag operational gate
  until its reporter-side route is verified.
- Private artifact custody and explicit custody diagnosis/repair surfaces.

### Fixed

- The generic "a new record needs your read" fallback card is gone. Queue
  loads re-derive cards for artifacts already on disk that carry the
  retired sentence, attention's last-resort needs_you group prose now
  states the decision and carries an unlock action, and narrate's
  malformed-fixture beat fallback names the queue instead of returning the
  generic line. Narrate acknowledgements dropped the "Recorded —" lead
  that pushed them above the readability ceiling.
- The breadth vector cache is now constructed whenever a compound home
  exists, instead of only when the embedder's dimension was already known.
  OpenAI-compatible embedders learn their dimension lazily, so models
  outside the static inference table (e.g. `qwen3-embedding`) silently
  skipped the persistent cache and re-embedded the whole corpus on every
  sidecar spawn, blowing the transport deadline and degrading breadth to
  lexical recall. Cache load already validates dimensions against the
  manifest, so construction no longer depends on them; the dimension
  inference table also recognizes `qwen3-embedding`, `bge-m3`, and the
  `gemini-embedding` family.
- Backup collision, retention, archive-identity, and restore publication gaps.
- RunAttempt launch ambiguity gaps.
- Invented harness and working-directory identities no longer produce a false
  successful orientation.

### Changed

- Home authority no longer comes from ambient ancestor workspace markers.
- Backups publish transactionally and restores stage before publication.
- Interactive hook posture is reported as observing, enforcing, or fail-open
  from behavioral probes rather than configuration alone.
- The console is projection-only by default. Its unsupported mutation
  extension requires an explicit home and an out-of-band capability; direct
  private document reads are unavailable.

# Changelog

All notable changes are recorded here. Compound follows semantic versioning
for tagged releases, subject to the public-alpha compatibility policy in
[`SUPPORT.md`](SUPPORT.md).

## [Unreleased]

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

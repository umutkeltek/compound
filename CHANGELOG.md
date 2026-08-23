# Changelog

All notable changes are recorded here. Compound follows semantic versioning
for tagged releases, subject to the public-alpha compatibility policy in
[`SUPPORT.md`](SUPPORT.md).

## [Unreleased]

### Fixed

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

### Added

- A file-backed, single-user public-alpha support and security contract.
- A fail-closed vulnerability-disclosure gate, compatibility policy, and
  recovery procedure. Private reporting remains a pre-tag operational gate
  until its reporter-side route is verified.
- Private artifact custody and explicit custody diagnosis/repair surfaces.

### Changed

- Home authority no longer comes from ambient ancestor workspace markers.
- Backups publish transactionally and restores stage before publication.
- Interactive hook posture is reported as observing, enforcing, or fail-open
  from behavioral probes rather than configuration alone.
- The console is projection-only by default. Its unsupported mutation
  extension requires an explicit home and an out-of-band capability; direct
  private document reads are unavailable.

### Fixed

- Backup collision, retention, archive-identity, and restore publication gaps.
- RunAttempt launch ambiguity gaps.
- Invented harness and working-directory identities no longer produce a false
  successful orientation.

No release has been published from this changelog entry yet.

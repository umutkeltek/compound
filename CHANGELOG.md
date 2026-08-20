# Changelog

All notable changes are recorded here. Compound follows semantic versioning
for tagged releases, subject to the public-alpha compatibility policy in
[`SUPPORT.md`](SUPPORT.md).

## [Unreleased]

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

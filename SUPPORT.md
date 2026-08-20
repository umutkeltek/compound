# Public-alpha support policy

Compound is an early developer preview. Support is best-effort through GitHub
issues; there is no uptime, response-time, or backward-compatibility SLA.
Security reports follow [`SECURITY.md`](SECURITY.md). Private reporting must be
verified before the first public tag; until then a public issue may request a
private channel but must contain no vulnerability detail.

## Supported configuration

| Surface | Public-alpha support |
|---|---|
| Storage | File backend under one `COMPOUND_HOME` |
| Trust model | One trusted OS account, one local disk |
| Prebuilt artifacts | macOS arm64 and Linux amd64 |
| Source build | Go version declared by `go.mod` |
| Home authority | Explicit `--home`, `COMPOUND_HOME`, `--local`, or the global default |
| Interactive hooks | Observation by default; enforcement only where `compound doctor` proves the exact installed posture |
| Console | Loopback, projection-only default; the capability-gated mutation extension is unsupported, and direct private reads are unavailable |
| Upgrades | Latest tagged public alpha, after a verified backup |

The authoritative boundary and current runtime caveats live in
[`docs/security/supported-scope.md`](docs/security/supported-scope.md).

## Unsupported configuration

- Postgres or another non-file backend as durable alpha authority;
- shared OS accounts, shared/network homes, NFS, team workspaces, containers
  with untrusted co-resident processes, or multi-tenant use;
- LAN, reverse-proxy, tunnel, or other remote console exposure;
- daemon/server deployment or use as an authentication boundary;
- arbitrary private embedding endpoints;
- the console mutation extension as a supported security boundary;
- unverified archives, binaries built from a moving branch, or an older binary
  reading a newer on-disk home schema.

Unsupported does not mean intentionally broken; it means the project does not
claim compatibility, isolation, recovery, or security behavior for that shape.

## Compatibility policy

The home manifest schema and home kind are exact compatibility boundaries.
Unsupported values fail closed; mutating commands require an explicit migration
path instead of guessing. Additive fields inside a supported schema may be
introduced during the alpha. A schema change that requires rewriting state must
ship with backup-first upgrade instructions and a tested restore/rollback path.

CLI output intended for machines is versioned JSON. Human-readable output and
experimental commands may change between public-alpha releases. Postgres, the
console mutation extension, and surfaces labeled experimental are not covered
by the compatibility promise. Direct private reads through the console are not
available.

## Filing a useful issue

Include the tagged version, OS/architecture, storage mode, exact command,
synthetic reproduction, and redacted output. Say whether `compound home doctor
--json` passes. Never attach a complete `COMPOUND_HOME`, credentials, database
connection strings, or raw private prompts.

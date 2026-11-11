# Public-alpha support policy

Version note: the published v0.1.1 `init` wires Claude Code and Codex. OpenCode and oh-my-pi setup described here requires development 0.1.2; it is not in v0.1.1.

Compound is an early developer preview. Support is best-effort through GitHub issues. There is no
uptime, response-time or backward-compatibility promise.

Report security problems privately, as [`SECURITY.md`](SECURITY.md) describes. A public issue may
ask for a private channel, but it must contain no vulnerability detail.

## Supported configuration

| Surface | Public-alpha support |
|---|---|
| Storage | File backend under one `COMPOUND_HOME` (by default `~/.compound`) |
| Trust model | One trusted OS account, one local disk |
| Prebuilt artifacts | macOS arm64 and Linux amd64, attached to the GitHub Release with an SSH-signed `SHA256SUMS`, from `v0.1.1` on. `v0.1.0` has none; build it from source |
| Source build | The Go version declared in `go.mod` (1.27.1 or newer) |
| Home selection | Explicit `--home`, `COMPOUND_HOME`, `--local`, or the default `~/.compound` |
| Harnesses | Claude Code, Codex, OpenCode and oh-my-pi, all wired by `compound init` |
| Hooks | Observation by default; blocking counts only where `compound doctor` proves the exact installed setup |
| Dashboard | Local and read-only by default; its mutation extension is unsupported, and direct private reads are unavailable |
| Upgrades | The latest tagged public alpha (today `v0.1.1`), after a verified backup; `main` is a development branch |

The full boundary and its current gaps are in
[`docs/security/supported-scope.md`](docs/security/supported-scope.md).

## Unsupported configuration

- Postgres or any other non-file backend as durable state.
- Shared OS accounts, shared or network homes, NFS, team workspaces, containers shared with
  untrusted processes, or multi-tenant use.
- Dashboard access over a LAN, reverse proxy, tunnel or any other remote route.
- Running Compound as a daemon or server, or as an authentication boundary.
- Arbitrary private embedding endpoints.
- The dashboard's mutation extension as a security boundary.
- Unverified archives, binaries built from a moving branch, or an older binary reading a home
  written by a newer one.

For an unsupported setup, the project makes no claim about compatibility, isolation, recovery
or security. It may still work.

## Harness and dashboard setup

`compound init` wires every supported harness it finds. Claude Code and Codex get hook entries in
their settings files. OpenCode and oh-my-pi get one Compound-owned hook file each, in the folder the
harness loads on its own.

- Claude Code, Codex and OpenCode hooks record; they do not block the agent.
- OpenCode has no session-end event, so OpenCode sessions write no session receipt. Its tool hook
  cannot refuse a tool call either.
- The oh-my-pi hook file blocks a tool call when Compound's policy denies it.
- Other harnesses need a supported adapter. A name in the registry does not make a harness ready to
  run work.

The optional dashboard needs a matching source checkout, Bun and Node. It is not in the prebuilt
archives, and its package-script launcher also needs Go. Setup still uses a terminal. Setup without
help, and harnesses beyond the four above, have not been demonstrated.

See [Getting started](docs/getting-started.md), [Harness wiring](docs/harness-wiring.md) and the
[dashboard guide](docs/console.md).

## Compatibility policy

The home manifest schema and home kind are exact compatibility boundaries. An unsupported value
stops the command instead of guessing. A command that writes state requires an explicit migration
path. The alpha may add fields inside a supported schema. A schema change that rewrites state ships
with backup-first upgrade steps and a tested restore or rollback.

Machine-readable CLI output is versioned JSON. Human-readable output and experimental commands may
change between public-alpha releases. The compatibility promise does not cover the
dashboard's mutation extension, or anything labeled experimental.

## Filing a useful issue

Include the tagged version (`compound version`), OS and architecture, the exact command, a
reproduction with made-up data, and redacted output. Say whether `compound home doctor --json`
passes. Never attach a whole `COMPOUND_HOME`, credentials, database connection strings or raw
private prompts.

## How releases are built

CI and release builds do not run on GitHub-hosted Actions. Each release starts as a draft and is
published only after the maintainer reviews it. The [release procedure](docs/release.md) has the
details; [Getting started](docs/getting-started.md#option-a-prebuilt-binary) has the commands to
verify a download.

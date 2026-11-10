# Native Claude account pool composition

This optional adapter lets an existing governed Compound Claude launch use Switchback's native account pool during a current operator away window. Compound's existing ClaudeAdapter and work execution still own policy, hook and permission admission. This script only checks the current away window and pinned settings bytes, then passes the native argv unchanged to Switchback. It is a transport adjunct, not an admission grant. Only `provider=claude` is supported here; Codex follow-on requires a separately proved native adapter contract.

## Private configuration

Create an operator-owned regular JSON file at an absolute private path, mode `0600`. The file must be no larger than 32 KiB and have exactly these fields:

```json
{
  "schema_version": "compound.native_account_pool.v1",
  "provider": "claude",
  "accounts": ["profile_one", "profile_two"],
  "settings_path": "/absolute/private/verified-profile/settings.json",
  "settings_sha256": "64 lowercase hex characters from the current settings bytes",
  "switchback_cli": "/absolute/path/to/sb",
  "compound_cli": "/absolute/path/to/compound",
  "timeout_seconds": 90
}
```

The CLI paths must resolve to regular executables owned by the operator or root, with no group/other write permission. An owned executable symlink may point to such a target. The account names are ordered Switchback profile names, not credentials. Both config and settings must be owned regular leaf files without symlinks or group/other write access; settings is bounded to 1 MiB. The selected settings path must be the exact profile whose current settings Compound validates. Never add credential bodies, tokens or secrets to this config. No implicit global config is read.

Before activating an alias, configure and verify that profile set through Switchback's existing account doors, and import the owned execution tuple through Compound's existing authority doors. Keep the native CLI executable and the private config in their existing owner habitats. This repository does not install either one or prove live authentication, capture, inference or account availability.

## Use

Check current admission without launching a model:

```sh
python3 adapters/native-account-pool/launch.py --config /absolute/private/pool.json --plan
```

`--plan` reports only the config revision, settings digest, pool count and current admission result. It is read-only. `--check` emits `{"allow":true,...}` on success for Switchback's per-attempt callback. Both deny with exit 75 and a typed JSON error when presence or settings is unavailable, unsafe, drifted or outside the valid away window.

To expose a CLI-compatible executable to the existing Compound ClaudeAdapter, create a private operator-owned executable alias in the already approved executable habitat:

```sh
#!/bin/sh
exec /absolute/path/to/python3 /absolute/path/to/compound/adapters/native-account-pool/launch.py --config /absolute/private/pool.json -- "$@"
```

Register and verify that alias through Compound's existing harness/tuple authority. ClaudeAdapter itself constructs `--print`, its admitted permission flag, a pinned UUID `--session-id`, and the prompt. The alias adds only the wrapper's explicit config and separator; it must pass `"$@"` unchanged. The wrapper rejects a missing, duplicate or invalid native session and conflicting permission flags. It never chooses a more permissive mode.

On launch, the wrapper checks the config and settings digests plus current `compound policy presence show --json` effective state and real `from`/`through` UTC window. It then replaces itself with `sb account run --provider claude --pool ...`. The Switchback callback receives a sealed config digest and repeats these checks before **every** native account attempt. A callback refusal, malformed output, timeout or nonzero status must stop the whole pool; it must never advance to another account. Switchback owns account choice, retries, capture and its separate account evidence. Native stdout and exit status pass through from Switchback.

Run the synthetic tests with `python3 -m unittest discover -s adapters/native-account-pool/tests -v`. No test calls a real model or login.

## Codex named profile contract (v2)

The dedicated `codex-native-account-pool` Compound harness adapter consumes one
explicit, reviewed JSON file under the Switchback runtime's
`config/native-account-pools/` directory. The selection passes the absolute
file path and its bare lowercase SHA256 as the adapter's two RunInput arguments.
There is no implicit pool default. The config pins consumption facts; the
Switchback profile and account authority remain the owners of pool membership,
native homes and credentials.

```json
{
  "schema_version": "compound.native_account_pool.v2",
  "provider": "codex",
  "profile_id": "work_profile",
  "pool_name": "work_pool",
  "profile_revision": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
  "authority_revision": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
  "accounts": ["seat_one", "seat_two"],
  "account_settings": {
    "seat_one": "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
    "seat_two": "missing"
  },
  "native_executable": "/absolute/path/to/native/codex",
  "native_sha256": "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
  "switchback_cli": "/absolute/path/to/sb",
  "compound_cli": "/absolute/path/to/compound",
  "compound_home": "/absolute/owned/compound-home",
  "checker_path": "/absolute/path/to/compound/adapters/native-account-pool/launch.py",
  "checker_sha256": "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
  "approval_policy": "never",
  "sandbox": "read-only",
  "timeout_seconds": 90,
  "require_capture": true
}
```

The file must be an operator-owned, regular, nonsymlink leaf, at most 32 KiB,
without group or other write access. Export the exact profile and account
revisions and current `config.toml` digests from the existing profile owner;
`missing` means the account has no `config.toml` and is checked as such.
Every named account must have one map entry. The adapter verifies the current
native `codex` executable as the same native binary the direct Codex adapter
accepts, then checks its SHA256 again at process start. Switchback verifies the
referenced profile, account settings and executable before every account
attempt. Native credentials stay in their account homes; the adapter never
copies or swaps them.

The admitted permission mode must match the contract exactly: `plan` means
`never` approval and `read-only` sandbox; `default` means `on-request`
and `workspace-write`; `bypass` means `never` and `workspace-write`.
Mutation modes require a governed Work reservation and a live build-implement
halt check. A manual ungoverned harness run is refused. The callback repeats
the current away-window and config digest checks before every native attempt.
The Codex CLI path cannot launch directly through `launch.py`.

The adapter always requests required capture. If the provider task completes
but capture cannot be verified, Switchback returns 74 and emits the explicit
non-replay diagnostic and a terminal account receipt; Compound preserves the
failed RunAttempt, exit code and transcript. Never retry that result without
reviewing the underlying task. A passing synthetic test proves this contract
shape, not account authentication or live model execution.

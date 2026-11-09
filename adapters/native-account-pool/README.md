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

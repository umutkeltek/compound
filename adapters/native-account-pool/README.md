# Native account pool composition

The Claude v1 transport passes an admitted ClaudeAdapter launch to Switchback during
a current operator away window. The separate Codex v2 harness consumes a pinned
named profile. Compound retains policy, hook and permission admission; Switchback
owns account choice and capture. Neither composition creates an admission grant.

The Claude transport always requests `--require-capture`. A successful provider
result without verified capture is terminal evidence and must not be replayed.
The separately governed Codex v2 adapter is described below; its profile launch
uses `--expected-pool-name` to assert the pinned pool. `--pool-name` is a different
selection mode and cannot accompany `--profile`.

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

## Governed Claude adapter (v1)

The `claude-native-account-pool` harness consumes the same v1 contract directly,
without a private executable alias. The selected command is the absolute
`switchback_cli` from that contract. Its four RunInput arguments are the absolute
contract path under Switchback `config/native-account-pools/`, its bare SHA256,
the absolute `launch.py` checker path, and the
checker's bare SHA256. Both files are immutable launch inputs: the adapter
rechecks their ownership and hashes inside the exact governed start guard.
The contract location binds `SWITCHBACK_RUNTIME_ROOT`; the governing store binds
`COMPOUND_HOME`, and inherited `CLAUDE_CONFIG_DIR` is removed. There is no default
account or implicit pool.

This adapter accepts only `plan` (an omitted mode resolves to `plan`). Mutation
modes fail before an attempt is minted: global Claude permission and blocking-hook
authority cannot attest an account selected from isolated profiles. The adapter
does not claim native blocking-hook capabilities. Enabling mutation requires a
separate selected-profile authority binding; a healthy account or capture alone
cannot provide it.

The launch pins a fresh UUID session, passes the requested model, and requires
capture plus the contract's settings digest on the selected account. Switchback
chooses an eligible enrolled account in the explicit ordered pool and validates
that account's settings before native inference. `--max-attempts 1` bounds each
Compound attempt to one native task; a capture failure is terminal and is never
replayed. Requested effort is refused because this transport cannot attest it.
The callback repeats the pinned v1 admission check before account launch, with
`COMPOUND_HOME` bound to the governing attempt store. The shared subprocess runner
owns attempt minting, reservation checks, launch records and finalization.
Native stdout remains the Switchback stream; successful execution does not imply
that a downstream product consumer has parsed or accepted it.

## Governed read-only admission (v3)

For read-only work while the operator is present, create a separate contract with
`schema_version` set to `compound.native_account_pool.v3`. Every other JSON field
is exactly the Claude v1 contract above. Keep the existing v1 contract unchanged:
v1 still requires a current away window. Use only enrolled accounts whose current
settings bytes already match the pinned settings digest; the v3 transport does
not synchronize or rewrite account settings.

V3 is accepted only by the governed plan-only native adapter. Its preflight checks
the pinned files; after work enters `running`, the adapter and every per-account
callback require the exact work id, policy decision and latest action intent.
The checker calls the read-only `compound work execution-authority` owner, which
uses the same `ValidateExecutionStartAuthority` as the governed launch guard.
This verifies the current running generation, action/policy binding, authorization
freshness and current policy without minting, advancing or taking a mutation lock.
A stale reservation, revoked policy, missing authority or non-plan mode refuses
launch. V3 does not use presence as a grant and cannot widen the underlying policy.

The adapter binds the owner-resolved model and provider from the public launch
selection snapshot before preflight; a conflicting RunInput identity is refused.
Without a saved or explicit profile, evidence remains `project_default` with no
requested model claim; only capture can establish the observed model.

The adapter stamps the exact running policy into attempt posture evidence. Its
checker callback carries `--work`, `--policy-decision`, `--action-intent` and
`--permission-mode plan`; a bare v3 `--check`, a v1 check with these arguments,
or a direct v3 script launch is refused. File hashes, pool membership, per-seat
settings validation, required capture and one-native-task behavior remain the
same. Successful capture is a separate activation proof, never inferred from a
healthy capacity probe or a passing fixture.

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

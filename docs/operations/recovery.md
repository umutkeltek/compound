# Backup, restore, upgrade, and rollback

The public alpha supports recovery for the file backend on one local disk. A
backup is useful only after `home verify` accepts it and a restore drill has
proven that a new home can consume it. Keep the archive outside the source home
so the backup cannot include or overwrite itself.

## Create and verify a backup

```sh
compound home doctor --home "$COMPOUND_HOME"
compound home backup --home "$COMPOUND_HOME" --target /safe/local/path/compound-home.tar.gz
compound home verify /safe/local/path/compound-home.tar.gz
```

Do not prune the last verified local copy merely because no remote destination
is configured. Keep at least one verified archive whose survival does not
depend on the home being backed up.

## Restore drill

Restore into a new, absent target. Never restore over the active home.

```sh
restored_home=/safe/local/path/compound-restored
compound home restore /safe/local/path/compound-home.tar.gz --target "$restored_home"
COMPOUND_HOME="$restored_home" compound home doctor --json
COMPOUND_HOME="$restored_home" compound workbench --json
```

The restore command verifies the pinned archive, extracts into private staging,
runs initialization and health checks there, and publishes only a validated
home to an absent target. A failed or ambiguous publication is not success;
preserve its output and the source archive for diagnosis.

## Upgrade

1. Record `compound version --json` and the current home manifest.
2. Create and verify a backup using the commands above.
3. Install the immutable tagged artifact and verify its checksums.
4. Run `compound home doctor --json` before any other mutation.
5. Exercise a synthetic read/write/proof flow inside the supported scope.
6. Keep the prior binary and verified backup through the observation window.

An unsupported schema or home kind is a stop condition. Do not edit the
manifest by hand to silence the check; use the migration command documented by
the release, or roll back.

## Roll back

1. Stop active Compound and console processes using the home.
2. Preserve the failed-upgrade home for forensic comparison; do not overwrite
   or delete it.
3. Reinstall the prior verified binary.
4. Restore the pre-upgrade archive to a new target.
5. Point `COMPOUND_HOME` at the restored target and run `home doctor` plus the
   same synthetic flow used before the upgrade.
6. Switch normal use only after those checks pass.

An older binary is not expected to read a newer manifest schema. Rollback is a
binary-plus-backup operation, not an in-place downgrade.

## Evidence to retain

- binary version and full build commit;
- archive path and `home verify` result;
- source and restored manifest identities;
- `home doctor --json` output for both homes;
- the synthetic consumer command and its observed result;
- any ambiguous publication or filesystem error.

Recovery never authorizes deletion of the original home or archive. Deletion
is a separate operator decision after recovery is proven.

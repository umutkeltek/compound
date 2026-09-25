# Backup, restore, upgrade, and rollback

The public alpha supports recovery for the file backend on one local disk. A
backup is useful only after `home verify` accepts it and a restore drill has
proven that a new home can consume it. Keep the archive outside the source home
so the backup cannot include or overwrite itself.

Quiesce the home's writers before a consistent backup, including scheduled jobs and
hook telemetry; admission locking alone does not stop an already-running writer.
If an entry grows or shrinks during copying, backup refuses publication and names
its relative path. Preserve that error, identify the writer, and settle it through
its existing owner before retrying. Do not treat a partial archive as a restore point
or bypass the backup owner with an unverified live-directory copy.

## Create and verify a backup

```sh
compound home doctor --home "$COMPOUND_HOME"
compound home backup --home "$COMPOUND_HOME" --target /safe/local/path/compound-home.tar.gz
compound home verify /safe/local/path/compound-home.tar.gz
```

Do not prune the last verified local copy merely because no remote destination
is configured. Keep at least one verified archive whose survival does not
depend on the home being backed up.

### Populated homes and the streamed profile

The default backup format remains v0.1: at most 250,000 logical tar members
(files, directories and metadata) and one manifest of at most 64 MiB. A writer
budget refusal publishes no archive. It is not permission to prune history or
to increase a verifier's limits.

For a larger home, explicitly select the bounded v0.2 profile:

```sh
compound home backup --home "$COMPOUND_HOME" --format streamed-v2 --target /safe/local/path/compound-home-v2.tar.gz
compound home verify /safe/local/path/compound-home-v2.tar.gz
```

`--format v1` preserves the default. Unknown or missing format values refuse.
The streamed format has a small leading version descriptor, bounded chunks of
file checksums, and a final index binding those chunks. Verification and restore
recognize that descriptor; an archive cannot supply its own resource limits.
Chunking does not waive checksums, duplicate-path checks, source identity,
staged-file verification, absent-target publication or writer quiescence.

Retain the exact recovery binary that produced and verified a streamed archive
alongside the archive's evidence. Older binaries do not understand v0.2. Use
the retained compatible recovery binary for the absent-target restore before
starting a prior application binary against the restored home. Changing the
archive format does not change the home schema or authorize a live migration.

A header inventory, successful archive write or synthetic scale test is not
populated-home recovery acceptance. The actual archive must pass verification,
an isolated restore, home health checks and a real restored-home consumer.

On macOS, streamed restore syncs each file to the device while staging remains
private and unpublished. It then requires a checked full-device flush after
directory sync and staged verification, before publishing the new home. A second
checked flush follows the rename and parent-directory sync. All staged entries
must remain on the same device; mounted subtrees are refused. The destination
filesystem must support `F_FULLFSYNC`: unsupported or failed flushes are errors,
not a fallback to weaker durability. A post-publication error preserves the new
target for manual verification and must not trigger an automatic repeat.
Unpublished staging is not a resumable recovery checkpoint. Keep it free of
concurrent writers. Legacy archives and other platforms retain their existing
sync protocol; these checks do not simulate physical power failure.

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

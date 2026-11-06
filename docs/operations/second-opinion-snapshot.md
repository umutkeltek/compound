# General second-opinion snapshot

For a broad external review of the system, run from the repository root:

```sh
bash scripts/desktop-compound-zip.sh --second-opinion
```

The result is the Desktop `compound.zip`, with a 450 MB hard limit (450,000,000 bytes). Existing Desktop `compound` and `compound.zip` are replaced only after integrity and size checks; use
`--name` and `--zip` to keep separate snapshots. Creating the archive does not
upload it anywhere. This mode includes private context and is for the operator's
chosen reviewer, not public source distribution.

The profile includes tracked source and nonignored local files, including local
changes, plus up to 32 MiB of current private context and recent text evidence.
It selects private architecture/planning/instruction documents, work handoff and
root context, workflow documents, product dossiers, and private procedure files.
Evidence includes the newest run per workflow for up to twelve recent workflows
and six recent work packets. Recency uses filesystem modification time with a
stable path tie-break; it does not favor successful outcomes. Extra context is
selected in whole text groups, without truncating JSON or other files.

Review metadata includes the source's package-time Git state, thirty recent
commit summaries, a general review brief, and a manifest with included file
sizes/hashes, selection reasons and budget/filter omissions. Caches, runtime
homes, raw corpora, archives, duplicate worktrees, Git objects and private media
are omitted. The real Compound home is not exported, and historical evidence is
not a fresh live-health check. The sample does not prove coverage of every
workflow, failure, or cross-reference; the manifest makes those limits visible.

To prioritize specific evidence in the same budget:

```sh
bash scripts/desktop-compound-zip.sh --second-opinion \
  --review-evidence docs_private/plan/workstreams \
  --review-data-mb 48
```

Explicit evidence must be a repository-relative file or directory containing
eligible text. It is selected before automatic context, and an explicit group
that cannot fit causes a refusal before replacing an existing snapshot.
`--exclude` still applies; the final manifest records the files it removes.
The final ZIP limit is independent of the private-data budget: an oversized
candidate is discarded and the command fails while preserving the previous
snapshot, rather than silently dropping source.

Ask the reviewer to read `REVIEW-START-HERE.md`, cite source paths and lines, test
the repository's assumptions, and distinguish findings from missing evidence.

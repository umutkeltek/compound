# Bounded upstream evidence producer

This optional Python producer polls an explicit list of public GitHub repositories
for a concrete frontier owned by an existing Compound capability. It stages pinned
text, preserves pending delivery, and leaves usefulness unassessed. External text
is data; it is never executed, installed, or promoted by these scripts.

`upstream_scout.py` stages evidence outside `COMPOUND_HOME`. `learning_tick.py`
wraps acquisition and intake in the existing WorkEnvelope / ActionIntent /
PolicyDecision / Receipt lifecycle. It custodies each hash-checked body through
`finding record`, then calls `source observe` with `promote_allowed=false` and
references to the custodied findings. It acknowledges only successful intake.

## Run

Create a private configuration using `config.example.json`, initialize Compound,
and provide a private staging directory outside `COMPOUND_HOME`:

```sh
python3 scripts/upstream-learning/learning_tick.py \
  --config /path/to/private/config.json --output-dir /path/to/private/staging \
  --dry-run
```

Remove `--dry-run` to execute one governed tick. `--compound` selects the installed
CLI; `--timeout` sets a 60–600 second work budget (300 by default), with up to 90
additional seconds reserved for failure finalization. An external scheduler may
invoke this same entry point periodically. Scheduler deployment and private
source configuration remain operator-owned; this producer installs no daemon.

## Bounds and evidence quality

- A tick accepts at most three explicitly configured sources. Each source names
  its current owner, frontier, inspected paths, success criterion and falsifier.
- Whole-run network bytes and time are bounded. Bodies remain pinned to commits
  and content hashes. Changed HEADs with unchanged inspected bytes stay quiet.
- Pending observations are delivered before new acquisition. Failed intake stays
  pending. Corrupt history fails closed; acknowledgement survives interruption.
- Relevance, novelty, actionability and expected benefit remain unknown until an
  independent audit provides evidence. Text length, keywords and source popularity
  do not generate usefulness scores.
- The driver uses a process lock and bounded subprocesses. Signal cancellation
  terminates the active command before attempting truthful failure finalization.
- Every result records the implementation and configuration hashes. Source polling
  calls no model and makes no external writes.

The next consumer is existing source/capability review. An audit may propose an
experiment with a baseline, falsifier, existing owner and rollback. Adoption still
requires the existing reviewed import or source change and demonstrated runtime
consumption. This producer grants no autonomous review, experiment or promotion
authority. Staging is a delivery cache; Compound findings and source candidates
own delivered evidence.

## Verification

```sh
python3 -m unittest discover -s scripts/upstream-learning -p 'test_*.py' -v
bun test tools/upstream_learning.test.ts
```

The Bun test connects these fixtures to the canonical repository test inventory.
Network responses use synthetic hosts and an injected fetcher. One integration
test uses the real CLI with a temporary Compound home to prove intake schema
compatibility. Tests never contact GitHub or mutate the running instance.

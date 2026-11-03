# Hook redesign: zero wait, the lesson at the moment, a gate only for the irreversible

Status: prototype on a branch (spool, drain, lesson index). The irreversible-class gate is a proposal only.

## The seam

The PreToolUse hook is the one place Compound touches every tool call in every harness. Nothing else
sees the call before it runs. That makes it the most valuable seam Compound has, and the most
expensive one to waste.

Today, in watch-only (interactive) harnesses, the hook spends its time on synchronous policy
classification that it never enforces. It does not block. It does not inject anything. The agent
still waits for it on every call.

Measured from interactive session transcripts, 2026-10-22 to 2026-11-03 (hook commands only):

| Hook | Calls | Median | p90 | Total wait |
|---|---|---|---|---|
| PreToolUse | 9,863 | 1,728 ms | 9.5 s | 10.8 h |
| PostToolUse | 9,653 | 235 ms | 613 ms | 1.2 h |

An earlier review sample put the PreToolUse median at 1,342 ms and the wait at about 11 hours per
week across all sessions. Both samples say the same thing: the cost is real and it buys nothing in
watch-only harnesses.

## Three jobs

The hook does three jobs, and only the third one is allowed to make the agent wait.

1. **Record asynchronously.** The hook appends the native input as one line to a spool file and
   answers. A drainer (`compound hook drain`) replays each line through the same record-building path
   the synchronous hook uses, at the time the call was made. The records still land; the agent does
   not wait for them.
2. **Inject the lesson at the moment.** The hook reads one small index file that maps a trigger (tool
   name plus a regular expression over the command or input) to a one-line "how to apply". On a
   match, the line goes back to the agent as `additionalContext`, in the same response shape the
   harness already accepts.
3. **Gate only the irreversible.** Enforcement stays where it is today: the isolated lanes that run
   with `--enforce`. This change adds no deny behavior. A future irreversible-class gate is proposed
   below.

## What is removed, and why

Removed: synchronous policy classification in watch-only harnesses. It cost about 1.3 to 1.7 s per
call and its verdict was never enforced or shown to the agent. The same classification still runs,
off the critical path, in the drainer.

What this costs, stated plainly:

- **Records arrive later.** With `drain --follow`, the delay is seconds. Without a running drainer,
  records wait in the spool until the next drain.
- **No coordination interrupt at PreToolUse.** A session bound to a work item used to receive
  coordination cards at PreToolUse. The spool path does not deliver them, and the drainer must not
  consume them (no agent would see them). Boundary delivery at session start and prompt submit is
  unchanged. A session that needs interrupt delivery keeps the synchronous hook.
- **Echoed source lists are kept as a digest.** If an adapter echoes served source lists
  (`source_envelopes`, `input_sources`, `evidence_refs`) in the hook input, the spool keeps a SHA-256,
  a count and a byte size, never a copy. The drainer rebuilds active sources from the home at drain
  time.

## Mechanics

**Spool.** `compound hook pre-tool-use --live --harness H --spool` reads stdin and appends one JSON
line to `COMPOUND_HOME/observed/hook-spool/<UTC day>.ndjson` (an existing evidence family, so
`home doctor` stays green). The line holds the schema version, hook, harness, call time, session id,
tool name, matched lesson ids, and the compacted input. The append takes an advisory file lock. The
spool never creates a home and never writes into one this binary cannot mutate: a missing or
incompatible manifest means it answers and records nothing. It always
exits 0. `--spool` is refused with `--enforce`, with `--parity-log`, and on any hook other than
pre-tool-use.

**Drain.** `compound hook drain [--once|--follow] [--interval 2s] [--max-lines N]` holds a
non-blocking drain lock, reads complete lines from each day file from a saved cursor, and replays
each line in observation mode at its call time. It skips a trailing partial line (a writer is still
appending). A line that fails to replay goes to `rejected.ndjson` with its error, and the queue moves
on. A drained file from a past day is removed after a one-minute grace.

**Lesson index.** `COMPOUND_HOME/indexes/hook-lessons.json` (rebuildable family), built by
`compound hook lessons-index --from lessons.json --skills DIR`. A lesson is
`{id, tools[], pattern, apply, source}`. The builder also accepts the producer spellings `trigger`
and `how_to_apply`. Limits: `apply` is one line of at most 300 bytes, at most 2 lessons per call, and
an index file of at most 256 KiB. The pattern runs against the shell command for shell tools, and
against the compact JSON input for every other tool. A lesson naming `Bash` also covers the shell
tool names other harnesses use. In a skill file, a trigger is one line: `trigger: {<lesson JSON>}`.
The builder rejects invalid lessons, keeps the valid subset, and exits 1 so automation notices.

## Acceptance numbers

| Measure | Today | Target | How counted |
|---|---|---|---|
| PreToolUse wait per call | 1,342 to 1,728 ms median | < 50 ms | `durationMs` of the hook in session transcripts |
| Prototype spool path (process level, lesson matched) | n/a | < 50 ms | measured 17 ms median against a 12 ms bare process start, on a heavily loaded machine |
| Hook wait per week | about 11 h | < 0.5 h | sum of the same `durationMs` per week |
| Record parity | n/a | drained records equal the synchronous ones | test: same call through both paths, same hook event except time-derived ids |
| Lesson hit | n/a | reported, then raised | see below |

**Lesson hit.** An injection is a hit when, later in the same session, the agent does not make a
call that matches the same trigger again. It is a miss when it does. Counted from transcripts: the
injected line carries its lesson id, and the spool line records which lesson ids fired. Compare the
repeat rate against the same triggers in transcripts from before injection existed.

A watch-only injection does not stop the call it fires on. The harness delivers PreToolUse
`additionalContext` with the next tool result, so the agent reads the lesson after the matching call has
run. In watch mode the lesson informs the next decision, not the current one. The trigger should
therefore fire on the step that comes before the damage where it can (for example, the command that
prepares an upload), not only on the damaging command itself. For a mistake that cannot be undone, the lesson alone is not enough;
that is the job of the gate.

## How it degrades

Every failure lands on "no injection" or "no record". None of them becomes a block.

| Condition | Result |
|---|---|
| Index missing, unreadable, over 256 KiB, wrong schema | no injection |
| One lesson with a bad pattern | that lesson is skipped; the others still fire |
| Home missing, not initialized, or incompatible | answer, no record, nothing created |
| Spool append fails | answer, a note on stderr, exit 0 |
| Drainer not running | the spool grows until the next drain (roughly 0.5 KB per call plus large tool inputs) |
| Second drainer | exits 3; `--follow` keeps polling |
| Replay fails for one line | the line goes to `rejected.ndjson`; the queue continues |

## Proposal (not built): the irreversible-class gate

Some actions cannot be undone: force-push, history rewrite, delete outside the workspace, store
submission, and spending money. For these, a lesson that arrives with the call is too late.

Proposed shape: the lesson index grows a `class` field. The hot path stays one regex prefilter over
the index. Only a prefilter match runs the full policy classification for that one call, and only an
irreversible-class verdict returns `ask` (a prompt to the operator) in interactive harnesses. Calls
that do not match never wait for classification.

Evidence that would justify building it, measured from drained records over at least four weeks:

- **Frequency.** How many irreversible-class calls interactive sessions make, per class.
- **Harm.** At least one real incident in a class, or the operator naming the class.
- **Precision.** At least 90% of prefilter matches are real irreversible-class calls, so the prompt
  does not train the operator to click through.
- **Cost.** The prefilter keeps the per-call wait under 50 ms, and the full classification runs on
  less than 1% of calls.

## Install

`compound hook install` renders the interactive Claude Code and Codex PreToolUse entry in the
`--spool` form with a 5-second timeout. A timeout already set on the existing Compound entry wins
over that default. Enforced lanes keep their synchronous `--enforce` entries, and the other events
keep their synchronous commands. The operator also runs a drainer (`compound hook drain --follow`, or
`--once` on a schedule) and builds the lesson index from the lesson store.

`compound doctor` probes a `--spool` route by running it, then running the same binary's
`hook drain --once` in the probe home, then checking the same policy evidence it checks for the
synchronous route. A spool route that answers but never records fails the probe.

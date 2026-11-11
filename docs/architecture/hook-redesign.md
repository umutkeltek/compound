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

Lesson-backed trigger entries resolve through one memory-owner snapshot during index compilation,
including current instruction and trigger bytes. The index records that snapshot's ledger sequence
and content revision, lists excluded non-current IDs, and excludes superseded or retired lessons.
Unresolved contradictions render as conflict notices, never as two accepted instructions. Historical
Markdown remains intact. Ordinary manually authored triggers still work without a memory lookup.
An unreadable memory snapshot fails the build visibly and preserves the last complete index.
The tool-call path continues to read only the bounded index; it never queries memory or rebuilds it.

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

## Session start is minimal; the pack is on demand

Claude Code and Codex session starts no longer build or inject the ContextPack. They serve, in
this order:

1. the Tier-0 law line (one file read, the same line a full pack summary carries);
2. the door map lines, unchanged;
3. the operator model block, when the home has one (the same capped block the full start serves);
4. one pointer: ``Context pack is on demand: run `compound orient --harness <H> --cwd <cwd> --task "<task>" --brief` before a non-trivial task.``

The session is still recorded: the SessionStart hook event (session id, harness, input hash) and the
session's orientation marker. The marker now says the pack was on demand, so the per-prompt pointer
tells the truth. No ContextRequest, EffectiveContract, or ContextPack is built or minted.

**Why.** A pre-registered, blind with-and-without run on 10 tasks. Arm A ran with all hooks off;
arm B had the session-start pack injected. A judge from a different model family graded both
orders. The rule, fixed before the run: keep the always-on pack only if B wins at least 6 of 10
outright. Result: B 3, A 3, ties 4; B used 28% fewer tokens. The pack did not earn its place.
Measured use of the served pack over 76 sessions points the same way: of 2,569 served items, agents
used 81: 63 of 1,437 door lines, 14 of 446 memory lines, 4 of 416 positions, 0 of 259 capability
offers, and 0 of 11 skills. The door map is the only part with real use, so it stays. The script, the
judge prompt and the per-task scores are in [the placebo test](../placebo-test/README.md).

**Cost.** In live sessions the full start measured 3.4 s median and injected about 5 KB. On the
built binary, with a scratch home (ten doors and a short synthetic operator model) and a repository
fixture (median of 10 runs, one warm-up):

| Mode | Wall time | Injected `additionalContext` |
|---|---|---|
| minimal | 19 ms | 3,018 bytes (law line 1,221; door map 1,113; operator model 437; pointer 241; separators) |
| full | 60 ms | 4,343 bytes (the same door map and operator model blocks) |

The scratch home holds no memory, capability, or decision state, so its full start is far cheaper
than the 3.4 s live median. The minimal start does not read that state, so its cost does not grow
with the home. With `COMPOUND_SESSION_PACK=full` the output equals the previous binary's output except
for the time-derived pack id, which also differs between any two runs of the previous binary.

**Who gets which mode.**

| Lane | Default |
|---|---|
| `claude_code`, `codex` (interactive, watch-only) | minimal |
| `claude_zai`, `claude_minimax`, `claude_qwen`, `oh_my_pi`, `opencode`, and any hook run with `--enforce` | full |

The default table is `hookplan.DefaultSessionPack`. `COMPOUND_SESSION_PACK=full` in the harness
environment restores the full start byte for byte; `COMPOUND_SESSION_PACK=minimal` forces minimal on
any lane. The hook process inherits the harness environment, so a re-test sets it per arm with no
reinstall. Fixture replays (without `--live`) always build the full pack. Codex keeps its per-prompt
context build; Claude Code has none by default, so for it `compound orient` is the only route to the
pack.

**Re-test.** Run the same protocol with arm B launched under `COMPOUND_SESSION_PACK=full`. If B wins
at least 6 of 10 outright, change the default table, not the hook.

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
keep their synchronous commands.

Provider lanes (`claude-minimax`, `claude-zai`, `claude-qwen`) install into the provider profile the
lane launcher starts them from; `hookplan.LaneHookSettingsPath` is the one owner of that path. A reinstall
keeps the PreToolUse posture a lane already has. A lane whose PreToolUse is watch-only is rewritten
to `--enforce-governed`: the hook blocks only when the process carries `COMPOUND_RUN_ATTEMPT_ID`,
which the shared harness runner sets on every launch and `work delegate` sets on delegated
sessions. The operator's own sessions on that profile keep observing; governed runs on it block.
Readiness and installed-hook authority count `--enforce-governed` as blocking, and `compound
doctor` probes it both ways (exit 0 without a run id, the exact gate exit with one). The operator also runs a drainer (`compound hook drain --follow`, or
`--once` on a schedule) and builds the lesson index from the lesson store.

`compound doctor` probes a `--spool` route by running it, then running the same binary's
`hook drain --once` in the probe home, then checking the same policy evidence it checks for the
synchronous route. A spool route that answers but never records fails the probe.

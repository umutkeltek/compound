# The placebo test

Does the thing you load into your coding agent help? This folder has a way to find out. Run the same
tasks with it on and with it off, and let a blind judge compare the answers.

The test was first run on Compound itself, and the result changed how Compound starts a session. The
method comes first on this page. The first run and its scores come after it.

## How it works

1. You write the rule first: how many of your tasks the thing must win to stay.
2. Each task runs twice in fresh, read-only Claude Code sessions. Same model, same prompt, same folder.
   Once with the thing on, once with it off.
3. A judge from a different model family gets the task, the known right action and both answers, with
   no labels. It sees each pair twice, once in each order. A win needs the same pick both times. A pick
   that changes with the order is a tie.
4. `score` counts the wins and applies your rule. It refuses a rule that changed after the first run
   started, and it gives no verdict while a pair is still waiting for the judge.

## What you need

- Python 3. No packages. Tested on 3.14.
- Claude Code (`claude`), signed in. Tested with 2.1.319.
- A judge you can call from a terminal: any command that takes the prompt as its last argument and
  prints the reply. Use a different model family from the one that writes the answers. The first run
  used `opencode run -m <provider/model>`.
- About ten tasks from your own recent work where you already know the right action.

Every run is a real session on your Claude plan. The first run below processed about 7 million tokens
across both arms, most of them cache reads.

## Run it

1. Copy [`tasks.example.json`](tasks.example.json) and write your own tasks. Each task has the folder
   it runs in, the question, and the known right action. The judge sees the known right action. The two
   runs never do.
2. Set the rule in the same file. `"win_bar": 6` means the thing must win 6 pairs outright.
   `"token_ratio_cap": 2.0` means it may use at most twice the tokens of the off run. Leave the cap out
   if you do not want one.
3. Print the commands without running anything:

   ```sh
   python3 placebo_test.py all --task-file my-tasks.json --out trial-1 \
     --judge-cmd "opencode run -m <provider/model>" --dry-run
   ```

4. Try two tasks first. A pilot is scored `INCOMPLETE`, never as a verdict.

   ```sh
   python3 placebo_test.py all --task-file my-tasks.json --out trial-1 \
     --judge-cmd "opencode run -m <provider/model>" --tasks T01,T02
   ```

5. Run everything, then read `trial-1/results.json`. The outcome is `KEEP` or `NOT-KEPT`.

   ```sh
   python3 placebo_test.py all --task-file my-tasks.json --out trial-1 \
     --judge-cmd "opencode run -m <provider/model>"
   ```

The three steps also run one at a time: `run`, `judge`, `score`. Every step keeps finished work. If a
run was cut off or the judge did not answer, run the same command again and it fills in only what is
missing.

## What "off" means

By default the off run disables every Claude Code hook (`--settings '{"disableAllHooks": true}'`).
That is the right switch for a memory that a session-start hook injects. Compound's pack arrived that
way.

For anything else, pass the `claude` arguments that switch your thing off and on:

```sh
--off-args "<arguments that switch it off>" --on-args "<arguments that switch it on>"
```

Everything else stays the same in both runs. If your rules file, notes and skills load in both, the
test measures what the thing adds on top of them.

Pass `--marker "some text"` with a string your thing always prints into the session. A run then counts
only if the on run shows the marker and the off run does not. Without a marker the script still refuses
a default off run that shows a session-start hook.

## What keeps it honest

- **Read-only runs.** Plan mode, no edit tools, no Git writes. A checker flags any call that could have
  written something (`mutation_flags` in the results), so you can look at it yourself. A flag marks an
  attempt, including one that was refused: in plan mode Claude Code tries to save its plan note with
  `Write`, the deny list refuses it, and the flag still appears.
- **Invalid runs never decide.** A run that timed out, came back empty or was in the wrong arm is run
  once more. If it is still invalid, the pair is a tie.
- **A judge with no project context.** The judge runs in an empty folder, so your project's
  instructions never reach it.
- **A locked rule.** The rule and the task list are written to `rule.json` when the first step runs.
  Every later step refuses a file that differs.
- **No second tries.** A valid run and a recorded verdict are never replaced. You cannot run a pair
  again until you like the answer. A new attempt needs a new `--out` folder.
- **Silence is not a tie.** If the judge hangs or prints no verdict, the pair is `unjudged` and the
  outcome is `INCOMPLETE`. Only a real `TIE`, or a pick that changes with the order, counts as a tie.
- **Alternating order.** Which arm runs first changes from task to task.

## The judge's question

The judge gets this text, once with the off answer first and once with the on answer first:

```text
You are judging two answers to the same task. Neither author knew the outcome; you do.

TASK (both answers got exactly this):
{task}

KNOWN RIGHT ACTION (established later from the real outcome; hidden from both authors):
{known}

ANSWER 1:
<<<
{one}
>>>

ANSWER 2:
<<<
{two}
>>>

Question: which answer would have led to the better action, judged only against the known right action?
Ignore length, tone and formatting. If both are equally close or equally far, answer TIE.
Reply with exactly one token on the last line: 1, 2, or TIE.
```

## The first run: Compound's session-start pack

On 2026-11-03, four days before the first public release, this test ran on Compound's own
session-start pack. A hook pushed the pack into every session: memory lines, positions, a count of
capability offers, and a list of the tools that work on the machine.

The rule, written before the run: the pack stays in every session only if the on run wins at least 6 of
10 pairs outright and uses no more than twice the tokens.

| | |
|---|---|
| On wins | 3 |
| Off wins | 3 |
| Ties | 4 (two called even in both orders, two where the pick changed with the order) |
| Tokens, off | 4,092,860 |
| Tokens, on | 2,956,178 (28% fewer; task T10 alone is 83% of that gap) |
| Outcome | `NOT-KEPT` |

The runs were Claude Code headless with Claude Opus, read-only, with an 8-minute limit. The off run had
every hook disabled. Both runs kept the project rules file, plain notes and skills. The judge was
GLM-5.3.

The script in this folder is a cleaned-up copy of the one that produced these numbers. The prompt, the
judge's question and the counting of wins and ties are the same. The copy adds the rule lock, the
marker check and the `--off-args` switch, keeps finished work, and no longer counts an unanswered
judge call as a tie. All 20 judge calls in this run were answered, so the score is the same under
both.

| Task | What it asked (reworded, names removed) | Kind | Pick, off first | Pick, on first | Result | Tokens off | Tokens on |
|---|---|---|---|---|---|---|---|
| T01 | Make store screenshots for an app with an image model | discovery | on | off | tie | 182,361 | 183,779 |
| T02 | Check yesterday's ad spend and cost per install; decide whether to pause a keyword | discovery | off | off | off | 234,909 | 320,568 |
| T03 | Post a release announcement through the scheduling tool | discovery | off | off | off | 216,398 | 230,762 |
| T04 | Check whether a given app version is waiting for review | discovery | off | on | tie | 317,872 | 238,270 |
| T05 | A change is ready to land but the test queue is busy. What now? | lesson | tie | tie | tie | 411,991 | 384,226 |
| T06 | A UI test hangs on the simulator and never finishes. What do you do? | known outcome | on | on | on | 199,742 | 118,293 |
| T07 | A feature needs a new record type in the production database schema. Deploy it. | known outcome | on | on | on | 226,447 | 301,205 |
| T08 | Runs fail a capacity check because usage data is stale. Re-enable the job that refreshes it? | known outcome | tie | tie | tie | 319,385 | 283,015 |
| T09 | Should this app scale up its search-ads spend? Decide. | known outcome | on | on | on | 349,848 | 208,410 |
| T10 | A safety stop on an automated store-listing update is about to be cleared. Anything to do first? | known outcome | off | off | off | 1,633,907 | 687,650 |

Kinds: a *discovery* task needs the agent to find the right tool or skill. The *lesson* task has a
right action that an earlier mistake taught. A *known outcome* task had already been done, so its
result was known. The same numbers, with wall time and turns, are in
[`results-2026-11-03.json`](results-2026-11-03.json).

The answers are not published. They quote account numbers, sales figures and release details of the
author's apps. The task wording above is reworded for the same reason.

The checker flagged four tool calls in the 20 runs. Two were one off run trying to write its own
planning note. Two were a retrieval tool the checker did not know.

### What real sessions did with the pack

A separate count over 76 real sessions (2026-10-22 to 2026-11-03) points the same way. The pack served
2,569 items. A later tool call used 81 of them:

| Part of the pack | Served | Used |
|---|---|---|
| List of working tools | 1,437 | 63 |
| Memory lines | 446 | 14 |
| Positions | 416 | 4 |
| Capability offers | 259 | 0 |
| Skills | 11 | 0 |

The offers and skills were served as a count, without their names. That zero is a defect in the pack
and says nothing about skills. "Used" means a later tool call matched the item. It does not prove the
pack caused the call, and it cannot see an item that changed the agent's reasoning without a tool call.

### What changed

Compound obeyed the rule. Session start no longer builds or injects the pack. It serves a short start
(one line of rules, the tool list, the operator model block when there is one) and one pointer. The
agent asks for a briefing with `compound orient` once it has a task. The design note is
[Hook redesign](../architecture/hook-redesign.md#session-start-is-minimal-the-pack-is-on-demand).

The briefing that `compound orient --brief` prints has not been through this test yet. It gets no
place at session start until it passes.

### Limits

- Ten tasks, one run per arm, one judge model, one person's setup.
- There was no off-against-off run, so the noise level is unknown.
- A tie means the pack could not show that it helps. It is not proof that it does nothing.
- Some right actions were also in plain notes that both runs could open. That was deliberate: the
  question was what the pack adds on top of them.
- The script drives Claude Code only.

## Tests

```sh
python3 -m unittest test_placebo_test
```

The tests cover the scoring rules: an order flip is a tie, the bar counts outright wins, a pilot is
never a verdict, an unanswered pair is not a tie, a recorded verdict is never replaced, and a changed
rule is refused. One test recomputes the first run's outcome from its published verdicts.

# Where were we?

Finish a session with a coding agent. Open a new one in the same folder and type three words:
**Where were we?** This folder holds a small test of what the new session knows, the script that
runs it, and the answers from the first 53 runs.

Short version: an agent's built-in memory belongs to that agent. A second agent in the same folder
does not see it. Notes written into the repository are seen by both.

## The test

One repository with one file, `hello.py`. One working session gets one job and two decisions, said
once:

```text
Add a --json flag to hello.py: with it the script prints {"greeting": "Hello, <name>"} as JSON instead of plain text. Two things you should know. First, we will not add colored output, because the output of this script gets piped into other tools. Second, the 1.0 release is waiting on me to pick the version name, so do not tag or release anything. Do not commit; I will.
```

The change is committed. Then a brand-new session, read-only and with no history, gets exactly
`Where were we?`. Its answer is checked for five facts:

1. The flag is done.
2. No colored output.
3. The reason: the output is piped into other tools.
4. The release is waiting on the version name.
5. So nothing gets tagged or released.

The first fact is in the Git log. The other four are decisions that exist nowhere in the code.

## Results (2026-11-12, 53 runs)

Each number is how many of the five facts a new session stated.

| What existed after the working session | New Claude Code session | New Codex session |
|---|---|---|
| Nothing written down | 1, 1, 1, 1, 1, 1 | 1, 1, 1, 1, 1, 1 |
| Claude Code's own saved notes | 5, 5, 5, 5, 5, 5 | 1, 1, 1, 1, 1, 1 |
| The same, asked in a Git worktree | 5, 5 | not run |
| The same, asked in a fresh clone | 1, 1, 1, 1, 1 | not run |
| Notes written into the repository | 5, 5, 5, 5, 5 | 5, 5, 5, 5, 4, 4 |
| Only Compound's record, briefing before the change | not run | 1, 1, 1 |
| Only Compound's record, briefing after the change | 5, 5, 1, 5, 5 | 5, 5, 5 |

Every answer, with its score and the number of lookups the session made, is in
[`results.json`](results.json).

### Nothing written down

Twelve runs. In four of them Codex had done the working session, in six Claude Code had done it and
saved no notes, and in two the change was applied by hand. A new session of either agent knew the
flag was done and nothing else.

### The agent's own notes

In 6 of the 7 working sessions where nothing was asked, Claude Code wrote the two decisions into its
memory and said so ("I saved both to project memory so they carry into later sessions."). Those notes
are files in the agent's own folder on disk, outside the repository, filed under the project's path.

- A new Claude Code session in the same folder read them: all five facts, six runs of six.
- In a Git worktree of the same repository: all five, two runs of two.
- In a clone of the repository in another folder: one fact, five runs of five.
- Codex in the same folder: one fact, six runs of six.

In the two working sessions that Codex did, neither agent found any notes afterwards.

### Notes written into the repository

The working session got six more lines at the end of its prompt:

```text
Before we stop, write down what the next session needs. Do not summarize the whole session.
1. HANDOFF.md: what is finished and checked, what is still open, and what is waiting on me.
2. decisions/<date>-<topic>.md, one file per decision we made: the decision, the reason in my words, and what would change it.
3. In HANDOFF.md, a "Do not redo" list.
Put the date at the top of every file.
```

`CLAUDE.md` and `AGENTS.md` each held one line:

```text
Start here: read HANDOFF.md and the newest files in decisions/ before doing anything else.
```

Eleven runs. Claude Code stated all five facts in five of five. Codex stated all five in four of six
and four in the other two, where it gave the decision without the reason. In two of the eleven, Codex
had written the notes. One of the eleven was made with the script in this folder.

Every handoff said the change was not committed yet, which stopped being true a minute later. All
eleven sessions that read such a note checked Git and reported the commit.

### Only Compound's record

Nothing about the decisions in the repository, and no agent notes. Compound's hooks had recorded the
working session: a receipt with the time, the changed files and the path of the transcript.

- With the briefing as it was before this change, a new Codex session asked for the briefing, got no
  facts for the checkout, and stated one fact. Three runs of three.
- With the briefing after this change (`compound orient --brief` names the last session in the
  checkout and the path of its transcript), a new session stated all five facts in seven runs of
  eight. In three runs the new session was Codex and Claude Code had done the work. In three both
  were Claude Code. In two Claude Code asked and Codex had done the work. In the eighth run the new
  Claude Code session did not ask for the briefing.

The change is described in [Runtime](../architecture/runtime.md), in the orient brief note.

## Run it yourself

You need Git, Python 3, and the agents you want to test: Claude Code (`claude`) and the Codex CLI
(`codex`), each signed in. Every session is a real one on your plan. One working session and one
question take two to four minutes.

```sh
# nothing is asked to be written down: Claude Code works, Codex asks
./where_were_we.sh --worker claude --asker codex

# the same agent asks three times
./where_were_we.sh --worker claude --asker claude --asks 3

# the working session also writes notes into the repository
./where_were_we.sh --worker claude --asker codex --notes repo

# the same agent, asked in a fresh clone or in a worktree
./where_were_we.sh --worker claude --asker claude --where clone
```

The script prints one line per answer: how many of the five facts it states and which are missing.
`score.py` holds the five rules and can score any saved answer: `python3 score.py answer.txt`.

The script uses your own settings, so the result is about your setup. If Compound's hooks are
installed, they record the working session, and a new session can ask for the briefing.

## How the runs were made

- Claude Code 2.1.319 with `claude -p` (model reported as `claude-fable-5-2`) and Codex CLI 0.167.0
  with `codex exec`, on one Mac. Working sessions could edit files. Asking sessions were read-only.
- Outside the Compound rows, Claude Code ran with all hooks off
  (`--settings '{"disableAllHooks": true}'`). Codex on that machine has Compound's hooks in its own
  configuration, so in those rows Compound was pointed at a new, empty home: Codex asked it for a
  briefing and got nothing.
- In the Compound rows, the working session ran with Compound's four hooks pointed at a scratch home.
  The home was copied when the working session ended, and every asking session got its own copy, so
  no run could read what another run had left.
- The script in this folder is a cleaned-up version of the scripts that made the runs. The prompts
  are identical. It does not set up scratch homes or turn hooks off. One run in the table was made
  with it, as a check that it works.
- Scoring is by the rules in `score.py`, applied to every answer alike. Every answer was also read.
  In the answers, machine paths, session ids and the operator's first name are replaced.

## Limits

- One tiny repository, two decisions, five facts, one prompt per session, one machine.
- Both agents ran headless, and the question came minutes after the working session. Codex has a
  memory feature of its own; it did not come into play in these runs. This test is about what a
  brand-new session can find right away.
- The transcript of the working session was small. A pointer to a long transcript is not a handoff.
- Both agents read tool output through a local proxy that shortens long output to save tokens. It
  applied to every run alike.
- Eight earlier runs are left out. In them, test sessions had written records into a scratch home
  that later sessions could read, so the setups were not what their labels said. Their scores were
  5, 5, 5, 5, 1, 1, 1, 1.

## Tests

```sh
python3 -m unittest test_score
```

The tests score real answers from the runs: full answers must score five, and answers built from the
repository alone must score one.

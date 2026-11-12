#!/usr/bin/env bash
# where_were_we.sh: let one coding agent do a small job, then ask a brand-new session "Where were we?"
# and count how many of five facts it knows.
#
#   where_were_we.sh --worker claude --asker codex                 nothing is asked to be written down
#   where_were_we.sh --worker claude --asker codex --notes repo    the worker also writes notes into the repo
#   where_were_we.sh --worker claude --asker claude --where clone  ask in a fresh clone (or: --where worktree)
#   options: --asks N (default 1)   --dir DIR (default: a new temporary folder)
#
# The worker gets one job and two decisions. The asker gets three words, read-only, with no history.
# Both run headless with your own settings (claude -p, codex exec), so the result is about YOUR setup.
# Every session is a real one on your plan. Answers land in DIR/answers; score.py counts the facts.
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
WORKER="" ASKER="" NOTES=none ASKS=1 WHERE=same DIR=""
while [ $# -gt 0 ]; do
  case "$1" in
    --worker) WORKER="$2"; shift 2 ;;
    --asker) ASKER="$2"; shift 2 ;;
    --notes) NOTES="$2"; shift 2 ;;
    --asks) ASKS="$2"; shift 2 ;;
    --where) WHERE="$2"; shift 2 ;;
    --dir) DIR="$2"; shift 2 ;;
    *) sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'; exit 2 ;;
  esac
done
case "$WORKER:$ASKER:$NOTES:$WHERE" in
  claude:claude:none:*|claude:codex:none:*|codex:claude:none:*|codex:codex:none:*) ;;
  claude:claude:repo:*|claude:codex:repo:*|codex:claude:repo:*|codex:codex:repo:*) ;;
  *) echo "need --worker claude|codex, --asker claude|codex, --notes none|repo" >&2; exit 2 ;;
esac
case "$WHERE" in same|clone|worktree) ;; *) echo "--where same|clone|worktree" >&2; exit 2 ;; esac

WORK='Add a --json flag to hello.py: with it the script prints {"greeting": "Hello, <name>"} as JSON instead of plain text. Two things you should know. First, we will not add colored output, because the output of this script gets piped into other tools. Second, the 1.0 release is waiting on me to pick the version name, so do not tag or release anything. Do not commit; I will.'
WRITEBACK='

Before we stop, write down what the next session needs. Do not summarize the whole session.
1. HANDOFF.md: what is finished and checked, what is still open, and what is waiting on me.
2. decisions/<date>-<topic>.md, one file per decision we made: the decision, the reason in my words, and what would change it.
3. In HANDOFF.md, a "Do not redo" list.
Put the date at the top of every file.'
POINTER='Start here: read HANDOFF.md and the newest files in decisions/ before doing anything else.'
ASK='Where were we?'

[ -n "$DIR" ] || DIR="$(mktemp -d "${TMPDIR:-/tmp}/where-were-we.XXXXXX")"
REPO="$DIR/app"; OUT="$DIR/answers"
[ ! -e "$REPO" ] || { echo "refusing an existing folder: $REPO" >&2; exit 2; }
mkdir -p "$REPO" "$OUT"
export GIT_AUTHOR_NAME=demo GIT_AUTHOR_EMAIL=demo@example.invalid GIT_COMMITTER_NAME=demo GIT_COMMITTER_EMAIL=demo@example.invalid

git -C "$REPO" init -q -b main
printf 'import sys\n\nname = sys.argv[1] if len(sys.argv) > 1 else "world"\nprint(f"Hello, {name}")\n' > "$REPO/hello.py"
printf '# hello\n\nA tiny greeting script.\n' > "$REPO/README.md"
git -C "$REPO" add -A && git -C "$REPO" commit -q -m "initial hello script"

result_text() {  # the final answer from `claude -p --output-format json`
  python3 -c 'import json,sys; print(json.load(sys.stdin).get("result") or "")'
}

# ---- the working session
PROMPT="$WORK"; [ "$NOTES" = repo ] && PROMPT="$WORK$WRITEBACK"
echo "working session: $WORKER, notes: $NOTES"
if [ "$WORKER" = claude ]; then
  ( cd "$REPO" && claude -p "$PROMPT" --permission-mode acceptEdits \
      --allowedTools Read Edit Write Glob Grep "Bash(python3 hello.py:*)" "Bash(mkdir:*)" "Bash(date:*)" "Bash(ls:*)" \
      --output-format json < /dev/null | result_text > "$OUT/work.txt" )
else
  ( cd "$REPO" && codex exec --sandbox workspace-write --skip-git-repo-check -o "$OUT/work.txt" "$PROMPT" < /dev/null > "$OUT/work.log" 2>&1 )
fi
if [ "$NOTES" = repo ]; then printf '%s\n' "$POINTER" > "$REPO/CLAUDE.md"; printf '%s\n' "$POINTER" > "$REPO/AGENTS.md"; fi
git -C "$REPO" add -A && git -C "$REPO" commit -q -m "add --json flag"
echo "files in the repo after the working session: $(cd "$REPO" && git ls-files | tr '\n' ' ')"

# ---- where the new session is asked
ASKDIR="$REPO"
if [ "$WHERE" = clone ]; then git clone -q "$REPO" "$DIR/app-clone"; ASKDIR="$DIR/app-clone"; fi
if [ "$WHERE" = worktree ]; then git -C "$REPO" worktree add -q -b second-copy "$DIR/app-worktree"; ASKDIR="$DIR/app-worktree"; fi

# ---- the question, in brand-new read-only sessions
i=1
while [ "$i" -le "$ASKS" ]; do
  A="$OUT/$ASKER-$i.txt"
  if [ "$ASKER" = claude ]; then
    ( cd "$ASKDIR" && claude -p "$ASK" --no-session-persistence \
        --allowedTools Read Glob Grep "Bash(git log:*)" "Bash(git status:*)" "Bash(git show:*)" "Bash(git diff:*)" "Bash(ls:*)" "Bash(cat:*)" "Bash(head:*)" \
        --disallowedTools Edit Write NotebookEdit --output-format json < /dev/null | result_text > "$A" )
  else
    ( cd "$ASKDIR" && codex exec --sandbox read-only --skip-git-repo-check -o "$A" "$ASK" < /dev/null > "$OUT/$ASKER-$i.log" 2>&1 )
  fi
  python3 "$HERE/score.py" "$A"
  i=$((i + 1))
done
echo "answers: $OUT"

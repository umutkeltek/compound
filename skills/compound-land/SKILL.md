---
name: compound-land
description: The landing ceremony for getting a change onto main in the compound repo — scope claim, full verification battery, rehearsed merge, --ff-only with a fresh main-SHA guard, post-land duties. Use when committing, merging, landing an executor/worker's diff or worktree, or when asked to "land"/"commit"/"merge" anything here.
---

# compound-land — how a change actually reaches main

Multiple agent lanes work this repo concurrently (an orchestrator lane, an autonomous executor
lane, occasional workers in worktrees). The guard rails below each exist because their absence
caused a real incident: concurrent landings caught only by the SHA guard, a doc-leak scan that
ran blind in a fresh worktree, workers that cannot commit, double-execution of queued work.

This skill replaces re-deriving the "standing session rules" from
`docs_private/plan/HANDOFF.md` every session. It specializes the generic
`finishing-a-development-branch` / `using-git-worktrees` skills for this repo.

## 0 · Scope claim (before touching anything)

- Check for a live concurrent lane: `git status --short` in the MAIN worktree (uncommitted work
  that isn't yours = a lane mid-ceremony — coordinate, do not land on top of it), plus the
  private worklog tail referenced from `docs_private/plan/HANDOFF.md` when present.
- Check the land-lease: `compound land-lease status --json` — read `holder`, `held`, `is_expired`,
  and (if present) `prior_holder`/`steal_reason` before starting. `free` (no holder) means clear to
  proceed to §3's acquire; `held by <holder>` means another lane is mid-ceremony — coordinate, or
  let §3's `--wait` block for it; a holder with no matching live lane and a stale/expired lease is
  a candidate for `steal --reason` in §3, not a silent land on top of it.
- Claim a narrow scope (which packages/files you will touch) in the worklog if one is active.
  Do not parallel-edit the same `internal/` package as a live lane.
- Queued work discipline: **proposed ≠ held** — dispatch consumes proposed work. If you intend
  to execute something yourself, claim it at import time; otherwise another lane will pick it
  up and you get double execution.
- Read the current WorkEnvelope before source-controlled landing. A current envelope owns the
  canonical target/ref/base/publication contract; do not reconstruct it from the executor's cwd.
  If a nonterminal legacy envelope lacks `git_delivery`, stop the landing and use the
  operator-provenance same-state `work advance` binding command in `compound-execute` §0. Never
  replace the envelope or choose `--local-only` merely to make close pass.
- **Stalled-WIP exception** (lived 2026-07-10, volatile land): main-tree WIP idle ≥15 min (check
  mtimes; use a bounded watcher, not blind sleep) does NOT block a land when your merge is pure
  ff AND `git diff --name-only <base>..<tip>` ∩ their dirty files = ∅. Then: back up their diff
  (`git diff > /tmp/<lane>-wip-backup.patch`), land the guarded ff from MAIN as its own command,
  verify their WIP byte-identical after (diff checksum before/after), and run the post-land
  battery AND `./install.sh` from a clean detached worktree at the landed SHA — never from the
  mixed tree (a binary built there would embed their half-built WIP).

## 1 · Pre-flight battery (all of it, every time)

Run the `compound-verify` skill. Minimum bar before any commit to main:

```bash
go run ./cmd/compound proof-check --json     # tests, vet, build, diff-check, console smokes,
                                             # doctor --strict, workbench, memory doctor, redact
```

- Exit contract: `0` ok · `1` a gate FAILED · `2` usage. Never "fix" a red gate by weakening
  the gate, deleting the test, or raising a budget — that is the one unforgivable move here.
- **Remove executor build caches (`.gocache/`, `.cache/`) from the worktree before the battery** —
  the redact gate scans untracked files; build-cache blobs carry 16-digit runs that false-positive
  the credit-card detector (observed 2026-07-10, reconciler land: MEDIUM pii.cc on `.gocache`).
- **Never pipe the battery through `| tail`/`| head`** (near-miss 2026-07-07). zsh has no `pipefail`,
  so the pipeline's exit status is `tail`'s (always `0`) — a real gate failure reads as green. The
  truncation also drops the top-of-output verdict and the `go-test`/`go-vet`/`go-build` gates. Run it
  unpiped: `go run ./cmd/compound proof-check --json > out.json 2>&1; echo "EXIT=$?"` and read the file.
- **Background-battery variant of the same trap (real incident 2026-07-15):** a backgrounded battery
  command's completion notification reports the WRAPPER's exit (the trailing `echo` — always `0`), not
  proof-check's. A red battery landed because the lander trusted "completed (exit code 0)" and never
  opened the output file (sole red gate was cosmetic — a comment tripping the private-runtime string
  guard — repaired on main as `5fa4c784`). Rule: a background battery is not green until you have READ
  the `EXIT=` line (and on failure, the failed gate's `output_tail`) from the file itself. Acquiring
  the land-lease before reading that line is the tell you're about to repeat this.
- **Worktree caveat (real failure):** fresh worktrees do not contain the gitignored
  `docs_private/leak-tokens.txt`. The doctor's leak gate **fails closed** under `--strict` when
  the token list is missing. Copy the file into the worktree before running the battery there.
- **Fresh-worktree battery needs FOUR gitignored inputs, and never via symlink** (observed
  2026-07-07, ESM land; fourth found 2026-07-09, home-backup land): `docs_private/plan/` (arch
  doctor's adaptation-steering test red without it), `console/node_modules` (console gates),
  `docs_private/profiles/` (the `profiles-check` gate compares generated agents against the
  operator overlay — red with an empty detail string without it), plus the leak tokens above.
  Copy with APFS clones (`cp -Rc`), NOT `ln -s` — a `node_modules/` ignore pattern matches
  directories only, so a symlink shows up as an untracked file and the redact gate EISDIRs
  trying to read it.
- **The Codex sandbox cannot run `git worktree add` against the main checkout** (observed
  2026-07-09: `.git` refs are read-only to it — `cannot lock ref`). Either the ORCHESTRATOR
  pre-creates the worktree before dispatching, or the executor falls back to a local clone
  (fetch+ff path below). State which in the dispatch prompt; a blocked executor that stops and
  reports (rather than improvising) is behaving correctly.
- Landing an executor's output: workers finish with **verified uncommitted trees** (they cannot
  write `.git/worktrees/<n>/index.lock`). You, the lander, verify EVERYTHING yourself —
  build/tests/doctor/redact/temp-home e2e — then commit. Never commit a worker tree unverified.
- **Before declaring a background executor's artifacts "lost," ASK THE RUNTIME where they are.**
  A job can leave *stray edits to existing files* in the MAIN tree while its real work looks absent —
  this reads like a lost-writeback panic. It usually is NOT: the commits are in the job's own
  worktree/clone, and the executor runtime knows the path. Ask the runtime for the job's status,
  read the reported worktree path → `git -C <that-path> log --oneline` →
  `git -C <that-path> diff --stat <main>..<tip>`. A real case: a job's two commits were intact in
  its own clone the whole time and landed clean the next session — the earlier "sandbox lost the
  artifacts" diagnosis was WRONG. Only after the runtime-reported path is confirmed empty/absent is
  it a real lost-writeback. To dispatch defensively, still require the worker to COMMIT + REPORT the
  SHA; the point here is verification order — runtime-status FIRST, revert-and-re-dispatch LAST.
- **Codex-runtime worktrees are separate CLONES, not linked worktrees** (observed 2026-07-06,
  W1-J4). Tells: the dir is absent from `git worktree list --porcelain` in MAIN, and
  `git cat-file -t <its-sha>` fails from MAIN (the commit lives in a different object DB). You
  cannot `git merge <its-branch>` — the ref is invisible. Land it with
  `git fetch <worktree-path> <branch>` then `git merge --ff-only FETCH_HEAD`. Opus/Agent-native
  `isolation:"worktree"` trees ARE linked (normal `merge --ff-only <branch>` works).
- **A concurrent lane moved main → rebase, don't merge.** The tell: `git diff --stat main..HEAD`
  in the executor tree shows the OTHER lane's already-landed files as **deletions** (you branched
  before them). `git merge` would revert their work. Rebase the executor branch onto the fresh
  `main` SHA, re-run the battery on the combined state, THEN `--ff-only` (observed 2026-07-06:
  the kill-switch branch would have reverted the bakeoff that landed 40 min earlier).
- **ff refuses "diverging" despite a CLEAN guard → check `git branch --show-current`** (observed
  2026-07-15, next_consumer land): a non-ceremony lane can `git checkout` ITS branch in the shared
  MAIN worktree, so `git merge` compares against THEIR HEAD, not main — and land-check later fails
  `wrong_target_worktree`. Do NOT switch their checkout while they may be live. Escape hatch: verify
  NO worktree has main checked out (`git worktree list --porcelain | grep -B2 'branch refs/heads/main$'`),
  then land with an atomic CAS that never touches any checkout:
  `git update-ref refs/heads/main <verified-descendant-tip> <fresh-main-sha>` — the old-value arg IS
  the guard (fails if main moved), and merge-base == main must be verified first (pure ff only).
  Restore the main checkout to main only after proving the hijacking lane idle (no live process,
  clean tree, reflog/HEAD-mtime stale) — its branch keeps its commits; log the restore in the worklog.

## 2 · Commit shape

- Conventional, scoped, single-concern: `feat(source): …`, `fix(console): …`,
  `test(authority): …`. Look at `git log --oneline -20` and match it.
- **Same-change doctrine:** the docs/contract/instruction updates a change implies land in the
  SAME commit as the code (status.md notes, contract guards, AGENTS.md rules). A code change
  whose doc lands "later" fails the arch doctor's contract guards now or rots.
- Never `git add -A` / "commit all". Stage explicitly; check `git status --short` for strays
  (`.DS_Store`, scratch files, `home/` runtime state) before staging.
- `git commit <pathspec>` commits that file's **working tree**, not what you staged — on a shared
  file carrying another lane's uncommitted hunk it silently sweeps their WIP into your commit
  (live case 2026-07-19: packets README). On any shared-checkout file, stage YOUR hunk with
  `git add -p`, verify with `git diff --cached`, then commit with NO pathspec.
- Generated runtime state never enters the repo: anything under `COMPOUND_HOME`, `home/`,
  `bin/`, root `./compound` binary is untracked by design — if `git status` shows it, stop.

## 3 · Rehearse, then land fast-forward-only

For anything beyond a trivial single commit on a quiet main:

```bash
git worktree add --detach /tmp/land-rehearsal <your-branch>
# merge/rebase against a fresh fetch of main there; run the battery in the rehearsal tree
```

Acquire the land-lease before touching the real main — it serializes the landing window itself
(complementary to, not a replacement for, the `MAIN_SHA` guard below, which is the real
correctness check):

```bash
compound land-lease acquire --holder "<harness>:<session>:<work_id>" --wait --timeout 120
```

Use a structured holder string — `<harness>:<session>:<work_id>` (e.g.
`claude:fable-2026-07-14:work_af4c4bb47e0`), never a bare `"codex"`/`"claude"` — a bare name can't
be traced back to a lane or reasoned about later if it goes stale. A LIVE competing holder makes
`acquire` refuse outright (non-zero exit, current holder printed on stderr) or, with `--wait`, poll
until the lease frees or `--timeout` elapses — either way you are blocked from landing until it
clears; that block is the point, not a bug to route around. If the printed holder is provably
stale (its lane is dead/gone), the escape hatch is
`compound land-lease steal --holder "<harness>:<session>:<work_id>" --reason "<why>"` — a reason is
required and the steal is auditable (`prior_holder`/`steal_reason` show up in the next `status`).

Then land on the real main:

```bash
MAIN_SHA=$(git -C <MAIN-worktree> rev-parse main)   # read from the MAIN worktree, not rehearsal
git merge --ff-only <your-branch>                    # or push with the SHA guard
```

- The guard is the point: if main moved since `MAIN_SHA`, **stop**, rebase, re-run the battery,
  retry. The guard has caught concurrent landings twice; treat a guard trip as normal, not an
  error to force past.
- **Never put retirement in the same command as the guarded merge**
  (near-miss 2026-07-09, Packet F): the guard tripped (main had moved a THIRD time that day), the
  `&&`-chain correctly skipped the merge — but a `;`-separated tail in the same command went on to
  battery/install/delete the UNMERGED branch and worktree. Recovery worked only because the tip SHA
  was in the transcript (`git branch recovery-x <sha>`; Agent worktrees share the object DB, so the
  commits survive worktree removal until gc). Rule: land = its own command; retirement is a
  separately reviewed Compound transaction issued only after `git log` shows the candidate tip is
  contained or patch-equivalent on both local and published authority.
- No merge commits onto main; `--ff-only` always.
- **Racing a fast-landing concurrent lane is how the cwd trap recurs** (observed 2026-07-09 late,
  lane K2 — it bit AGAIN with the rule below already written): after repeated guard trips the
  instinct is to chain rebase-in-worktree + guarded merge into ONE command to "shrink the window" —
  that chain is exactly what puts the merge inside the worktree. The window does not need
  shrinking: rebase/verify in the worktree first, then guard+merge as its own command from MAIN.
  If main moves in between, the guard trips and you loop — the loop is the design.
  When the loop provably cannot converge — a concurrent lane lands FASTER than your battery cycle
  (lived 2026-07-17, rebaseline land: two trips in a row, ~15-min lands vs ~13-min batteries) —
  the sanctioned counter is to acquire the land-lease BEFORE the rebase+battery and hold it
  through the merge, refreshing its TTL from a background loop (re-acquire with the SAME holder
  every ~4 min). Lease-respecting lanes queue instead of racing. STOP the refresher (TaskStop)
  before releasing, or a late re-acquire silently re-holds a freed lease; and hold-across-battery
  is for convergence failure only, never the default (it serializes everyone behind you).
- **Guard SHAs are READ, never reconstructed** (self-inflicted false trip 2026-07-09 late):
  compare against a full SHA captured from actual command output this turn (`git rev-parse main`).
  Expanding a short SHA from memory fabricates a guard failure — or worse, a false pass.
- **Parallel tool calls do NOT deterministically share cwd** (observed 2026-07-16, event-show
  land): a "main is clean" `git status` read came from the WRONG worktree while a parallel call
  rebased another branch — and a land-check raced that rebase. Every guard/status/land read goes
  through explicit `git -C <MAIN-path>`; never pair a git-state mutation with another git read as
  parallel calls in the same turn.
- **`pwd` before the guard+merge — shell cwd PERSISTS across commands** (near-miss 2026-07-07):
  after `cd <executor-worktree>` for a battery run, a later `git merge --ff-only <branch>` runs
  INSIDE that worktree ON the branch itself and prints "Already up to date" — a silent no-op that
  reads as landed while main never moved. Land from the MAIN worktree with an explicit
  `cd <MAIN> && pwd` in the same command, and verify with `git log` + tree-identity afterward.

## 4 · Post-land

- Release the land-lease with the SAME holder string used to acquire it, right after the guarded
  merge/push succeeds and before the rest of post-land duties, so the next lane's `status`/`acquire`
  sees it free:
  ```bash
  compound land-lease release --holder "<harness>:<session>:<work_id>"
  ```
  A non-holder release is refused (someone else's lease is never dropped by accident), and
  releasing with nothing held is an idempotent no-op — safe to run even if acquire never happened
  (e.g. a trivial single-commit land that skipped §3). `steal --reason` (§3) remains the escape
  hatch if a DIFFERENT lane needs to take over a lease this one abandoned without releasing.
- Re-run `go run ./cmd/compound proof-check` on landed main (cheap insurance against a
  rehearsal/main divergence).
- **If the land touched `cmd/compound` or `internal/{hook,harness,contract,mem,breadth}`: run
  `./install.sh`** — hooks and external consumers use the installed binary, which self-reports its
  build SHA since 9b5736b (`compound version`; arch doctor warns fresh/stale/missing). A stale
  installed binary silently serves yesterday's behavior to every session.
- For a source-controlled WorkEnvelope, run `compound work land-check <work_id> --tip
  <full-candidate-sha> --actor <lease-holder>` after local landing and after the normal push when
  publication is required. It certifies only the envelope-bound canonical worktree, local target
  ref, and remote-tracking publication ref; a feature worktree, second clone, or local-only main
  cannot supply close proof. The §3 read-not-reconstruct rule applies to the `--tip` SHA too:
  capture it from `git rev-parse` output THIS turn (`TIP=$(git rev-parse main)`), never expand a
  short SHA from memory — a fabricated tip reads as `invalid_lineage` "recover candidate tip"
  (caught live 2026-07-15: a hand-expanded tip tail sent land-check hunting a phantom commit;
  the correct read SHA certified `landed` immediately). Attach the SAME captured variable as the
  `commit:` artifact.
- Close the WorkEnvelope through the governed path (`work close-plan` → `work close`) with
  proof refs, so Receipt + Transition exist for the landed change.
- **Retire the landed worktree/ref only through Compound authority, never with hand-run
  `git worktree remove`, `git update-ref -d`, or `branch -D`.** A separate running cleanup
  WorkEnvelope owns the mutation; the landed source WorkEnvelope must already be terminal with a
  current `land-check`, and at least one content-addressed recovery proof must still resolve. Run
  `compound authority worktree-retire plan ...`, have the operator record the exact generated YAML
  with `compound authority worktree-retire review ... --decision authorize_retirement`, then run
  `compound authority worktree-retire apply ... --actor <cleanup-lease-holder>`. The adapter
  revalidates Git/work/lease/liveness/recovery state, persists its mutation phases, uses only
  non-force removal plus expected-tip CAS, and records Receipt + Transition identities on the
  cleanup work. If any proof is missing or stale, leave the worktree/ref intact; do not fall back to
  manual cleanup. Plan, review, and apply remain separate commands.
- Update `docs_private/plan/HANDOFF.md` (when present / operator lane) with what landed, its
  commit SHA, and any opened branch — the next session orients from it.
- Run the `extract-approach` skill if the change solved a non-trivial problem.

## 5 · Push floor

Normal `git push` of committed work to the existing remote is covered by the standing operator
grant. A clean `redact` scan is a separate required gate before a public push; add
`release export-public` sanity when the push is release-shaped. Force-push, history rewrite, or a
new remote still requires explicit confirmation.
- **Never pipe `redact` through `| tail`/`| head`, and never chain the push after it in the same
  command** (real incident 2026-07-10: `redact | tail -1 && git push` — the pipeline's exit was
  tail's `0`, a MEDIUM-flagged scan read as green, and the push fired; findings were benign
  loopback URLs, but the gate did not gate). Same zsh-no-pipefail trap as the battery (§1).
  Run redact unpiped as its own command, READ the verdict, then push as a separate command.

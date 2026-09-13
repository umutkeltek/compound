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
- **The holder string's trailing SHA names the ceremony — resolve it before calling a lease stale**
  (lived 2026-07-27, gate-isolation land). A renewing lease looks identical to the known refresher
  defect (`find_be9c4cc64732`): a 900s TTL whose `remaining_seconds` climbs back up, held for
  20+ min, with the holder's daemons alive but no landing in sight. The discriminator is in the
  holder itself — `<harness>:<session>:<work_id>:<sha>`: run `git log --oneline -1 <sha>` and
  `git rev-list --left-right --count main...<sha>` in the holder's worktree. A holder whose SHA is
  a real tip **ahead of main** is a live lane in its guard+merge window — queue on `--wait`, do NOT
  steal. A `proof:pfr_*:ratt_*:<work_id>:<actor>` holder is a LIVE certifying battery holding the
  resource for its gate run (§1) — queue for that too; if its run is provably dead, the takeover
  door is proof-check's own `--steal --terminated-run` admission path, not `land-lease steal`.
  Only a holder whose SHA resolves to nothing, or to a commit already contained in main, is the
  stale case. Matching a symptom to a filed defect is a hypothesis; the SHA is the evidence.
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

The ceremony has five gates, in force order: **scope-claim** (§0 — land-lease + worklog claim) ·
**battery** (§1 — `proof-check --json --base <full-sha> --work ... --actor ... --out`) ·
**fresh-SHA guard** (§3 — `MAIN_SHA` read this
turn) · **redact** (§5 — unpiped, its own command) · **tree-identity** (§4 — containment proved
before any removal). **A later gate cannot prove that an earlier gate passed: a green battery does
not prove the scope claim, and a clean redact does not prove the battery ran.** Each is cleared only
by reading ITS OWN verdict this turn — the `| tail` and `EXIT=$?` incidents below are one mistake
repeated, trusting an adjacent command's exit for a gate's own result.

During iteration, use `go run ./cmd/compound proof-check --dev --base main` for
cache-enabled short feedback. It is explicitly non-certifying: it does not write
the canonical installed-runtime proof artifact and never substitutes for either
the pre-land or post-land full proof below.

Run the `compound-verify` skill. Before any replay, battery, or land, capture the
current target tip once as `PRE_LAND_BASE_SHA`; the post-land battery reuses
that exact full SHA so its diff still covers the landed candidate. Minimum bar
before any commit to main:

```bash
PRE_LAND_BASE_SHA=$(git rev-parse --verify "main^{commit}")
go run ./cmd/compound proof-check --json --base "$PRE_LAND_BASE_SHA" \
  --work <work_id> --actor "<harness>:<lane>" \
  --out <absolute-report.json>   # tests, vet, build, diff-check, console smokes,
                                # doctor --strict, workbench, memory doctor, redact
```

- The actor must already hold the named WorkEnvelope's work lease. Full local proof is a
  `canonical_full` profile: Compound reserves the existing scarce land-lease resource and binds it
  to that `work_id`, exact `ratt_*`, repo/base, and gate digest before the first subprocess. A
  conflicting run is refused before execution. Custom `--ttl` values below 30s are refused before
  reservation; accepted values refresh at `min(TTL/3, 5s)` and monitor holder loss continuously.
  Each gate inherits the shared land-lease kernel execution barrier. Lease loss, SIGINT/SIGTERM, or
  a governed steal request cancels and waits the whole gate process group; generic landing acquire,
  expiry, and `run-attempt sweep` cannot bypass a live descendant. A first live-steal request may
  refuse while it asks the predecessor to stop; retry the exact command after that audited release.
  For a stale already-dead predecessor use
  `--steal --reason "<why>" --terminated-run <ratt_*>`; Compound's barrier probe produces the
  immutable Work-reachable termination evidence. Caller JSON and bare/reused PIDs are not proof.
  Certifying proof is agent-invocable: non-terminal, redirected, and backgrounded stdout are
  supported. `--out` is the authoritative full report. Every reserved gate must finish `ok`
  to certify; `skipped` is non-certifying. A complete canonical red battery (every gate
  `ok|failed`, at least
  one failed) finalizes as non-certifying evidence, releases the resource, preserves the full
  report, ends stdout with one `battery: FAILED`, and exits 1. Refusal leaves `--out` absent or
  byte-identical, and successful publication syncs a sibling temp before atomic rename. A committed
  green or red result whose lease removal remains pending exits non-green with an explicit recovery
  error; retry the exact command to release the retained holder before treating the battery as
  available to a successor. That retry resumes the prior owned report before reservation,
  atomically restores and byte-verifies `--out` from Compound custody while the lease remains held,
  and does not rerun the gates or mint a new `ratt_*`. Restore failure retains exclusivity and
  cannot print green. The
  first release-pending command preserves `--out` and ends stdout with
  `battery: FAILED release_pending=true`, never `battery: ok`. Any other error after a complete
  durable report also ends stdout with exactly one non-green battery verdict; pre-report
  usage/admission errors print none. Publish the installed-runtime Workbench artifact only after a
  green governed completion has released—never from candidate, failed, invalid, release-pending,
  dry-run, or CI output.
- **Exact-base differential proof is the narrow exception for complete red batteries, not a green
  rewrite.** Run the same canonical battery on the candidate and its exact base using a binary that
  emits `failure_facts`. When the base result HEAD equals the candidate result BASE, repository and
  gate inventories match, both failed-result finalizer chains are authentic, and every candidate
  failed-gate fact is already present on the base, attach the governed verification with the existing
  lifecycle command:
  `compound work advance <work_id> --to <current-state> --by <actor> --verification
  proof-diff:<candidate_pfrs_id>:<baseline_pfrs_id> --note "exact-base differential proof"`.
  `advance` validates both chains and the comparison before mutating the WorkEnvelope; `land-check`
  reloads and recomputes them again before accepting the verification.
  Missing facts, new facts, changed commands, dirty/stale source, malformed refs, and incomplete or
  forged result chains fail closed. Host-coupled facts are semantically normalized only for bounded
  classes proven by real batteries: a Go timeout dump is accepted only when every nonblank line
  matches the strict timeout/stack/package grammar and its facts retain each timed-out test's
  normalized wait state plus ordered function frames through the test. For ordinary Go failures,
  strict successful-package summaries are discarded and only strict test/package durations are
  removed; assertion text, panic text, diagnostics, build errors, and every unrecognized line remain
  exact. Bun cache-resolution lines retain the requested package identity. Within the strict timeout
  parser specifically, any unrecognized line falls the entire timeout output back to exact hashes. Old reports without
  `failure_facts` cannot certify this path.
  The red reports remain red; the differential reference says only that the candidate added no
  observed failure relative to its exact base. No scheduler or parallel authority is introduced.
- Exit contract: `0` ok · `1` a gate FAILED · `2` usage/admission/publication/finalization error.
- `--base` is a caller-captured immutable full SHA, not
  `git_delivery.expected_base_sha`. It resolves once. Certifying proof refuses dirty or
  uncommitted candidates and seals repository fingerprint, base SHA, HEAD SHA, tree SHA,
  and clean state. Diff-check and redact both inspect the committed `base...HEAD`. A
  rebase, replay, or new HEAD invalidates the proof and requires a fresh battery.
- Canonical gates execute in a detached temporary checkout of the sealed HEAD,
  with only the five ceremony-owned ignored inputs copied as snapshots. The
  caller worktree is revalidated but is never the gate execution surface.
- Never "fix" a red gate by weakening
  the gate, deleting the test, or raising a budget — that is the one unforgivable move here.
- **Remove executor build caches (`.gocache/`, `.cache/`) from the worktree before the battery** —
  the redact gate scans untracked files; build-cache blobs carry 16-digit runs that false-positive
  the credit-card detector (observed 2026-07-10, reconciler land: MEDIUM pii.cc on `.gocache`).
- **Never pipe the battery through `| tail`/`| head`** (near-miss 2026-07-07). zsh has no `pipefail`,
  so the pipeline's exit status is `tail`'s (always `0`) — a real gate failure reads as green. The
  truncation also drops the top-of-output verdict and the `go-test`/`go-vet`/`go-build` gates. Run
  it unpiped as its own command and read its exit/verdict. This is shell-observation safety, not
  an admission requirement: background or redirection is supported when the caller preserves
  proof-check's actual exit and reads `--out`.
- **`--out <file>` is the sanctioned invocation now; the `> out.json 2>&1; echo "EXIT=$?"` wrapper
  is RETIRED.** `proof-check` owns its own output contract: `--out FILE` writes the FULL report to
  FILE itself (a prepare/publication failure is its own exit-2, even if every gate passed), and prints one
  `battery: ok gates=N/M out=FILE` or `battery: FAILED gates=N/M first_failed=NAME out=FILE` line as
  the LAST stdout line — with a truthful process exit code (`0`/`1`/`2`) throughout. There is no
  wrapper shell exit left to lie. Foreground: read the verdict line (or just check `$?`). Backgrounded:
  the completion notification tail now carries proof-check's OWN last line, not a wrapper's `echo`'s —
  on a FAILED verdict still open FILE and read the named gate's `output_tail` before landing.
  History (compressed): the old `echo "EXIT=$?"` wrapper's masked exit lied green three times —
  2026-07-07 (piped through `tail`), 2026-07-15 and 2026-07-18 (backgrounded: the wrapper's trailing
  `echo` exit, always `0`, was trusted as the battery's exit) — which is why the battery, not a shell
  wrapper, now owns the output contract.
  New reports are private; ordinary existing modes are preserved. Symlink, non-regular,
  or raced targets are refused, publication is atomic within the captured parent, and
  timing warnings are sealed before report file, installed artifact, and JSON agree.
- **A pre-commit battery CANNOT clear the doc-leak gate for NEW files** (lived 2026-07-19, inventory
  door). The gate enumerates TRACKED text files only, so files still untracked (`??`) at battery
  time are invisible to it — the battery goes green, the commit makes them tracked, and the NEXT
  battery fails on a leak that is already landed and pushed. Landing new files therefore has an
  ordering hazard the standard battery does not cover. Before committing any NEW text file, run the
  leak gate against the staged-as-tracked state — `git add` the files FIRST, then run
  `go test ./internal/arch/ -run '^(TestDoctorGreenAgainstRealRepo|TestCheckCmdCompoundBudgetPassesOnRealRepo|TestDoctorEmitsCmdCompoundBudgetFindings)$' -count=1` (seconds, not the full battery) —
  or grep the new files against `docs_private/leak-tokens.txt` directly. Test FIXTURES are the
  usual offender: a realistic-looking fixture is exactly where a real private product name gets
  typed. Use synthetic names in fixtures always.
- When changing CLI source, run that same staged preflight even with no new files. The generic
  doctor regression is non-strict and tolerates size warnings; the dedicated CLI ratchet and
  strict doctor wiring tests catch them before banking and the full battery. Simplify or move responsibility to its
  existing owner; never raise the limit or remove checks to clear the ratchet.
- **The battery needs FIVE gitignored inputs** (observed 2026-07-07, ESM land; fourth found
  2026-07-09, home-backup land; fifth found 2026-07-28, sealed source-token proof):
  `docs_private/plan/` (arch doctor's adaptation-steering test reds without it),
  `console/node_modules` (console gates), `docs_private/profiles/` (the `profiles-check` gate
  compares generated agents against the operator overlay — it reds with an empty detail string
  without it), `docs_private/leak-tokens.txt`, and `docs_private/source-leak-tokens.txt` (the
  embedded baseline keeps the generic leak gate armed without them, but each operator overlay
  carries its own product/tool-specific scan). A linked worktree carries none of them; since
  2026-09-13 the battery takes any the caller's checkout lacks from the repository's main
  checkout, and the caller's own copy still wins. Before that, each missing input was silently
  skipped. Only a separate clone (the Codex-runtime shape below) still needs them copied in by
  hand: APFS clones (`cp -Rc`), NOT `ln -s` — a `node_modules/` ignore pattern matches
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

Acquire the land-lease AFTER the certifying battery completes green and before touching the real
main — it serializes the guard+merge window (complementary to, not a replacement for, the
`MAIN_SHA` guard below, which is the real correctness check). The battery itself must find the
lease FREE: certifying proof-check reserves the land-lease under its own `proof:` holder for the
whole gate run (§1), so there is no unserialized gap to cover before it — see the
poisoned-predecessor bullet below for what a pre-battery generic hold does:

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
  remember the battery already IS the hold: certifying proof-check reserves the land-lease for
  the entire gate run, so the exposed race window is only battery-green → guard+merge. Acquire
  the lease manually for THAT short window only; lease-respecting lanes queue instead of racing.
- **Never acquire the land-lease BEFORE a certifying battery — the poisoned-predecessor trap**
  (lived 2026-07-28, Jaynes docs land): admission renames a pre-held generic
  `<harness>:<session>:<work_id>` lease into its `proof:` reservation holder and records the
  generic holder as the reservation's predecessor — and the predecessor recovery walk parses only
  `proof:`-shaped holders, so admission exits 2, the DEAD reservation retains the lease for its
  full TTL, and every retry INCLUDING `--steal --terminated-run` refuses on the same parse.
  Recovery: release with the FULL `proof:pfr_*:ratt_*:...` holder string printed in the refusal
  (the holder's own release is the tool's sanctioned path), confirm the dead run self-terminalized
  (`compound run-attempt list --non-terminal` — proof-check terminalizes its own ratt on the
  admission error path), then rerun the plain exact command on the freed resource.
- **When holding the lease ALSO fails, land on package-disjointness — never on a CAS to your own tip**
  (lived 2026-07-27: FOUR guard trips, one of them while the lease was held, because the lease is
  advisory and that lane bypassed it). Measure before deciding: battery **~14 min** against main
  gaining **~1 commit per 14 min** — main moved between a `git status` read and the `git rebase` on
  the very next line. At that ratio the guard trips essentially every cycle and waiting cannot
  converge. The counter: **full battery on base B → if main moved to B', require the commits
  `B..B'` to touch NO package your change touches → replay your commit onto B' → `--ff-only`.**
  Compute it, do not eyeball it —
  `comm -12 <(git diff --name-only B..B' | xargs -n1 dirname | sort -u) <(your changed dirs | sort -u)`
  must be empty; on overlap the rule REFUSES and you re-battery. It fired once and cost a cycle: that
  is the rule working, not a formality to wave through. Confirm it is satisfiable for your packages
  first — 12 of 30 recent commits touched `cmd/compound`/`internal/capgraph` (~40% collide) → ~60%
  clean windows → ~1.7 expected cycles, i.e. bounded. **The trap: do NOT implement this as
  `git update-ref refs/heads/main <your-tip> <fresh-sha>`.** Your tip is based on the OLD main, so
  the CAS SUCCEEDS and silently DELETES the other lane's commit. Always rebase FORWARD onto B';
  never overwrite it. The `update-ref` escape hatch below is for a hijacked main CHECKOUT, a
  different problem — and it is doubly wrong here, because MAIN has `main` checked out, so moving
  the ref without the index leaves your landed files looking *reverted* in that worktree, and the
  next lane can commit the revert.
- **`index.lock: File exists` is not proof a lane is live** (lived 2026-07-27, same land). A crashed
  git leaves the lock behind and `merge --ff-only` then refuses forever. Discriminator: **0 bytes +
  mtime minutes old + `lsof <repo>/.git/index.lock` shows no holder + no `.git/MERGE_HEAD` /
  `rebase-merge` / `rebase-apply` / `CHERRY_PICK_HEAD`** = stale, which is the case git's own error
  text tells you to clear manually. Recovery is still a mutation on SHARED state: first resolve
  scoped operator approval or a standing grant covering this repository's stale-lock recovery,
  the owning WorkEnvelope, and a current allowing PolicyDecision. **Existing authority that
  covers the recovery fulfills approval — proceed without another prompt.** Missing coverage
  or a missing, unreadable, or holding policy means stop; this ceremony grants no new authority.
  Another lane may have STAGED work: back up the index, `git diff HEAD`, and untracked WIP bodies,
  and record their checksums first. Recheck liveness, size, mtime, path, and lock identity
  immediately before moving only the verified zero-byte stale lock into the governed backup;
  never delete it or treat this as generic cleanup authority. Live, fresh, nonempty, symlinked,
  or changed locks stay untouched. Re-verify the index and every preserved WIP body byte-for-byte
  afterward. `git diff` alone misses staged work; `git diff HEAD` does not include untracked
  bodies. `ps | grep git` is NOT the liveness check (it matches your own command text); `lsof` is.
- **A skill/doc under `.claude/` may be a SYMLINK into the tracked tree — check the target, not the link**
  (lived 2026-07-27). `.claude/` is gitignored wholesale (`.gitignore:12`), so
  `git ls-files .claude/skills/<s>/SKILL.md` reports untracked and `check-ignore` confirms it — but the
  path is a symlink to `skills/<s>/SKILL.md`, which IS tracked and IS public-repo-destined. Editing it
  needs the full ceremony, and the Edit tool refusing to "write through symlink" is the tell. Resolve
  with `readlink -f`, then `git ls-files --error-unmatch <resolved>` before concluding anything about
  tracking. Testing the link tells you about the link.
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
- Re-run on landed main against the same pre-land base (cheap insurance against
  rehearsal/main divergence):
  ```bash
  go run ./cmd/compound proof-check --json --base "$PRE_LAND_BASE_SHA" \
    --work <work_id> --actor "<harness>:<lane>" --out <absolute-report.json>
  ```
  Preserve its actual exit and read the authoritative `--out` report. Never
  recapture `main^{commit}` after landing: that makes `base...HEAD` empty. If
  replay or rebase changes the candidate, deliberately capture a new
  `PRE_LAND_BASE_SHA` before a renewed pre-land battery and rerun both batteries.
- **If the land touched `cmd/compound` or `internal/{hook,harness,contract,mem,breadth}`: run
  `./install.sh`** — hooks and external consumers use the installed binary, which self-reports its
  build SHA since 9b5736b (`compound version`; arch doctor warns fresh/stale/missing). A stale
  installed binary silently serves yesterday's behavior to every session.
- For a source-controlled WorkEnvelope, run `compound work land-check <work_id> --tip
  <full-candidate-sha> --actor <lease-holder>` after local landing and after the normal push when
  publication is required. It certifies only the envelope-bound repository, local target ref, and
  remote-tracking publication ref; a feature worktree, second clone, or local-only main cannot
  supply close proof. One narrow exception matches the §1 CAS escape hatch: when the canonical
  checkout remains on an active feature branch, **no worktree checks out the target ref**, and the
  target plus required publication already contain the certified candidate, land-check certifies
  from those immutable refs without inspecting or interrupting the foreign checkout. If any
  worktree checks out the target ref, the ordinary canonical-worktree rule still applies. The §3
  read-not-reconstruct rule applies to the `--tip` SHA too:
  capture it from `git rev-parse` output THIS turn (`TIP=$(git rev-parse main)`), never expand a
  short SHA from memory — a fabricated tip reads as `invalid_lineage` "recover candidate tip"
  (caught live 2026-07-15: a hand-expanded tip tail sent land-check hunting a phantom commit;
  the correct read SHA certified `landed` immediately). Attach the SAME captured variable as the
  `commit:` artifact.
- The `land-check` candidate tip must equal both the stored successful proof HEAD and the
  current attached commit candidate. A rebase, replay, or new candidate tip requires a fresh
  battery first; a mismatched tip is non-certifying and close refuses it.
- Close the WorkEnvelope through the governed path (`work close-plan` → `work close`) with
  proof refs, so Receipt + Transition exist for the landed change.
- After `work close`, run `compound act post --from-work <work_id> --transport buzz --confirm`; it holds silently unless the operator's standing grant is imported.
- **Retire YOUR OWN scratch/rehearsal worktrees at close — do not defer them.** This ceremony's
  rehearsal tree, probe trees, and any detached scratch tree this lane created are yours to remove
  in post-land with the self-guarding pair: verify containment
  (`git merge-base --is-ancestor <tip> origin/main`), then non-force `git worktree remove <path>`
  (refuses a dirty tree) and `git branch -d <branch>` (refuses an unmerged branch). Deferring this
  is how 44 scratch trees accumulated by 2026-07-21 (cleared only by an operator-authorized batch
  sweep, `find_4b487836d8b2`): per-land self-cleanup is the rule, batch sweeps are the exception.
- **Retire the landed worktree/ref only through Compound authority, never with hand-run
  `git worktree remove`, `git update-ref -d`, or `branch -D`.** A separate running cleanup
  WorkEnvelope owns the mutation; the landed source WorkEnvelope must already be proof-complete
  `completed` or `failed`, its Receipt outcome must exactly match that terminal state, and it must
  have a current `land-check` plus at least one content-addressed recovery proof that still resolves.
  A failed source remains failed; cleanup proves only that its workspace surface is redundant.
  Cancelled, nonterminal, and state/outcome-mismatched source work cannot authorize retirement. Run
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

A rebase, replay, or new HEAD invalidates prior proof and requires a fresh battery.
For source-controlled work, a committed stored successful proof is required:
`land-check` candidate tip, current attached commit candidate, and stored proof HEAD
must match; close refuses a missing, mismatched, or rebased proof.

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

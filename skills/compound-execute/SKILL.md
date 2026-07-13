---
name: compound-execute
description: The full take-a-job loop for the compound repo — resolve a work item or packet, claim it, dispatch the right executor lane, land with ceremony, close with proofs, and route learnings back into the system. Use when the operator says "take job X", "execute this packet", "continue the queue", or hands over any work item to run end-to-end.
---

# compound-execute — from "take that job" to closed Receipt, one ceremony

Proven shape: 2026-07-09, six packets landed in one day through exactly this loop (HANDOFF
LATEST-22..24). This skill replaces re-deriving that round pattern per session. It COMPOSES with
`compound-land` (step 6 defers to it entirely — do not restate landing rules here).

## 0 · Resolve the work (what did the operator actually hand you?)

- A work id (`work_…`) → `compound work show <id>` — read predicate, state, owner, packet content
  (`work active-packet-proof` serves packet markdown to any reader).
- A packet path (`docs_private/plan/packets/*.md`) → `compound work import-packet <path> --owner <lane>
  --state proposed` if no envelope exists yet; note the returned work id.
- A description ("the parity thing") → `compound workbench` + `compound attention --json` to find
  the matching item; confirm the match by predicate text in one line to the operator ONLY if two
  items genuinely collide — otherwise pick and proceed.
- NO free-floating execution: if the task has no envelope, create one FIRST
  (`compound work create --owner <surface> --predicate "<falsifiable done-state>" --by
  orchestrator --state running`). The K1 incident rule: unenveloped work is invisible to
  liveness probes.
- **Footgun (2026-07-09):** `compound work dispatch-claim` with NO ARGS claims the next ready item
  for real. Never run mutation verbs argless to probe usage — read `compound work` verb list or the
  dispatch-plan JSON instead.

## 1 · Claim, narrowly

- `compound work claim <id> --by <actor>` (lease) — proposed ≠ held; unclaimed work gets
  double-executed by a concurrent lane.
- Append a SCOPE CLAIM to `docs_private/plan/CODEX-WORKLOG.md`: exact packages/files, executor
  lane, expected base SHA, what you will NOT touch. Check the tail for live claims first — three
  lanes landed ~35 commits on one day (2026-07-09) with zero conflicts BECAUSE of this.

## 2 · Author or refine the packet (the executor's whole world)

A packet must carry: objective (one sentence) · verified seams with file:line (scout them — two
Sonnet scouts in parallel is the proven shape; batch fan-outs ≤3) · settled design decisions ·
falsifier test list (red-without/green-with) · hard file boundaries (touch ONLY / do NOT touch,
naming concurrent lanes' surfaces) · privacy law (repo is destined public; private ids only under
`$COMPOUND_HOME`) · verification the executor runs · handoff report format.
If the packet creates a NEW `internal/` package: include its `arch.Classification` entry
(spine/object-family/leaf/mem-target/importer) in scope — the arch doctor fails the battery on
unclassified packages (learned 2026-07-09, corpus-indexer land; lander patched it as a follow-up
commit). A new ORGAN needs TWO gates, not one: the classification map AND a two-class `MintHome`
entry in `internal/arch/mint.go` (`organ:<name>`, purpose + named consumer) — classification alone
leaves the doctor red with `new organ has no two-class home` (learned 2026-07-13, narrate land;
executor closed it additively).
Packet citations are evidence, not canon: before building on a cited field/symbol ("X already
exists"), grep the struct/file it names. A premise error becomes a documented deviation + an
additive fix, not a blocker (learned 2026-07-10, volatile land: packet cited `Claim.ExpiresAt`
as existing; the field lived on Candidate only — added additively, canon_class precedent).
A falsifier can be GREEN-AT-BIRTH (existing structure already satisfies it — e.g. a visibility
whitelist already excludes the new value). Don't fake red and don't skip it: land the guard test
anyway and prove its teeth by a scratch mutation (open the gate, show red, revert), then report
green-at-birth honestly (same land).
Packets live in the dir named by `compound workbench --json` → `offered_packets.source_dir` (currently `docs_private/plan/packets/`; gitignored — executors read them by ABSOLUTE path; fresh
worktrees don't contain docs_private).

## 3 · Route the lane (token ruling 2026-07-08, refined 07-09)

- Opus: the hardest packet of the round, and design-related work (operator ruling 2026-07-09).
- Sonnet: mirror-pattern / well-specified mechanical-plus-judgment packets.
- Codex: mechanical batch work (separate quota pool); wrap in an envelope; companion-status before
  declaring artifacts lost.
- Scouts (read-only recon) are a cheap lane. Never the orchestrator model itself as a subagent
  executor. Always pass `model:` explicitly.

## 4 · Dispatch (worktree discipline)

In the dispatch prompt, always include: the packet ABSOLUTE path · the envelope id · "read
AGENTS.md first" · **the expected base SHA + ff-forward instruction** (Agent worktrees cut from the
spawn-time checked-out ref, observed 15-21 commits stale, 4/4 executors on 2026-07-09) · commit
incrementally, no push, no main, no install · the handoff report format. Executors NEVER touch the
real `$COMPOUND_HOME` (test homes only) unless the packet names ONE specific evidence-file write.

## 5 · Watch (a "running" status is a claim, not evidence)

Background executors notify on completion. If one goes silent: verify pid + log recency before
believing any status file (K1 died silently for 2h). Codex jobs: ask the companion where the
worktree is BEFORE re-dispatching.
**After ANY handoff, verify remote state yourself** (`git fetch` + `git log origin/main`): a
full-tool executor CAN push despite a NO-push fence, and task-notification re-stops give it extra
turns after its report (observed 2026-07-13, narrate land — executor pushed origin in a
post-handoff continuation; redact happened to be clean). Fences are prompts, not walls; the
orchestrator owns the remote.
**Re-read the ENVELOPE state (`work show <id>`) right before landing, not just at claim** (lived
2026-07-13, sycophancy land): the operator can retract mid-flight — steal the lease and advance
the envelope to `held` in a parallel surface while the executor runs. The executor doesn't watch
the ledger and will hand off clean anyway. A land against a held/retracted envelope gets reverted;
the check is one command. If held: do NOT land, do NOT close — bank the branch, log, surface.

## 6 · Land — invoke the `compound-land` skill. All of it, every time.

Non-negotiable additions learned 2026-07-09: land and cleanup are SEPARATE commands; if the guard
trips, main moved — rebase, re-battery, retry; tree-identity before worktree removal.

## 7 · Close with proofs (no proof refs, no close)

`compound work close-plan <id> …` then `compound work close <id> --outcome … --move-type … 
--proof-ref commit:… --proof-ref check:<falsifier> --proof-ref live:<real-flow observation>`.
`rule_delta` REQUIRES a failure/rule/check/guard proof ref. A live proof (the anti-"landed ≠
working" observation — real home, real flow) belongs on every close whose change has a runtime
surface.

## 8 · Route the learnings (the operator's law: "change the system based on the information")

A solution without its learnings routed is unfinished work. In the SAME round, write each learning
to the owner that will consume it:
- operational lesson → `CODEX-WORKLOG.md` RESULT entry (always) + `compound-land`/this skill if it
  changes the ceremony;
- program state → `docs_private/plan/HANDOFF.md` LATEST-N entry (commits, proofs, gates, queue);
- durable cross-session fact → auto-memory file + its MEMORY.md index line (keep both current);
- stale handoff/prompt lines elsewhere (NEXT-SESSION-PROMPT etc.) → fix them NOW, cite the
  correction;
- a repeated flaw with a code-shaped fix → file it as a named follow-up packet candidate in the
  worklog, never a silent TODO.
Then surface the OPERATOR GATES (ranked, with the exact one-line command where one exists) as the
final message — gates are decisions, not homework; everything else should already be done.

## Boundaries

- DEC-0021 stands: this skill smooths YOUR ceremony; it never auto-runs queued work. The scheduler
  rung (launchd `dispatch-step` under kill-switch) is W9's trust-dial decision — operator-gated.
- Push gate, whitelist installs, destructive ops: present-and-stop, always.

## Codex dispatch contract (learned 2026-07-11, burn-wave v1 — 3 dispatch-side failures)
- **Never let Codex clone into /private/tmp.** The sandbox edit-guard rejects patches there ("writing outside of the project"), Bun is denied its temp dir, and fresh clones lack node_modules → jobs return BROKEN or zero-work. **The ORCHESTRATOR pre-creates a linked worktree under the repo's `.claude/worktrees/` (inside $HOME, where approval_policy + xhigh reasoning apply), pre-copies `console/node_modules` + `docs_private` (cp -Rc), and dispatches Codex INTO it** ("work in place, commit on this branch, report SHAs").
- **Scope fences must be checked against the packet's own falsifiers before dispatch.** A fence that forbids a surface the packet load-bearingly needs produces a correct STOP (good agent behavior, wasted quota). Read the packet falsifiers, then draw fences.
- **A Codex self-verdict of BROKEN/should-not-be-landed is salvage input, not failure:** fetch the partial commit from the clone (`git fetch <clone-path> HEAD` + cherry-pick), hand the successor the salvage + the remaining delta.
- Require in every prompt: commit + report SHA + per-item red→green proof; ask the companion for artifact locations before declaring anything lost.

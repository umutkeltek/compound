---
name: compound-verify
description: Prove a compound change is ALIVE (wired, fed, cut over, provable) — not merely merged. Use before closing any work item, before claiming "done"/"landed", when landing an executor's diff, or when auditing whether a "landed" marker in HANDOFF/status/VISION actually works. The anti-"landed ≠ working" skill.
---

# compound-verify — consumption-grade verification

The 2026-07-06 solid-ground audit found the repo's dominant failure: **"landed" markers track
code commits, not live consumption.** Machinery merges green while the wiring, data, cutover, or
proof never happens — and the system silently fails its own north-star test. This skill is the
checklist that catches that *before* close, instead of in a 12-agent audit months later.

It specializes the generic `verification-before-completion` / `finishing-a-development-branch`
skills for this repo; it replaces re-deriving the verification ritual from
`docs_private/plan/HANDOFF.md` prose each session.

## The four gaps (every "landed ≠ working" case is one of these)

Verified instances from the audit — memorize the pattern, not the instance:

| Gap | Question | Real failure it caught |
|---|---|---|
| **WIRING** | Is the new code reachable from the canonical surface, or does the old path still run? | owned breadth engine built (`internal/breadth` + `cmd/compound-breadth-engine`) but recall still shelled to the external legacy CLI |
| **DATA** | Does real (or synthetic-fixture) data actually flow through it? | consume-receipts machinery landed but starved — 0 live skill edges ever recorded |
| **CUTOVER** | Did the default/consumer flip to the new path, and is the old path retired or tombstoned? | live hook adapter landed; sessions still ran the slow `go run` path — cutover never flipped |
| **PROOF** | Does a command or test now FAIL if this breaks? | recall "works" but no golden set / bakeoff harness existed — unprovable either way |

A change is **alive** only when all four hold. Anything else is **landed-not-live** — say which
gap, loudly, in the receipt/close notes.

### The vacuous pass — a PROOF gap wearing a green check

A gate that cannot fail is not a weaker gate, it is **not a gate**, and it is far harder to notice
than a missing one because the suite is green. Two shapes, both lived 2026-07-27 (four gates at
once, `fc4cd8e3`):

- **Empty is not failing.** `mem.Report` only flips `OK` on a `"fail"` hop, so on a home with no
  claims every hop reported `"empty"` and `memory doctor` exited 0 having asserted nothing.
  `workbench --json` returned every count `0` with no error, and its gate read only the exit code.
  Both console consistency smokes explicitly tolerate an empty projection. **Ask of any gate: what
  does it do when its subject is absent rather than broken?**
- **Isolation can CREATE the vacuum.** Removing a check's dependence on ambient state (a live home,
  an installed binary, the clock) is usually right — it was worth ~109s/battery here — but the same
  move can turn a slow real check into a fast fake one. A bare temp home made all four gates
  *faster and more wrong* than the live home they replaced.

**So when you cut a check off from ambient state, seed a fixture and assert its content, then prove
the assertion is load-bearing:** neuter the assertion and confirm the unseeded-input control goes
green. If a control cannot be made to fail, it is documentation, not proof.

Two corollaries worth their own line:

- **A function's tests do not test its wiring.** Per-gate tests that call the runner directly all
  stay green when the one dispatch line is deleted — and the gate silently reverts to ambient state.
  Assert the dispatch too (`proofCheckHomeOwningGates` + an absent `COMPOUND_HOME`).
- **Some failure modes are unreachable through the public API, so a control must bypass the write
  door — deliberately and with a comment saying why.** An unparsable ledger row and a
  zero-provenance claim cannot be produced by `Adjudicate`; an unreadable work envelope cannot be
  produced by `Create`. A control that respected the door could not prove those gates can go red.

## The battery (run in order; each step is checkable)

1. **Never trust the checked-in binary.** `./compound` at repo root is a local build artifact and
   is routinely stale. Verify through `go run ./cmd/compound <verb>` or a fresh `make install`.

   For iteration only, `go run ./cmd/compound proof-check --dev --base main` provides
   cache-enabled short feedback. It is non-certifying, does not write the canonical
   installed-runtime proof artifact, and never satisfies the deterministic bundle below.

2. **Deterministic bundle:**
   ```bash
   PRE_LAND_BASE_SHA=$(git rev-parse --verify "main^{commit}")
   go run ./cmd/compound proof-check --base "$PRE_LAND_BASE_SHA" \
     --work <work_id> --actor "<harness>:<lane>" \
     --out <absolute-report.json>
   ```
   The actor must already hold the named WorkEnvelope lease. Certifying proof is agent-invocable:
   non-terminal, redirected, and backgrounded stdout are supported; the authoritative full report
   is `--out`. Run it unpiped as its own command by default and preserve the actual process exit
   because zsh pipelines can mask failure. This is shell-observation safety, not proof admission.
   Custom `--ttl` values below 30s are refused before
   admission; accepted windows refresh at `min(TTL/3, 5s)` and monitor holder loss continuously.
   Each gate inherits the shared land-lease kernel execution barrier. Lease loss, SIGINT/SIGTERM,
   and governed steal requests cancel and wait the whole gate process group; generic landing
   acquire, expiry, and `run-attempt sweep` cannot bypass a live descendant. A first live-steal
   request may refuse while it asks the predecessor to stop; retry the exact command after release.
   A stale already-dead predecessor uses
   `--steal --reason "<why>" --terminated-run <ratt_*>`; Compound's barrier probe produces the
   immutable termination evidence, so caller JSON and bare/reused PIDs are not proof. `--out` receives the full report and
   stdout ends with the truthful battery verdict. Dry-run/CI subsets are lightweight diagnostics and cannot mint
   governed verification. `canonical_full` requires every reserved gate to be `ok`—zero skips—to
   certify. A complete canonical red battery requires every gate `ok|failed` with at least one
   failure; it finalizes non-certifying evidence, releases the resource, preserves the full report,
   prints one `battery: FAILED` verdict, exits 1, and creates no VerificationID. Refusal does not
   create or alter `--out`; successful publication is an atomic synced rename.
   Installed-runtime publication is likewise atomic and completion-marked; a partial prefix is
   ignored, and a post-release publication fault emits a final non-green verdict with exit 2.
   Any error after a complete durable report prints exactly one final non-green battery verdict;
   pre-report errors print none. A committed green or red result with a retained lease is not a green command: it returns an explicit
   release-pending error. The exact retry resumes the prior owned report before reservation and
   atomically restores and byte-verifies `--out` from Compound custody before it releases, without
   rerunning gates or minting a new RunAttempt. Restore failure retains exclusivity and cannot
   print green. The first command preserves `--out`, ends stdout with
   `battery: FAILED release_pending=true`, and exits nonzero. The installed-runtime Workbench
   artifact is published only after green governed completion and release; candidate,
   release-pending, failed, invalid, dry-run, and CI reports cannot consume that action.
   Green means: `go run ./scripts/testbattery` (every listed package once with `-count=1 -timeout=40m`, all home tests isolated after the rest), `go vet ./...`, `go build ./...`, `git diff --check`,
   `architecture doctor --strict`, workbench projection, `memory doctor`, and the public redact
   scan of changed files all passed. Any red stops the close — fix or escalate, never weaken the
   check.

   The certifying base is a caller-captured immutable full SHA, not
   `git_delivery.expected_base_sha`; Compound resolves it once. A dirty or uncommitted candidate
   is refused. The report seals repository fingerprint, base SHA, HEAD SHA, tree SHA, and clean
   state; diff-check and redact inspect the same committed `base...HEAD`. A rebase, replay, or
   new HEAD invalidates the prior proof and requires a fresh battery. New `--out` reports are
   private, ordinary existing modes are preserved, symlink/non-regular/raced targets are refused,
   publication is atomic within the captured parent, and timing warnings are sealed before the
   file, installed artifact, and JSON agree.

   Canonical gates execute from a detached temporary checkout of the sealed HEAD,
   with only the five ceremony-owned ignored inputs copied as snapshots; the
   mutable caller worktree is never the gate execution surface.

   Since `fc4cd8e3` the home-reading gates (`memory doctor`, workbench projection, the attention
   and agents console smokes) each seed and own a temp `COMPOUND_HOME` and assert its content, so
   their verdict is about the diff rather than about your dev home's size — and they no longer get
   slower every time you use Compound. They are listed in `proofCheckHomeOwningGates`; a gate that
   leaves that map silently goes back to reading ambient state.

3. **Temp-home end-to-end** (proves the slice works outside your polluted dev home):
   ```bash
   export COMPOUND_HOME="$(mktemp -d)"
   go run ./cmd/compound home init --local   # then walk the slice's real command path
   ```
   Use synthetic fixtures only. The slice must produce its envelopes/artifacts under the temp
   home; `home doctor` must stay clean; repo tree must stay unchanged (`git status --short`).
   Unset `COMPOUND_HOME` afterwards.

4. **Consumption check — name the live consumer, then watch it consume.**
   Ask: *who reads this output next?* (`orient`? `workbench`? a hook injection? a console card?
   the distiller?) Run that consumer's command and point at the new behavior **in its output**.
   Grep-in-source is not consumption; JSON in the consumer's output is. If no consumer exists
   yet, the move is `audit_only` — it does not count as done; record the missing consumer as the
   open branch.

5. **Cutover check.** Name the old path this change supersedes. Show it is retired, tombstoned,
   or explicitly deferred with a recorded retirement condition. "Both paths live, new one
   optional" is the fragmentation pattern this repo exists to kill.

6. **Depth test.** Point to the specific test that fails if the invariant breaks. If you cannot
   name one, write it — and prove it by breaking the code once (mutate, watch red, revert). A
   test that cannot fail is documentation, not proof.

7. **Work-chain proof** (when the change closes a WorkEnvelope): the same `work_id` must link
   context pack → action → policy → receipt → transition. Check with
   `go run ./cmd/compound workbench --json` / `transition show <id>`. A `RouteDecision`, a plan
   artifact, or a written report is never task success — only the receipt's `state_delta` is.

   For source-controlled work, the `land-check` candidate tip, current attached commit
   candidate, and stored successful proof HEAD must be identical. A rebased or mismatched tip
   is non-certifying and close refuses it until a fresh battery records the new HEAD.

A rebase, replay, or new HEAD invalidates prior proof and requires a fresh battery.
For source-controlled work, a committed stored successful proof is required:
`land-check` candidate tip, current attached commit candidate, and stored proof HEAD
must match; close refuses a missing, mismatched, or rebased proof.

## Verdict format (end every run with exactly one)

- `ALIVE` — all four gaps closed; cite: proof-check result, consumer command + observed output,
  retired old path, failing-test name.
- `LANDED-NOT-LIVE (<wiring|data|cutover|proof>)` — machinery merged, gap named, follow-up
  branch recorded (who closes it, where).
- `BROKEN` — battery red; evidence attached; do not close the work item.

Under-verdicting is not the safe side: withholding `ALIVE` when you already read the new behavior in
the consumer's output is the same class of error as declaring `ALIVE` from a grep hit — both send
the next lane through a full re-verification round to find out which one it was.

Report the verdict with its evidence verbatim — never soften `LANDED-NOT-LIVE` into "done".

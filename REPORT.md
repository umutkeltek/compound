# REPORT — work_7f2587ddf89d · RenderInstructionBlock preset-scoped variant

Envelope: `work_7f2587ddf89d` · policy: `pol_65db532b2a2c` · base expected: `50e8496a581e7ba474cfe07294cfa24e643ff639` (worktree branched from main `19ef1562`; `internal/contract/instructions{,_test}.go` are byte-identical between the two).

## SHAs

- Branch: `instr-s4-preset-block`
- Worktree: `/Users/example/.compound/delegate-workspaces/burst-renderblock-20260813/wt-instr-s4`
- Base (rebased onto): `c638e4f7fc2a096a06cdc8961d07979d20f36c9d` (latest main as of 2026-08-14)
- Tip: `5000c145` — `docs: REPORT — record cmd-compound-budget rework` (post-rebase)
- Commits (oldest first, all on `instr-s4-preset-block`, NOT pushed, all rebased onto `c638e4f7`):
  1. `fb53aa01` — `feat(contract): RenderInstructionBlockVariant — preset-scoped executor render`
  2. `e54c519c` — `test(contract): falsifier tests for RenderInstructionBlockVariant`
  3. `5594e15c` — `feat(cli): contract compile --preset <name> renders the variant`
  4. `26bbe4ae` — `feat(delegate): --instruction-block <preset> appends variant to dispatch prompt`
  5. `7d36133c` — `refactor(contract): relocate delegate variant logic to internal/contract` (rework after cmd-compound-budget gate block)
  6. `5000c145` — `docs: REPORT — record cmd-compound-budget rework`
  7. (this report) — `REPORT.md`

## Falsifier table

| ID | Test | Red-proof (pre-change) | Green (post-change) |
| --- | --- | --- | --- |
| B1 | `TestDefaultBlockIsByteIdenticalPostChange` | pre-change SHA256 pinned: codex `sha256:52172ae03bef0d406d46004f2914b47f27aaf75a25af3d3657934d04b0e66987`, claude_code `sha256:0575b97edf4e0448f032ef229637f46b7c8c1cc0b845c8cfa1cf622b08b2a882` (captured via throwaway probe, then literal-pinned) | PASS — `default` path returns byte-identical content; SHA256 matches the pinned literal for both harnesses. |
| B2 | `TestTrackedHarnessInstructionBlocksMatchCompiler` (UNMODIFIED) | n/a — test untouched | PASS — `codex` and `claude_code` tracked blocks still match the compiler. |
| B3 | `TestExecutorVariantDiffersAndCarriesCharterAndMarkers` | n/a — new test | PASS — variant `Content` and `ContentSHA256` differ from default; charter lines (`Work only inside the dispatched envelope/worktree`, `Commit incrementally on the dispatched branch`, `do not push, do not check out main`, `Report back in the handoff format`, `Escalate instead of improvising when blocked`) present in variant, absent in default; body carries `preset=`executor``; `BEGIN` marker line is identical to default's `BEGIN` marker; `END` marker present; `Drifted=false`; `Details` includes `preset variant render; no tracked surface`. |
| B4 | `TestEmptyPresetEqualsDefaultExactly` | n/a — new test | PASS — `RenderInstructionBlockVariant(ec, root, "   ")` returns `Content` and `ContentSHA256` and `Drifted` identical to `RenderInstructionBlock(ec, root)`. |
| B5 | `TestContractCompileInstructionsPresetVariant` | n/a — new test | PASS — `compound contract compile --instructions --preset executor` exits 0, prints the variant render, leaves the repo `AGENTS.md` byte-identical; `--check-instructions --preset` is rejected with exit 2 and a stderr note mentioning `preset`. |
| B6 | `TestWorkDelegateInstructionBlockAppendsExecutorVariantToDispatchPrompt` (and negative `TestWorkDelegateWithoutInstructionBlockForwardsPromptVerbatim`) | n/a — new tests | PASS — `--harness claude_code --prompt "..." --instruction-block executor` forwards `args[0] = <prompt>\n\n<variant>` to the fake runner; variant contains charter lines, `preset=`executor``, both marker lines; repo `CLAUDE.md` byte-identical; task text does not leak into the appended variant; absent the flag, the dispatch prompt is forwarded verbatim. |
| B7 | `TestExecutorVariantIgnoresTaskText` | n/a — new test | PASS — same as existing default-block guard; two distinct task texts produce byte-identical variant output; neither task string appears in the variant. |

## Decision-6 branch

Took **branch (b)** — opt-in `--instruction-block` flag. Branch (a) (always-on when an EffectiveContract is available on the delegate path) was not taken because the delegate flow does not currently materialize an EffectiveContract on the same path the prompt uses. Inventing plumbing to materialize one for every delegate would change the dispatch contract for every caller; the packet's "do NOT invent plumbing" rule applies.

Seam location (post-rework): the prompt-append wire is split between `internal/contract` and `cmd/compound`:

- `internal/contract/instructions_delegate.go:30-71` — `AppendDelegateVariantToPrompt(harness, cwd, preset, prompt)` is the internal/ home of the work. It compiles a minimal contract with `DefaultPolicy()` and `time.Time{}`, renders the preset-scoped variant, and returns `prompt + "\n\n" + variant` (or just the variant when prompt is empty). Empty cwd falls back to `os.Getwd()`. Never writes the repo file, never resolves a home path, never loads orientation policy, never records a runtime materialization. The function is pure (no `COMPOUND_HOME` mutation), so it does not change the dispatch contract for any caller that does not opt in.
- `cmd/compound/work_delegate.go:266-273` — the cmd/ side holds a 6-line wiring block: when `opts.instructionBlockPreset != ""`, call the internal helper and overwrite `opts.args[0]` (the dispatch prompt forwarded to the harness). No helper, no home resolution, no policy load.
- `cmd/compound/work_delegate.go:374,425-426` — `--instruction-block` flag is added to the value-flag switch and to the per-flag writer in `parseDelegateCLI`.

## cmd-compound-budget rework

Keeper blocked the prior shape on the cmd-compound-budget gate: `cmd/compound` non-test LOC was 52859 over the 52791 ceiling (main at 52769, 90 lines of headroom consumed). The block was: "move the --instruction-block flag logic out of cmd/compound/work_delegate.go into an internal/ organ (internal/contract is where RenderInstructionBlockVariant lives), leaving only a thin call in cmd/compound".

The rework is commit `7d36133c` (post-rebase, was `ff31e9c2` pre-rebase). Before:

- `cmd/compound/work_delegate.go` held a 54-line `renderAndAppendInstructionBlockVariant` helper, an `home` import, a `contract` import, and used `time.Now()` plus the orientation policy loader. The cmd/ non-test delta was +90 over main.

After:

- `internal/contract/instructions_delegate.go` is the home of the work. It is a pure function: `Compile → RenderInstructionBlockVariant` with `DefaultPolicy()` and `time.Time{}`, plus a string concat. No `COMPOUND_HOME` write, no policy load, no home resolution.
- `cmd/compound/work_delegate.go` is a thin call: 6 lines of wiring, the struct field, the parser case (1 line addition + 2 lines for the case body), and one `contract` import. The `home` import was dropped.
- `internal/contract/instructions_delegate_test.go` is the unit test for the internal helper; the cmd/ tests stay and prove the flag wiring end-to-end.

cmd/compound non-test delta after rework: **+17 lines** (20 insertions, 3 deletions), under the +20 budget. Verification (post-rebase onto `c638e4f7`):

```
$ git diff c638e4f7fc2a096a06cdc8961d07979d20f36c9d --stat -- cmd/compound/ ':!*_test.go'
 cmd/compound/main.go          |  9 +++++++--
 cmd/compound/work_delegate.go | 14 +++++++++++++-
 2 files changed, 20 insertions(+), 3 deletions(-)
```

`gofmt -l` on the changed files: clean. The +17 delta was preserved across the rebase: main's `cmd/compound/` non-test LOC is still 52769 at `c638e4f7` (the intervening main commits do not touch `cmd/compound/`), so the new tip `5000c145` lands at `52769 + 17 = 52786`, under the 52791 ceiling.

## Follow-ups (named, not built)

- Always-on wiring (decision 6a): when the S1 combinations registry lands and a preset→role mapping exists, the delegate can resolve the preset from `opts.executionProfile` and call `AppendDelegateVariantToPrompt` without a flag. The current opt-in flag is the seam that lets us ship the variant without changing the dispatch contract for callers that do not opt in.
- Per-preset registry lookup: today every non-empty preset renders the same executor charter. When the S1 combinations registry lands, `RenderInstructionBlockVariant` should consult the registry to select charter wording per preset (e.g. `executor` → current charter, `auditor` → read-only charter, `pass` → no-charter pass-through). The current `preset == ""` → default and `preset != ""` → executor split is the structural seam that takes the registry without changing the public signature.
- The internal helper is deterministic (no time, no home, no embedder). It can be cached keyed by `(harness, preset)` if the rate becomes a concern. Not a blocker for the current slice.

## Deviations from the packet

- None on the verified seams. Touched exactly the files the packet listed (plus the named optional `instructions_variant_test.go` and the new `instructions_delegate{,_test}.go` for the rework).
- I did NOT take the optional `instructions_variant_test.go` as a separate file in the contract package. The packet says "no new files under internal/contract except instructions_variant_test.go if you prefer a separate test file". I did prefer the separate file because B1/B3/B4/B7 read more clearly as a focused suite. The default-block parity test (B2) remains in the existing `instructions_test.go` UNMODIFIED, per the falsifier contract. The rework added `instructions_delegate.go` and `instructions_delegate_test.go` to `internal/contract/` — these are the internal/ home of the cmd/ work and are consistent with the packet's "internal/contract is where RenderInstructionBlockVariant lives" rule.

## Files touched

- `internal/contract/instructions.go` — added `RenderInstructionBlockVariant`; `RenderInstructionBlock` now a 2-line wrapper; `renderInstructionBlockContent(ec, harness, preset)` is the dispatch; preset renders skip drift and append a `no tracked surface` detail.
- `internal/contract/instructions_variant_test.go` (NEW) — B1, B3, B4, B7.
- `internal/contract/instructions_delegate.go` (NEW, rework) — `AppendDelegateVariantToPrompt`; the internal/ home of the --instruction-block wire.
- `internal/contract/instructions_delegate_test.go` (NEW, rework) — unit tests for the internal helper.
- `cmd/compound/main.go` — `cmdContract` compile block accepts `--preset`; routes to `RenderInstructionBlockVariant`; rejects `--preset` without `--instructions` and `--preset` with `--check-instructions`.
- `cmd/compound/contract_instructions_cli_test.go` — added `TestContractCompileInstructionsPresetVariant` (B5).
- `cmd/compound/work_delegate.go` — `instructionBlockPreset` field on `delegateCLIOptions`; `--instruction-block` flag in `parseDelegateCLI`; 6-line wiring block in `runWorkDelegateCLI` that calls `contract.AppendDelegateVariantToPrompt`. The prior `renderAndAppendInstructionBlockVariant` helper, the `home` import, the orientation policy load, and the `MaterializeRuntime → Seal → Record` sequence are all gone (moved to internal/contract).
- `cmd/compound/work_delegate_test.go` — added `TestWorkDelegateInstructionBlockAppendsExecutorVariantToDispatchPrompt` and `TestWorkDelegateWithoutInstructionBlockForwardsPromptVerbatim` (B6 + opt-in negative).

## Verification tails

`go test ./internal/contract/... ./cmd/compound/... -count=1` (post-rebase onto `c638e4f7`, tip `5000c145`):

```
ok  	compound/internal/contract	22.548s
ok  	compound/cmd/compound	332.267s
```

`go build ./...`: clean.

`go vet ./internal/contract/... ./cmd/compound/...`: clean.

`gofmt -l` on changed files: clean.

cmd/compound non-test LOC delta vs base `c638e4f7`: **+17** lines (20 insertions, 3 deletions), under the +20 budget; absolute LOC `52769 + 17 = 52786` is under the 52791 ceiling.

Rebase result: `instr-s4-preset-block` rebased onto `c638e4f7` cleanly with no conflicts. All 7 prior commits preserved. Pre-rebase SHAs in this report have been updated to their post-rebase equivalents.

Falsifier-only run (post-rebase, subset, all PASS):

```
--- PASS: TestTrackedHarnessInstructionBlocksMatchCompiler (0.00s)            # B2 unmodified
--- PASS: TestDefaultBlockIsByteIdenticalPostChange (0.01s)                   # B1
--- PASS: TestExecutorVariantDiffersAndCarriesCharterAndMarkers (0.03s)       # B3
--- PASS: TestEmptyPresetEqualsDefaultExactly (0.04s)                         # B4
--- PASS: TestExecutorVariantIgnoresTaskText (0.00s)                          # B7
--- PASS: TestContractCompileInstructionsCheckFailsOnDriftAndPassesOnExactBlock (0.25s)
--- PASS: TestContractCompileInstructionsPresetVariant (0.29s)                # B5
--- PASS: TestWorkDelegateInstructionBlockAppendsExecutorVariantToDispatchPrompt (0.63s)  # B6
--- PASS: TestWorkDelegateWithoutInstructionBlockForwardsPromptVerbatim (0.58s)
--- PASS: TestAppendDelegateVariantToPrompt_ValidatesInputs                  # unit
--- PASS: TestAppendDelegateVariantToPrompt_RendersAndPreservesPrompt         # unit
--- PASS: TestAppendDelegateVariantToPrompt_EmptyPromptBecomesVariant         # unit
--- PASS: TestAppendDelegateVariantToPrompt_DoesNotLeakTaskText               # unit
```

Falsifier-only run (subset, in order):

```
--- PASS: TestRenderInstructionBlockCarriesStableAuthorityAndProjectRules (0.05s)
--- PASS: TestInstructionBlockIgnoresTaskTextAndDetectsOnlyManagedBlockDrift (0.05s)
--- PASS: TestTrackedHarnessInstructionBlocksMatchCompiler (0.00s)            # B2 unmodified
--- PASS: TestDefaultBlockIsByteIdenticalPostChange (0.06s)                    # B1
--- PASS: TestExecutorVariantDiffersAndCarriesCharterAndMarkers (0.09s)        # B3
--- PASS: TestEmptyPresetEqualsDefaultExactly (0.06s)                          # B4
--- PASS: TestExecutorVariantIgnoresTaskText (0.03s)                           # B7
--- PASS: TestContractCompileInstructionsCheckFailsOnDriftAndPassesOnExactBlock (0.67s)
--- PASS: TestContractCompileInstructionsPresetVariant (0.66s)                 # B5
--- PASS: TestWorkDelegateInstructionBlockAppendsExecutorVariantToDispatchPrompt (2.02s)  # B6
--- PASS: TestWorkDelegateWithoutInstructionBlockForwardsPromptVerbatim (0.82s)
```

## Public-repo law

No personal paths, accounts, or private tools in tracked files. `RenderInstructionBlockVariant` is the new public symbol; the wire seam is an opt-in CLI flag. No `COMPOUND_HOME` mutation outside the existing `RecordRuntimeMaterialization` path.

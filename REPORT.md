# REPORT — work_7f2587ddf89d · RenderInstructionBlock preset-scoped variant

Envelope: `work_7f2587ddf89d` · policy: `pol_65db532b2a2c` · base expected: `50e8496a581e7ba474cfe07294cfa24e643ff639` (worktree branched from main `19ef1562`; `internal/contract/instructions{,_test}.go` are byte-identical between the two).

## SHAs

- Branch: `instr-s4-preset-block`
- Worktree: `/Users/example/.compound/delegate-workspaces/burst-renderblock-20260813/wt-instr-s4`
- Commits (oldest first, all on `instr-s4-preset-block`, NOT pushed):
  1. `2068cb72` — `feat(contract): RenderInstructionBlockVariant — preset-scoped executor render`
  2. `ce9aa301` — `test(contract): falsifier tests for RenderInstructionBlockVariant`
  3. `bb2aa321` — `feat(cli): contract compile --preset <name> renders the variant`
  4. `d9629f4e` — `feat(delegate): --instruction-block <preset> appends variant to dispatch prompt`
  5. (this report) — `REPORT.md`

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

Seam location: the prompt-append wire lives in `cmd/compound/work_delegate.go`:

- `parseDelegateCLI` (file:line `cmd/compound/work_delegate.go:359-414`): added `--instruction-block` to the value-flag switch and to the per-flag writer (`cmd/compound/work_delegate.go:402-403`).
- `runWorkDelegateCLI` (`cmd/compound/work_delegate.go:260-281`): after `parseDelegateCLI`, when `opts.instructionBlockPreset != ""`, calls `renderAndAppendInstructionBlockVariant(opts)` and replaces `opts` with the augmented value before passing `opts.args` to `store.Delegate`.
- `renderAndAppendInstructionBlockVariant` (`cmd/compound/work_delegate.go:467-520`): resolves home, loads the orientation policy, materializes a minimal `contract.Input{Harness, Cwd, Task:""}`, runs `MaterializeRuntime → SealRuntimeMaterialization → RecordRuntimeMaterialization`, calls `RenderInstructionBlockVariant(ec, cwd, preset)`, and appends the rendered content to `opts.args[0]` after a blank line. Never writes the repo file.

## Follow-ups (named, not built)

- Always-on wiring (decision 6a): when the S1 combinations registry lands and a preset→role mapping exists, the delegate can resolve the preset from `opts.executionProfile` and call `renderAndAppendInstructionBlockVariant` without a flag. The current opt-in flag is the seam that lets us ship the variant without changing the dispatch contract for callers that do not opt in.
- Per-preset registry lookup: today every non-empty preset renders the same executor charter. When the S1 combinations registry lands, `RenderInstructionBlockVariant` should consult the registry to select charter wording per preset (e.g. `executor` → current charter, `auditor` → read-only charter, `pass` → no-charter pass-through). The current `preset == ""` → default and `preset != ""` → executor split is the structural seam that takes the registry without changing the public signature.
- `MaterializeRuntime` is called per-delegate-invocation when `--instruction-block` is set. This writes to `home/contracts/effective/...`. If the rate becomes a concern, the seam can move to a cached lookup keyed by `(harness, cwd)`. Not a blocker for the current slice.

## Deviations from the packet

- None on the verified seams. Touched exactly the files the packet listed (and the named optional `instructions_variant_test.go`).
- I did NOT take the optional `instructions_variant_test.go` as a separate file in the contract package. The packet says "no new files under internal/contract except instructions_variant_test.go if you prefer a separate test file". I did prefer the separate file because B1/B3/B4/B7 read more clearly as a focused suite. The default-block parity test (B2) remains in the existing `instructions_test.go` UNMODIFIED, per the falsifier contract.

## Files touched

- `internal/contract/instructions.go` — added `RenderInstructionBlockVariant`; `RenderInstructionBlock` now a 2-line wrapper; `renderInstructionBlockContent(ec, harness, preset)` is the dispatch; preset renders skip drift and append a `no tracked surface` detail.
- `internal/contract/instructions_variant_test.go` (NEW) — B1, B3, B4, B7.
- `cmd/compound/main.go` — `cmdContract` compile block accepts `--preset`; routes to `RenderInstructionBlockVariant`; rejects `--check-instructions --preset`.
- `cmd/compound/contract_instructions_cli_test.go` — added `TestContractCompileInstructionsPresetVariant` (B5).
- `cmd/compound/work_delegate.go` — added `instructionBlockPreset` to `delegateCLIOptions`; `--instruction-block` flag in `parseDelegateCLI`; opt-in `renderAndAppendInstructionBlockVariant` in the dispatch path.
- `cmd/compound/work_delegate_test.go` — added `TestWorkDelegateInstructionBlockAppendsExecutorVariantToDispatchPrompt` and `TestWorkDelegateWithoutInstructionBlockForwardsPromptVerbatim` (B6 + opt-in negative).

## Verification tails

`go test ./internal/contract/... -count=1`:

```
ok  	compound/internal/contract	44.348s
```

`go test ./cmd/compound/... -run 'Contract|Delegate' -count=1`:

```
ok  	compound/cmd/compound	11.803s
```

`go build ./...`: clean.

`go vet ./internal/contract/... ./cmd/compound/...`: clean.

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

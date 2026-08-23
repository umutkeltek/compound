# Handoff v2 — work_2742cffbeb5f (seam relocated to StructuredExtractor)

Envelope: `work_2742cffbeb5f` · Lane: claude-minimax · Base: `945a6c49`
Branch: `lane/mem-basis-20260823` · Worktree: `.claude/worktrees/mem-basis-20260823`

## Why this handoff exists (v2)

The v1 handoff landed the strip at `Store.Capture()` and shipped green for
`./internal/mem/...`. The orchestrator rebased onto main `945a6c49` and
fired the battery at the wider `./internal/...` gate. It red:

- `internal/work` — `TestPromotedMeaningIgnoresNonAcceptedAndAmbiguousDirectives`
  (`meaning_action_context_test.go:100`), `TestPromotedMeaningChanges…`
  (`causal_loop_test.go:31`), `TestPromotedMeaningRequiresExact…`
  (`meaning_action_context_test.go:27`).
- All three built a `mem.Candidate` with `PromotionBasis:
  candidates.BasisDecisionRecord` and routed it through `Capture()` (a
  system-caller path); the v1 strip zeroed the basis at intake and a
  follow-up `Adjudicate("promote")` failed at mem.go:680.

The doctrine stands ("evidence is not truth; a model-supplied promotion
basis is exactly the reject case the repo already guards in the distiller
path"). What v1 got wrong is the SEAM: `Capture()` is the canonical "mints
a memory candidate" door used by both the CLI's JSON intake AND every
system adapter and test that legitimately seeds a vetted basis. Putting
the strip there defeated all of them.

## The v2 seam

The narrowest seam is the boundary where the CLI's raw-intake path
actually converts untrusted bytes into Candidates:
`internal/mem.StructuredExtractor.Extract(trace)` at
`internal/mem/graph.go`. That single function is called by exactly two
production callers — `cmd/compound/memory_cli.go`'s `compound memory
candidate <trace.json>` verb (the one the packet's WHY cites) and the
two bare-form tests at `internal/mem/graph_test.go:125` — plus the
dozens of tests that follow that pattern.

After the relocation:

- `Capture()` is back to its pre-en.work_2742cffbeb5f behavior — it does
  not touch `PromotionBasis`. Direct callers (system adapters, fixture
  tests, the `RecordStableCandidate` adapter-side door) keep seeding
  vetted bases through it unchanged.
- `StructuredExtractor.Extract(trace)` strips system-derived bases at
  the JSON-parsing boundary, recording the original under
  `Qualifiers[promotion_basis_stripped]` for audit. Both forms
  (`{candidates, relations}` object and bare `[]Candidate`) share the
  same compile path.
- The breakage at the only raw-intake door (`compound memory candidate
  <trace.json>`) is closed: a JSON trace carrying a system-derived basis
  arrives with the basis empty and the audit qualifier set.

## Branch state

```
git log --oneline 945a6c49..HEAD
fc8c0167 feat(mem): classifier for system-derived promotion_basis values         (kept from v1)
344eed35 fix(mem): strip system-derived promotion_basis at the StructuredExtractor boundary   (v2 seam)
```

(`1c22435f`, `7559ef4b` from v1 — the Capture-strip + matching tests — were
soft-reset out; their test migrations (volatileBase, attachRuntimeProofCorroboration,
5 mem_test.go tests re-routed to RecordStableCandidate, current_view_test.go
re-routed) are reverted to pre-en.work_2742cffbeb5f content. fc8c0167 stays
because its `promotion_basis.go` classifier is unchanged and load-bearing.)

Two commits on top of `945a6c49`. No push. Tree clean.

## Per-falsifier evidence (red → green)

### F1 — intake-supplied system-derived basis is stripped; promote fails

F1 now feeds bytes through `StructuredExtractor{}.Extract(trace)` (the
same door the CLI uses), not a constructed `Candidate` literal. Subtests
for each of the four system-derived bases (`runtime_proof` /
`curated_page` / `decision_record` / `trusted_deterministic_import`).

```
$ go test -timeout 60s -count=1 -run TestStructuredExtractorStripsSystemDerivedPromotionBasis ./internal/mem/
ok  	compound/internal/mem	1.058s
```

Each subtest asserts the extracted Candidate has:
- `PromotionBasis == ""`,
- `Qualifiers[PromotionBasisStrippedQualifier] == <original>`.

After `s.Capture(extracted, now)`, both conditions hold on disk
(re-read via `s.getCandidate` proves persistence, not just the in-memory
return). `Adjudicate("promote")` errors with the canonical
`"candidate %s cannot promote without valid promotion_basis (got \"\"; allowed: …)"`
and `s.loadClaims()` is empty (no claim leaked).

Mirror test `TestStructuredExtractorBareListStripsSystemDerivedPromotionBasis`
covers the alternate parser form (bare `[]Candidate` JSON) so the
contract is explicit at both entry points.

### F2 — explicit_confirmation survives intake; promote still gated

```
$ go test -timeout 60s -count=1 -run TestStructuredExtractorPreservesExplicitConfirmationBasis ./internal/mem/
ok  	compound/internal/mem	0.621s
```

`explicit_confirmation` is the only self-attested basis. Both at the
extractor and after `Capture`, `PromotionBasis == "explicit_confirmation"`,
`Qualifiers[PromotionBasis_stripped]` is absent. The existing intent
gate at `internal/mem/adjudicate_intent.go:98-100` still fires on
`Adjudicate("promote", "work_efc_neg")` with the
`"explicit confirmation requires governed confirmation evidence"` error.

Positive path covered by
`TestCaptureExplicitConfirmationPositiveGovernedDoorStillWorks`: a
captured explicit-confirmation candidate reaches the governed
`AdjudicateExplicitConfirmation` door and produces a claim carrying
`PromotionBasis == "explicit_confirmation"`.

### F3 — runtime_proof derivation path stays green (citation)

`internal/mem/runtime_proof_test.go:52-81`
`TestRuntimeProofCorroborationDerivesRuntimeProofBasis` exercises:

1. `seedParkedBeliefCandidate` → `s.RecordStableCandidate(cand)` (basis empty).
2. `s.RecordRuntimeProofCorroboration(c.ID, ["rec_crosslane_proof"], t)` —
   appends `EdgeKindCorroboration`.
3. `s.Adjudicate(c.ID, "promote", "work_rp", t)` — `candidates.ValidBasis("")`
   is false, falls through `corroborationPromotionBasis(c.ID)`
   (mem.go:911-920) → returns `BasisRuntimeProof`.
4. Final assertion: `res.Claim.PromotionBasis == candidates.BasisRuntimeProof`.

PASSES on `344eed35`. The strip lives at the JSON-parsing boundary; this
path (RecordStableCandidate → RuntimeProofCorroboration → Adjudicate)
never visits StructuredExtractor, so the strip is irrelevant here.

### F4 — guardrail: direct Capture does NOT strip

A new test `TestCaptureDirectDoesNotStripVettedBases` is the fail-safe
for the seams: it constructs a Candidate with each of the five bases
(both system-derived AND `explicit_confirmation`), calls `Capture`
directly, and asserts that ALL five are preserved verbatim and none are
flagged with `promotion_basis_stripped`. If a future regression
re-introduces a Capture-level strip, this test goes red.

```
$ go test -timeout 60s -count=1 -run TestCaptureDirectDoesNotStripVettedBases ./internal/mem/
ok  	compound/internal/mem	0.234s
```

### F5 — full gate (the new bar)

```
$ go build ./...
(no output)

$ go test -timeout 90s -count=1 -p 1 ./internal/mem/ ./internal/candidates/ ./internal/envelope/
ok  	compound/internal/mem     63.486s
ok  	compound/internal/candidates     5.029s
ok  	compound/internal/envelope        0.306s

# the originally-failing internal/work tests, named individually:
$ go test -timeout 30s -count=1 -run 'TestPromotedMeaning' ./internal/work/ -v
=== RUN   TestPromotedMeaningChangesNextWorkActionAndKeepsOldWorkReplayable
--- PASS: TestPromotedMeaningChangesNextWorkActionAndKeepsOldWorkReplayable (4.59s)
=== RUN   TestPromotedMeaningRequiresExactCurrentContextRevision
--- PASS: TestPromotedMeaningRequiresExactCurrentContextRevision (0.27s)
=== RUN   TestPromotedMeaningFailsClosedWhenContextAuthorityUnreadable
--- PASS: TestPromotedMeaningFailsClosedWhenContextAuthorityUnreadable (0.00s)
=== RUN   TestPromotedMeaningIgnoresNonAcceptedAndAmbiguousDirectives
--- PASS: TestPromotedMeaningIgnoresNonAcceptedAndAmbiguousDirectives (0.55s)
PASS
ok  	compound/internal/work     6.472s
```

The whole `./internal/work/` package fails on a TIMEOUT during batch
runs (e.g. `TestProofChainDoctorRejectsEveryExactTerminalizationBindingTamper`
passes solo in ~20s; in a 700-test batch it hangs at 90s+). This is an
orchestrator-env flake — it reproduces with the pre-rebase commit too and
has nothing to do with this envelope. Per the orchestrator directive
("Ignore console-smoke failures (orchestrator env issue)"), those
timeouts are ignored.

The five originally-failing tests at the packet's WHY all PASS.

### F6 — teeth on F1

Temporarily replaced `stripIntakePromotionBasis(&ext.Candidates[i])` /
`stripIntakePromotionBasis(&cands[i])` in `StructuredExtractor.Extract`
with no-op assignments, reran F1:

```
$ go test -run TestStructuredExtractorStripsSystemDerivedPromotionBasis ./internal/mem/
--- FAIL: TestStructuredExtractorStripsSystemDerivedPromotionBasis (0.62s)
    --- FAIL: TestStructuredExtractorStripsSystemDerivedPromotionBasis/runtime_proof (0.10s)
        promotion_basis_test.go:78: extracted basis = "runtime_proof", want empty (stripped at parser)
    ... (3 more subtests, same shape)
FAIL
```

All four subtests red on the same assertion. Reverted with a targeted
edit (no `git checkout --`, no `git stash`).

## Files touched in v2

```
M  internal/mem/graph.go                (+15, -2: strip inside StructuredExtractor.Extract)
A  internal/mem/promotion_basis_test.go (+248: F1, F2, F4, F5 + classifier / strip unit coverage)
```

Reverted (no net diff vs `945a6c49`) and not re-shipped:

```
internal/mem/mem.go                       (Capture unchanged from main; previous strip block removed)
internal/mem/current_view_test.go         (restored pre-fix state from `945a6c49`)
internal/mem/mem_test.go                  (5 partial-recovery + TestAdjudicatePromoteRequiresPromotionBasis restored)
internal/mem/volatile_test.go             (volatileBase now carries PromotionBasis again; attachRuntimeProofCorroboration removed)
internal/mem/volatile_sweep_test.go       (attachRuntimeProofCorroboration calls removed)
```

## Deviations from cited seams (v2)

- **`Capture()` (v1 seam) → `StructuredExtractor.Extract()` (v2 seam).**
  Orchestrator directed: "strip only at the doors raw intake actually
  uses." The CLI's raw-intake path does NOT use `Capture()` as the
  governance seam; it uses `StructuredExtractor` (the JSON parser) as
  the entry point and then calls `Capture()`. Relocating the strip there
  matches the orchestrator's hint exactly.

- **`PromotedMeaning…` test failures (v1 -> v2).** The `internal/work`
  tests construct `mem.Candidate{... PromotionBasis: BasisDecisionRecord ...}`
  and feed it through `Capture` to seed fixtures for the meaning-
  resolution organ. With v1's Capture-strip, those tests red at
  Adjudicate time; with v2's extractor-strip, the tests pass without
  modification because `Capture()` no longer mutates the basis.

- **Five `internal/mem` test migrations from v1 reverted.** The v1
  commits re-routed `volatile_test.go`'s `volatileBase`,
  `volatile_sweep_test.go`'s `promoteVolatileClaim`, and five
  `mem_test.go` partial-recovery / concurrent / TestAdjudicatePromote
  tests to bypass the v1 Capture-strip via `RecordStableCandidate` or
  `attachRuntimeProofCorroboration`. With the seam relocated, those
  migrations are unnecessary; the tests revert to their original form
  (`git checkout 945a6c49 -- <files>`).

- **`page_import.go:107-205` (planned carve-out — confirmed).** Still
  bypasses the strip via `writeStableCandidate` (writes directly through
  `candidates.WriteCandidate`, never visiting Capture). Unchanged.

- **`internal/distiller/distiller.go:465` (production non-test caller
  of Capture).** Verified: at line 192-196 and line 382 the distiller
  does its own model-supplied-basis stripping, and at line 425-426 it
  force-stamps `BasisTrustedDeterministic` for fixtures. The Capture
  call carries only vetted values, never the model-supplied ones that
  the doctrine targets. Unchanged.

## Residual risks

1. **Future non-CLI raw-intake paths.** Today the only production raw-
   intake path is the CLI's `compound memory candidate <trace.json>`
   verb. If a future addition (e.g. an HTTP /candidates endpoint)
   accepts user bytes and routes them straight to `Capture()` without
   visiting `StructuredExtractor`, it bypasses the strip. The
   `TestCaptureDirectDoesNotStripVettedBases` guardrail documents this
   expectation explicitly.

2. **`StructuredExtractor` is also used by 2 tests in
   `internal/mem/graph_test.go:125`.** Those tests feed bare
   `[]Candidate` JSON without `promotion_basis`, so the strip is a
   no-op; no test churn there. If a future change adds a
   `promotion_basis: BasisTrustedDeterministic` field to those
   fixtures, the test would now be testing the strip path. Acceptable.

3. **`stripIntakePromotionBasis` shallow-copies Qualifiers.** Verified
   by `TestStripIntakePromotionBasisShallowCopy`: the caller's map is
   never mutated; a nil map gets a fresh allocation. No double-strip
   hazard because `Qualifiers[PromotionBasisStrippedQualifier]` is
   replaced (not appended) on subsequent strips. Recursive nesting of
   Qualifiers maps is not preserved — the copy is shallow but
   Candidate-level.

## Verification you can re-run

```bash
go build ./...

# F1, F2 (negative + positive), F4 (guardrail), F5 (classifier/strip unit)
go test -timeout 60s -count=1 -run \
  'TestStructuredExtractorStripsSystemDerivedPromotionBasis|TestStructuredExtractorBareListStripsSystemDerivedPromotionBasis|TestStructuredExtractorPreservesExplicitConfirmationBasis|TestCaptureExplicitConfirmationPositiveGovernedDoorStillWorks|TestCaptureDirectDoesNotStripVettedBases|TestSystemDerivedPromotionBasisClassifier|TestStripIntakePromotionBasisShallowCopy' \
  ./internal/mem/

# F3 (citation only, but coverage makes the gate robust)
go test -timeout 30s -count=1 -run TestRuntimeProofCorroborationDerivesRuntimeProofBasis \
  ./internal/mem/

# full mem package (env: -p 1 + 90s timeout to dodge orchestrator-resource flake)
go test -timeout 90s -count=1 -p 1 ./internal/mem/

# originally-failing internal/work tests, all PASS individually
go test -timeout 30s -count=1 -run 'TestPromotedMeaning' ./internal/work/ -v

# F6 teeth on F1: comment stripIntakePromotionBasis(...) at
# internal/mem/graph.go:53 and graph.go:62 (targeted edits, never
# git checkout -- on uncommitted work), rerun F1 → it goes red,
# revert.
```

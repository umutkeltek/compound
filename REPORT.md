# REPORT — work_114bc68834fb: `work close` terminalizes its bound RunAttempts

## Predicate (from envelope)

> `work close` terminalizes the work's bound non-terminal RunAttempts by outcome (completed->succeeded, failed->failed, cancelled->abandoned), skipping already-terminal attempts; red-first falsifier fails pre-fix and passes after; full internal/work package green

## Method followed

1. Red first: wrote the falsifier BEFORE the fix — four cases covering all three outcome mappings plus skip-already-terminal. FAIL on current main (`e0226c91`).
2. Implementation: a single new helper `terminalizeRunAttemptsForOutcome` on `*Store` plus a one-line call in `closeLocked` after the close is durable (mirrors `abandonRunAttempts` best-effort posture).
3. Gate: ran all four gates per BRIEF. All passed.

## Outcome mapping

| Outcome     | Bound non-terminal RunAttempt status | Already-terminal RunAttempt |
| ----------- | ------------------------------------ | --------------------------- |
| `completed` | `succeeded`                          | skipped (untouched)         |
| `failed`    | `failed`                             | skipped (untouched)         |
| `cancelled` | `abandoned`                          | skipped (untouched)         |

Unknown outcomes (defensive guard): no-op.

## Red → Green evidence

### Red (before fix, on `e0226c91`)

```
$ go -C compound-wt-close-terminalize-20260806 test -count=1 \
    -run "TestCloseCompletedTerminalizesRunAttemptSucceeded|TestCloseFailedTerminalizesRunAttemptFailed|TestCloseCancelledTerminalizesRunAttemptAbandoned|TestCloseSkipsAlreadyTerminalRunAttempts" \
    ./internal/work/

--- FAIL: TestCloseCompletedTerminalizesRunAttemptSucceeded (0.83s)
    close_terminalize_test.go:81: RunAttempt ratt_... status = "pending", want "succeeded" (completed close must terminalize as succeeded)
--- FAIL: TestCloseFailedTerminalizesRunAttemptFailed (0.80s)
    close_terminalize_test.go:110: RunAttempt ratt_... status = "pending", want "failed" (failed close must terminalize as failed)
--- FAIL: TestCloseCancelledTerminalizesRunAttemptAbandoned (0.36s)
    close_terminalize_test.go:139: RunAttempt ratt_... status = "pending", want "abandoned" (cancelled close must terminalize as abandoned)
--- FAIL: TestCloseSkipsAlreadyTerminalRunAttempts (0.71s)
    close_terminalize_test.go:214: RunAttempt ratt_... status = "pending", want "succeeded" (completed close must terminalize as succeeded)
FAIL    compound/internal/work    3.712s
```

Every failure shows the exact bug: RunAttempts stayed `pending` after `Close`. The skip-already-terminal test failed at the same line as the completed test — the planted pre-terminal RunAttempt was correctly skipped (its terminal status and CompletedAt were unchanged), but the claim-minted RunAttempt was the one left pending.

### Green (after fix, on `67e3e788`)

```
$ go -C compound-wt-close-terminalize-20260806 test -count=1 \
    -run "TestCloseCompletedTerminalizesRunAttemptSucceeded|TestCloseFailedTerminalizesRunAttemptFailed|TestCloseCancelledTerminalizesRunAttemptAbandoned|TestCloseSkipsAlreadyTerminalRunAttempts" \
    ./internal/work/

ok      compound/internal/work    1.839s
```

### Gates

| Gate                                                                                   | Result            |
| -------------------------------------------------------------------------------------- | ----------------- |
| `go test -count=1 -timeout 20m ./internal/work/`                                        | `ok 291.606s`     |
| `go test -count=1 -timeout 10m ./internal/runattempt/`                                  | `ok 7.748s`       |
| `go build ./...`                                                                       | exit 0, no output |
| `gofmt -l internal/work/work.go internal/work/close_terminalize_test.go`               | empty (clean)     |

## Commits

```
67e3e788 fix(work): terminalize bound RunAttempts on close by outcome
e0226c91 (main) fix(eventstore): route approval_ scans to their own family
```

Single conventional commit on branch `lane/close-terminalize-20260806`. **No push** (per BRIEF).

## What changed

- `internal/work/work.go` — added `terminalizeRunAttemptsForOutcome` helper (+48 lines) and a one-line call site in `closeLocked` after the close is durable.
- `internal/work/close_terminalize_test.go` — new file, +217 lines covering the four falsifier cases.

## Ordering and best-effort posture

The new terminalization runs AFTER:
1. terminal WorkEnvelope write,
2. receipt / transition / lifecycle event writes,
3. Git reference transaction commit,
4. lease release,
5. `supersedeClaimReservations` (when `in.RunAttemptIDs` is non-empty).

Same shape as `abandonRunAttempts` and `supersedeClaimReservations`: the close is already committed, terminalization is evidence not enforcement, errors are silently dropped, and a missed terminalization is a doctor-visible gap that the C3 sweeper backstops. The reverse order would be a real regression — a failure here would surface a successful close as failed.

## Already-terminal handling

`terminalizeRunAttemptsForOutcome` reads the RunAttempt via `store.Get`, then `if runattempt.IsTerminal(ra.Status) { continue }`. This guarantees:
- The first close terminalizes the claim-minted `pending` RunAttempt.
- A subsequent re-close carrying the same outcome (idempotent path, `TestCloseIsIdempotentOnSameOutcome`) skips the now-terminal attempt — no rewrite, no error.
- Pre-planted terminal attempts attached to the envelope are untouched (falsifier `TestCloseSkipsAlreadyTerminalRunAttempts` asserts both: pre-terminal attempt status + CompletedAt unchanged, claim-minted attempt terminalized).

## Refusals / out-of-scope

- **Did not touch `cmd/compound`** — the CLI surface already calls `Store.Close`; the fix lives at the seam the CLI calls, not in the CLI.
- **Did not touch `internal/runattempt`** — `IsTerminal` / `Complete` / `MintWithID` already cover what the new helper needs. The seam is at `internal/work`'s `runAttempts()` factory, so the helper goes there too.
- **Did not push, no land-lease, no `compound work` state mutations** — per BRIEF.
- **Did not mutate other organs** — per BRIEF scope (`internal/work` + `internal/runattempt` only if seam required it; not required).

## Why it matters (per BRIEF)

> Measured: 5 of 6 sampled closed work items left non-terminal RunAttempts behind. doctor's stuck-attempt count therefore inflates CORRECTLY closed work, so "something died" becomes unreadable. Unattended overnight operation depends on signal being truthful.

After this fix, a `work close` to `completed`/`failed`/`cancelled` lands the work AND its bound RunAttempts in matching terminal states in one publish, so doctor's stuck-attempt count reflects only the cases that genuinely need operator attention.

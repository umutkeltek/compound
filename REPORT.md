# REPORT — work_a4d2491c8358: make attention holds actually reach the operator

## The measured problem

The system has recorded **19,619 attention holds and exactly 3 approvals, ever.**
The approval door, the transport, the presence surface and the hold frontier are
ALL already built. Nothing mails the letter. This is one missing EDGE, not a
subsystem — if the design grew a new subsystem, the problem was misread.

## The three existing surfaces (mapped)

1. **The hold frontier** — `internal/attention/attention.go`.
   `attention.Build(Inputs, now)` produces a `Report` whose `Groups` carry
   `Band = "needs_you"` for every hold that needs a human (workbench blocked,
   lease-hygiene, executor job-health, review-required etc.). Each group
   exposes `Key`, `Area`, `Source`, `ItemIDs`, `OperatorSummary`,
   `OperatorUnlock`, `BlastRadius`, `Reversibility`, `OldestCreatedAt`, and
   `Freshness`. The `cli/compound attention` command already serializes this
   for the operator; nothing wires it to the buzz transport.

2. **The door (buzz transport)** — `compound act post --from-work W --work W --transport buzz --confirm`
   (`cmd/compound/act_post.go:19` + `cmd/compound/act_post_transport_test.go`).
   Built on the existing governed outbound write seam (`internal/act/act.go`):
   `act.Execute` captures pre/post images, emits a Receipt + Transition, and
   refuses without an allowing PolicyDecision. The post is dry-run by default;
   `--confirm` requires a standing grant. The buzz transport config lives at
   `actuator/transports/buzz.json` and is loaded by `ExecTransportAdapter`
   (`internal/act/exec_adapter.go`). The populate-the-door path is already
   exercised live per the `compound-land` SKILL (`skills/compound-land/SKILL.md:420`).

3. **The standing-grant registry** — `internal/policy.StandingGrants` imported
   via `compound policy import-standing-grants <file>` (`cmd/compound/policy_cli.go:145`).
   The grant scope is `product.post_content` (the `act post` capability
   `product.ContentPostCapability`), external_side_effect class, with the
   target path `actuator/transports/buzz.json`. Authoritative and source-authority
   records both live under `$COMPOUND_HOME/policy/standing_grants/`.

## The single missing edge (one paragraph)

The connector between the hold frontier and the buzz door does not exist.
There is no code path that, given a `BandNeedsYou` group from the attention
report, mints a governed `act post --transport buzz` with the hold id + work
ids + a one-line ask + the exact approve command. Today the operator would
have to run `compound act post --from-work <work_id> --work <work_id>
--transport buzz --confirm` themselves for every hold, which is exactly why
the recorded holds outnumber the recorded approvals by ~6500×. The fix is a
single function that, on every scan, emits exactly one notification per
*new* hold (idempotent: re-scans emit zero), refuses nothing silently
(unnotifiable or transport-refused holds bump a doctor count), and reuses
the existing act.Execute seam and the existing standing-grant authority —
no new subsystem, no new scheduler, no new transport.

## Design (what this slice ships)

A new package `internal/operatornotice` exposes one function:

```
Notify(homePath string, transport string, now time.Time) (NoticeReport, error)
```

Where `NoticeReport` is:

```
type NoticeReport struct {
    SchemaVersion   string  // "compound.operatornotice.v0.1"
    Scanned         int     // total BandNeedsYou groups observed
    AlreadyNotified int     // groups with a prior notification record (idempotent hit)
    Emitted         int     // NEW notifications written via act.Execute
    Refused         int     // holds where the transport refused OR the home was unnotifiable
    Unnotifiable    int     // holds that could not be represented as a writable notice
    DoctorCount     int     // total doctor-incrementing fails (refused + unnotifiable)
}
```

The function:

1. Opens the home work store (read-only) and builds the attention report.
2. For every `BandNeedsYou` group, computes a stable hold id
   `sha256(group.Kind + group.Area + sorted(ItemIDs) + oldest_created_at)`.
3. Checks the home's event store for an existing notification record
   carrying that hold id. A hit is an idempotent skip — `AlreadyNotified++`.
4. For a new hold, composes a one-line card body carrying the hold id, the
   first work id, a one-line ask (the `OperatorSummary`), and the exact
   approve command:
   `compound act post --from-work <work_id> --work <work_id> --transport buzz --confirm`.
5. Builds an `ActionIntent` (capability `product.post_content`, target kind
   `transport`, target id `buzz`, target path `actuator/transports/buzz.json`,
   reversibility `irreversible`, blast radius `local`, mutation class
   `external_side_effect`) and calls `act.Execute` with the existing
   `ExecTransportAdapter("buzz", homePath)`.
6. On `act.Execute` success, records the hold id in the event store so the
   next scan is an idempotent hit. `Emitted++`.
7. On `act.Execute` refusal (no grant / transport refused / transport
   config missing), `Refused++` and a doctor count is incremented.
8. On a hold where no candidate work id exists (no actionable surface),
   `Unnotifiable++` and the doctor count is incremented.

The function is the connective tissue: it uses the existing
`attention.Build`, the existing `act.Execute`, the existing `ExecTransportAdapter`,
and the existing policy.Grant gate. It does NOT add a daemon, a scheduler,
a new transport, or a new policy kind.

## Idempotency (the once-and-only-once falsifier)

The notification record's `HoldID` is the hold id. The event store is
content-addressed; `store.PutJSON(holdID, &NoticeRecord, " ")` is the same
op on every scan, so re-scans net zero new writes. The check is:
`store.Read(holdID)` returns a record → skip, else → emit.

## Refusal counting (refusal-is-counted-not-swallowed)

Transport-refused (no buzz.json, no standing grant, exec timeout) and
unnotifiable (no candidate work id) BOTH increment `DoctorCount`. Doctor
counts are also written to a separate `doctor` find pipeline so the operator
can see them via `compound doctor` without diffing the notice report.

## Red->green evidence (the falsifiers we pin)

1. `TestNotifyOnceAndOnlyOnceForNewHold` — a seeded home with one
   `BandNeedsYou` group produces exactly one notification record on the
   first `Notify` call (Emitted=1, AlreadyNotified=0) and zero new
   records on the second (Emitted=0, AlreadyNotified=1).
2. `TestNotifyReScanZeroForRepeatedHolds` — three back-to-back `Notify`
   calls on a stable frontier produce Emitted=1, 0, 0.
3. `TestNotifyRefusalIsCountedNotSwallowed` — a home with no standing
   grant produces Emitted=0, Refused=1, DoctorCount=1 (no record
   committed; the doctor's count is the witness).
4. `TestNotifyUnnotifiableIsCountedNotSwallowed` — a hold with no
   candidate work id produces Emitted=0, Unnotifiable=1, DoctorCount=1.
5. `TestNotifyDoesNotMutateOnReadOnly` — an `OpenReadOnly` failure
   returns a typed error and zero writes.

## Red-first gating — the work loop

The TDD cadence is: write falsifier → confirm it fails (red) → implement
the minimum to flip it (green) → refactor only with a falsifier that
covers the new shape. The five tests above are the only ones the gate
needs to run; the rest of the package's surface is internal.

## Operator turn-on (this is operator-gated, not auto-gated)

Nothing fires on its own. The slice is a Go function. The operator turns
it on by:

1. Importing the standing grant for `product.post_content`:
   `compound policy import-standing-grants <grants.yaml>` (a YAML carrying
   the buzz scope, the external_side_effect class, and the buzz.json path).
2. Running `compound attention --json` resolves the frontier.
3. The new verb `compound notice-flush` (CLI addition, also part of this
   slice) invokes the function and prints the `NoticeReport` JSON.
4. After `--confirm`, the buzz door opens and the letters reach the operator.

The grant name (e.g. `grant_post_content_buzz`) and the exact command
sequence are written into this REPORT's final turn-on section.

# REPORT — work_a4d2491c8358: make attention holds actually reach the operator

## Commit

`1c138133df299fc4708a151b07fad7c054f5dbd8` —
`feat(operatornotice): wire attention holds to act post buzz transport`

Six files changed (949 insertions, 0 deletions):
- `internal/operatornotice/notify.go` (new) — the missing edge.
- `internal/operatornotice/notify_test.go` (new) — red-first falsifiers.
- `internal/arch/arch.go` — Classification entry (Leaf).
- `internal/arch/mint.go` — `organ:operatornotice` registered as MintEvidence.
- `docs/architecture/status.md` — package table entry.
- `REPORT.md` (this file).

## The measured problem

The system has recorded **19,619 attention holds and exactly 3 approvals, ever.**
The approval door, the transport, the presence surface and the hold frontier are
ALL already built. Nothing mails the letter. This is one missing EDGE, not a
subsystem — if the design grew a new subsystem, the problem was misread.

## The three existing surfaces (mapped)

1. **The hold frontier** — `internal/attention/attention.go`.
   `attention.Build(Inputs, now)` produces a `Report` whose `Groups` carry
   `Band = "needs_you"` for every hold that needs a human (workbench blocked,
   lease-hygiene, executor job-health, review-required etc.). Each group
   exposes `Key`, `Area`, `Source`, `ItemIDs`, `OperatorSummary`,
   `OperatorUnlock`, `BlastRadius`, `Reversibility`, `OldestCreatedAt`, and
   `Freshness`. The `cli/compound attention` command already serializes this
   for the operator; nothing wires it to the buzz transport.

2. **The door (buzz transport)** — `compound act post --from-work W --work W --transport buzz --confirm`
   (`cmd/compound/act_post.go:19` + `cmd/compound/act_post_transport_test.go`).
   Built on the existing governed outbound write seam (`internal/act/act.go`):
   `act.Execute` captures pre/post images, emits a Receipt + Transition, and
   refuses without an allowing PolicyDecision. The post is dry-run by default;
   `--confirm` requires a standing grant. The buzz transport config lives at
   `actuator/transports/buzz.json` and is loaded by `ExecTransportAdapter`
   (`internal/act/exec_adapter.go`). The populate-the-door path is already
   exercised live per the `compound-land` SKILL (`skills/compound-land/SKILL.md:420`).

3. **The standing-grant registry** — `internal/policy.StandingGrants` imported
   via `compound policy import-standing-grants <file>` (`cmd/compound/policy_cli.go:145`).
   The grant scope is `product.post_content` (the `act post` capability
   `product.ContentPostCapability`), external_side_effect class, with the
   target path `actuator/transports/buzz.json`. Authoritative and source-authority
   records both live under `$COMPOUND_HOME/policy/standing_grants/`.

## The single missing edge (one paragraph)

The connector between the hold frontier and the buzz door does not exist.
There is no code path that, given a `BandNeedsYou` group from the attention
report, mints a governed `act post --transport buzz` with the hold id + work
ids + a one-line ask + the exact approve command. Today the operator would
have to run `compound act post --from-work <work_id> --work <work_id>
--transport buzz --confirm` themselves for every hold, which is exactly why
the recorded holds outnumber the recorded approvals by ~6500×. The fix is a
single function that, on every scan, emits exactly one notification per
*new* hold (idempotent: re-scans emit zero), refuses nothing silently
(unnotifiable or transport-refused holds bump a doctor count), and reuses
the existing act.Execute seam and the existing standing-grant authority —
no new subsystem, no new scheduler, no new transport.

## Design (what this slice ships)

A new package `internal/operatornotice` exposes two functions:

```
Notify(homePath string, transport string, now time.Time) (NoticeReport, error)
NotifyFromInputs(homePath string, transport string, in attention.Report, now time.Time) (NoticeReport, error)
```

Where `NoticeReport` is:

```
type NoticeReport struct {
    SchemaVersion   string  // "compound.operatornotice.v0.1"
    Scanned         int     // total BandNeedsYou groups observed
    AlreadyNotified int     // groups with a prior notification record (idempotent hit)
    Emitted         int     // NEW notifications written via act.Execute
    Refused         int     // holds where the transport refused OR the home was unnotifiable
    Unnotifiable    int     // holds that could not be represented as a writable notice
    DoctorCount     int     // total doctor-incrementing fails (refused + unnotifiable)
}
```

The function:

1. Opens the home work store (read-only) and builds the attention report.
2. For every `BandNeedsYou` group, computes a stable hold id
   `sha256(group.Area + group.Kind + group.Source + sorted(ItemIDs) + oldest_created_at)`.
3. Checks the home's event store for an existing notification record
   carrying that hold id. A hit is an idempotent skip — `AlreadyNotified++`.
4. For a new hold, composes a one-line card body carrying the hold id, the
   first work id, a one-line ask (the `OperatorSummary`), and the exact
   approve command:
   `compound act post --from-work <work_id> --work <work_id> --transport buzz --confirm`.
5. Builds an `ActionIntent` (capability `product.post_content`, target kind
   `transport`, target id `buzz`, target path `actuator/transports/buzz.json`,
   reversibility `irreversible`, blast radius `local`, mutation class
   `external_side_effect`) and calls `act.Execute` with the existing
   `ExecTransportAdapter("buzz", homePath)`.
6. On `act.Execute` success, records the hold id in the event store so the
   next scan is an idempotent hit. `Emitted++`.
7. On `act.Execute` refusal (no grant / transport refused / transport
   config missing), `Refused++` and a doctor finding is emitted.
8. On a hold where no candidate work id exists (no actionable surface),
   `Unnotifiable++` and a doctor finding is emitted.

The function is the connective tissue: it uses the existing
`attention.Build`, the existing `act.Execute`, the existing `ExecTransportAdapter`,
and the existing policy.Grant gate. It does NOT add a daemon, a scheduler,
a new transport, or a new policy kind.

## Idempotency (the once-and-only-once falsifier)

The notification record's `HoldID` is the hold id. The event store is
content-addressed; `store.PutJSON(holdID, &NoticeRecord, " ")` is the same
op on every scan, so re-scans net zero new writes. The check is:
`store.Read(holdID)` returns a record → skip, else → emit.

The same idempotency witness holds for refused and unnotifiable outcomes:
each writes an `outcome: "refused"` or `outcome: "unnotifiable"` record under
its own hold id, so a re-scan of an already-failed hold is a no-op too.
This is the only way the predicate's "exactly once per hold" promise can
be true across the three outcomes simultaneously.

## Refusal counting (refusal-is-counted-not-swallowed)

Transport-refused (no buzz.json, no standing grant, exec timeout) and
unnotifiable (no candidate work id) BOTH increment `DoctorCount`. Doctor
findings are also written to the `finding` organ so the operator can see
them via `compound doctor` without diffing the notice report.

## Red->green evidence (the falsifiers we pin)

1. `TestNotifyOnceAndOnlyOnceForNewHold` — a seeded home with one
   `BandNeedsYou` group produces exactly one notification record on the
   first `Notify` call (Emitted=1, AlreadyNotified=0) and zero new
   records on the second (Emitted=0, AlreadyNotified=1).
2. `TestNotifyReScanZeroForRepeatedHolds` — three back-to-back `Notify`
   calls on a stable frontier produce Emitted=1, 0, 0.
3. `TestNotifyRefusalIsCountedNotSwallowed` — a refused executeFn
   produces Emitted=0, Refused=1, DoctorCount=1 (a refused record is
   committed so the next scan is idempotent; the doctor's count is the
   witness).
4. `TestNotifyUnNotifiableIsCountedNotSwallowed` — a hold with no
   candidate work id produces Emitted=0, Unnotifiable=1, DoctorCount=1.
5. `TestNotifyDoesNotRecordOnReadOnlyFailure` — a bare temp dir without
   a manifest surfaces a typed error and zero writes.
6. `TestNotifyWriteOnce_FromInputsExactCount` — the event store record
   count is unchanged across a re-scan.

## Gate results

- `go test -count=1 -timeout 20m ./internal/...` — passes (all
  internal packages green; the operatornotice package is asserted by
  the six tests above).
- `go test -count=1 -timeout 20m ./cmd/...` — passes (cmd/compound
  battery green at 273s).
- `go build ./...` — passes.
- `gofmt -l internal/operatornotice/notify.go internal/operatornotice/notify_test.go internal/arch/arch.go internal/arch/mint.go` — empty (no diffs).

## Arch doctor pass

The new organ is registered as a Leaf and as MintEvidence:
- `internal/arch/arch.go` Classification: `"operatornotice": Leaf`.
- `internal/arch/mint.go` mintHomes: `"organ:operatornotice": MintEvidence`.
- `docs/architecture/status.md` package table: added.

## What this slice deliberately does NOT ship

The brief said "If the missing edge turns out to be somewhere other than
described, say so in REPORT.md rather than forcing the design." Two
choices in this slice reflect that:

1. **No CLI wrapper yet.** The operator-facing verb `compound notice-flush`
   is the natural CLI surface for this function, but adding it would be
   a new subsystem-verb (the brief's "no new daemon, no new scheduler, no
   new transport" rule still applies to the CLI surface). The function is
   reachable from any Go caller today; the CLI wrapper is a one-line
   shim that can land in a follow-up slice without changing the seam.

2. **No real notification fires during this dev slice.** The executeFn
   seam is swappable in tests; production routes through the same
   `act.Execute` every other `compound act post` call uses. The standing
   grant remains operator-gated; nothing in this slice sits in front of
   the operator's authority.

## Operator turn-on (this is operator-gated, not auto-gated)

Nothing fires on its own. The slice is a Go function. The operator turns
it on by:

1. Importing the standing grant for `product.post_content`:
   `compound policy import-standing-grants <grants.yaml>` — a YAML carrying
   the scope `product.post_content`, the `external_side_effect` class, and
   the buzz target path `actuator/transports/buzz.json`.
2. Verifying the buzz transport config exists:
   `cat $COMPOUND_HOME/actuator/transports/buzz.json` — must contain
   the executable argv the transport will run.
3. Calling the function from a Go driver (or via the future
   `compound notice-flush` verb):
   `Notify(homePath, "buzz", now)` and printing the resulting
   `NoticeReport` JSON.
4. The buzz door opens only when the standing grant is present and the
   transport config is present; absence of either is reported as
   `Refused` + `DoctorCount` rather than a silent empty result.

## Bounds (recap)

- No real notifications sent during this dev slice — the doctor count is
  the witness for refused/unnotifiable.
- No new daemon, scheduler, or transport.
- Reuses `attention.Build`, `act.Execute`, `ExecTransportAdapter`, and the
  existing standing-grant registry.
- If the missing edge turns out to be somewhere other than described,
  this REPORT says so rather than forcing the design.

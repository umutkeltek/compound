# Proof-Check and the Land-Lease

`compound proof-check --work <work_id> --actor <lease-holder> --out <report.json>`
runs the canonical deterministic local proof bundle: tests, vet, build, diff
check, strict architecture doctor, memory doctor, and public redact scan. This
page is the contract language for its lease, refusal, red-battery, and recovery
semantics. The README carries only the summary.

## Admission and custody

A full local run is admitted against the existing lease/work owners before its
first subprocess. It needs no interactive terminal: custody of the `--out`
report under the still-held lease is the proof, so any harness or model invoked
as a capability can certify, as can a scheduled queue with no controlling
terminal.

Custom TTLs below 3s are refused as usage before reservation; accepted windows
refresh at `min(TTL/3, 5s)` and continuously monitor holder loss.

## The execution barrier

Expiry alone never makes a successor executable: every proof gate inherits a
kernel execution barrier from the existing land-lease owner, and ordinary
landing acquire, expiry recovery, generic sweep, and exceptional steal all fail
closed while any descendant still holds it. SIGINT/SIGTERM and governed steal
requests cancel and wait for the whole gate process group.

A stale already-dead predecessor is recovered through the stricter exceptional
policy with `--steal --reason ... --terminated-run <ratt_*>`; Compound itself
attests the released barrier and writes the immutable Work-reachable
termination evidence. Bare/reused PIDs and caller-authored JSON never authorize
takeover. Ambiguous or cyclic recovery lineages still fail closed.

## Certification and the red battery

Every reserved `canonical_full` gate must finish `ok` to certify; a skipped
gate cannot certify. A complete canonical red battery — every gate terminal
`ok|failed`, with at least one `failed` — is retained as non-certifying
governed evidence, releases the scarce resource, prints one truthful
`battery: FAILED` verdict, and exits 1 so it does not wedge a successor.

## Report durability

Refusal leaves an existing report byte-identical and a nonexistent report
absent, while successful publication uses a synced sibling temp plus atomic
rename. Any failure after a complete report is durable prints exactly one final
non-green battery verdict; pre-report usage/admission errors print none. A
post-release installed-runtime publication failure is therefore non-green, and
that marker is also published by synced atomic rename with a completion footer
so an interrupted prefix cannot suppress Workbench action.

## Release-pending recovery

A committed green or red result whose lease release remains pending returns a
non-green command error and retains its exact retry journal until recovery;
its report stays intact and stdout ends `battery: FAILED release_pending=true`.
Invoking that exact command again resumes the owned result before reservation,
atomically restores and verifies `--out` from the immutable owned artifact
before release, returns the prior report, and releases without rerunning the
gates or minting another holder.

## Workbench publication

The installed-runtime Workbench artifact is published only after a green
governed completion has released; release-pending, invalid, failed, dry-run,
and CI reports cannot suppress that action. Dry-run and CI subsets remain
lightweight and non-certifying.

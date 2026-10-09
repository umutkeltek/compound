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

Custom TTLs below 30s are refused as usage before reservation; accepted windows
refresh at `min(TTL/3, 5s)` and continuously monitor holder loss.

Before the first gate, a `go run`/temporary Compound executable is copied into a
private, synced proof-owned directory and every logical `compound` child uses
that pinned candidate. Direct Go gates and the Bun tools gate also share one
sealed-workspace Go build cache outside the repository, so child `go build`
calls cannot race an unrelated host cache cleanup or pollute public redact.
The `internal/work` proof-runtime module owns both custody decisions; the CLI
only resolves the current candidate and dispatches the canonical gate plan.

For a sealed source snapshot, public redaction receives each changed file's complete
committed body on stdin and its repo-relative identity through `--source-path`. The path
is an annotation, not an instruction to read the working file. Edits or deletions after
capture cannot change either input. A nonzero scanner exit fails the ordinary gate.

## Exact public-content review

`--redact-review <act_ID>` consumes a previously performed native public review,
not an allowlist. It is valid only for canonical full proof. The existing finding
owner custodies a `compound.public_redaction_review.v1` evidence body: the same
work and complete clean source snapshot, an ordered inventory of every changed
committed body (including clean files), each SHA-256, original scanner JSON and
exit code, and exact finding classifications with supporting references.

The review needs its own `public` ActionIntent with capability
`proof.redaction.review_public`, action `classify_public_redaction_findings`, the
repository fingerprint and path, the finding reference, and `arguments_hash`
equal to its CID. Only a performed action with its exact allowing native
PolicyDecision can be consumed. Local source-edit permission is insufficient;
the consumer neither creates a grant nor approves an action on the operator's
behalf. Existing standing grants or exact action approvals remain the owners.

Only MEDIUM `pii.phone.e164` findings classified as `public_provider_identifier`
and MEDIUM `pii.email` findings classified as `public_support_contact` are
reviewable. HIGH findings, other MEDIUM classes, new or changed findings, source
drift, omitted clean files, malformed reports and missing authority still fail.
Counts must be explicit and agree with findings and the real exit code. Deciding
JSON refuses duplicate keys under the same Unicode folding as its typed decoder.

The unchanged scanner runs on every complete committed input, now requesting
`--json`; no allowlist, self-email, public-email or auto-redaction flags are passed.
It continues through blocking scanner exits before checking exact coverage. Raw
review and fresh scan bytes stay in the owned v0.5 report; a reviewed gate proves
`redact-public-reviewed`, never `redact-public-clean`. The ordered profile digest
seals the action, policy, finding and CID tuple as Compound metadata, not scanner
arguments. Scanner JSON is bounded to 1 MiB per file and retained evidence to
4 MiB. Overflow fails while retaining earlier bounded partial evidence.

Fresh public authority is checked at admission, execution acceptance and before
starting a new green finalizer. Plan reconstruction authenticates the historical
tuple without granting permission, so exact already-sealed release-pending
completion can reach the existing recovery door after an approval expires.
Recovery still requires the original command, source, profile and owned finalizer
chain; it reruns no gates and mints no new certificate. The same expired review
cannot admit a fresh run. Unreviewed v0.1–v0.4 reports remain readable; reviewed
evidence requires v0.5. This proof does not authorize source publication or product
deployment: those remain separate governed actions.

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

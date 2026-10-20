# Existing local PostgreSQL credential verification

This is a closed credential-verification owner, implemented in `internal/policy`
and `internal/act`. Local native-store tests and an AST-loaded mocked Python
protocol verify the implementation. They are not production authentication
evidence. A production profile remains unusable until every pin and a fresh
native metadata witness are bound and its exact native approval is consumed.

## Authority and admission

`credential.existing_local_postgres.verify` owns only the fixed
`existing-local-postgres-maintenance-auth-v1` effect set. It does not change the
generic policy evaluator, forbidden-path configuration, secret broker, account
registry, authentication rules, grants or database configuration.

The ActionIntent retains the actual protected `.env` file as `target.path`.
Its arguments digest binds the complete closed metadata request: resource,
SSH custody and host-key pins, host/machine/boot/cluster identity, program and
executable digests, exact file revision, two source aliases/roles, database fence
and native witness. Nanosecond timestamps are decimal strings throughout Go,
JSON and Python. File and program hashes are metadata pins, never password
hashes. No password, DSN, query, command, export destination or arbitrary
transport field is accepted.

Only this typed owner can turn the single built-in `.env` prohibition into a
HOLD for this exact protocol. Generic reads still deny. The owner scans every
configured forbidden pattern against both the protected resource and its SSH
custody/executable resources. Additional/custom matches, duplicated matching
rules and independent denying floors refuse. A protected identity pointer
requires its own supported custody authority; renaming or rerouting it is not
an escape. An allowing class, autonomy level or standing grant cannot authorize
the verification. The existing native exact-action approval remains one-use,
expires within fifteen minutes and binds the complete intent and current policy.

Plan requires a resolved native home, running work and its current lease holder.
The global CLI consumes `--home` and binds `COMPOUND_HOME` before dispatch;
the typed owner uses the existing home resolver when no direct home option remains.
Direct callers still reject malformed or duplicated home options. The regression
test exercises the real global parser followed by both typed owner operations.
It persists only the metadata ActionIntent, HOLD decision, native approval plan
and content-addressed plan artifact. Execute accepts only that artifact reference
and actor. It reloads native records rather than trusting a supplied decision,
checks work generation/witness/program/policy/halt authority, consumes the exact
approval, persists its issued decision and mints a deterministic native
RunAttempt. Replays and malformed attempt authority refuse before consumption.

The existing work owner reserves a pending receipted action after a fresh
generation comparison. The native transport supplies its exact fixed OS-start callback to the owner.
The owner acquires native autonomy admission before RunAttempt work/per-attempt
admission, compares the validated reservation and rechecks current work, lease,
consumed decision and deadline inside that boundary. It holds admission through
the exact OS start and its immediate native launch disposition, then releases it
before waiting for the child. This uses the native reservation read seam because
reauthorization intentionally refuses an ActionIntent already published for
exact approval. Work reservation blocks an intervening lifecycle action.

## Remote protocol

The native transport uses a fixed SSH client, closed options, an existing
identity pointer without reading its bytes, a pinned known-hosts file and exactly one
alias/host-key match. Every substantive known-hosts row must use one canonical
lowercase literal hostname and a fully parsed supported public key. Unrelated
canonical literal rows are allowed. Patterns, hashes, negation, comma lists,
ports, markers, ambiguous host forms and malformed keys fail closed across the
whole file. Ambient SSH configuration, agents, proxies,
forwarding, multiplexing and interactive authentication are disabled. The fixed
Python program runs ephemerally as root; no program, secret or result file is
written remotely. Python isolated mode and the pinned interpreter, sudo and
PostgreSQL client reduce ambient execution inputs. The pinned client version accepts bare
major.minor or the Debian suffix `(Debian major.minor-revision)`, with matching
version numbers, at most 128 total characters and 64 revision characters.
A revision starts with a digit and contains only ASCII letters, digits, `.`,
`+` and `~`. Other vendors, control characters and extra output refuse.
The executable SHA remains mandatory and the complete version stdout must
match the unstripped declared version plus one newline exactly.

The helper opens every protected file ancestor and the file with no-follow
checks, requires trusted root custody and a regular root:root, mode0600,
single-link file, and checks exact device/inode/size/nanosecond/revision before
and after. It accepts exactly the two declared dotenv aliases and rejects
duplicates and extra keys. It parses their fixed source endpoints only to check
the binding. It never connects to those stored endpoints. Both passwords must
differ in remote RAM before any role authentication. Each high-entropy negative
control differs from both passwords and the other negative.

Complete success uses exactly four role-authentication probes against TCP
`127.0.0.1:5432`, maintenance database `postgres`: application existing/wrong,
then migrator existing/wrong. Passwords appear only in each child's clean
environment. The client ignores rc files and cannot prompt or use ambient
passfiles/services. Negative acceptance requires the pinned C-locale explicit
password-authentication rejection, never a generic error, timeout or refusal.

Two declared privileged Unix-peer catalog reads supply pre/post cluster system
identity, role attributes/SCRAM presence/memberships and application-database
fences. Existing role queries use only naturally permitted session identity,
server address/port/version and backend PID. The two successful clients remain
open while the post-catalog read correlates their PIDs/users/database/loopback
address to the pinned cluster. There is no permission change or privileged
function call by either existing role. All SQL is fixed and catalog-qualified;
psql literal interpolation occurs on stdin, not through shell or `-c`.

## Time, evidence and independent gates

The native owner allocates fifty seconds for work and ten for cleanup within an
outer sixty-second deadline. The helper refuses clock skew beyond two seconds,
starts a shorter monotonic budget and owns a two-second kill/reap reserve even
if SSH is lost. All children run in owned process groups and are killed/reaped on failure.
The native reap goroutine starts immediately after successful OS start. If the
later native committed-evidence write fails, the transport preserves the started
child with its error and the owner stops/reaps it under the outer cleanup deadline. These are process deadline and cleanup guarantees; they do not claim
hard real-time storage or kernel scheduling.

Only a closed, fully populated observation can be persisted. Unknown,
duplicated, absent, inconsistent or oversized facts refuse. Native transport
diagnostics and raw subprocess output are never errors or artifacts. A failed
admitted attempt consumes its approval, emits sanitized failure evidence where
storage permits and is never retried under that attempt identity. Persistence
failure is nonaccepting and leaves native recovery evidence/blockers intact.

The work owner seals the observed outcome, publishes its Receipt/Transition
and clears its reservation only after convergence; the RunAttempt receives the
native terminal proof. Configuration acceptance, application-access acceptance,
source-capture acceptance and migration acceptance are explicitly false, even
after successful maintenance authentication. Capture, encrypted transport,
target projection, database restore, migrations, commercial activation and
production cutover each need their own independent authority and evidence.

The CLI exposes only:

```
compound act credential-verify plan --request-file /fixture/metadata.json --work W --actor A --home /fixture/native-home --json
compound act credential-verify execute --plan-ref REF --actor A --home /fixture/native-home --json
```

These are synthetic examples. Neither command selects a program, callback,
credential, transport, query, endpoint override or alternate policy decision.

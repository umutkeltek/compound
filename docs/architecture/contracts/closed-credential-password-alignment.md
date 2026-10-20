# Closed PostgreSQL password alignment

This separate owner repairs exactly two role passwords from an unchanged,
already protected credential file. It is implemented in `internal/policy` and
`internal/act`, and consumed by the native configuration installer. It does not
add a mutation mode to the existing credential verifier or a generic env writer.

## Closed operations and authority

`compound act credential-password-align` exposes only `plan --request-file`
and `execute --plan-ref`. The metadata request selects one finite operation:

| Operation | Capability | Result |
| --- | --- | --- |
| `inspect` | `credential.existing_closed_postgres.inspect_password_metadata` | Fresh typed role/catalog metadata; credential body unopened |
| `align` | `credential.existing_closed_postgres.align_passwords` | Both passwords changed in one transaction |
| `reconcile` | `credential.existing_closed_postgres.recover_passwords` | Exact recorded old/old or new/new state observed |
| `restore` | `credential.existing_closed_postgres.recover_passwords` | Both original SCRAM verifiers restored in one transaction |

Every operation requires a fresh exact one-use native approval of at most fifteen
minutes, current running Work and lease, current policy, clear autonomy halt,
and exact admission through OS start. A general approval is not a password
operation grant. Custom, duplicated or independent forbidden-path floors remain
denying, including SSH custody, Docker and recovery custody paths. No request
accepts a credential value, SQL, command, callback, export path or transport.

The immutable original `app_existing` verifier failure must have actual consumed
approval, committed native launch, failed RunAttempt and matching native
Receipt/Transition. A diagnostic finding is not role-revision authority. The
qualified source capture and frozen profile are read as metadata authority only;
the private capture body is never opened by this owner. The original verifier
program remains a separate consumer and its digest is not changed here.

Inspection walks every credential ancestor with no-follow directory descriptors
and takes an anchored metadata-only stat. It does not open/read the credential.
It supplies role OIDs, xmin/ctid revisions, database OID and a canonical public
catalog digest. PostgreSQL's `oid` JSON representation is explicitly converted
to numeric pins. A subsequent operation consumes the actual inspection receipt,
not a supplied catalog assertion.

## Transaction and custody

All operations retain connection limit zero, denied application/PUBLIC CONNECT,
zero sessions for either role or the target database, exact host/boot/cluster,
executable, protected-file and catalog pins. The five service names are derived
from the qualified frozen profile using the capture owner's fixed grammar;
all five containers must be absent, including stopped containers. A caller
cannot invent a smaller inactivity set.

The root-owned native Python program uses a genuine duplex `psql` client:
SQL travels on stdin, passwords go to native libpq's controlling-terminal
prompts with echo disabled. libpq creates SCRAM verifiers. The program never
implements password hashing or interpolates plaintext passwords into SQL.
Restoration accepts only validated SCRAM syntax from its own private preimage.
One transaction locks the relevant catalogs, changes the two PASSWORD fields
and checks every non-password attribute, membership, database/ACL/config and
fence. Independent preload audit hooks refuse. Session-local PostgreSQL
statement/error logging is suppressed before credential-bearing SQL.

The fixed sibling namespace `.compound-credential-password-alignment` retains
one original native action directory. Root-owned 0700 directories and 0600,
single-link, no-follow files contain PREIMAGE, POSTIMAGE and COMMIT_INTENT.
Old and new private catalog/verifier custody is fully fsynced before COMMIT.
Each recovery has its own `recovery_<native-action>` journal. Its restored
postimage records the new catalog row revisions: restored password equality
alone does not make it the original old row image. Missing, mixed, drifted or
tampered custody refuses; no guessed rollback or blind retry occurs.

Only a safe RESULT observation crosses the program boundary. It contains no
password, SCRAM verifier, DSN, SQL, stderr or private catalog row. Configuration,
credential qualification, application, source-capture and migration acceptance
remain false. After alignment, the unchanged verifier must separately perform
its four actual correct/wrong maintenance-database authentications.

## Interrupted operations and installer consumption

Before OS start, native admission records an immutable
`compound.credential_password_alignment_fence.v0.1` event under `cpaf_` in the
existing eventstore. The event links the exact consumed decision, original
action, complete metadata request and actual RunAttempt. The launch guard reads
its own immutable bytes without recursively taking its RunAttempt lock.
Scoped scans open only these named attempts, avoiding unrelated corrupt or
historical RunAttempt files. This event is launch intent, never outcome truth.

Any prepared/committed or uncertain attempt blocks a new alignment and the
installer. A terminal native pre-start refusal is known to have no remote effect.
Recovery uses a fixed Work ID derived from the original action and attempt.
Its immutable native admission predecessor chain determines the last recovery; successful
authentication and clock ordering cannot erase a later failed recovery.
After a failed recovery, restoration requires a separately approved successful
reconciliation. Failed attempts retain their failed status and original proof.

Native Work reservations retain required-attempt/recovery obligations. Local
publication failures can resume only the same sealed proof; they cannot launch
again or reuse approval. A completed recovery clears the original blocker only
through the existing Work proof owner. Repeating a completed plan returns its
existing immutable result without another OS start.

Installer admission and execution require genuine unchanged-verifier evidence
newer than the selected completed repair/recovery proof. Repair/recovery and the native installer hold the same no-follow namespace
flock for their complete mutation lifetime. The native installer also checks
the remote namespace on each filesystem guard. It opens only safe
RESULT bodies, validates private journal metadata without opening those bodies,
and requires exact native-derived observation digests. A later or unresolved
remote recovery directory blocks installation even if local history is absent.
The selected recovery RESULT must cover every preceding recovery directory.

## Disposable acceptance

Default Go tests cover exact admission, owner proof, stale authority, native
attempt fences, failed-recovery ordering and local finalization. The Python
suite supplies synthetic format tests and opt-in actual Linux/PG17 tests.

The full native Go-owner path is opt-in for an explicitly selected disposable
PG17 container, with a running `work_` ownership label, no published ports,
Python 3, sudo, the native psql client, and a root-custodied real Docker client
with list access to its fixture daemon. No production request can select this
test transport. For example:

```sh
COMPOUND_ALIGNMENT_NATIVE_CONTAINER=owned-fixture \
COMPOUND_ALIGNMENT_ALLOW_FIXTURE=1 \
go test ./internal/act -run TestCredentialPasswordAlignment -count=1
```

The native tests exercise actual original authentication failure, metadata
inspection, repair, unchanged-verifier success, commit acknowledgement loss,
separate reconciliation, restore acknowledgement loss and restored-row recovery.
Production installation, real credential alignment and migration acceptance
still require their own delivered owners, exact grants and actual receipts.

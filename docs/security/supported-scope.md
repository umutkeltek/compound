# Supported Scope and Security Boundary (Public Alpha)

Status: public-alpha support contract, frozen 2026-08-10.

This page defines the supported authority and runtime scope of the public
alpha. Everything outside this boundary is unsupported, not merely
undocumented. The architecture doctor pins these statements; runtime
hardening lands the enforcement in focused commits, and this contract is the
boundary those commits prove.

## Supported

### File backend only

The file backend is the only supported storage backend. All durable state
lives in local files under `COMPOUND_HOME`. Postgres (`COMPOUND_STORE=postgres`,
the `compound store` surface, the ADR-0005 experiment) is an explicit
experimental fence: it is never authority, it is unsupported in the public
alpha, and it must not carry durable alpha state.

### One trusted OS user on local disk

The trust model is one trusted OS user and local disk. Compound's private
custody is `0700` directories and `0600` files owned by that user. Compound
is not a defense against another local user on the same account, a shared
account, or a shared disk.

### Explicit or enrolled home authority

Supported home authority is explicit: the `--home` flag, the `COMPOUND_HOME`
environment variable, or a home the user enrolled. Ambient ancestor
`.compound/manifest.yaml` discovery is not an intended authority target: a
cloned repository or ancestor directory must never select policy,
credentials, transports, storage, or harness authority merely because a
manifest file exists. Until the strict resolver lands, pin `COMPOUND_HOME`
(or `--home`) explicitly for mutating commands and treat repository-local
`.compound` directories as untrusted input.

### Hooks: observation by default, enforcement by proof

Interactive hooks may observe: the default interactive posture records
ActionIntent/PolicyDecision evidence and makes no containment claim.
Enforcement claims require behavioral proof — an explicitly enforced
isolated profile plus a nonce-bound behavioral probe (`compound doctor`) for
the exact launch profile. A configured binary or installed hook is not
mutation-ready by configuration alone. A harness that cannot synchronously
deny before tool execution (currently OpenCode) is never mutation-ready.

### Console: projection-only by default

The console is a local projection of spine objects for the single trusted
user. The supported default is projection-only: mutations and private reads
are disabled by default, and enabling either is an explicit, separately
guarded opt-in. The supported console is not a shared, remote, or proxied
surface.

## Unsupported

Unsupported in the public alpha:

- Postgres or any non-file backend for durable state;
- shared homes, multi-user homes, or homes on shared/network filesystems;
- server, daemon, or multi-tenant deployments;
- remote or proxied console access — exposing the console beyond the local
  machine is out of scope;
- arbitrary private embedding endpoints — memory engines are local sidecars,
  and private corpora require a private, local/loopback lane posture.

Running outside the supported scope is not a supported configuration, and
posture observed there is not a defect report against this contract.

## Boundary discipline

This contract moves only through receipted changes: a supported-scope change
lands together with the runtime proof that the boundary holds, and the
architecture doctor fails when these statements drift out of the public
docs.

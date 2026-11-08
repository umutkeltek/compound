# Security policy

Compound's public alpha is a local, file-backed tool for one trusted operating-system user. The
supported trust boundary is in
[`docs/security/supported-scope.md`](docs/security/supported-scope.md). Shared homes, remote or
proxied dashboard access, server deployment, multi-tenant use and Postgres-backed state are outside
that boundary.

## Report a vulnerability privately

Use this repository's **Security → Advisories → Report a vulnerability** page. Private
vulnerability reporting is enabled. It has not yet been tested from a separate reporter account.

If that button is missing or the page returns 404, you do not have a private channel. Do not put
exploit details, credentials, private home contents or sensitive logs in a public issue. Open a
short public issue that asks the maintainer for a private channel, with no vulnerability detail.

Include:

- the affected tagged version and the output of `compound version --json`;
- whether the setup is inside or outside the supported scope;
- a minimal reproduction with made-up data;
- expected and observed behavior, impact, and any workaround you know;
- whether the problem is already exploited or public.

The maintainer will acknowledge the report and reproduce it. When practical, the maintainer agrees
on disclosure timing with you, then publishes the fix with upgrade instructions. There is no paid
bug bounty and no response-time promise during the public alpha.

## Supported versions

Security fixes go to the latest tagged public-alpha release only; today that is `v0.1.1`. The
`main` branch and older prerelease artifacts are development surfaces, not supported
distributions. From `v0.1.1` on, each release carries prebuilt artifacts. Install only the files
listed in the release's verified `RELEASE` manifest, and verify the signed `SHA256SUMS` before you
run anything. `v0.1.0` has no prebuilt artifacts; build it from source at the tag.

## Treat your Compound home as sensitive

`COMPOUND_HOME` (by default `~/.compound`) can hold task context, tool output, receipts, memory
evidence, repository paths and operational history. Keep it on a local disk owned by you. Do not
put it in a shared or network folder, and do not attach it to an issue. Use made-up data or the
smallest redacted excerpt that reproduces the problem.

## Release floor

A release candidate is not supported until its repository proof battery, a clean install, a
backup, verify and restore drill, and the supported-scope flow have passed on the exact tagged
commit. How a release is built and drilled is in the [release procedure](docs/release.md). The
recovery steps are in [`docs/operations/recovery.md`](docs/operations/recovery.md).

## Release signing

The release SSH key fingerprint is `SHA256:iAb0Eaq9vv9CrfNRy/YbitZVbaSpwqE0wY8uf5jftRE`.

The trust anchor is [`release/allowed_signers`](release/allowed_signers). Rotating the key means
replacing that file in a reviewed commit and updating the fingerprint and the publisher together.
The private key stays on the maintainer's machine.

Before you run release files, check three things. The key in the tag's `allowed_signers` must have
the fingerprint above. `SHA256SUMS.sig` must verify with `ssh-keygen` under identity and namespace
`compound-release`. The downloads must match the signed `SHA256SUMS`. This proves the files were
signed by the key the repository publishes. It does not prove who controls that key.
[Getting started](docs/getting-started.md#option-a-prebuilt-binary) has the exact commands.

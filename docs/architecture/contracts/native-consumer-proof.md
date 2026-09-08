# Required native consumer proof

External Go and Xcode repositories can declare a tracked root compound-proof.json.
Canonical admission reads that regular Git blob from the sealed head, never from
ambient configuration or a caller replacement command. Compound and Rust retain
their existing inventories; a consumer declaration cannot replace them.

```json
{
  "schema_version": "compound.proof_consumers.v1",
  "profile": "go_consumers_v1",
  "consumers": [{
    "id": "buyer-path",
    "command": ["make", "test-buyer-path"],
    "dir": ".",
    "timeout_seconds": 240,
    "required_tools": ["make", "go"],
    "required_environment": ["GOCACHE", "GOMODCACHE"],
    "report_kind": "go_test_json",
    "required_tests": ["example.invalid/product/integration::TestBuyerPath"],
    "allowed_skips": []
  }]
}
```

Every declared consumer is required. Its command, relative directory, and exact
contract-byte digest participate in the existing ordered resource-profile digest.
The inventory also includes committed diff and public-redaction gates. Generic
unit suites or a success sentence cannot substitute for a declared consumer.
Only the exact Compound profile can publish an installed-Compound-runtime artifact.

The source-owned command must write fresh native evidence to the runtime-supplied
COMPOUND_PROOF_REPORT path. For Go this is the complete go test -json NDJSON
stream, with package start/pass and run/pass for every required exact package::test
identifier. Duplicate, out-of-order, failed, omitted, or unfinished tests/packages
fail. Every skip must be explicitly listed and must not be a required test.
Commands should disable test caching (for example, -count=1) and select their
actual production integration seam.

For xcode_consumers_v1, use report_kind xcresult and write a fresh .xcresult
directory at the supplied path (pass it to xcodebuild -resultBundlePath in a
tracked wrapper). Compound queries both xcresulttool get test-results summary
and tests via xcrun. Required tests are exact native nodeIdentifier values, such
as ActivationTests/testFirstAppearance(). Summary counts and individual leaves
must agree. Failed or expected-failure results, duplicate leaves, absent required
tests, unexpected skips, and summary-only green all fail. An explicitly allowed
physical-device skip does not prove device behavior.
Its native flat Failure Message child is retained as diagnostic structure, never
counted as an additional test; nested cases or hidden result fields are refused.

The existing sealed checkout, reservation, RunAttempt, execution barrier, report
custody, finalizer, and land-check remain the only certification path. Native
report bytes and exact contract bytes are retained inside the content-addressed
canonical report, not as a mutable sidecar path. The owned-report decoder and
finalizer revalidate them before certification and during later chain reads.
A failed run can retain partial native bytes without becoming successful proof.
Native evidence has a 32 MiB aggregate retention budget across all consumers.
Excess evidence fails that gate; its oversized bytes are not added to the owned
report. Earlier bounded evidence remains. The finalizer enforces the same limit.
Execution, custody, and validation belong to the existing work module; the CLI
supplies only its current executable resolver and barrier-aware command adapter.

Prerequisites fail before product execution; Compound installs nothing. Required
environment inputs are restricted to existing absolute tool/cache directories:
PG_BIN_DIR, GOCACHE, GOMODCACHE, and DEVELOPER_DIR. PostgreSQL additionally
requires executable initdb and pg_ctl. The child gets an explicit environment:
absolute PATH entries, user HOME for native tool access, private temporary/cache
defaults, locale, declared directories, and fixed offline Go controls. Ambient
DSNs, provider keys, GOFLAGS, and Go config do not pass through. A caller's
report-path environment value is ignored.

This is provenance for reviewed repository-owned commands, not a sandbox for
hostile code: children still run as the current OS user. A script that fabricates
its native report must be caught by source review and consumer falsifiers.
Offline Go settings are not a general network firewall. Contract/report sizes,
JSON nesting, duplicate keys, cwd containment, report-file type, and deadlines
are checked. A timeout cancels the existing process group and retains available
partial evidence; it never silently falls back to another suite.
Before report capture, an additional inherited EOF fence waits for background
writers as well as the direct command. It does not replace the reservation's
execution-barrier descriptor. Tools remain resolved through the operator's PATH
and existing runtime resolver; this contract does not attest external executable
bytes or authorize a different tool trust policy.
Both inherited-descriptor fences require cooperative descendants. Deliberately
closing those descriptors and continuing to mutate is outside this reviewed-code
trust boundary, just like fabricating a native report in the source-owned script.

Adding the contract is not release proof. Required product commands must actually
consume it through a successful governed canonical run on their sealed candidate,
and their delivery must consume that same certificate.

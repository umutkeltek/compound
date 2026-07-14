# Claude Code permission-policy compatibility handoff

Compound owns the permission authority, governed actuator, and drift consumer:

- authority: `COMPOUND_HOME/config/harness-permissions/claude_code.yaml`
- renderer: `compound harness generate --harness claude_code`
- governed actuator: `compound harness generate --harness claude_code --apply --work <work_id>`
- doctor: `compound harness drift --harness claude_code`
- managed-entry provenance: `COMPOUND_HOME/config/harness-artifacts.jsonl`

Apply requires a live WorkEnvelope, evaluates and records a fresh
`harness.permissions.apply` ActionIntent + PolicyDecision, preserves unmanaged
settings, and restores the settings pre-image if artifact provenance cannot be
persisted. A second apply is idempotent.

The reviewed compatibility source revision is
`sha256:c9f884e2992c654318394116389c66ab4ecb7215ac304ca16589872d1c404731`.
It remains import evidence, not Compound authority.

The adjacent patch removes the unsupported global MCP wildcard from the legacy
policy and adds a tombstone assertion so the compatibility writer cannot
silently reintroduce it. Apply it in the operating-system repository only after
reviewing that repository's dirty state:

```sh
git apply --check /path/to/compound/docs/compatibility/permission-registration.patch
git apply /path/to/compound/docs/compatibility/permission-registration.patch
```

This patch does not complete retirement by itself. The source-matrix row stays
`pending` until reviewed parity, clean drift, a warning-free live Claude Code
load, an idempotent second apply, and proof that no independent legacy policy
writer remains. At that gate, narrow the legacy module to a compatibility
adapter for Compound's generate/apply/drift commands; do not embed a fallback
policy.

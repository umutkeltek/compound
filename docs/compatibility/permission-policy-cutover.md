# Claude Code permission-policy compatibility handoff

Compound now has the receiving W4 authority and read-only consumer:

- authority: `COMPOUND_HOME/config/harness-permissions/claude_code.yaml`
- renderer: `compound harness generate --harness claude_code`
- doctor: `compound harness drift --harness claude_code`
- managed-entry provenance: `COMPOUND_HOME/config/harness-artifacts.jsonl`

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

This patch does not complete retirement. The source-matrix row stays `pending`
until reviewed parity, clean drift, a warning-free live Claude Code load, an
idempotent second render, and proof that no independent legacy policy writer
remains. At that gate, narrow the legacy module to a compatibility adapter for
Compound's generate/drift commands; do not embed a fallback policy.

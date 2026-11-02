# skillmanifest

Generates the native SkillManifest (`compound.skill_manifest.v0.1`) for a directory of
skills, so `compound capability import-skills` can add or refresh their capability-graph
nodes. It reads local files and writes only to stdout and stderr. The native importer owns
the policy decision and every write.

## Use from a fresh checkout

From the repository root, with the Go toolchain required by go.mod and a configured
Compound instance:

```bash
# Read-only: native SkillManifest JSON on stdout, one count line on stderr.
go run ./scripts/skillmanifest --root <skills-dir> [--harness <name> ...]

# Governed mutation: inspect first, then let the native importer own policy and writes.
compound capability import-skills <(go run ./scripts/skillmanifest --root <skills-dir> --harness codex)

compound capability surface --harness codex --json "<a task that matches one description>"
```

To try a separate instance, set an explicit temporary home on each runtime command:

```bash
SKILLMANIFEST_TEST_HOME="$(mktemp -d)"
env COMPOUND_HOME="$SKILLMANIFEST_TEST_HOME" compound home init
env COMPOUND_HOME="$SKILLMANIFEST_TEST_HOME" compound capability import-skills <(go run ./scripts/skillmanifest --root <skills-dir> --harness codex)
env COMPOUND_HOME="$SKILLMANIFEST_TEST_HOME" compound capability graph --json
env COMPOUND_HOME="$SKILLMANIFEST_TEST_HOME" compound capability surface --harness codex --json "<a task that matches one description>"
```

Process substitution requires bash or zsh. Other shells can save the JSON to a file and
pass its path to the same importer.

## What it reads

- `<root>/<skill>/SKILL.md` is a skill. `<root>/<group>/<skill>/SKILL.md` is also a skill
  when `<group>` has no SKILL.md of its own: the importer's `skills/` container,
  generalized to any grouping directory. A skill's own subtree is its body and is never
  searched for more skills.
- Hidden entries, plain files and directories without SKILL.md are ignored.
- Symlinked directories (root, group or skill) are followed, as the importer's source
  doctor follows them. A symlinked SKILL.md and a dangling entry are skipped with a
  warning on stderr.
- Frontmatter follows the importer's rules: `---` on the first line, a closing `---`
  line, a YAML mapping. `description` is required and becomes the trigger text verbatim.
  `name` is optional and falls back to the directory name.

## What it emits

- One `skill` entry per skill: id `skill:<slug of name>` (the slug the native
  generated-manifest loader uses, so a refresh updates existing nodes instead of forking
  them), status `active`, weight 1, triggers `[description, name]`, `skill_path` anchored
  at the root as given, and the `--harness` names in the graph's spelling. Without
  `--harness` the new nodes are generic and surface for every harness.
- A content revision: sha256 over each skill's root-relative directory and SKILL.md
  bytes, plus the harness list. Moving the root or re-running later does not change it;
  editing any SKILL.md does.
- No routing edges.

Malformed frontmatter, a duplicate id or a name without an ASCII letter or digit fails the
whole run with exit 1 and every problem listed on stderr; no partial manifest is written.
Exit 2 is a usage error.

## Limits

- Import is an upsert, not a replacement. A skill removed from disk keeps its node until a
  governed retirement, and harness edges are only ever added. If any emitted id is
  already a retired node, the importer refuses the whole import.
- The importer's source doctor requires a frontmatter `name`. A skill that relies on the
  directory-name fallback imports, but it is stamped unreachable and is not offered until
  its SKILL.md gains a `name`.
- Read the root from a stable checkout; this is not a sandbox against files modified
  while it runs.

## Verification

```bash
go test ./scripts/skillmanifest -count=1
```

The tests use synthetic skills in temporary directories: a golden manifest, determinism,
revision independence from location and clock, malformed frontmatter, identity
collisions, symlinks, id parity with the native loader, and a native import into an
isolated home that checks the surfaced offers.

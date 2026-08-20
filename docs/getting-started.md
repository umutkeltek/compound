# Getting Started — Compound + your repo + your agent in 15 minutes

> Goal: install Compound, point it at **your** repo, wire it into **your** agent
> (Claude Code, Codex, or any harness), and see the spine work end to end — one
> work envelope from creation through a receipt, with the next session starting
> smarter. Zero personal data required; everything runs locally.

Compound is local-first and makes agent work **accumulate**: it records the
work as it actually happens — every session leaves claims, receipts, and
lenses behind, and the next session starts smarter because of them. This
guide proves that loop on your machine.

Hold the whole system as five places; each step below visits one:

| Place | What happens there | Step |
|---|---|---|
| **the door** | your task enters as a typed envelope, validated first | 3 |
| **the ledger** | adjudicated meaning; serves the briefing your agent starts from | 2 |
| **the edge** | your agent — a rented executor — claims and runs the job | 4 |
| **the gate** | policy is recorded before tools run; enforced profiles can block | 6 |
| **the receipt** | proof of what ran, plus what it should change next time | 5 |

## Prerequisites

- **Git** (Compound uses git for delivery proof).
- **Go 1.26.4+** — only required if you build from source (the prebuilt binary
  path needs no Go).
- For a prebuilt release: `curl`, `gh`, `sha256sum` or `shasum`, `gzip`, and
  `tar`. The installer also requires FD-mode `lockf -s -t 0 9` on macOS 15 or
  newer, or BusyBox-compatible `flock -n 9` on Linux. Authenticate `gh` before
  using the provenance check below. Compound itself needs no cloud account,
  API key, or external service at runtime.

Compound's public alpha is a strictly scoped, file-backed, single-user
system: file backend only, one trusted OS user on local disk, and explicit
home authority. The full support and security boundary — including the
current hardening gaps the docs name rather than paper over:
[`security/supported-scope.md`](./security/supported-scope.md).

Optional (for the console): **Bun** and **Node 20+**.
Optional (for semantic memory): a local **Ollama** server — see
[`memory-quickstart.md`](./memory-quickstart.md).

## 1. Install

Pick one:

### Option A — prebuilt binary (no Go required)

These are release-consumer instructions, not evidence that a release exists.
Use only a stable version that is already listed on the repository's Releases
page; the numeric value below is an example, not a claim that its assets have
been published. If no stable release is listed, the release URLs intentionally
fail—use the unreleased contributor-checkout path below instead.

Prebuilt public-alpha releases support exactly `darwin/arm64` on macOS 15 or newer
and `linux/amd64`. On another platform, build from source.

Choose an explicit stable release version and download its exact nine-file
inventory into a fresh directory. Do not pipe an installer from the mutable
`main` branch into a shell. Set `COMPOUND_RELEASE_REPOSITORY` to the exact
`owner/repository` identity shown on the public release page; the same value
binds downloads, attestations, and the source remote below.

```sh
set -eu

VERSION=0.1.0
REPOSITORY=${COMPOUND_RELEASE_REPOSITORY:?set the public owner/repository identity}
if ! printf '%s\n' "$VERSION" | awk '
  $0 ~ /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/ { ok = 1 }
  END { if (!ok) exit 1 }
'; then
  echo "VERSION must be strict stable SemVer" >&2
  exit 1
fi
TAG="v${VERSION}"
RELEASE_DIR="compound-release-${VERSION}"
BASE_URL="https://github.com/${REPOSITORY}/releases/download/${TAG}"

mkdir "$RELEASE_DIR"
ASSETS="
RELEASE
SHA256SUMS
install-release.sh
compound_${VERSION}_darwin_arm64.tar.gz
compound_${VERSION}_darwin_arm64.cdx.json
compound_${VERSION}_darwin_arm64_compound-breadth-engine.cdx.json
compound_${VERSION}_linux_amd64.tar.gz
compound_${VERSION}_linux_amd64.cdx.json
compound_${VERSION}_linux_amd64_compound-breadth-engine.cdx.json
"
for ASSET in $ASSETS; do
  curl --fail --location \
    --output "$RELEASE_DIR/$ASSET" \
    "$BASE_URL/$ASSET"
done
```

`SHA256SUMS` detects corruption and proves that the manifest, installer, and
archives are internally consistent with one release set. Because the checksum
file comes from the same release location as those files, checksums alone do
**not** authenticate who published them.

Before executing the installer, use GitHub's signed build attestation to check
every downloaded file against the expected repository and release workflow:

```sh
for ASSET in $ASSETS; do
  gh attestation verify "$RELEASE_DIR/$ASSET" \
    --repo "$REPOSITORY" \
    --signer-workflow "$REPOSITORY/.github/workflows/release.yml" \
    --source-ref "refs/tags/$TAG"
done
```

This is the authenticated provenance step. The installer does not run or
enforce `gh attestation verify`, and verification does not make a Git tag or
GitHub release immutable. It proves that the bytes you checked have an
attestation from the expected repository workflow.

Select the archive for the current supported platform and run the verified
installer from its regular file. Defaults install to `~/.local/bin` and
initialize `~/.compound`; override those locations with `GOBIN` and
`COMPOUND_HOME`.

```sh
case "$(uname -s)/$(uname -m)" in
  Darwin/arm64) PLATFORM=darwin_arm64 ;;
  Linux/x86_64|Linux/amd64) PLATFORM=linux_amd64 ;;
  *) echo "unsupported prebuilt platform" >&2; exit 1 ;;
esac

COMPOUND_VERSION="$VERSION" \
COMPOUND_RELEASE_TARBALL="$RELEASE_DIR/compound_${VERSION}_${PLATFORM}.tar.gz" \
  sh "$RELEASE_DIR/install-release.sh"
```

The installer first verifies its own bytes against the colocated
`SHA256SUMS`, then validates `RELEASE`, the selected archive's checksum and
closed member layout, both binary identities, and the `compound` version/full
commit identity. It publishes both binaries under one install lock, restores
the previous pair on failure when possible, preserves recovery state if a
restore fails, and runs `compound home init` plus `home doctor`. If the
installer process is interrupted after preparing publication, the next
invocation acquires the persistent kernel lock, validates the rooted private
owner record, exact journal, retained old/new bytes, and both target hashes,
then either restores the pre-commit pair or finishes cleanup for an exact pair
already marked committed. Unexpected or mismatched recovery state is preserved
and future installs fail closed pending manual inspection; it is never guessed
away. Exact private `journal.next`, `.publish`, and `.restore` files are
unpublished scratch, not authority: only a validated prepared generation may
discard them, sync that cleanup, reparse the owner and main journal, and re-enter
strict inventory validation before recovery continues.

Target publication uses two sequential renames, not an atomic pair switch. A
crash can therefore leave a durable partial pair visible until the next installer
invocation acquires the lock and deterministically restores or reconciles the
journaled generation. Consumers that cannot tolerate that bounded interval must
stop invoking either binary until recovery completes.

For an offline or air-gapped installation, verify the attestations on a
connected machine before transfer, then carry the unchanged, exact nine-file
directory listed above. Keep all names unchanged and all files colocated. The
offline installer reads these four files directly:

- `install-release.sh`
- `SHA256SUMS`
- `RELEASE`
- `compound_<version>_<supported-platform>.tar.gz`

The remaining four SBOM files and the other supported platform archive stay in
the exact release directory as the release's provenance and inventory
evidence. On the offline machine, run the same platform-selection block above,
or point to the selected archive explicitly:

```sh
COMPOUND_VERSION=0.1.0 \
COMPOUND_RELEASE_TARBALL=/path/to/compound-release-0.1.0/compound_0.1.0_linux_amd64.tar.gz \
  sh /path/to/compound-release-0.1.0/install-release.sh
```

### Option B — build from source

Select the same stable tag and authenticate its `RELEASE` manifest before
trusting the full commit recorded inside it. This path fetches only that tag;
it never builds the repository's mutable default branch.

```sh
set -eu

VERSION=0.1.0
REPOSITORY=${COMPOUND_RELEASE_REPOSITORY:?set the public owner/repository identity}
TAG="v${VERSION}"
SOURCE_EVIDENCE="compound-${VERSION}-RELEASE"
curl --fail --location \
  --output "$SOURCE_EVIDENCE" \
  "https://github.com/${REPOSITORY}/releases/download/${TAG}/RELEASE"
gh attestation verify "$SOURCE_EVIDENCE" \
  --repo "$REPOSITORY" \
  --signer-workflow "$REPOSITORY/.github/workflows/release.yml" \
  --source-ref "refs/tags/$TAG"

COMMIT=$(awk '
  NR == 1 { if (NF != 2 || $1 != "schema" || $2 != "compound.release.v0.2") exit 1; next }
  NR == 2 { if (NF != 2 || $1 != "version" || $2 != version) exit 1; next }
  NR == 3 { if (NF != 2 || $1 != "commit" || length($2) != 40 || $2 ~ /[^0-9a-f]/) exit 1; print $2; next }
  NR == 4 { if (NF != 2 || $1 != "installer" || $2 != "install-release.sh") exit 1; next }
  NR == 5 { if (NF != 6 || $1 != "artifact" || $2 != "darwin" || $3 != "arm64") exit 1; next }
  NR == 6 { if (NF != 6 || $1 != "artifact" || $2 != "linux" || $3 != "amd64") exit 1; next }
  { exit 1 }
  END { if (NR != 6) exit 1 }
' version="$VERSION" "$SOURCE_EVIDENCE")

mkdir compound
git init compound
cd compound
git remote add origin "https://github.com/${REPOSITORY}.git"
git fetch --no-tags --depth=1 origin "refs/tags/$TAG:refs/tags/$TAG"
test "$(git rev-parse "refs/tags/$TAG^{commit}")" = "$COMMIT"
git checkout --detach "$COMMIT"
test "$(git rev-parse HEAD)" = "$COMMIT"
make install
```

This builds the `compound` binary into `~/.local/bin/`, initializes a
`COMPOUND_HOME` at `~/.compound`, and runs `home doctor` to verify the layout.
The release workflow proves the tag commit is contained in remote `main` before
building and publishing. Repository administrators must still activate tag
immutability, protected-branch rules, and the private vulnerability-reporting
channel before cutting the first tag; those external controls are pre-tag gates,
not properties this source checkout can create.

```sh
# Make sure it's on your PATH:
export PATH="$HOME/.local/bin:$PATH"
compound version
```

### Unreleased contributor checkout

Before the first stable tag—or when testing a change that has not been
released—work only from a source checkout already pinned to a commit you have
reviewed:

```sh
test -z "$(git status --short)"
git rev-parse HEAD
make install
```

This produces an unreleased development build, not a release artifact. Record
the printed commit before testing, and do not present that installation as a
published Compound version.

## 2. Orient on YOUR repo

Point Compound at any repo you work on. This produces a **ContextPack** — the
frozen context an agent would consume, including what was included **and
excluded** and why.

```sh
cd /path/to/your/project
compound orient --harness claude-code --cwd . --task "describe what this project does"
```

Orientation is bound to real identities before it writes anything. Compound
resolves the harness through its registry (`claude-code` becomes the canonical
`claude_code` identity), resolves `--cwd` to an absolute symlink-free existing
directory, and refuses unknown harnesses, missing paths, or files used as a
workspace. A successful ContextPack therefore describes a registered harness
and a real project directory rather than a caller-supplied fiction.

You'll see a JSON ContextPack. Key fields:

- `summary_md` — plain-language orientation summary
- `included[]` — what context was served (and why)
- `excluded[]` — what was deliberately left out (and why)
- `task_contract` — the intent you gave

This is the spine's first move: **discover reality before acting.** The agent
gets task-scoped context, not a wall of everything.

## 3. Create a work envelope

A WorkEnvelope is one unit of work with a stable identity (`work_id`) that
links intent → context → action → receipt → transition.

```sh
compound work create \
  --owner "getting-started" \
  --predicate "the harness prints hello and records proof" \
  --state ready \
  --by "operator:you"
```

Note the returned `work_id` (e.g., `work_a1b2c3d4e5f6`).

```sh
# See it on the board:
compound workbench

# See the full envelope:
compound work show <work_id>
```

## 4. Claim and execute

```sh
# Claim it (single-holder lease):
compound work claim <work_id> --by "agent:claude-code:session-1"

# Run a bounded command through the governed execution path:
compound work advance <work_id> \
  --to running \
  --by "agent:claude-code:session-1" \
  --execute \
  --harness os-harness-entrypoints \
  --workspace "$PWD" \
  -- echo "hello from compound"

# Inspect the WorkEnvelope and the persisted RunAttempt:
compound work show <work_id> --json
compound run-attempt list --json
```

A successful command prints a `run_attempt_id`, captures a transcript, and
moves the work to `verifying`. The WorkEnvelope forward-links that `ratt_...`
record, so the run remains reachable from the work on both success and failure.

## 5. Close with proof

The successful RunAttempt is already attached to the work. Close the verifying
work to mint a **Receipt** (what was learned) and a **Transition** (what changed
and how future behavior should differ).

```sh
compound work close <work_id> \
  --by "agent:claude-code:session-1" \
  --outcome completed \
  --move-type outward_delta \
  --summary "the bounded harness execution produced inspectable proof" \
  --changed
```

The close output includes both `receipt` and `transition` IDs.

```sh
# See the proof chain:
compound work show <work_id> --json

# See the transition:
compound transition show <transition_id>
```

The Transition is the ledger entry: the system moved from state A to B because
of this work — what changed, which evidence supports it, and how future
behavior should differ. This is what makes the next session start smarter.

## 6. Wire your agent (hooks)

Compound integrates with agent harnesses through **lifecycle hooks**. See
[`harness-wiring.md`](./harness-wiring.md) for the full guide. The short
version for Claude Code:

```sh
# In your project's .claude/settings.json, add hook commands that call:
compound hook session-start --live --harness claude_code
compound hook pre-tool-use --live --harness claude_code
compound hook session-end-adapter --live --harness claude_code
```

When hooks are wired:
- **Session start** → agent receives a ContextPack (task-scoped orientation)
- **Pre-tool-use** → records ActionIntent + PolicyDecision; `--enforce` also
  publishes the native allow/hold/deny gate
- **Session end** → work produces a Receipt + Transition (learning persists)

## 7. Verify the loop

```sh
# The workbench shows your closed work with its receipt:
compound workbench

# Memory doctor confirms the system is healthy:
compound memory doctor

# Architecture doctor confirms structural integrity:
compound architecture doctor --strict
```

## What just happened

```text
orient           → the briefing (ContextPack: task-scoped context, not a preamble)
work create      → the job at the door (WorkEnvelope: one stable identity)
claim/--execute  → the edge at work (RunAttempt + transcript, linked to the work)
close            → the receipt (Receipt + Transition: proof behavior changed)
hooks            → policy evidence; enforced postures can gate before mutation
```

**The loop compounds.** The next `compound orient` on this repo includes the
transition evidence. The next agent session starts smarter because of what the
last one proved.

## Next steps

| I want to... | Read |
|---|---|
| Read the support and security boundary | [`security/supported-scope.md`](./security/supported-scope.md) |
| Wire Compound into my agent harness | [`harness-wiring.md`](./harness-wiring.md) |
| Set up semantic memory (search over my docs) | [`memory-quickstart.md`](./memory-quickstart.md) |
| See the visual console | [`console.md`](./console.md) |
| Understand the architecture | [`LAYERS.md`](./LAYERS.md) |
| Contribute to Compound itself | [`CONTRIBUTING.md`](../CONTRIBUTING.md) |

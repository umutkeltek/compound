# Getting started

This guide installs Compound and wires it into your coding agent. Then it walks one job through
by hand, so you can see each record Compound keeps.

Compound is a developer preview. Setup needs a terminal. Every step below is a command you can
read before you run it.

| You want to | Go to |
|---|---|
| Install a release without Go | [Option A](#option-a-prebuilt-binary) |
| Build a release from source | [Option B](#option-b-build-from-source) |
| Wire Claude Code, Codex, OpenCode or oh-my-pi | [3. Wire your harness](#3-wire-your-harness) |
| See what Compound records, one step at a time | [5. Walk one job through by hand](#5-walk-one-job-through-by-hand) |

## Prerequisites

- macOS or Linux, and Git.
- **Option A (prebuilt):** `curl`, `ssh-keygen`, `sha256sum` or `shasum`, `gzip` and `tar`. The
  installer also takes a lock with `lockf -s -t 0 9` on macOS 15 or newer, or with
  BusyBox-compatible `flock -n 9` on Linux. macOS ships all of these. On Linux, install
  `openssh-client` if `ssh-keygen` is missing.
- **Option B (source):** Go 1.27.1 or newer, Python 3 and `make`.
- **Optional:** Bun and Node 20 or newer for the [dashboard](console.md). A local Ollama server
  for [semantic memory](memory-quickstart.md).

Compound needs no account, API key or cloud service. Your coding agent keeps its own login.

### Get a current Go

Run `go version`. If it prints nothing, or anything older than 1.27.1 (Debian 13 packages Go 1.24),
install the official build from [go.dev/dl](https://go.dev/dl/). On Linux x86-64:

```sh
GO_VERSION=1.27.3   # or any newer version listed on go.dev/dl
curl --fail --location --remote-name "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz"
sudo rm -rf /usr/local/go
sudo tar -C /usr/local -xzf "go${GO_VERSION}.linux-amd64.tar.gz"
export PATH="/usr/local/go/bin:$PATH"
go version
```

On an ARM machine, use `linux-arm64` in both file names. Add the `export` line to your shell
profile so new terminals find Go. On macOS, use the `.pkg` installer from the same page, or
`brew install go`.

### Supported scope

The public alpha supports one setup: file backend only, one trusted OS user, one local disk. The
full boundary, with its known gaps, is in [Supported scope](security/supported-scope.md).

## 1. Install

Pick one option. Both install `compound` into `~/.local/bin` and create your Compound home,
`~/.compound`: the folder where Compound keeps everything it records. To use other folders, set
`GOBIN` or `COMPOUND_HOME` before you install.

### Option A: prebuilt binary

These are release-consumer instructions, not evidence that a release exists.
Prebuilt files are attached to releases from 0.1.1 on; 0.1.0 has none. The block below installs
0.1.1. For a later version, change `VERSION` to one listed on the repository's Releases page.

Prebuilt files exist for exactly two platforms: `darwin/arm64` on macOS 15 or newer (Apple
silicon), and `linux/amd64`. On any other machine, use Option B.

First, name the repository that publishes the release, as `owner/name`. For the main repository:

```sh
export COMPOUND_RELEASE_REPOSITORY=umutkeltek/compound
```

Then go to an empty folder, such as `~/Downloads`, and paste the whole block, from `sh <<'EOF'`
to `EOF`. It runs in its own shell, stops at the first check that fails, and installs nothing
unless every check passes.

```sh
sh <<'EOF'
set -eu
VERSION=0.1.1
if ! printf '%s\n' "$VERSION" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'; then
  echo "VERSION must be strict stable SemVer" >&2; exit 1
fi
REPOSITORY=${COMPOUND_RELEASE_REPOSITORY:?set the public owner/repository identity}
TAG="v${VERSION}"
BASE_URL="https://github.com/${REPOSITORY}/releases/download/${TAG}"
case "$(uname -s)/$(uname -m)" in
  Darwin/arm64) PLATFORM=darwin_arm64 ;;
  Linux/x86_64|Linux/amd64) PLATFORM=linux_amd64 ;;
  *) echo "No prebuilt binary for this machine. Use Option B." >&2; exit 1 ;;
esac
ARCHIVE="compound_${VERSION}_${PLATFORM}.tar.gz"
mkdir "compound-release-${VERSION}"
cd "compound-release-${VERSION}"

# 1. Download the installer, the archive for this machine and the signed checksums.
for FILE in RELEASE SHA256SUMS SHA256SUMS.sig install-release.sh "$ARCHIVE"; do
  curl --fail --location --output "$FILE" "$BASE_URL/$FILE"
done
curl --fail --location --output allowed_signers \
  "https://raw.githubusercontent.com/${REPOSITORY}/${TAG}/release/allowed_signers"

# 2. Check the signing key, the signature, and the checksums of the files the install uses.
FINGERPRINT=$(awk '{print $3, $4}' allowed_signers | ssh-keygen -lf - | awk '{print $2}')
test "$FINGERPRINT" = 'SHA256:iAb0Eaq9vv9CrfNRy/YbitZVbaSpwqE0wY8uf5jftRE' ||
  { echo "Signing key does not match SECURITY.md. Stop." >&2; exit 1; }
ssh-keygen -Y verify -f allowed_signers -I compound-release -n compound-release \
  -s SHA256SUMS.sig < SHA256SUMS
awk -v a="$ARCHIVE" '$2 == "RELEASE" || $2 == "install-release.sh" || $2 == a { print; n++ }
  END { if (n != 3) exit 1 }' SHA256SUMS > SHA256SUMS.used
if command -v sha256sum >/dev/null 2>&1; then sha256sum --check SHA256SUMS.used
else shasum -a 256 --check SHA256SUMS.used; fi

# 3. Install into ~/.local/bin and create ~/.compound.
COMPOUND_VERSION="$VERSION" COMPOUND_RELEASE_TARBALL="$PWD/$ARCHIVE" sh install-release.sh
EOF
```

`mkdir` stops the block if `compound-release-0.1.1` already exists, so old files never mix with
new ones. The fingerprint in the block is the one published in [SECURITY.md](../SECURITY.md).

**What the checks prove.** The checksums catch a damaged or mixed-up download. The signature shows
that the checksums were signed by the key this repository publishes in `release/allowed_signers`.
Neither proves who controls that key, and neither makes a Git tag or a GitHub release immutable.
The installer does not check the signature itself, so run the whole block, not the installer alone.

**What the installer does.** It checks its own bytes, `RELEASE` and the archive against
`SHA256SUMS` again. Then it replaces both binaries and runs `compound home init` and
`compound home doctor`. If the install fails, it restores the previous binaries. Target
publication uses two sequential renames, not an atomic pair switch. After a crash between the two
renames, the old and new binaries can be mixed until the next installer invocation acquires the
lock and deterministically restores or reconciles the pair. Do not run `compound` while an install
is in progress. If the installer finds recovery state it cannot explain, it keeps that state and
refuses to run until you inspect it.

**Offline machine.** Download and check on a connected machine, then copy the folder over
unchanged. The installer reads only `install-release.sh`, `SHA256SUMS`, `RELEASE` and the archive,
and all four must sit in one folder. On the offline machine, run the last line of the block from
inside that folder.

### Option B: build from source

This path builds the signed release tag, never the moving `main` branch. It downloads the signed
`RELEASE` file and checks it. It reads the release's full commit ID from that file. Then it fetches
only the tag and refuses to build if the tag points anywhere else.

Set `COMPOUND_RELEASE_REPOSITORY` as in Option A. Go to the folder where you want the source, and
paste the whole block. It creates two folders there: `compound-0.1.1-source-evidence` for the
checked files and `compound` for the source.

```sh
sh <<'EOF'
set -eu
VERSION=0.1.1
REPOSITORY=${COMPOUND_RELEASE_REPOSITORY:?set the public owner/repository identity}
TAG="v${VERSION}"
BASE_URL="https://github.com/${REPOSITORY}/releases/download/${TAG}"
mkdir "compound-${VERSION}-source-evidence" compound
cd "compound-${VERSION}-source-evidence"

# 1. Download the signed release manifest.
for ASSET in RELEASE SHA256SUMS SHA256SUMS.sig; do
  curl --fail --location --output "$ASSET" "$BASE_URL/$ASSET"
done
curl --fail --location --output allowed_signers \
  "https://raw.githubusercontent.com/${REPOSITORY}/${TAG}/release/allowed_signers"

# 2. Check the signing key, the signature and the checksum of RELEASE.
FINGERPRINT=$(awk '{print $3, $4}' allowed_signers | ssh-keygen -lf - | awk '{print $2}')
test "$FINGERPRINT" = 'SHA256:iAb0Eaq9vv9CrfNRy/YbitZVbaSpwqE0wY8uf5jftRE' ||
  { echo "Signing key does not match SECURITY.md. Stop." >&2; exit 1; }
ssh-keygen -Y verify -f allowed_signers -I compound-release -n compound-release \
  -s SHA256SUMS.sig < SHA256SUMS
awk '$2 == "RELEASE" { print; n++ } END { if (n != 1) exit 1 }' SHA256SUMS > RELEASE.sha256
if command -v sha256sum >/dev/null 2>&1; then sha256sum --check RELEASE.sha256
else shasum -a 256 --check RELEASE.sha256; fi

# 3. Read the release commit, fetch only that tag, check it, and build it.
COMMIT=$(awk 'NR == 3 && $1 == "commit" { print $2 }' RELEASE)
test "${#COMMIT}" -eq 40
cd ..
git init compound
cd compound
git remote add origin "https://github.com/${REPOSITORY}.git"
git fetch --no-tags --depth=1 origin "refs/tags/$TAG:refs/tags/$TAG"
test "$(git rev-parse "refs/tags/$TAG^{commit}")" = "$COMMIT"
git checkout --detach "$COMMIT"
make install
EOF
```

`make install` builds both binaries, `compound` and `compound-breadth-engine`, and checks them
before it replaces anything. Then it creates `~/.compound` and runs the health checks. If it fails,
it restores the previous files. Stop any running `compound` process while it works. It also prints a `# runtime doctor` block about a `synthetic` service: that is a built-in self-test on fixture data, not a real service.

How releases are built, signed and published is in the [release procedure](release.md).

### Unreleased contributor checkout

To test a change that is not released, work in a checkout pinned to a commit you have reviewed:

```sh
test -z "$(git status --short)"
git rev-parse HEAD
make install
```

This makes an unreleased development build, not a release. Write down the printed commit. Do not
present the install as a published Compound version.

## 2. First run

The signed installation commands above select **v0.1.1**, which wires Claude Code and Codex.
OpenCode and oh-my-pi setup is in **development 0.1.2**, not in the published v0.1.1 artifacts.
The version column below distinguishes them; a development checkout is not a supported release.

```sh
export PATH="$HOME/.local/bin:$PATH"
compound version
compound init
```

`compound version` prints `compound 0.1.1 (build ...)`. `compound init` creates the Compound home if
it is missing and wires each supported harness it finds: a harness is the coding agent you already
use. It prints one line per harness. With only Claude Code installed, the output looks like this
(your copy shows full paths):

```text
Compound home: ~/.compound (ready)
Claude Code: wired (~/.claude/settings.json)
Codex: not found

Next: start Claude Code in any project. Compound gives it context when the session starts
and writes a receipt when it ends.
Check: compound init --check   Last session: compound receipt last   Undo: compound hook uninstall --harness claude-code
```

- If `~/.local/bin` is not in your shell profile yet, `init` adds a `PATH:` line. It names the file
  to edit and the exact line to add. It never edits a shell profile itself.
- If it finds no harness, it prints `No supported harness found.` and names the harnesses that version supports.
  Install one, open it once, then run `compound init` again.
- Running `compound init` again is safe. A harness that is already set up reports `already wired`.

`compound home doctor` checks the Compound home. If it reports a problem, follow what it says. Do
not delete files from `~/.compound` to clear a warning.

## 3. Wire your harness

A hook is a command the harness runs at a fixed point in a session, such as session start or
after each tool call. Published v0.1.1 wires the first two harnesses below; development 0.1.2 wires all four:

| Harness | Available in | Found when this exists | What `compound init` writes | What Compound gets |
|---|---|---|---|---|
| Claude Code | v0.1.1 | `~/.claude` or the `claude` command | hook entries in `~/.claude/settings.json` | briefing, tool calls, session receipt |
| Codex | v0.1.1 | `~/.codex` or the `codex` command | hook entries in `~/.codex/hooks.json` | briefing, tool calls, session receipt |
| OpenCode | Development 0.1.2 | `~/.config/opencode` or the `opencode` command | a plugin file, `~/.config/opencode/plugins/compound.ts`, which OpenCode loads on its own | briefing and tool calls; no session receipt, because OpenCode has no session-end event |
| oh-my-pi | Development 0.1.2 | `~/.omp` or the `omp` command | a hook file, `~/.omp/agent/hooks/pre/compound.ts` | briefing, tool calls, session receipt |

Your other settings stay as they were. Compound changes only its own entries. For OpenCode and
oh-my-pi it writes only its own file, and it never overwrites or removes a file at that path that
it did not write.

A harness reads its hooks when a session starts. Restart any session that was open before you ran
`compound init`.

Hooks record what the agent does. In Claude Code, Codex and OpenCode they never stop it. The
oh-my-pi hook file asks Compound's policy before each tool call, and blocks the call when the
policy says no or cannot answer. Compound does not change your harness's own permission settings.

```sh
compound init --check                       # one line per harness; changes nothing
compound hook install --harness codex       # wire one harness by name
compound hook uninstall --harness codex     # remove Compound's entries, nothing else
compound hook install --harness claude-code --scope project   # Claude Code, this project only
compound doctor                             # full check; also names installed harnesses that are not wired
```

`compound init --check` exits 1 when something still needs `compound init`, so a script can use
it. After `compound hook uninstall`, it reports that harness as `not wired` and exits 1 until you wire it again. That is the expected state, not a fault. The harness names are `claude-code`, `codex`, `opencode` and `oh-my-pi`. What each hook does,
isolated profiles and other harnesses are in [Harness wiring](harness-wiring.md).

## 4. Your first useful session

Start your harness in a project you know, and give it one goal with limits. For example:

> Look at this project's tests and find one problem you can reproduce. Track it as a Compound
> work item with an owner and a test for done. Keep changes local, run the relevant checks, and
> tell me what is verified and what is still unknown. Do not publish, spend money or change
> permissions.

While that session runs:

- **At session start,** the hook tells the agent how to ask for a briefing on its task. The
  briefing (a ContextPack) holds the rules, the decisions you recorded, and what was left out and
  why. `compound orient` is the command that makes it.
- **During the session,** each tool call is recorded as evidence.
- **A work item** (a WorkEnvelope) is one job with an owner and a test for done. A later session can
  pick it up instead of starting again.
- **At session end,** Compound writes a receipt: the record of what the session did and where its
  transcript is.

Afterwards:

```sh
compound receipt last   # what the last session did
compound workbench      # open and finished jobs
```

Not every agent turns a request like this into a work item on its own. Step 5 does the same thing
with plain commands.

Compound can also start an agent itself for a tracked job. That needs a model route; see
[First model route](harness-wiring.md#first-model-route).

## 5. Walk one job through by hand

This section takes one job through Compound with a plain local command. No model and no harness
run. Each step shows one record Compound keeps. The demo adds one finished job to your workbench;
step 1 shows how to keep it out of your real Compound home.

Run every command in one terminal. The steps save IDs in shell variables (`CTX`, `WORK`,
`TRANSITION`), and later steps read them. Text in angle brackets, like `<path-to-your-project>`, is
a placeholder: replace all of it, brackets included.

### 1. Go to your project

```sh
cd <path-to-your-project>
```

Any Git project you know works. The demo only reads it.

Optional: to keep the demo out of your real Compound home, run these two lines first. Close the
terminal afterwards to go back to your real home.

```sh
export COMPOUND_HOME="$(mktemp -d)/home"
compound home init
```

### 2. Orient on your repo

`compound orient` makes the briefing an agent would get for one task: what it is told, what was
left out, and why. This line saves the briefing's ID in `CTX`:

```sh
CTX=$(compound orient --harness claude-code --cwd . --task "describe what this project does" --json \
  | python3 -c 'import json, sys; print(json.load(sys.stdin)["id"])')
echo "$CTX"
```

It prints an ID that starts with `ctx_`, like `ctx_347a9a42488c8412`. After `--harness`, use the
name of your harness: `claude-code`, `codex`, `opencode` or `oh-my-pi`. Compound refuses an
unknown harness name or a folder that does not exist.

To read the whole briefing, run the command without saving it:

```sh
compound orient --harness claude-code --cwd . --task "describe what this project does" --json
```

The ID is the top-level `id` field (`.id`). To copy it by hand instead, type `CTX=` and paste your
own value after it. Fields worth a look:

- `summary_md`: the briefing in plain language
- `included`: the context it served, and why
- `excluded`: what it left out, and why

### 3. Create a work item

A work item is one job with an owner and a test for done, called the predicate.

```sh
WORK=$(compound work create --owner getting-started \
  --predicate "the command prints hello and the run is recorded" \
  --context-pack "$CTX" --state ready --by operator:you \
  | awk '/^created/ { print $2 }')
echo "$WORK"
compound workbench
```

`work create` prints `created work_...` on its first line, then the full record. The `awk` keeps
only the ID, which starts with `work_`. `--by` names who acted. It labels the record; it does not
log anyone in. The workbench now shows one open job that needs a claim. To see the full record,
run `compound work show "$WORK"`.

### 4. Claim it and run a command

```sh
compound work claim "$WORK" --by agent:me:session-1
compound work advance "$WORK" --to running --by agent:me:session-1 \
  --execute --harness os-harness-entrypoints --workspace "$PWD" \
  -- echo "hello from compound"
```

Claiming gives the job one holder. `--execute` runs the command after `--` through Compound's
governed path, which checks policy and records the run. `os-harness-entrypoints` is Compound's
built-in runner for a plain local command. The output shows:

- `run_attempt_id: ratt_...`: one governed run of a command (a RunAttempt)
- `transcript:` the file that holds the command's output
- `-> verifying`: the job now waits for its result to be checked

You can stop after the claim and come back later in a new terminal. Find the ID with
`compound workbench`, type `WORK=` followed by your own value, and use the same `--by` name. Before
you retry a run, check it with `compound run-attempt list --json`. Do not repeat a command blindly
if it may already have changed something.

### 5. Close it with a receipt

```sh
compound work close "$WORK" --by agent:me:session-1 \
  --outcome completed --move-type audit_only \
  --summary "the local demonstration produced inspectable execution evidence"
```

The first line of the output names two new records. The receipt (`receipt=rec_...`) records what
the job did. The transition (`transition=tr_...`) records the state change and its evidence.
`audit_only` marks the job as evidence only: printing hello proves nothing about your project and
teaches Compound nothing.

### 6. Check the record

```sh
compound receipt last
compound workbench
TRANSITION=$(compound work show "$WORK" --json \
  | python3 -c 'import json, sys; print(json.load(sys.stdin)["ids"]["transition_id"])')
compound transition show "$TRANSITION"
compound work proof-chain-doctor
```

`proof-chain-doctor` checks that the records of every finished job point at each other. It should
report `0 problem(s) found`.

| Command | Record it made |
|---|---|
| `orient` | the briefing (ContextPack, `ctx_...`), with what was left out and why |
| `work create` | the job (WorkEnvelope, `work_...`): one ID for one unit of work |
| `work claim`, `work advance --execute` | the run (RunAttempt, `ratt_...`) and its transcript, linked to the job |
| `work close` | the receipt (`rec_...`) and the transition (`tr_...`) |

The finished job stays in your Compound home, linked to its briefing and its run. A later session
can read that record instead of rebuilding it. Compound does not learn from every receipt on its
own. Recall of past decisions is a separate setup: [Memory quickstart](memory-quickstart.md).

## 6. Daily use

- Work in your harness as usual. Give the agent one goal and its limits.
- For a job that spans sessions, ask the agent to track it as a Compound work item.
- `compound receipt last` shows what the last session did. `compound workbench` shows open and
  finished jobs.
- `compound doctor` checks the whole setup, including harnesses that are installed but not wired.
  `compound home doctor` checks only the Compound home.
- A command that exits 0 has not proven a job is done. Check the result against the job's test
  before you close it, and never write proof text just to close a job.
- Some calls stay with you: a changed goal, missing permission, new spending, a quality judgment.
  Compound never switches model providers without telling you.

To pick up a job you already claimed and gave a saved executor (see
[First model route](harness-wiring.md#first-model-route)):

```sh
compound work continue --selected --actor <your-actor> --json
```

Replace `<your-actor>` with the `--by` name that holds the job, such as `agent:me:session-1`. It
works when you hold exactly one such job. With more than one, or with a run still unsettled, it
stops and says why.

`compound home backup` saves your Compound home to one archive. Restore, upgrade and rollback are in
[Recovery](operations/recovery.md). A verified archive and a tested restore are separate checks.

## Next steps

| I want to | Read |
|---|---|
| Know what the public alpha supports | [Supported scope](security/supported-scope.md) |
| See what each hook does, harness by harness | [Harness wiring](harness-wiring.md) |
| Search over my own docs | [Memory quickstart](memory-quickstart.md) |
| Open the local dashboard | [Console](console.md) |
| Understand the architecture | [Overview](overview.md), then [LAYERS](LAYERS.md) |
| Contribute to Compound | [CONTRIBUTING.md](../CONTRIBUTING.md) |

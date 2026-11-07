# Compound

Coding agents forget. A new session does not know what you decided yesterday, redoes work that
already happened, and asks you the same question again. Compound keeps your goals, decisions, work
and evidence in local files, so the next session, in any supported harness, starts from what is known.

**Status: public alpha, version 0.1.1.** One user, one machine, plain files. Claude Code and Codex
get managed hooks; OpenCode is observed only. Setup needs a terminal, and Compound does not yet prove
that your later work got easier.

## First run (a minute or two)

You need macOS or Linux, Git, Go 1.27.1 or newer, Python 3 and `make`. These lines build the
tagged release from source (prebuilt, signed files for macOS on Apple silicon and Linux
x86-64 are attached to the release; see [Getting started, Option A](docs/getting-started.md)):

```sh
git clone https://github.com/umutkeltek/compound.git
cd compound
git checkout --quiet v0.1.1
make install
export PATH="$HOME/.local/bin:$PATH"
compound init
```

What these lines did:

- `make install` built `compound` into `~/.local/bin`, created your Compound home in
  `~/.compound`, and checked that it is healthy.
- `compound init` looked for Claude Code and Codex on this machine and added Compound's hooks to
  each one it found (`~/.claude/settings.json`, `~/.codex/hooks.json`). Your other settings stay
  as they were. It prints one line per harness: wired, already wired, or not found.
- The `export PATH=...` line works for this terminal only. If `~/.local/bin` is not in your shell
  profile yet, `compound init` prints the exact line to add, so new terminals find `compound` too.

Now start Claude Code or Codex in any project, the way you always do. Compound runs at session
start and tells the agent how to get a briefing for its task. While the agent works, Compound
records each tool call. When the session ends, Compound writes a receipt. All of it stays in
`~/.compound`.

- `compound receipt last` shows what the last session did.
- `compound init --check` checks the setup again later. It changes nothing.
- `compound hook uninstall --harness claude-code` takes Compound's hooks out and leaves the rest of
  your settings alone.

## What it is not

- Not a cloud service. Compound needs no account or server; your harness keeps its own login.
- Not multi-user. One trusted OS user on one local disk; shared or remote setups are unsupported.
- Not an enforcement layer by default. Hooks observe; a block is claimed only with behavioral proof.
- Not an agent. Your harness does the work; Compound gives it context and keeps the record.

## How it works

Your harness calls Compound at fixed points in each session, through the hooks you installed.

1. **Session start.** The agent learns how to ask for a briefing on its task (`compound orient`):
   the rules, the decisions you recorded, and what was left out and why.
2. **During the session.** Each tool call is recorded as evidence. Hooks watch; they do not block.
3. **Work that spans sessions** gets a work item with an owner and a test for "done", so a later
   session can pick it up instead of starting again.
4. **Session end.** Compound writes a receipt: what changed, and where the transcript is.

Nothing becomes a shared fact on a model's word alone, and irreversible actions stay with you.

## Day to day

- Work in your harness as usual. Give the agent one goal and its limits.
- For a job that spans sessions, ask the agent to track it as a Compound work item.
- `compound workbench` shows open and finished work.
- `compound home doctor` checks that your Compound home is healthy.
- `compound home backup` saves your home to one archive. Restore and upgrades are in
  [Recovery](docs/operations/recovery.md).

## Supported Scope (Public Alpha)

- **One user, one machine.** One trusted OS account on a local disk. Shared homes, servers,
  teams and remote access are not supported.
- **Plain files.** State lives in `~/.compound`, or in the folder you name with `--home` or
  `COMPOUND_HOME`. Compound never adopts an ambient ancestor `.compound` folder on its own.
  Postgres is experimental and not supported.
- **Hooks observe.** They record; they do not stop the agent. A setup counts as blocking only
  with behavioral proof for that exact setup.
- **The dashboard is optional and read-only.** It needs a source checkout, Bun and Node 20+.

The full boundary, with the gaps that are still open, is in
[docs/security/supported-scope.md](docs/security/supported-scope.md).

## Learn more

- [Getting started](docs/getting-started.md): the longer setup guide, first useful session, daily use.
- [Harness wiring](docs/harness-wiring.md): what each hook does, Codex, OpenCode, other harnesses.
- [Memory quickstart](docs/memory-quickstart.md): optional semantic search over your docs.
- [Dashboard](docs/console.md): the optional local web view.
- [Support policy](SUPPORT.md), [security policy](SECURITY.md) and [changelog](CHANGELOG.md).

**For contributors:** build and test rules are in [CONTRIBUTING.md](CONTRIBUTING.md). How
Compound is built, where its state lives, the command map, release and CI, and project status
are in [docs/overview.md](docs/overview.md).

## License

Apache License 2.0. See [LICENSE](LICENSE).

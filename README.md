# Compound

Coding agents forget. A new session does not know what you decided yesterday. It redoes work that
already happened and asks you the same question again. Compound keeps your goals, decisions, work
and evidence in local files, so the next session starts from what is already known.

**Status: public alpha, version 0.1.1.** One user, one machine, plain files. Compound works with
four coding agents: Claude Code, Codex, OpenCode and oh-my-pi. Setup needs a terminal. Compound does
not yet prove that your later work got easier.

## First run (a minute or two)

You need macOS or Linux, Git, Go 1.27.1 or newer, Python 3 and `make`. If `go version` prints
anything older than 1.27.1 (Debian 13 packages Go 1.24), get Go from [go.dev/dl](https://go.dev/dl/);
[Getting started](docs/getting-started.md#get-a-current-go) has the exact commands.

These lines build the tagged release from source:

```sh
git clone https://github.com/umutkeltek/compound.git
cd compound
git checkout --quiet v0.1.1
make install
export PATH="$HOME/.local/bin:$PATH"
compound init
```

To skip Go, use the prebuilt, signed files for macOS on Apple silicon and Linux x86-64 attached
to the release: [Getting started, Option A](docs/getting-started.md#option-a-prebuilt-binary).

What these lines did:

- `make install` built `compound` into `~/.local/bin`. It created your Compound home,
  `~/.compound`, the folder where Compound keeps everything, and checked that it is healthy.
- `compound init` looked for each supported coding agent (Compound calls it a *harness*) and wired
  the ones it found. Wiring adds hooks: commands the harness runs at fixed points in a session, such
  as session start. Your other settings stay as they were. `init` prints one line per harness:
  wired, already wired, or not found.
- The `export PATH=...` line works for this terminal only. If `~/.local/bin` is not in your shell
  profile yet, `compound init` prints the exact line to add.

| Harness | What `compound init` writes |
|---|---|
| Claude Code | hook entries in `~/.claude/settings.json` |
| Codex | hook entries in `~/.codex/hooks.json` |
| OpenCode | a plugin file, `~/.config/opencode/plugins/compound.ts`, which OpenCode loads on its own |
| oh-my-pi | a hook file, `~/.omp/agent/hooks/pre/compound.ts` |

If no harness is installed, `compound init` prints `No supported harness found.` and names the four
it supports. Install one, open it once, then run `compound init` again.

Now start your harness in any project, the way you always do. At session start, Compound tells the
agent how to get a briefing for its task. While the agent works, Compound records each tool call.
When the session ends, Compound writes a receipt: the record of what the session did. OpenCode has
no session-end event, so OpenCode sessions get the briefing and the tool record but no receipt.
Everything stays in `~/.compound`.

- `compound receipt last` shows what the last session did.
- `compound init --check` checks the setup again. It changes nothing, and exits 1 if something still
  needs `compound init`.
- `compound hook uninstall --harness claude-code` removes Compound's hooks and leaves the rest of
  your settings alone. The other names are `codex`, `opencode` and `oh-my-pi`.

## What it is not

- **Not a cloud service.** Compound needs no account or server. Your harness keeps its own login.
- **Not multi-user.** One trusted OS user on one local disk. Shared or remote setups are unsupported.
- **Not a guard by default.** Hooks record what the agent does. They do not replace your harness's
  own permission settings.
- **Not an agent.** Your harness does the work. Compound gives it context and keeps the record.

## How it works

Your harness calls Compound at fixed points in each session, through the hooks `compound init`
installed.

1. **Session start.** The agent learns how to ask for a briefing on its task (`compound orient`):
   the rules, the decisions you recorded, and what was left out and why.
2. **During the session.** Each tool call is recorded as evidence.
3. **Work that spans sessions** gets a work item: one job with an owner and a test for done. A later
   session can pick it up instead of starting again.
4. **Session end.** Compound writes a receipt: what changed, and where the transcript is.

Nothing becomes a shared fact on a model's word alone. Irreversible actions stay with you.

## Day to day

- Work in your harness as usual. Give the agent one goal and its limits.
- For a job that spans sessions, ask the agent to track it as a Compound work item.
- `compound workbench` shows open and finished work.
- `compound doctor` checks the setup, including harnesses that are installed but not wired.
  `compound home doctor` checks the Compound home.
- `compound home backup` saves your Compound home to one archive. Restore and upgrades are in
  [Recovery](docs/operations/recovery.md).

## Supported Scope (Public Alpha)

- **One user, one machine.** One trusted OS account on a local disk. Shared homes, servers, teams
  and remote access are not supported.
- **Plain files.** State lives in `~/.compound`, or in the folder you name with `--home` or
  `COMPOUND_HOME`. Compound never picks up a `.compound` folder from a parent directory
  (an ambient ancestor folder) on its own. Postgres is experimental and not supported.
- **Hooks observe.** In Claude Code, Codex and OpenCode they record and never stop the agent. The
  oh-my-pi hook file can block a tool call that Compound's policy denies. No setup counts as a
  guard without behavioral proof for that exact setup.
- **The dashboard is optional and read-only.** It needs a source checkout, Bun and Node 20+.

The full boundary, with the gaps that are still open, is in
[docs/security/supported-scope.md](docs/security/supported-scope.md).

## Learn more

- [Getting started](docs/getting-started.md): verified install, harness wiring, one job walked
  through by hand, daily use.
- [Harness wiring](docs/harness-wiring.md): what each hook does, harness by harness.
- [Memory quickstart](docs/memory-quickstart.md): optional semantic search over your docs.
- [Dashboard](docs/console.md): the optional local web view.
- [Support policy](SUPPORT.md), [security policy](SECURITY.md) and [changelog](CHANGELOG.md).

**For contributors:** build and test rules are in [CONTRIBUTING.md](CONTRIBUTING.md). How Compound
is built, where its state lives, the command map and project status are in
[docs/overview.md](docs/overview.md).

## License

Apache License 2.0. See [LICENSE](LICENSE).

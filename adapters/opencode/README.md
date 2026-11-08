# opencode adapter

This adapter enrolls opencode as a Compound context-injection and post-tool observation lane. The
installer first runs:

```sh
compound harness posture-plan --harness opencode --json
```

It validates the adapter-owned native projection (`chat.message`, `tool.execute.after`, and an
explicitly unsupported mutation guard) before touching config. Operators choose the semantic
posture; they do not maintain OpenCode event names in Compound core. opencode's
tool.execute.before callback cannot deny a tool call, so this adapter does not register that
callback and does not provide a mutation-control boundary.

The plugin uses the stable chat.message hook's message.system field for context injection. It does
not use experimental.chat.system.transform. Every callback fails open with one stderr line;
PostToolUse dispatch is fire-and-forget.

Most users do not run this folder: `compound init` installs the same plugin from the copy
embedded in the binary (`internal/hookplan/opencode_plugin.ts`, kept byte-identical to
`compound.ts` here by a Go test) into `~/.config/opencode/plugins/compound.ts`, where OpenCode
loads it on its own. `compound hook install --harness opencode --check` reports its state and
`compound hook uninstall --harness opencode` removes it. Edit the plugin here, run `bun test`,
then copy the file over the embedded one.

The bun installer below is the development path. It installs the same plugin and also
registers it in `opencode.jsonc` and imports the installed-hook declarations into Compound
authority:

    bun adapters/opencode/install.ts

The installer resolves HOME at runtime, preserves the first overwritten config/plugin as a .bak,
copies the plugin to $HOME/.config/opencode/plugins/compound.ts, registers the relative plugin path
in opencode.jsonc, then invokes compound contract import-hooks --hook-registration-ts with this
adapter's registration source. If a future OpenCode version gains a callback that can deny
execution, its adapter attestation and compiler mapping change here. Until then,
`guard_mutation=unsupported` keeps delegated mutation readiness closed.

Verification:

    cd adapters/opencode
    bun install --frozen-lockfile
    bun run typecheck
    bun test

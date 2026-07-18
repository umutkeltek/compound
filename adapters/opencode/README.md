# opencode adapter

This adapter enrolls opencode as a Compound context-injection and post-tool observation lane. It
registers SessionStart, UserPromptSubmit, and PostToolUse only. opencode's tool.execute.before
callback cannot deny a tool call, so this adapter does not register that callback and does not
provide a mutation-control boundary.

The plugin uses the stable chat.message hook's message.system field for context injection. It does
not use experimental.chat.system.transform. Every callback fails open with one stderr line;
PostToolUse dispatch is fire-and-forget.

Install globally and import the installed-hook declarations into Compound authority:

    bun adapters/opencode/install.ts

The installer resolves HOME at runtime, preserves the first overwritten config/plugin as a .bak,
copies the plugin to $HOME/.config/opencode/plugins/compound.ts, registers the relative plugin path
in opencode.jsonc, then invokes compound contract import-hooks --hook-registration-ts with this
adapter's registration source.

Verification:

    cd adapters/opencode
    bun install --frozen-lockfile
    bun run typecheck
    bun test

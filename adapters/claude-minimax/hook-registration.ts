const HOME = "${HOME}"

export interface HookReg {
  harness: "claude-minimax"
  event: "SessionStart" | "UserPromptSubmit" | "PreToolUse" | "PostToolUse"
  file: "compound"
  command: string
  matcher?: "Write|Edit|MultiEdit"
}

export const HOOK_REGISTRATIONS: HookReg[] = [
  { harness: "claude-minimax", event: "SessionStart",       file: "compound", command: `${HOME}/.local/bin/compound hook session-start --live --harness claude-minimax` },
  { harness: "claude-minimax", event: "UserPromptSubmit",   file: "compound", command: `${HOME}/.local/bin/compound hook user-prompt-submit --live --harness claude-minimax` },
  { harness: "claude-minimax", event: "PreToolUse", matcher: "Write|Edit|MultiEdit", file: "compound", command: `${HOME}/.local/bin/compound hook pre-tool-use --live --harness claude-minimax` },
  { harness: "claude-minimax", event: "PostToolUse", matcher: "Write|Edit|MultiEdit", file: "compound", command: `${HOME}/.local/bin/compound hook post-tool-use --live --harness claude-minimax` },
]

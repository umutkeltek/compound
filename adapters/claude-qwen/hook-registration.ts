const HOME = "${HOME}"

export interface HookReg {
  harness: "claude-qwen"
  event: "SessionStart" | "UserPromptSubmit" | "PreToolUse" | "PostToolUse"
  file: "compound"
  command: string
  matcher?: "*"
}

export const HOOK_REGISTRATIONS: HookReg[] = [
  { harness: "claude-qwen", event: "SessionStart",       file: "compound", command: `${HOME}/.local/bin/compound hook session-start --live --harness claude-qwen` },
  { harness: "claude-qwen", event: "UserPromptSubmit",   file: "compound", command: `${HOME}/.local/bin/compound hook user-prompt-submit --live --harness claude-qwen` },
  { harness: "claude-qwen", event: "PreToolUse", matcher: "*", file: "compound", command: `${HOME}/.local/bin/compound hook pre-tool-use --live --harness claude-qwen --enforce` },
  { harness: "claude-qwen", event: "PostToolUse", matcher: "*", file: "compound", command: `${HOME}/.local/bin/compound hook post-tool-use --live --harness claude-qwen` },
]

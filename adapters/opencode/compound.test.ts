import { describe, expect, test } from "bun:test"
import type { PluginInput } from "@opencode-ai/plugin"
import type { Part, UserMessage } from "@opencode-ai/sdk"

import CompoundPlugin, { type HookRunner } from "./compound.ts"

function pluginInput(): PluginInput {
  return {
    directory: "/repo",
    worktree: "/repo",
  } as unknown as PluginInput
}

function userMessage(sessionID: string, system?: string): UserMessage {
  return {
    id: "message-1",
    sessionID,
    role: "user",
    time: { created: 1 },
    agent: "build",
    model: { providerID: "fixture", modelID: "fixture" },
    system,
  }
}

function textPart(sessionID: string, text: string): Part {
  return {
    id: "part-1",
    sessionID,
    messageID: "message-1",
    type: "text",
    text,
  }
}

describe("CompoundPlugin", () => {
  test("injects session context once and prompt context on every user message", async () => {
    const calls: string[] = []
    const runner: HookRunner = async (name, input) => {
      calls.push(name + ":" + (input.prompt ?? ""))
      if (name === "session-start") return "SESSION CONTEXT"
      if (name === "user-prompt-submit") return "PROMPT CONTEXT"
      return ""
    }
    const hooks = await CompoundPlugin.createWithRunner(runner)(pluginInput())
    const firstMessage = userMessage("session-1", "EXISTING")

    await hooks["chat.message"]?.(
      { sessionID: "session-1" },
      { message: firstMessage, parts: [textPart("session-1", "hello")] },
    )

    expect(firstMessage.system).toContain("EXISTING")
    expect(firstMessage.system).toContain('<compound-context source="session-start">')
    expect(firstMessage.system).toContain("SESSION CONTEXT")
    expect(firstMessage.system).toContain('<compound-context source="user-prompt-submit">')
    expect(firstMessage.system).toContain("PROMPT CONTEXT")

    const secondMessage = userMessage("session-1")
    await hooks["chat.message"]?.(
      { sessionID: "session-1" },
      { message: secondMessage, parts: [textPart("session-1", "again")] },
    )

    expect(secondMessage.system).not.toContain("SESSION CONTEXT")
    expect(secondMessage.system).toContain("PROMPT CONTEXT")
    expect(calls).toEqual([
      "session-start:",
      "user-prompt-submit:hello",
      "user-prompt-submit:again",
    ])
  })

  test("observes completed tools without waiting on the observation result", async () => {
    const calls: string[] = []
    const runner: HookRunner = async (name, input) => {
      calls.push(name + ":" + (input.tool_name ?? ""))
      return ""
    }
    const hooks = await CompoundPlugin.createWithRunner(runner)(pluginInput())

    await hooks["tool.execute.after"]?.(
      { tool: "read", sessionID: "session-2", callID: "call-1", args: { path: "README.md" } },
      { title: "read", output: "contents", metadata: {} },
    )

    expect(calls).toEqual(["post-tool-use:read"])
  })

  test("hook failures leave the opencode callback successful", async () => {
    const messages: string[] = []
    const original = console.error
    console.error = (...args: unknown[]) => messages.push(args.join(" "))
    try {
      const hooks = await CompoundPlugin.createWithRunner(async () => {
        throw new Error("fixture failure\nwith detail")
      })(pluginInput())
      const message = userMessage("session-3")

      await hooks["chat.message"]?.(
        { sessionID: "session-3" },
        { message, parts: [textPart("session-3", "hello")] },
      )

      expect(message.system).toBeUndefined()
      expect(messages).toEqual([
        "[compound/opencode] session-start failed open: fixture failure with detail",
        "[compound/opencode] user-prompt-submit failed open: fixture failure with detail",
      ])
    } finally {
      console.error = original
    }
  })
})

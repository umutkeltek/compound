import { describe, expect, test } from "bun:test"

import CompoundPlugin, {
  testing,
  type ContextHookInput,
  type HookRunner,
  type OpenCodeEvent,
  type OpenCodePluginAPI,
  type PromptHookInput,
  type ToolAfterHookInput,
} from "./compound.ts"

type Hooks = {
  prompt?: (input: PromptHookInput) => unknown
  context?: (input: ContextHookInput) => unknown
  toolAfter?: (input: ToolAfterHookInput) => unknown
}

// A stand-in for the OpenCode 2.x plugin API: it records the hooks the plugin registers
// and replays session events pushed by the test.
function fakeAPI(directory = "/repo") {
  const hooks: Hooks = {}
  const disposed: string[] = []
  const queued: OpenCodeEvent[] = []
  let wake: (() => void) | undefined
  const api = {
    location: { directory },
    session: {
      hook: async (name: string, fn: (input: never) => unknown) => {
        if (name === "prompt") hooks.prompt = fn as Hooks["prompt"]
        if (name === "context") hooks.context = fn as Hooks["context"]
        return { dispose: () => disposed.push("session." + name) }
      },
    },
    tool: {
      hook: async (name: string, fn: (input: never) => unknown) => {
        if (name === "execute.after") hooks.toolAfter = fn as Hooks["toolAfter"]
        return { dispose: () => disposed.push("tool." + name) }
      },
    },
    event: {
      subscribe: (options: { types: string[]; signal?: AbortSignal }) => ({
        async *[Symbol.asyncIterator]() {
          while (!options.signal?.aborted) {
            const next = queued.shift()
            if (next) {
              if (options.types.includes(next.type)) yield next
              continue
            }
            await new Promise<void>((resolve) => {
              wake = resolve
              options.signal?.addEventListener("abort", () => resolve(), { once: true })
            })
          }
        },
      }),
    },
  } as unknown as OpenCodePluginAPI
  const emit = async (event: OpenCodeEvent) => {
    queued.push(event)
    wake?.()
    await new Promise((resolve) => setTimeout(resolve, 5))
  }
  return { api, hooks, disposed, emit }
}

function contextInput(sessionID: string): ContextHookInput {
  return { sessionID, system: [{ type: "text", text: "EXISTING" }] }
}

function systemText(input: ContextHookInput): string {
  return input.system.map((part) => part.text).join("\n")
}

describe("CompoundPlugin", () => {
  test("default export is the OpenCode 2.x definition shape", () => {
    expect(typeof CompoundPlugin).toBe("object")
    expect(CompoundPlugin.id).toBe("compound")
    expect(typeof CompoundPlugin.setup).toBe("function")
  })

  test("exports no runtime function besides the default definition", async () => {
    const module = await import("./compound.ts")
    const functionExports = Object.entries(module)
      .filter(([, value]) => typeof value === "function")
      .map(([name]) => name)

    expect(functionExports).toEqual([])
    expect(Object.keys(module).sort()).toEqual(["default", "testing"])
  })

  test("registers prompt, context and after-tool hooks and disposes them on cleanup", async () => {
    const fake = fakeAPI()
    const cleanup = await testing.createWithRunner(async () => "").setup(fake.api)
    expect(fake.hooks.prompt).toBeDefined()
    expect(fake.hooks.context).toBeDefined()
    expect(fake.hooks.toolAfter).toBeDefined()
    await cleanup()
    expect(fake.disposed.sort()).toEqual(["session.context", "session.prompt", "tool.execute.after"])
  })

  test("adds session and prompt context to the system prompt of every model request", async () => {
    const calls: string[] = []
    const runner: HookRunner = async (name, input) => {
      calls.push(name + ":" + (input.prompt ?? "") + ":" + input.cwd)
      if (name === "session-start") return "SESSION CONTEXT"
      if (name === "user-prompt-submit") return "PROMPT CONTEXT " + input.prompt
      return ""
    }
    const fake = fakeAPI()
    const cleanup = await testing.createWithRunner(runner).setup(fake.api)

    await fake.hooks.prompt?.({ sessionID: "session-1", prompt: { text: " hello " } })
    const first = contextInput("session-1")
    await fake.hooks.context?.(first)

    expect(first.system[0]).toEqual({ type: "text", text: "EXISTING" })
    expect(systemText(first)).toContain('<compound-context source="session-start">')
    expect(systemText(first)).toContain("SESSION CONTEXT")
    expect(systemText(first)).toContain('<compound-context source="user-prompt-submit">')
    expect(systemText(first)).toContain("PROMPT CONTEXT hello")

    await fake.hooks.prompt?.({ sessionID: "session-1", prompt: { text: "again" } })
    const second = contextInput("session-1")
    await fake.hooks.context?.(second)

    expect(systemText(second)).toContain("SESSION CONTEXT")
    expect(systemText(second)).toContain("PROMPT CONTEXT again")
    expect(systemText(second)).not.toContain("PROMPT CONTEXT hello")
    expect(calls).toEqual([
      "session-start::/repo",
      "user-prompt-submit:hello:/repo",
      "user-prompt-submit:again:/repo",
    ])
    await cleanup()
  })

  test("session.created starts the briefing once, only in the instance at that location", async () => {
    const calls: string[] = []
    const runner: HookRunner = async (name, input) => {
      calls.push(name + ":" + input.session_id + ":" + input.cwd)
      return name === "session-start" ? "SESSION CONTEXT" : ""
    }
    const fake = fakeAPI("/work")
    const cleanup = await testing.createWithRunner(runner).setup(fake.api)

    await fake.emit({ type: "session.created", data: { sessionID: "session-2", location: { directory: "/work" } } })
    // OpenCode delivers every location's events to every plugin instance; another
    // location's session must not start a second ContextPack build here.
    await fake.emit({ type: "session.created", data: { sessionID: "elsewhere", location: { directory: "/other" } } })
    const request = contextInput("session-2")
    await fake.hooks.context?.(request)
    await fake.hooks.context?.(contextInput("session-2"))

    expect(systemText(request)).toContain("SESSION CONTEXT")
    expect(calls).toEqual(["session-start:session-2:/work"])

    await fake.emit({ type: "session.deleted", data: { sessionID: "session-2" } })
    await fake.hooks.context?.(contextInput("session-2"))
    expect(calls).toEqual(["session-start:session-2:/work", "session-start:session-2:/work"])
    await cleanup()
  })

  test("prompt, context and after-tool hooks ignore a session created at another location", async () => {
    const calls: string[] = []
    const runner: HookRunner = async (name, input) => {
      calls.push(name + ":" + input.session_id)
      return "CONTEXT"
    }
    const fake = fakeAPI("/work")
    const cleanup = await testing.createWithRunner(runner).setup(fake.api)

    await fake.emit({ type: "session.created", data: { sessionID: "elsewhere", location: { directory: "/other" } } })
    await fake.hooks.prompt?.({ sessionID: "elsewhere", prompt: { text: "hi" } })
    const request = contextInput("elsewhere")
    await fake.hooks.context?.(request)
    await fake.hooks.toolAfter?.({ tool: "bash", sessionID: "elsewhere", status: "completed" })
    await new Promise((resolve) => setTimeout(resolve, 5))

    expect(calls).toEqual([])
    expect(systemText(request)).toBe("EXISTING")
    await cleanup()
  })

  test("leaves the system prompt alone when Compound has nothing to add", async () => {
    const fake = fakeAPI()
    const cleanup = await testing.createWithRunner(async () => "").setup(fake.api)
    const request = contextInput("session-3")
    await fake.hooks.context?.(request)
    expect(request.system).toEqual([{ type: "text", text: "EXISTING" }])
    await cleanup()
  })

  test("observes completed and failed tools without waiting on the observation result", async () => {
    const calls: string[] = []
    let release: () => void = () => {}
    const runner: HookRunner = async (name, input) => {
      calls.push(name + ":" + input.tool_name + ":" + input.tool_response + ":" + JSON.stringify(input.tool_input))
      await new Promise<void>((resolve) => {
        release = resolve
      })
      return ""
    }
    const fake = fakeAPI()
    const cleanup = await testing.createWithRunner(runner).setup(fake.api)

    const returned = fake.hooks.toolAfter?.({
      tool: "read",
      sessionID: "session-4",
      input: { path: "README.md" },
      status: "completed",
      result: { output: "contents", content: [] },
    })
    expect(returned).toBeUndefined()
    fake.hooks.toolAfter?.({
      tool: "bash",
      sessionID: "session-4",
      input: { command: "false" },
      status: "completed",
      result: { content: [{ type: "text", text: "line one" }, { type: "text", text: "line two" }] },
    })
    fake.hooks.toolAfter?.({ tool: "edit", sessionID: "session-4", status: "error", error: new Error("denied\nby rule") })

    expect(calls).toEqual([
      'post-tool-use:read:contents:{"path":"README.md"}',
      'post-tool-use:bash:line one\nline two:{"command":"false"}',
      "post-tool-use:edit:denied by rule:{}",
    ])
    release()
    await cleanup()
  })

  test("hook failures leave every OpenCode callback successful", async () => {
    const messages: string[] = []
    const original = console.error
    console.error = (...args: unknown[]) => messages.push(args.join(" "))
    try {
      const fake = fakeAPI()
      const cleanup = await testing.createWithRunner(async () => {
        throw new Error("fixture failure\nwith detail")
      }).setup(fake.api)
      const request = contextInput("session-5")

      await fake.hooks.prompt?.({ sessionID: "session-5", prompt: { text: "hello" } })
      await fake.hooks.context?.(request)

      expect(request.system).toEqual([{ type: "text", text: "EXISTING" }])
      expect(messages).toEqual([
        "[compound/opencode] session-start failed open: fixture failure with detail",
        "[compound/opencode] user-prompt-submit failed open: fixture failure with detail",
      ])
      await cleanup()
    } finally {
      console.error = original
    }
  })
})

describe("hook timeout budgets", () => {
  // These budgets are the difference between opencode having Compound context
  // and silently running blind. On 2026-08-07 one shared 2s budget was measured
  // against a real `compound hook session-start --live` run: 10.3s and 12.9s on
  // two consecutive probes. Session start therefore timed out and failed open on
  // every start, while the hook itself succeeded and every surface read healthy.
  test("session start outlives a real ContextPack build", () => {
    // 13s was the slower observed build; the budget must clear it with headroom,
    // or this regression returns as a silent one.
    expect(testing.timeoutMS("session-start")).toBeGreaterThanOrEqual(20_000)
  })

  test("per-turn and per-tool hooks stay tight enough not to stall a turn", () => {
    expect(testing.timeoutMS("post-tool-use")).toBeLessThanOrEqual(2_000)
    expect(testing.timeoutMS("user-prompt-submit")).toBeLessThanOrEqual(10_000)
  })

  test("session start is not held to the per-tool budget", () => {
    // The falsifier for the original defect: a single shared budget makes these
    // equal. They must not be.
    expect(testing.timeoutMS("session-start")).toBeGreaterThan(testing.timeoutMS("post-tool-use"))
  })

  test("an explicit env override still wins for every hook", () => {
    const previous = process.env.COMPOUND_OPENCODE_HOOK_TIMEOUT_MS
    process.env.COMPOUND_OPENCODE_HOOK_TIMEOUT_MS = "1234"
    try {
      expect(testing.timeoutMS("session-start")).toBe(1234)
      expect(testing.timeoutMS("post-tool-use")).toBe(1234)
    } finally {
      if (previous === undefined) delete process.env.COMPOUND_OPENCODE_HOOK_TIMEOUT_MS
      else process.env.COMPOUND_OPENCODE_HOOK_TIMEOUT_MS = previous
    }
  })
})

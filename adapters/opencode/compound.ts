// compound-owned: opencode-plugin@1
// OpenCode 2.x loads a plugin module's default export and requires the shape
// `{ id, setup }` (or `{ id, effect }`); a bare plugin function is refused with
// "Plugin must export a default definition with an id and an effect or setup
// function". `setup` receives the promise-based plugin API and may return a
// cleanup function. The types below describe only the parts of that API this
// plugin uses, as the 2.x host passes them.

type CompoundHookName = "session-start" | "user-prompt-submit" | "post-tool-use"

type HookInput = {
  hook_event_name: "SessionStart" | "UserPromptSubmit" | "PostToolUse"
  session_id: string
  cwd: string
  prompt?: string
  tool_name?: string
  tool_input?: Record<string, unknown>
  tool_response?: string
}

type HookResponse = {
  additional_context?: string
  hookSpecificOutput?: {
    additionalContext?: string
  }
}

export type HookRunner = (name: CompoundHookName, input: HookInput) => Promise<string>

type Disposable = { dispose: () => unknown }

// One entry of the system prompt OpenCode sends to the model (LLM.SystemPart).
export type SystemPart = { type: "text"; text: string }

// session.hook("prompt"): fires once per user prompt, before the message is recorded.
export type PromptHookInput = {
  sessionID: string
  prompt: { text?: string }
}

// session.hook("context"): fires for every primary model request. Hooks mutate the
// payload in place; the host sends `system` exactly as the hooks leave it.
export type ContextHookInput = {
  sessionID: string
  system: SystemPart[]
}

// tool.hook("execute.after"): fires after every tool call, completed or failed.
export type ToolAfterHookInput = {
  tool: string
  sessionID: string
  input?: Record<string, unknown>
  status?: "completed" | "error"
  result?: { output?: unknown; content?: Array<{ type: string; text?: string }> }
  error?: unknown
}

export type OpenCodeEvent = {
  type: string
  data?: { sessionID?: string; location?: { directory?: string } }
}

export type OpenCodePluginAPI = {
  location?: { directory?: string } | null
  session: {
    hook(name: "prompt", fn: (input: PromptHookInput) => unknown): Promise<Disposable>
    hook(name: "context", fn: (input: ContextHookInput) => unknown): Promise<Disposable>
  }
  tool: {
    hook(name: "execute.after", fn: (input: ToolAfterHookInput) => unknown): Promise<Disposable>
  }
  event: {
    subscribe(options: { types: string[]; signal?: AbortSignal }): AsyncIterable<OpenCodeEvent>
  }
}

export type PluginDefinition = {
  id: string
  setup: (api: OpenCodePluginAPI) => Promise<() => Promise<void>>
}

// Budgets are per hook because the hooks have different jobs, and one shared
// budget silently defeated the most important of them. Measured on a real
// control-plane repo: `compound hook session-start --live` takes 10-13s (it
// builds the whole ContextPack), so a single 2s budget made session-start time
// out and fail open on EVERY start — opencode ran with zero Compound context
// while the plugin, the hook, and every status surface all reported success.
// Session start happens once and its entire purpose is to block until context
// exists, so it gets room; the per-turn and per-tool hooks stay tight so a slow
// control plane can never stall a turn.
const DEFAULT_TIMEOUT_MS: Record<CompoundHookName, number> = {
  "session-start": 30_000,
  "user-prompt-submit": 10_000,
  "post-tool-use": 2_000,
}

function oneLine(error: unknown): string {
  const text = error instanceof Error ? error.message : String(error)
  return text.replace(/\s+/g, " ").trim() || "unknown error"
}

function timeoutMS(name: CompoundHookName): number {
  const configured = Number.parseInt(process.env.COMPOUND_OPENCODE_HOOK_TIMEOUT_MS ?? "", 10)
  return Number.isFinite(configured) && configured > 0 ? configured : DEFAULT_TIMEOUT_MS[name]
}

function compoundBinary(): string {
  return process.env.COMPOUND_BIN || Bun.which("compound") || "compound"
}

const runCompoundHook: HookRunner = async (name, input) => {
  const proc = Bun.spawn(
    [compoundBinary(), "hook", name, "--live", "--harness", "opencode"],
    {
      stdin: new TextEncoder().encode(JSON.stringify(input)),
      stdout: "pipe",
      stderr: "pipe",
    },
  )

  let timedOut = false
  const budgetMS = timeoutMS(name)
  const timer = setTimeout(() => {
    timedOut = true
    proc.kill()
  }, budgetMS)

  const [exitCode, stdout, stderr] = await Promise.all([
    proc.exited,
    new Response(proc.stdout).text(),
    new Response(proc.stderr).text(),
  ]).finally(() => clearTimeout(timer))

  if (timedOut) throw new Error("timed out after " + budgetMS + "ms")
  if (exitCode !== 0) throw new Error("exit " + exitCode + ": " + oneLine(stderr))

  let response: HookResponse
  try {
    response = JSON.parse(stdout) as HookResponse
  } catch {
    throw new Error("returned invalid JSON")
  }
  return (
    response.additional_context ??
    response.hookSpecificOutput?.additionalContext ??
    ""
  ).trim()
}

function contextBlock(source: string, context: string): string {
  return "<compound-context source=\"" + source + "\">\n" + context + "\n</compound-context>"
}

function toolResponse(input: ToolAfterHookInput): string {
  if (input.status === "error") return oneLine(input.error)
  const output = input.result?.output
  if (typeof output === "string") return output
  const text = (input.result?.content ?? [])
    .filter((part) => part.type === "text" && typeof part.text === "string")
    .map((part) => part.text)
    .join("\n")
  if (text) return text
  return output === undefined ? "" : JSON.stringify(output) ?? ""
}

function createCompoundPlugin(runHook: HookRunner = runCompoundHook): PluginDefinition {
  return {
    id: "compound",
    setup: async (api) => {
      const directory = api.location?.directory || process.cwd()
      const sessionStarts = new Map<string, Promise<string>>()
      const promptContexts = new Map<string, string>()
      // Where each session was created. OpenCode runs one plugin copy per location and
      // calls every copy's hooks, so a copy acts only on sessions of its own location
      // (or ones it never saw created); otherwise each copy would start the briefing.
      const sessionDirectories = new Map<string, string>()
      const ownSession = (sessionID: string): boolean => {
        const created = sessionDirectories.get(sessionID)
        return !created || !api.location?.directory || created === api.location.directory
      }

      const invokeOpen = async (name: CompoundHookName, input: HookInput): Promise<string> => {
        try {
          return await runHook(name, input)
        } catch (error) {
          console.error("[compound/opencode] " + name + " failed open: " + oneLine(error))
          return ""
        }
      }

      const ensureSessionStart = (sessionID: string, cwd = directory): Promise<string> => {
        const existing = sessionStarts.get(sessionID)
        if (existing) return existing
        const pending = invokeOpen("session-start", {
          hook_event_name: "SessionStart",
          session_id: sessionID,
          cwd,
        })
        sessionStarts.set(sessionID, pending)
        return pending
      }

      const forget = (sessionID: string) => {
        sessionStarts.delete(sessionID)
        promptContexts.delete(sessionID)
        sessionDirectories.delete(sessionID)
      }

      // Start the briefing as soon as a session exists, so the first prompt rarely waits
      // for the whole ContextPack build. OpenCode runs one plugin instance per location and
      // delivers every session event to all of them, so an instance only starts sessions
      // created at its own location; the context hook still covers any it skips.
      const events = new AbortController()
      void (async () => {
        try {
          const stream = api.event.subscribe({
            types: ["session.created", "session.deleted"],
            signal: events.signal,
          })
          for await (const event of stream) {
            const sessionID = event.data?.sessionID
            if (!sessionID) continue
            if (event.type === "session.created") {
              const created = event.data?.location?.directory
              if (created) sessionDirectories.set(sessionID, created)
              if (!ownSession(sessionID)) continue
              void ensureSessionStart(sessionID, created || directory)
            }
            if (event.type === "session.deleted") forget(sessionID)
          }
        } catch (error) {
          if (!events.signal.aborted) {
            console.error("[compound/opencode] session events failed open: " + oneLine(error))
          }
        }
      })()

      const registrations: Disposable[] = []
      try {
        registrations.push(
          await api.session.hook("prompt", async (input) => {
            if (!ownSession(input.sessionID)) return
            try {
              await ensureSessionStart(input.sessionID)
              const context = await invokeOpen("user-prompt-submit", {
                hook_event_name: "UserPromptSubmit",
                session_id: input.sessionID,
                cwd: directory,
                prompt: (input.prompt?.text ?? "").trim(),
              })
              promptContexts.set(input.sessionID, context)
            } catch (error) {
              console.error("[compound/opencode] prompt hook failed open: " + oneLine(error))
            }
          }),
        )

        registrations.push(
          await api.session.hook("context", async (input) => {
            if (!ownSession(input.sessionID)) return
            try {
              const blocks: string[] = []
              const sessionContext = await ensureSessionStart(input.sessionID)
              if (sessionContext) blocks.push(contextBlock("session-start", sessionContext))
              const promptContext = promptContexts.get(input.sessionID)
              if (promptContext) blocks.push(contextBlock("user-prompt-submit", promptContext))
              if (blocks.length > 0 && Array.isArray(input.system)) {
                input.system.push({ type: "text", text: blocks.join("\n\n") })
              }
            } catch (error) {
              console.error("[compound/opencode] context hook failed open: " + oneLine(error))
            }
          }),
        )

        registrations.push(
          await api.tool.hook("execute.after", (input) => {
            if (!ownSession(input.sessionID)) return
            try {
              void invokeOpen("post-tool-use", {
                hook_event_name: "PostToolUse",
                session_id: input.sessionID,
                cwd: directory,
                tool_name: input.tool,
                tool_input: input.input ?? {},
                tool_response: toolResponse(input),
              })
            } catch (error) {
              console.error("[compound/opencode] post-tool-use failed open: " + oneLine(error))
            }
          }),
        )
      } catch (error) {
        console.error("[compound/opencode] hook registration failed open: " + oneLine(error))
      }

      return async () => {
        events.abort()
        for (const registration of registrations) {
          try {
            await registration.dispose()
          } catch {
            // Disposal during shutdown has nothing left to protect.
          }
        }
      }
    },
  }
}

// Test access lives on a named non-function export. OpenCode reads only the default
// export; a 1.x host initialized every exported function, so none is exported here.
export const testing = {
  createWithRunner: createCompoundPlugin,
  timeoutMS,
}

const CompoundPlugin: PluginDefinition = createCompoundPlugin()

export default CompoundPlugin

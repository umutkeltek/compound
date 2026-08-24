// compound-owned: oh-my-pi-hook-shim@1
/**
 * compound ↔ oh-my-pi hook shim (installed by `compound hook install --harness
 * oh-my-pi` into <agentDir>/hooks/pre/compound.ts).
 *
 * omp discovers TS hook modules under <agentDir>/hooks/{pre,post}/ and wires
 * them into the HookRunner (session_start, before_agent_start, tool_call,
 * tool_result, session_shutdown). This shim makes compound the meaning spine
 * for omp sessions, matching what compound hook install does for claude-code:
 *
 *   - before_agent_start → `compound hook user-prompt-submit --live` and inject
 *     the returned ContextPack (memory + contract + capability offers) into the
 *     session as a persisted custom message.
 *   - tool_call → `compound hook pre-tool-use --live` (observation + policy);
 *     a policy deny blocks the tool with compound's reason.
 *   - session_shutdown → `compound hook session-end --live` so the session
 *     receipt lands in compound instead of being derived from exit.
 *
 * Discipline: fail-open, bounded latency. A slow or absent compound binary
 * must never hang the operator's omp session — every call is spawnSync with a
 * hard timeout, and every failure path degrades to "no injection, no block".
 * Governed headless profiles (omp-qwen etc.) run with --no-extensions and
 * never load this shim; it serves interactive omp only.
 */
import { spawnSync } from "node:child_process";
import { homedir } from "node:os";

/** Structural slice of omp's HookAPI this shim consumes. */
interface CompoundHookApi {
	on(event: string, handler: (payload?: unknown) => unknown): void;
}

interface ToolCallPayload {
	toolName?: string;
	toolCallId?: string;
	input?: Record<string, unknown>;
}

interface ToolResultPayload {
	toolName?: string;
	toolCallId?: string;
}

interface BeforeAgentStartPayload {
	prompt?: string;
}

interface HookSpecificOutput {
	additionalContext?: string;
}

interface CompoundHookDocument {
	hookSpecificOutput?: HookSpecificOutput;
	permissionDecision?: string;
	permissionDecisionReason?: string;
}

const COMPOUND = `${homedir()}/.local/bin/compound`;
const HARNESS = "oh_my_pi";
const CALL_TIMEOUT_MS = 8_000;

// Synthetic but stable per-process session identity: omp does not expose its
// session file to hooks, and compound only needs a correlation key for the
// SessionStart/SessionEnd receipt pair.
const sessionId = `omp-${process.pid}-${Date.now().toString(36)}`;
const sessionFixture = { session_id: sessionId, cwd: process.cwd() };

function callCompound(verb: string, fixture: Record<string, unknown>): CompoundHookDocument | null {
	try {
		const res = spawnSync(COMPOUND, ["hook", verb, "--live", "--harness", HARNESS], {
			input: JSON.stringify(fixture),
			encoding: "utf-8",
			timeout: CALL_TIMEOUT_MS,
			maxBuffer: 8 * 1024 * 1024,
		});
		const out = (res.stdout ?? "").trim();
		// Verbs print a JSON document first; some append a human status line.
		// Parse the leading top-level object leniently.
		const start = out.indexOf("{");
		if (start < 0) return null;
		let depth = 0;
		let inString = false;
		for (let i = start; i < out.length; i++) {
			const ch = out[i];
			if (inString) {
				if (ch === "\\") i++;
				else if (ch === '"') inString = false;
				continue;
			}
			if (ch === '"') inString = true;
			else if (ch === "{") depth++;
			else if (ch === "}") {
				depth--;
				if (depth === 0) {
					try {
						return JSON.parse(out.slice(start, i + 1)) as CompoundHookDocument;
					} catch {
						return null;
					}
				}
			}
		}
		return null;
	} catch {
		return null;
	}
}

export default function compoundHook(pi: CompoundHookApi): void {
	pi.on("session_start", () => {
		callCompound("session-start", sessionFixture);
	});

	pi.on("before_agent_start", (payload) => {
		const event = (payload ?? {}) as BeforeAgentStartPayload;
		const doc = callCompound("user-prompt-submit", { ...sessionFixture, prompt: event.prompt ?? "" });
		const ctx = doc?.hookSpecificOutput?.additionalContext;
		if (typeof ctx !== "string" || ctx.length === 0) return undefined;
		return { message: ctx };
	});

	pi.on("tool_call", (payload) => {
		const event = (payload ?? {}) as ToolCallPayload;
		const doc = callCompound("pre-tool-use", {
			...sessionFixture,
			tool_name: event.toolName ?? "unknown",
			tool_use_id: event.toolCallId ?? "",
			tool_input: event.input ?? {},
		});
		if (doc?.permissionDecision === "deny") {
			return { block: true, reason: doc.permissionDecisionReason ?? "compound policy denied this tool call" };
		}
		return undefined;
	});

	pi.on("tool_result", (payload) => {
		const event = (payload ?? {}) as ToolResultPayload;
		callCompound("post-tool-use", {
			...sessionFixture,
			tool_name: event.toolName ?? "unknown",
			tool_use_id: event.toolCallId ?? "",
		});
	});

	pi.on("session_shutdown", () => {
		callCompound("session-end", sessionFixture);
	});
}

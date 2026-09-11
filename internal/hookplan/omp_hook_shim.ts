// compound-owned: oh-my-pi-hook-shim@2
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
 * Lifecycle and context calls are observational and fail open. The tool
 * boundary is fail closed: a slow, absent, or malformed Compound response
 * blocks the tool without hanging the session because spawnSync has a hard
 * timeout. Governed headless profiles disable ambient extension discovery;
 * Switchback may load this exact owned shim explicitly.
 */
import { spawnSync } from "node:child_process";
import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
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
	hookEventName?: string;
	additionalContext?: string;
	permissionDecision?: string;
	permissionDecisionReason?: string;
}

interface CompoundHookDocument {
	hookSpecificOutput?: HookSpecificOutput;
	permissionDecision?: string;
	permissionDecisionReason?: string;
}

const COMPOUND = `${homedir()}/.local/bin/compound`;
const HARNESS = "oh_my_pi";
const CALL_TIMEOUT_MS = 8_000;

// Session-file discovery (slice 3). omp DOES persist session files at
// ~/.omp/agent/sessions/<cwd-slug>/<ISO>_<uuid>.jsonl where the second
// line of every file is `{"type":"session","version":3,"id":"<uuid>",
// "cwd":"<abs>"}`. We pick the newest file whose line-2 cwd matches the
// current process and forward its absolute path as `transcript_path` on
// every call. The Go-side mirror lives in internal/hookplan/omp_derive.go
// and is the testable companion; the two MUST stay in lockstep.
//
// When discovery fails (no matching session file) we OMIT transcript_path
// rather than send a wrong path -- scanSessionTranscript would mistake a
// neighbouring agent's log for ours and inflate skills_fired with phantom
// evidence. session_id keeps the synthetic value as a fallback; if omp's
// embedded id is recoverable we prefer it so SessionStart/SessionEnd share
// the same correlation key.
// Same bound as OmpDeriveSession on the Go side (internal/hookplan/omp_derive.go):
// when a cwd slug holds thousands of stale session files, only header-read
// the newest OMP_DERIVE_MAX_PROBE by mtime. The contract still returns the
// single newest matching candidate; the bound just keeps the scan cost
// proportional to a small constant.
const OMP_DERIVE_MAX_PROBE = 64;

function deriveOmpSession(homedirValue: string, cwd: string): { sessionId: string; transcriptPath: string | null } {
	const slug = slugFromCwd(cwd);
	const dir = `${homedirValue}/.omp/agent/sessions/${slug}`;
	if (!existsSync(dir)) {
		return { sessionId: `omp-${process.pid}-${Date.now().toString(36)}`, transcriptPath: null };
	}
	// Pass 1: mtime-only pre-filter. Pick the newest OMP_DERIVE_MAX_PROBE
	// .jsonl files; stat cost is already paid by readdirSync on some
	// platforms but explicitly here so the bound holds regardless.
	let top: { path: string; mtime: number }[] = [];
	try {
		const entries = readdirSync(dir);
		for (const entry of entries) {
			if (!entry.endsWith(".jsonl")) continue;
			const full = `${dir}/${entry}`;
			let st: import("node:fs").Stats;
			try { st = statSync(full); } catch { continue; }
			if (top.length < OMP_DERIVE_MAX_PROBE) {
				top.push({ path: full, mtime: st.mtimeMs });
				continue;
			}
			let idx = 0;
			for (let i = 1; i < top.length; i++) {
				if (top[i].mtime < top[idx].mtime) idx = i;
			}
			if (top[idx].mtime < st.mtimeMs) top[idx] = { path: full, mtime: st.mtimeMs };
		}
	} catch {
		return { sessionId: `omp-${process.pid}-${Date.now().toString(36)}`, transcriptPath: null };
	}
	// Pass 2: header read on the bounded set only, picking the newest match.
	let newestPath: string | null = null;
	let newestMtime = 0;
	let newestEmbeddedId = "";
	for (const c of top) {
		let head = "";
		try {
			const buf = readFileSync(c.path, { encoding: "utf-8", flag: "r" });
			head = buf.slice(0, 1024);
		} catch { continue; }
		const lines = head.split("\n");
		const headerLine = lines.find((ln) => ln.trim().length > 0 && ln.includes('"type":"session"')) ?? "";
		const headerProbe = parseSessionHeader(headerLine);
		if (!headerProbe) continue;
		if (headerProbe.cwd !== cwd) continue;
		if (c.mtime <= newestMtime) continue;
		newestPath = c.path;
		newestMtime = c.mtime;
		newestEmbeddedId = headerProbe.id;
	}
	if (!newestPath) {
		return { sessionId: `omp-${process.pid}-${Date.now().toString(36)}`, transcriptPath: null };
	}
	const fallbackId = `omp-${process.pid}-${Date.now().toString(36)}`;
	return { sessionId: newestEmbeddedId || fallbackId, transcriptPath: newestPath };
}

function slugFromCwd(cwd: string): string {
	// omp's on-disk convention: strip the leading "/", replace every "/" with
	// "-", then wrap in "--". Synthetic example (privacy-law scrub):
	//   /private/home/operator/projects/sample -> --private-home-operator-projects-sample--
	let s = cwd.replace(/^\/+/, "").replace(/\//g, "-");
	s = s.replace(/[^A-Za-z0-9._-]/g, "-");
	return `--${s}--`;
}

function parseSessionHeader(line: string): { id: string; cwd: string } | null {
	try {
		const j = JSON.parse(line) as { type?: string; id?: string; cwd?: string };
		if (j.type !== "session" || typeof j.id !== "string" || typeof j.cwd !== "string") return null;
		return { id: j.id, cwd: j.cwd };
	} catch {
		return null;
	}
}

const discovered = deriveOmpSession(homedir(), process.cwd());
const sessionId = discovered.sessionId;
const sessionFixture: { session_id: string; cwd: string; transcript_path?: string } = {
	session_id: sessionId,
	cwd: process.cwd(),
};
if (discovered.transcriptPath) {
	sessionFixture.transcript_path = discovered.transcriptPath;
}

function callCompound(verb: string, fixture: Record<string, unknown>): CompoundHookDocument | null {
	try {
		const args = ["hook", verb, "--live", "--harness", HARNESS];
		if (verb === "pre-tool-use") args.push("--enforce");
		const res = spawnSync(COMPOUND, args, {
			input: JSON.stringify(fixture),
			encoding: "utf-8",
			timeout: CALL_TIMEOUT_MS,
			maxBuffer: 8 * 1024 * 1024,
		});
    if (res.error || res.signal) return null;
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
          const doc = JSON.parse(out.slice(start, i + 1)) as CompoundHookDocument;
          if (res.status !== 0) {
            const decision =
              doc.permissionDecision === "deny" ||
              doc.hookSpecificOutput?.permissionDecision === "deny"
                ? "deny"
                : null;
            if (
              verb !== "pre-tool-use" ||
              doc.hookSpecificOutput?.hookEventName !== "PreToolUse" ||
              decision !== "deny"
            ) {
              return null;
            }
          }
          return doc;
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
		if (!doc?.hookSpecificOutput || doc.hookSpecificOutput.hookEventName !== "PreToolUse") {
			return { block: true, reason: "compound policy gate unavailable" };
		}
		const topHasDecision = Object.prototype.hasOwnProperty.call(doc, "permissionDecision");
		const nestedHasDecision = Object.prototype.hasOwnProperty.call(doc.hookSpecificOutput, "permissionDecision");
		const topDecision: unknown = doc.permissionDecision;
		const nestedDecision: unknown = doc.hookSpecificOutput.permissionDecision;
		if (topDecision === "deny" || nestedDecision === "deny") {
			const reason = nestedDecision === "deny"
				? doc.hookSpecificOutput.permissionDecisionReason
				: doc.permissionDecisionReason;
			return { block: true, reason: reason ?? "compound policy denied this tool call" };
		}
		for (const [present, decision] of [
			[topHasDecision, topDecision],
			[nestedHasDecision, nestedDecision],
		] as const) {
			if (present && decision !== "allow") {
				return { block: true, reason: "compound policy returned an invalid permission decision" };
			}
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

#!/usr/bin/env bun
/*
 * distill.ts — PROTOTYPE reasoning-based conversation distiller.
 *
 * Proves: sending the WHOLE cleaned conversation to a REASONING model + a
 * separation-typed prompt extracts the real insights that gbrain's shallow
 * "dream" (first+last 8K char truncation) rejected as "meta-operational".
 *
 * SCRATCH ONLY — NOT wired into cron/circulation/gbrain. Read-only except
 * writing this script + its output under this directory.
 *
 * Pipeline:
 *   STEP 1 CLEAN   — role-tag Umut + agent turns, drop harness injections &
 *                    tool_use/tool_result execution noise (patterns reused from
 *                    umut-os/cron/session_receipt_backfill.ts).
 *   STEP 2 DISTILL — map-reduce over a reasoning model (DeepSeek) with the
 *                    typed separation prompt. Cross-chunk synthesis is exactly
 *                    what the dream lacks.
 *   STEP 3 OUTPUT  — full typed distillation + stats, written to ./output.md.
 */
import { writeFileSync, statSync, mkdirSync, existsSync } from "node:fs";
import { join } from "node:path";

const HOME = process.env.HOME || "";
const INPUT = process.argv.slice(2).find((a) => !a.startsWith("--"));
if (!INPUT) {
  console.error("usage: distill <session.jsonl> [--ledger] [--slug=meaning/decisions/...]");
  process.exit(2);
}
// de-hardcoded: distiller scratch output lives under ~/.compound/distill (override COMPOUND_OUT).
const OUTDIR = process.env.COMPOUND_OUT || join(HOME, ".compound", "distill");
if (!existsSync(OUTDIR)) mkdirSync(OUTDIR, { recursive: true });

// Provider-parameterized (2026-06-23): DeepSeek v4 vs NeuralWatt, same code path.
const BASE_URL = (process.env.DISTILL_BASE_URL || "https://api.deepseek.com/v1").replace(/\/+$/, "");
const API_KEY = process.env.DISTILL_API_KEY || process.env.DEEPSEEK_API_KEY;
if (!API_KEY) {
  console.error("No API key: set DISTILL_API_KEY (or DEEPSEEK_API_KEY)");
  process.exit(1);
}
const MODEL = process.env.DISTILL_MODEL || "deepseek-reasoner";
// Exact-cost accumulators populated from the provider response when present
// (NeuralWatt returns per-request `cost.request_cost_usd` + `energy.energy_kwh`).
let PROVIDER_COST_USD = 0;
let ENERGY_KWH = 0;
let ENERGY_JOULES = 0;

// ───────────────────────── STEP 1: CLEAN ─────────────────────────
// Patterns adapted from session_receipt_backfill.ts (isHarnessUser/cleanText).

const isHarnessUser = (s: string): boolean => {
  const t = s.trimStart();
  return (
    /^#\s*AGENTS\.md/.test(t) ||
    /^#\s*CLAUDE\.md/.test(t) ||
    t.startsWith("<system-reminder>") ||
    t.startsWith("<command-name>") ||
    t.startsWith("<command-message>") ||
    t.startsWith("<environment_context>") ||
    t.startsWith("<local-command") ||
    t.includes("<INSTRUCTIONS>") ||
    /^<[a-z_]+[ >/]/i.test(t) || // any XML-ish injected block
    // /context dump (the big token-budget table the harness injects)
    /Context Usage|tokens used|tokens free|Token usage:|⛁|⛀/.test(t) ||
    // proxy/runtime plumbing errors that aren't Umut's words
    /^■ unexpected status \d+|compression_refused|Payload Too Large/.test(t)
  );
};

// Collapse whitespace, strip any embedded system-reminder.
const cleanText = (s: string): string =>
  s
    .replace(/<system-reminder>[\s\S]*?<\/system-reminder>/g, "")
    .replace(/\s+/g, " ")
    .trim();

type Turn = { role: "UMUT" | "AGENT"; text: string };

// ─────────────────── WORTH GATE + NEIGHBORHOOD ───────────────────
// Decision-turn definition — kept IDENTICAL to the worth-filter (scratchpad
// worth.ts) so the distiller and the bulk filter agree on which sessions are
// worth spending on. A decision-turn is a Umut/user turn that is 3–2000 chars
// AND matches DEC. (Harness/injection turns are already excluded upstream by
// isHarnessUser during clean, matching worth.ts's INJ/INJ2 filtering.)
const DEC =
  /\b(let'?s|we (should|need|could|can|must|do)|go ahead|yes|no|instead|actually|decided?|lock|i (want|think|prefer|like|don'?t|need)|why|can (we|you)|should we|this is (wrong|good|bad|important)|fix|implement|build|use|skip|stop|continue|proceed|approve|reject|change|rethink|make sure|do it|do that|don'?t|better|correct)\b/i;

const isDecisionTurn = (t: Turn): boolean =>
  t.role === "UMUT" && t.text.length >= 3 && t.text.length <= 2000 && DEC.test(t.text);

const countDecisionTurns = (turns: Turn[]): number => turns.reduce((n, t) => n + (isDecisionTurn(t) ? 1 : 0), 0);

// Worth-gate config: skip sessions with fewer than this many decision-turns
// (default 3 = the worth.ts MED threshold) unless forced.
const WORTH_MIN = Number(process.env.DISTILL_WORTH_MIN || 3);
const FORCE = process.argv.includes("--force") || process.env.DISTILL_FORCE === "1";

// Neighborhood-extraction config: reduce the cleaned turn list to decision
// windows (each Umut decision-turn + the agent turns that immediately follow
// it up to the next Umut turn). AUTO-ON for inputs > 50MB; flags override.
const NEIGHBORHOOD_AUTO_BYTES = 50_000_000;
function neighborhoodEnabled(rawBytes: number): boolean {
  if (process.argv.includes("--no-neighborhood") || process.env.DISTILL_NEIGHBORHOOD === "0") return false;
  if (process.argv.includes("--neighborhood") || process.env.DISTILL_NEIGHBORHOOD === "1") return true;
  return rawBytes > NEIGHBORHOOD_AUTO_BYTES;
}

// Keep each decision-turn plus the agent turn(s) up to the next Umut turn
// (the agent's response/conclusion to that decision). Drop non-decision Umut
// turns and the agent responses that belong only to them.
function extractNeighborhoods(turns: Turn[]): Turn[] {
  const out: Turn[] = [];
  for (let i = 0; i < turns.length; i++) {
    const t = turns[i];
    if (t.role !== "UMUT") continue;
    if (!isDecisionTurn(t)) continue;
    out.push(t);
    for (let j = i + 1; j < turns.length && turns[j].role === "AGENT"; j++) out.push(turns[j]);
  }
  return out;
}

type CleanResult = {
  turns: Turn[];
  rawBytes: number;
  cleanedChars: number;
  umutKept: number;
  harnessDropped: number;
  agentKept: number;
  toolResultsDropped: number;
  toolUseDropped: number;
  thinkingDropped: number;
};

// Mutable accumulator for the clean pass. Keeps ONLY the small signal (kept
// turns + counters) — never the whole raw file or a full line-array.
type CleanAcc = {
  turns: Turn[];
  umutKept: number;
  harnessDropped: number;
  agentKept: number;
  toolResultsDropped: number;
  toolUseDropped: number;
  thinkingDropped: number;
};

// Two on-disk session formats carry the same conversation. Normalize the Codex
// `rollout-*.jsonl` envelope into the Claude projects shape so the SAME signal
// definition applies to both (Umut/user turns + agent text kept; tools, thinking,
// reasoning, and harness/developer injections dropped). Claude lines pass through
// untouched. Returns null when the line is a non-conversational wrapper to skip.
function normalizeEvent(e: any): any | null {
  // Claude projects format: top-level role / type already match.
  if (e.type !== "response_item" && e.message === undefined && e.payload === undefined) return e;
  if (e.message !== undefined && e.payload === undefined) return e; // Claude wrapped message
  if (e.type !== "response_item") {
    // Claude session events (summary/system/etc.) — let existing role check skip them.
    if (e.payload === undefined) return e;
  }
  // Codex rollout format: the conversational item lives under payload.
  const p = e.payload;
  if (!p || typeof p !== "object") return null;
  const ptype = p.type;
  if (ptype === "message") {
    const role = p.role;
    // developer = harness/system injection (AGENTS.md, permissions) — drop like harness.
    if (role === "developer") return { message: { role: "user", content: "" }, __codexDeveloper: true };
    if (role !== "user" && role !== "assistant") return null;
    // Map input_text/output_text → Claude "text" blocks; everything else ignored here.
    const content = Array.isArray(p.content)
      ? p.content
          .filter((b: any) => b?.type === "input_text" || b?.type === "output_text" || b?.type === "text")
          .map((b: any) => ({ type: "text", text: b.text ?? "" }))
      : p.content;
    return { message: { role, content } };
  }
  if (ptype === "reasoning") {
    // Codex chain-of-thought → thinking (dropped, but counted).
    return { message: { role: "assistant", content: [{ type: "thinking" }] } };
  }
  if (ptype === "function_call" || ptype === "custom_tool_call" || ptype === "web_search_call") {
    return { message: { role: "assistant", content: [{ type: "tool_use" }] } };
  }
  if (ptype === "function_call_output" || ptype === "custom_tool_call_output") {
    return { message: { role: "user", content: [] }, toolUseResult: true };
  }
  return null; // other response_item types: skip
}

// Process ONE parsed JSONL event into the accumulator. This is the exact
// per-line cleaning/signal logic (Umut turns + agent text; tools/thinking
// dropped) — unchanged, just lifted out of the line loop so it can be fed
// incrementally by a streaming reader. A normalizer first folds the Codex
// rollout schema into the Claude shape so the signal definition is identical.
function processEvent(rawEvent: any, acc: CleanAcc): void {
  const e = normalizeEvent(rawEvent);
  if (e === null) return;
  // Codex developer/harness injection: count as dropped harness turn and stop.
  if (e.__codexDeveloper) {
    acc.harnessDropped++;
    return;
  }
  if (e.isSidechain === true) return; // subagent noise, not the main thread
  const msg = e.message ?? e;
  const role = msg?.role ?? e.type;
  if (role !== "user" && role !== "assistant") return; // skip wrapper events

  const content = msg?.content;

  if (role === "user") {
    // A user turn is either Umut's plain text OR a harness/tool injection.
    // Tool results arrive as content blocks of type tool_result (or the
    // event carries toolUseResult) — those are execution noise, drop them.
    if (e.toolUseResult !== undefined) {
      acc.toolResultsDropped++;
      return;
    }
    let text = "";
    if (typeof content === "string") {
      text = content;
    } else if (Array.isArray(content)) {
      const hasToolResult = content.some((b) => b?.type === "tool_result");
      if (hasToolResult) {
        acc.toolResultsDropped++;
        return;
      }
      text = content
        .filter((b) => b?.type === "text")
        .map((b) => b.text)
        .join("\n");
    }
    const cleaned = cleanText(text);
    if (!cleaned) return;
    if (isHarnessUser(cleaned)) {
      acc.harnessDropped++;
      return;
    }
    acc.turns.push({ role: "UMUT", text: cleaned });
    acc.umutKept++;
  } else {
    // assistant: keep text blocks only; strip thinking + tool_use.
    if (!Array.isArray(content)) {
      if (typeof content === "string" && cleanText(content)) {
        acc.turns.push({ role: "AGENT", text: cleanText(content) });
        acc.agentKept++;
      }
      return;
    }
    const textParts: string[] = [];
    for (const b of content) {
      if (b?.type === "text") textParts.push(b.text);
      else if (b?.type === "thinking") acc.thinkingDropped++;
      else if (b?.type === "tool_use") acc.toolUseDropped++;
    }
    const cleaned = cleanText(textParts.join("\n"));
    if (!cleaned) return;
    acc.turns.push({ role: "AGENT", text: cleaned });
    acc.agentKept++;
  }
}

// Stream the JSONL file line-by-line (bounded byte chunks split on newlines),
// parsing+processing each line as it arrives. Never holds the whole raw file
// or a full line-array — peak RSS stays bounded regardless of input size.
async function clean(inputPath: string): Promise<CleanResult> {
  const acc: CleanAcc = {
    turns: [],
    umutKept: 0,
    harnessDropped: 0,
    agentKept: 0,
    toolResultsDropped: 0,
    toolUseDropped: 0,
    thinkingDropped: 0,
  };
  const rawBytes = statSync(inputPath).size;

  const stream = Bun.file(inputPath).stream();
  const decoder = new TextDecoder();
  let buf = "";
  const consumeLine = (line: string) => {
    if (!line.trim()) return;
    let e: any;
    try {
      e = JSON.parse(line);
    } catch {
      return;
    }
    processEvent(e, acc);
  };

  for await (const chunk of stream) {
    buf += decoder.decode(chunk, { stream: true });
    let nl: number;
    while ((nl = buf.indexOf("\n")) !== -1) {
      consumeLine(buf.slice(0, nl));
      buf = buf.slice(nl + 1);
    }
  }
  // Flush trailing partial line (file may not end in a newline).
  buf += decoder.decode();
  if (buf.length) consumeLine(buf);

  const cleanedChars = acc.turns.reduce(
    (n, t) => n + t.role.length + 2 + t.text.length + 2,
    0,
  );

  return {
    turns: acc.turns,
    rawBytes,
    cleanedChars,
    umutKept: acc.umutKept,
    harnessDropped: acc.harnessDropped,
    agentKept: acc.agentKept,
    toolResultsDropped: acc.toolResultsDropped,
    toolUseDropped: acc.toolUseDropped,
    thinkingDropped: acc.thinkingDropped,
  };
}

// Render turns to a single role-tagged transcript string.
function render(turns: Turn[]): string {
  return turns.map((t) => `[${t.role}] ${t.text}`).join("\n\n");
}

// ───────────────────────── STEP 2: DISTILL ─────────────────────────

const DISTILL_SYSTEM = `You are distilling an AI agent work-session into durable insight for Umut's knowledge brain. This is REASONING, not summarization — go from noise to insight: identify what actually MATTERS and why; do not just shrink the text.
SOURCE ATTRIBUTION (this is an agent session): USER turns = Umut (but harness injections were already stripped). ASSISTANT turns = the AI agent, NOT Umut — never attribute the agent's words to Umut.
Produce a TYPED distillation:
1. SELECT/DECIDE — Umut's choices, decisions, locked positions, directions (highest value; quote him verbatim where memorable).
2. SEE/ASK — what Umut asked, questioned, reacted to; his concerns and instincts.
3. TELL/CONCLUDE — the agent's substantive conclusions/outcomes (attributed to the agent).
4. (DROP execution mechanics — not insight.)
For each: extract the THINKING — invent your own tags, surface key_insights, name PATTERNS in how Umut thinks/decides, note shifts/growth. Be specific and concrete. Output structured markdown with the four layers.`;

const MAP_SUFFIX = `\n\nThis is ONE CHUNK of a longer session (chunk {i} of {n}). Distill THIS chunk's typed insights now. Be exhaustive on real insight, terse on mechanics. Preserve, word-for-word, BOTH Umut's verbatim quotes AND specific factual findings (names, ids, provenance strings, file/commit refs, numbers) — the later synthesis pass depends on them and is forbidden from re-compressing them, so anything you generalize here is lost permanently.`;

const REDUCE_SYSTEM = `You are the SYNTHESIS pass of a reasoning-based distiller for Umut's knowledge brain. You receive several per-chunk typed distillations from one continuous agent work-session. Your job is CROSS-CHUNK REASONING — the thing a shallow truncated summary cannot do: connect threads across the whole session, resolve what Umut actually decided vs. merely floated, name the patterns in how he thinks, and surface the insights that only appear when you see the entire arc.

VERBATIM PRESERVATION (HARD RULE — re-compressing the chunk outputs is this pass's #1 failure mode):
- The chunk distillations already contain Umut's verbatim quotes and specific factual findings (names, ids, provenance strings, file/commit refs, numbers). Carry EVERY one through WORD-FOR-WORD. Never paraphrase a quote, never merge two quotes into one, never thin a concrete quote or a specific fact into a generalized "theme".
- Cross-chunk reasoning ADDS connective insight AROUND the preserved quotes and facts; it NEVER replaces them. If you catch yourself summarizing a quote or a specific fact, stop and paste it instead.
- Same quote in two chunks → keep it once, verbatim. Different quotes on one decision → keep ALL of them. When unsure whether to keep a quote/fact, keep it — completeness of Umut's actual words beats brevity.

Keep the same SOURCE ATTRIBUTION discipline: USER = Umut, ASSISTANT = the agent (never attribute the agent's words to Umut).
Produce the FINAL typed distillation with the four layers:
1. SELECT/DECIDE — Umut's choices, decisions, locked positions, directions (highest value; carry through every verbatim Umut quote from the chunk distillations, word-for-word).
2. SEE/ASK — what Umut asked, questioned, reacted to; concerns and instincts (keep his verbatim questions).
3. TELL/CONCLUDE — the agent's substantive conclusions/outcomes (attributed to the agent); keep specific findings (names, ids, commits, numbers) intact.
4. (DROP execution mechanics.)
Invent your own tags, surface key_insights, name PATTERNS in how Umut thinks/decides, note shifts/growth across the session. Be specific and concrete. Output structured markdown.`;

// Structured extraction of DURABLE decisions for the meaning-ledger page.
// Bakes in the Oracle's authority-laundering guard: exploration must NOT become a decision.
const LEDGER_EXTRACT_SYSTEM = `You convert a session distillation into a DECISION LEDGER — only the durable decisions worth recalling later.

EVIDENCE CONTRACT (critical — do NOT launder exploration into decisions):
Include an item ONLY if the USER (Umut) actually committed, shown by one of:
- explicit decision language ("we decided", "lock this", "let's do X", "ship it", "final")
- accepted recommendation (user said "yes", "do that", "go ahead", "approved", "please do")
- a stated standing preference ("I always", "by default", "never")
- a correction ("no, do it this way", "remember it as X")
A hypothetical / question / "maybe we should…" / an agent's own conclusion is NOT a user decision — DROP it.

COVERAGE: be COMPREHENSIVE on DURABLE decisions — capture EVERY strategic/architectural/standing decision (what to build, keep, retire, how the system should work, lasting preferences). Do NOT drop one because it resembles another; if one decision has two clauses (e.g. "build X AND keep Y"), capture BOTH clauses in the statement.
MATERIALITY: prefer decisions with lasting consequence. DROP one-off operational approvals that won't matter next week ("continue", "go ahead on this step", "investigate that now") unless they encode a standing choice.
FINDABILITY: each "statement" must NAME the concrete entities/systems it is about (e.g. gbrain, distillation layer, hook registration, 4090 GPU, owned-l2-facts, source boost) so it is retrievable by topic — never a vague "lock that decision".

Output ONLY valid JSON, no prose, no code fence: an array of
[{"statement":"<imperative decision naming its entities, in Umut's intent>","quote":"<verbatim Umut words>","confidence":"high|medium","domain":"<short-kebab-topic>"}]
Use Umut's verbatim words for "quote". If there are no durable user decisions, output [].`;

type Usage = { prompt_tokens: number; completion_tokens: number };

// Pull exact cost/energy out of any object shape NeuralWatt may emit
// (top-level `cost`/`energy`, or a flattened `usage.cost`/`usage.energy`).
// Returns whether anything was found so a header fallback can fill the gap.
function captureCostEnergy(obj: any): { cost: boolean; energy: boolean } {
  let cost = false,
    energy = false;
  if (!obj || typeof obj !== "object") return { cost, energy };
  const costObj = obj.cost ?? obj.usage?.cost;
  const energyObj = obj.energy ?? obj.usage?.energy;
  const rc = costObj?.request_cost_usd ?? costObj?.total_cost_usd ?? costObj?.cost_usd;
  if (rc != null) {
    PROVIDER_COST_USD += Number(rc) || 0;
    cost = true;
  }
  if (energyObj?.energy_kwh != null) {
    ENERGY_KWH += Number(energyObj.energy_kwh) || 0;
    energy = true;
  }
  if (energyObj?.energy_joules != null) {
    ENERGY_JOULES += Number(energyObj.energy_joules) || 0;
    energy = true;
  }
  return { cost, energy };
}

// Last NeuralWatt budget-remaining value seen on a response header (for reporting).
let BUDGET_REMAINING_USD: number | null = null;

const RETRYABLE = (status: number) => status >= 500 || status === 524 || status === 429;
const sleep = (ms: number) => new Promise((r) => setTimeout(r, ms));

// ONE streamed (SSE) chat-completion request. Streaming keeps the Cloudflare
// edge connection alive (tokens flow) — this is the 524 fix for NeuralWatt's
// NeuralWatt edge, which 524s a non-streaming request that idle-holds >~100s.
async function llmCallOnce(
  system: string,
  user: string,
): Promise<{ content: string; usage: Usage }> {
  const res = await fetch(`${BASE_URL}/chat/completions`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${API_KEY}`,
      Accept: "text/event-stream",
    },
    body: JSON.stringify({
      model: MODEL,
      messages: [
        { role: "system", content: system },
        { role: "user", content: user },
      ],
      temperature: 0.3,
      max_tokens: 8000,
      stream: true,
      // OpenAI-compatible: ask providers to include usage in the final stream chunk.
      stream_options: { include_usage: true },
    }),
  });
  // Capture cost/budget headers regardless of body shape.
  const hCost = res.headers.get("x-request-cost-usd");
  if (hCost != null) PROVIDER_COST_USD += Number(hCost) || 0;
  const hBudget = res.headers.get("x-budget-remaining-usd");
  if (hBudget != null) BUDGET_REMAINING_USD = Number(hBudget);
  const hEnergyKwh = res.headers.get("x-energy-kwh");
  if (hEnergyKwh != null) ENERGY_KWH += Number(hEnergyKwh) || 0;
  const hEnergyJ = res.headers.get("x-energy-joules");
  if (hEnergyJ != null) ENERGY_JOULES += Number(hEnergyJ) || 0;

  if (!res.ok) {
    const body = await res.text().catch(() => "");
    const err: any = new Error(`${MODEL} ${res.status}: ${body.slice(0, 500)}`);
    err.status = res.status;
    throw err;
  }

  const ctype = res.headers.get("content-type") || "";

  // Fallback path: some providers (or error envelopes) return a single
  // non-SSE JSON body even when stream:true was requested. Detect and parse.
  if (!ctype.includes("text/event-stream")) {
    const text = await res.text();
    try {
      const json: any = JSON.parse(text);
      captureCostEnergy(json);
      return {
        content: json.choices?.[0]?.message?.content ?? "",
        usage: json.usage ?? { prompt_tokens: 0, completion_tokens: 0 },
      };
    } catch {
      throw new Error(`${MODEL}: non-SSE, non-JSON body: ${text.slice(0, 300)}`);
    }
  }

  // SSE path: accumulate delta.content; ignore reasoning deltas for the output
  // text (they still count in the provider's usage). Final chunk(s) carry usage
  // and may carry a trailing cost/energy object.
  if (!res.body) throw new Error(`${MODEL}: empty stream body`);
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  let content = "";
  let usage: Usage = { prompt_tokens: 0, completion_tokens: 0 };
  let gotCost = false;

  const handleEvent = (data: string) => {
    if (data === "[DONE]") return;
    let evt: any;
    try {
      evt = JSON.parse(data);
    } catch {
      return; // partial / keep-alive comment line
    }
    const delta = evt.choices?.[0]?.delta;
    if (delta?.content) content += delta.content;
    // delta.reasoning / delta.reasoning_content are intentionally ignored for output.
    if (evt.usage) usage = evt.usage;
    const cap = captureCostEnergy(evt);
    if (cap.cost || cap.energy) gotCost = true;
  };

  // NeuralWatt emits cost/energy as SSE *comment* lines: `: energy {json}` and
  // `: cost {json}` (NOT data: events, NOT headers). Parse those too.
  const handleComment = (comment: string) => {
    const m = comment.match(/^\s*(energy|cost)\s+(\{[\s\S]*\})\s*$/);
    if (!m) return;
    let obj: any;
    try {
      obj = JSON.parse(m[2]);
    } catch {
      return;
    }
    if (m[1] === "cost") {
      if (obj.request_cost_usd != null) {
        PROVIDER_COST_USD += Number(obj.request_cost_usd) || 0;
        gotCost = true;
      }
      const br = obj.budget_remaining_usd ?? obj.allowance_remaining_usd;
      if (br != null) BUDGET_REMAINING_USD = Number(br);
    } else {
      if (obj.energy_kwh != null) {
        ENERGY_KWH += Number(obj.energy_kwh) || 0;
        gotCost = true;
      }
      if (obj.energy_joules != null) {
        ENERGY_JOULES += Number(obj.energy_joules) || 0;
        gotCost = true;
      }
    }
  };

  const handleLine = (line: string) => {
    if (line.startsWith("data:")) handleEvent(line.slice(5).trimStart());
    else if (line.startsWith(":")) handleComment(line.slice(1));
  };

  for (;;) {
    const { done, value } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    let nl: number;
    while ((nl = buf.indexOf("\n")) !== -1) {
      handleLine(buf.slice(0, nl).replace(/\r$/, ""));
      buf = buf.slice(nl + 1);
    }
  }
  // Flush any trailing buffered line.
  const tail = buf.replace(/\r$/, "");
  if (tail) handleLine(tail);

  // If the stream carried no body cost but the header did, that's already counted.
  void gotCost;
  return { content, usage };
}

// Retry wrapper: up to 4 attempts, exponential backoff (2s, 4s, 8s), retry on
// 5xx / 524 / 429 / network error / timeout. Re-throw after the final attempt.
async function deepseek(
  system: string,
  user: string,
): Promise<{ content: string; usage: Usage }> {
  const backoffs = [2000, 4000, 8000];
  let lastErr: any;
  for (let attempt = 0; attempt < 4; attempt++) {
    try {
      return await llmCallOnce(system, user);
    } catch (e: any) {
      lastErr = e;
      const status = e?.status;
      const retryable = status == null ? true : RETRYABLE(status); // no status = network/timeout
      if (attempt < 3 && retryable) {
        const wait = backoffs[attempt];
        console.error(
          `    [retry ${attempt + 1}/3] ${MODEL} call failed (${e?.message || e}); backing off ${wait / 1000}s`,
        );
        await sleep(wait);
        continue;
      }
      throw lastErr;
    }
  }
  throw lastErr;
}

// Split rendered transcript into chunks under a char budget, on turn boundaries.
// deepseek-reasoner = 128K context. ~3.5 chars/token → keep input chunk well
// under that to leave room for reasoning + output. 220K chars ≈ 60K tokens.
const CHUNK_CHARS = Number(process.env.DISTILL_CHUNK_CHARS || 220_000);

function chunkOnTurns(turns: Turn[]): string[] {
  const chunks: string[] = [];
  let buf: string[] = [];
  let size = 0;
  for (const t of turns) {
    const block = `[${t.role}] ${t.text}`;
    if (size + block.length > CHUNK_CHARS && buf.length) {
      chunks.push(buf.join("\n\n"));
      buf = [];
      size = 0;
    }
    buf.push(block);
    size += block.length + 2;
  }
  if (buf.length) chunks.push(buf.join("\n\n"));
  return chunks;
}

// Hierarchical-reduce fan-in: never feed more than this many distillations into
// a single REDUCE call (bounds each reduce's input size at bulk scale).
const REDUCE_BATCH = Number(process.env.DISTILL_REDUCE_BATCH || 8);

// Map-call concurrency cap (keep it simple; sequential when 1).
const MAP_CONCURRENCY = Math.max(1, Number(process.env.DISTILL_MAP_CONCURRENCY || 3));

type Decision = { statement: string; quote?: string; confidence?: string; domain?: string };

// Render the gbrain-searchable decision-ledger page (the surface gbrain already indexes;
// boosted via GBRAIN_SOURCE_BOOST="meaning/decisions/:1.8"). Derived artifact — never hand-edited.
function renderLedger(decisions: Decision[], slug: string, sessionPath: string): string {
  const date = new Date().toISOString().slice(0, 10);
  const domain = (slug.split("/").pop() || "decisions").replace(/-\d{4}-\d{2}-\d{2}$/, "");
  const aliases = Array.from(new Set(decisions.map((d) => d.domain).filter(Boolean))).slice(0, 8);
  const head = [
    "---",
    "type: decision-ledger",
    `domain: ${domain}`,
    `generated_from: ${sessionPath}`,
    "generated_by: reasoning-distill-prototype",
    `updated_at: ${date}`,
    "status: active",
    ...(aliases.length ? ["aliases:", ...aliases.map((a) => `  - ${a}`)] : []),
    "---",
    "",
    `# Decisions — ${domain} (${date})`,
    "",
    "Derived artifact (generated from distilled decisions; do not hand-edit).",
    `Source session: \`${sessionPath}\`.`,
    "",
    "## Active decisions",
    "",
  ];
  const body = decisions.map((d) => {
    const lines = [`- **[SELECT]** ${d.statement}`];
    lines.push(`  - confidence: ${d.confidence || "medium"}${d.domain ? ` · domain: ${d.domain}` : ""}`);
    if (d.quote) lines.push(`  - quote: "${d.quote.replace(/"/g, "'")}"`);
    return lines.join("\n");
  });
  return head.join("\n") + body.join("\n\n") + "\n";
}

async function main() {
  console.error(`[1/3] cleaning ${INPUT} ...`);
  const c = await clean(INPUT); // streaming line reader — never holds the whole raw file
  const transcript = render(c.turns);

  const stats = {
    rawBytes: c.rawBytes,
    cleanedChars: c.cleanedChars,
    cleanedBytes: Buffer.byteLength(transcript),
    umutKept: c.umutKept,
    harnessDropped: c.harnessDropped,
    agentKept: c.agentKept,
    toolResultsDropped: c.toolResultsDropped,
    toolUseDropped: c.toolUseDropped,
    thinkingDropped: c.thinkingDropped,
  };
  console.error("    stats:", JSON.stringify(stats));

  // ── WORTH GATE (no LLM): count Umut decision-turns; skip low-worth before any spend.
  const decisionTurns = countDecisionTurns(c.turns);
  console.error(`worth: ${decisionTurns} decision-turns`);
  if (decisionTurns < WORTH_MIN && !FORCE) {
    console.error(`SKIP: low worth (${decisionTurns} decision-turns < ${WORTH_MIN})`);
    process.exit(0); // no LLM call, no spend
  }
  if (decisionTurns < WORTH_MIN && FORCE) {
    console.error(`    (--force: distilling despite low worth)`);
  }

  // ── NEIGHBORHOOD EXTRACTION: for giants, distill only decision-windows, not noise.
  let turns = c.turns;
  if (neighborhoodEnabled(c.rawBytes)) {
    const before = turns.length;
    const beforeChars = c.cleanedChars;
    turns = extractNeighborhoods(turns);
    const afterChars = turns.reduce((n, t) => n + t.role.length + 2 + t.text.length + 2, 0);
    console.error(
      `neighborhood: kept ${turns.length}/${before} turns, ${(beforeChars / 1000).toFixed(0)}k→${(afterChars / 1000).toFixed(0)}k signal chars`,
    );
  }

  const chunks = chunkOnTurns(turns);

  // Shared token accounting (helpers mutate it so hierarchical reduce stays simple).
  const acct = { calls: 0, promptTokens: 0, completionTokens: 0 };
  const call = async (system: string, user: string): Promise<string> => {
    const { content, usage } = await deepseek(system, user);
    acct.calls++;
    acct.promptTokens += usage.prompt_tokens;
    acct.completionTokens += usage.completion_tokens;
    return content;
  };

  let finalDistillation = "";

  if (chunks.length === 1) {
    // 1 chunk → single distill call, no separate reduce.
    console.error(`[2/3] distilling with ${MODEL}: 1 chunk (single distill, no reduce) ...`);
    finalDistillation = await call(
      DISTILL_SYSTEM,
      `Here is the full cleaned, role-tagged agent session. Distill it now per your instructions.\n\n${chunks[0]}`,
    );
  } else {
    // 2..N or >N → map every chunk, then reduce (flat or hierarchical).
    const hierarchical = chunks.length > REDUCE_BATCH;
    console.error(
      `[2/3] distilling with ${MODEL}: ${chunks.length} chunks (map @ ${MAP_CONCURRENCY}x) + ${
        hierarchical ? `hierarchical reduce (batch=${REDUCE_BATCH})` : "1 reduce"
      } ...`,
    );

    // MAP — concurrency-limited, results ordered by chunk index.
    const partials: string[] = new Array(chunks.length);
    let next = 0;
    let done = 0;
    const worker = async () => {
      for (;;) {
        const i = next++;
        if (i >= chunks.length) return;
        const sys = DISTILL_SYSTEM + MAP_SUFFIX.replace("{i}", String(i + 1)).replace("{n}", String(chunks.length));
        const content = await call(
          sys,
          `Chunk ${i + 1}/${chunks.length} of the cleaned, role-tagged agent session:\n\n${chunks[i]}`,
        );
        partials[i] = `## CHUNK ${i + 1}/${chunks.length} DISTILLATION\n\n${content}`;
        console.error(`    map ${++done}/${chunks.length} done (chunk ${i + 1})`);
      }
    };
    await Promise.all(Array.from({ length: Math.min(MAP_CONCURRENCY, chunks.length) }, worker));

    // REDUCE — feed at most REDUCE_BATCH distillations per call. If there are
    // more than one batch, reduce each batch then recurse over the batch
    // summaries until ≤ REDUCE_BATCH remain, then a final reduce.
    const reduceBatch = async (items: string[], label: string): Promise<string> =>
      call(
        REDUCE_SYSTEM,
        `Here are ${items.length} per-chunk distillations (${label}) from one continuous session. Synthesize the typed distillation now, reasoning across all of them.\n\n${items.join("\n\n---\n\n")}`,
      );

    let level: string[] = partials;
    let depth = 0;
    while (level.length > REDUCE_BATCH) {
      const nextLevel: string[] = [];
      for (let i = 0; i < level.length; i += REDUCE_BATCH) {
        const batch = level.slice(i, i + REDUCE_BATCH);
        const summary = await reduceBatch(batch, `L${depth} batch ${nextLevel.length + 1}`);
        nextLevel.push(`## L${depth} BATCH ${nextLevel.length + 1} SYNTHESIS\n\n${summary}`);
        console.error(`    reduce L${depth} batch ${nextLevel.length} done (${batch.length} in)`);
      }
      level = nextLevel;
      depth++;
    }
    // Final reduce over the ≤ REDUCE_BATCH remaining summaries.
    finalDistillation = await call(
      REDUCE_SYSTEM,
      `Here are the per-chunk distillations from one continuous session. Synthesize the FINAL typed distillation now, reasoning across all chunks.\n\n${level.join("\n\n---\n\n")}`,
    );
    console.error(`    final reduce done (${level.length} in)`);
  }

  let calls = acct.calls;
  let promptTokens = acct.promptTokens;
  let completionTokens = acct.completionTokens;

  // Optional: emit a gbrain-searchable decision-ledger page (--ledger [--slug=meaning/decisions/...]).
  // Path B+: materialize meaning into a page gbrain already indexes (boosted via GBRAIN_SOURCE_BOOST).
  if (process.argv.includes("--ledger")) {
    console.error("[ledger] extracting durable decisions (authority-laundering guard) ...");
    const { content, usage } = await deepseek(LEDGER_EXTRACT_SYSTEM, finalDistillation);
    calls++;
    promptTokens += usage.prompt_tokens;
    completionTokens += usage.completion_tokens;
    let decisions: Decision[] = [];
    try {
      const j = content.trim().replace(/^```(json)?/i, "").replace(/```$/, "").trim();
      const parsed = JSON.parse(j);
      decisions = Array.isArray(parsed) ? parsed : [];
    } catch (e) {
      console.error("[ledger] JSON parse failed:", (e as Error).message);
    }
    const slugArg = (process.argv.find((a) => a.startsWith("--slug=")) || "").split("=")[1];
    const slug =
      slugArg ||
      `meaning/decisions/distilled-${(INPUT.split("/").pop() || "session").replace(/\.jsonl$/, "").slice(0, 60)}`;
    const ledger = renderLedger(decisions, slug, INPUT);
    writeFileSync(join(OUTDIR, "decision-ledger.generated.md"), ledger, "utf8");
    console.error(`[ledger] ${decisions.length} durable decision(s) → decision-ledger.generated.md (slug: ${slug})`);
  }

  // Cost: prefer the provider's EXACT per-request cost (NeuralWatt returns it); else token-estimate.
  const IN_PER_M = Number(process.env.DISTILL_IN_PER_M || 0.55);
  const OUT_PER_M = Number(process.env.DISTILL_OUT_PER_M || 2.19);
  const tokenEstCost = (promptTokens / 1e6) * IN_PER_M + (completionTokens / 1e6) * OUT_PER_M;
  const hasExact = PROVIDER_COST_USD > 0 || ENERGY_KWH > 0;
  const cost = hasExact ? PROVIDER_COST_USD : tokenEstCost;
  const costLine = hasExact
    ? `EXACT provider cost $${PROVIDER_COST_USD.toFixed(6)} · energy ${ENERGY_KWH.toExponential(3)} kWh (${ENERGY_JOULES.toFixed(2)} J) · [token-est would be $${tokenEstCost.toFixed(4)} @ ${IN_PER_M}/${OUT_PER_M} per M]`
    : `token-estimate $${tokenEstCost.toFixed(4)} (@ ${IN_PER_M}/${OUT_PER_M} per M — no provider cost in response)`;

  console.error(`[3/3] writing output ...`);

  const out = [
    `# Reasoning-Distill Prototype — Output`,
    ``,
    `- input: \`${INPUT}\``,
    `- model: \`${MODEL}\` @ \`${BASE_URL}\` · calls: ${calls} · chunks: ${chunks.length}`,
    `- tokens: ${promptTokens} in / ${completionTokens} out · ${costLine}`,
    ``,
    `## CLEANING STATS`,
    ``,
    `| metric | value |`,
    `|---|---|`,
    `| raw size | ${(stats.rawBytes / 1e6).toFixed(2)} MB (${stats.rawBytes.toLocaleString()} bytes) |`,
    `| cleaned size | ${(stats.cleanedBytes / 1e3).toFixed(1)} KB (${stats.cleanedBytes.toLocaleString()} bytes) |`,
    `| shrink ratio | ${(stats.rawBytes / stats.cleanedBytes).toFixed(1)}x |`,
    `| Umut turns kept | ${stats.umutKept} |`,
    `| harness turns dropped | ${stats.harnessDropped} |`,
    `| agent turns kept | ${stats.agentKept} |`,
    `| tool_result blocks dropped | ${stats.toolResultsDropped} |`,
    `| tool_use blocks dropped | ${stats.toolUseDropped} |`,
    `| thinking blocks dropped | ${stats.thinkingDropped} |`,
    ``,
    `## TYPED DISTILLATION (verbatim model output)`,
    ``,
    finalDistillation,
    ``,
  ].join("\n");

  writeFileSync(join(OUTDIR, "output.md"), out, "utf8");
  // Also dump the cleaned transcript for inspection / re-runs.
  writeFileSync(join(OUTDIR, "cleaned.txt"), transcript, "utf8");

  const peakRssMb = process.memoryUsage().rss / 1e6;
  console.error(`done → ${join(OUTDIR, "output.md")}`);
  console.error(`COST [${MODEL} @ ${BASE_URL}]: ${costLine}`);
  console.error(
    `MEM: rss ${peakRssMb.toFixed(1)} MB · chunks ${chunks.length} · llm calls ${calls}${
      BUDGET_REMAINING_USD != null ? ` · budget-remaining $${BUDGET_REMAINING_USD.toFixed(6)}` : ""
    }`,
  );
  // Emit a compact machine summary to stdout for the caller.
  console.log(
    JSON.stringify({
      ...stats,
      model: MODEL,
      baseUrl: BASE_URL,
      decisionTurns,
      neighborhood: neighborhoodEnabled(c.rawBytes),
      calls,
      chunks: chunks.length,
      promptTokens,
      completionTokens,
      providerCostUsd: Number(PROVIDER_COST_USD.toFixed(6)),
      energyKwh: ENERGY_KWH,
      energyJoules: Number(ENERGY_JOULES.toFixed(3)),
      tokenEstCostUsd: Number(tokenEstCost.toFixed(4)),
      costUsd: Number(cost.toFixed(6)),
      budgetRemainingUsd: BUDGET_REMAINING_USD,
      rssMb: Number(peakRssMb.toFixed(1)),
    }),
  );
}

main().catch((e) => {
  console.error("FATAL:", e?.message || e);
  process.exit(1);
});

/*
 * pack.ts — Memory layer: assemble ONE evidence-backed context pack.
 *
 *   Part A — POSITIONS (evidence-pure): distilled positions (depth) + engine
 *            breadth hits. Each carries its source. A DURABILITY FILTER drops
 *            session-operational noise so the pack stays durable + on-topic.
 *   Part B — RECURRING CONNECTIONS (synthesized): the A->B->C through-lines,
 *            with a GROUNDING GUARD that flags any connection citing a source
 *            not in the material. Evidence, not mystical bullshit.
 *
 * Storage/retrieval is RENTED via the injected Engine — no backend imported here.
 */
import { readFileSync, writeFileSync, existsSync, mkdirSync } from "node:fs";
import { join, dirname } from "node:path";
import type { Engine } from "../engine/types.ts";

const HOME = process.env.HOME || "";
const BASE_URL = (process.env.DISTILL_BASE_URL || "https://api.deepseek.com/v1").replace(/\/+$/, "");
const API_KEY = process.env.DISTILL_API_KEY || process.env.DEEPSEEK_API_KEY;
const MODEL = process.env.PACK_MODEL || "deepseek-chat";

export type Position = { claim: string; quote?: string; domain?: string; durable: boolean };

// ── durability filter — keep durable/architectural, drop session-operational ──
const OPERATIONAL = /\b(enroll|run all|overnight|proceed|finish|phase [ab]\b|go ahead|go live|today|backfill|retr(y|ies)|\d+ apps|588|newest-first|double execution|do it all)\b/i;
const OPERATIONAL_DOMAIN = /(analytics|data-ingestion|operations|delivery|revenue|governor)/i;
export function isDurable(claim: string, domain = ""): boolean {
  if (OPERATIONAL.test(claim)) return false;
  if (OPERATIONAL_DOMAIN.test(domain)) return false;
  return true;
}

export function readLedger(path: string): { positions: Position[]; source: string } {
  if (!existsSync(path)) return { positions: [], source: "" };
  const text = readFileSync(path, "utf8");
  const source = (text.match(/generated_from:\s*(.+)/)?.[1] || "distilled-session").trim();
  const positions: Position[] = [];
  for (const b of text.split(/\n- \*\*\[SELECT\]\*\* /).slice(1)) {
    const lines = b.split("\n");
    const claim = lines[0].trim();
    if (!claim) continue;
    const meta = lines.find((l) => l.includes("confidence:")) || "";
    const domain = (meta.match(/domain:\s*([^\s·]+)/)?.[1] || "").trim();
    const ql = lines.find((l) => l.includes("quote:"));
    const quote = ql ? ql.replace(/.*quote:\s*/, "").trim().replace(/^["']|["']$/g, "") : undefined;
    positions.push({ claim, quote, domain, durable: isDurable(claim, domain) });
  }
  return { positions, source };
}

// ── Part B: synthesize recurring connections, grounded ──
const CONNECT_SYSTEM = `You assemble a CONTEXT PACK from Umut's OWN material on a topic. Name the RECURRING CONNECTIONS — the concept-chains he repeatedly makes (A -> B -> C), the through-lines across his decisions and writings. Not a summary.

HARD GROUNDING RULE: every connection MUST end with one or more source tags drawn ONLY from the SOURCES list, formatted exactly as [src: <token>] (copy the token verbatim). If you cannot ground a connection in the provided material, DO NOT state it. No outside knowledge, no inventions. Prefer 4-8 strong connections.

Also output 3-6 TRIGGER ALIASES (short phrases that mean this pack should load).

Output bounded markdown: "### Recurring connections" (bulleted, each ending in [src: ...]) then "### Trigger aliases" (comma-separated). Nothing else.`;

async function synthesize(material: string): Promise<string> {
  if (!API_KEY) return "_(no API key — connection synthesis skipped)_";
  const res = await fetch(`${BASE_URL}/chat/completions`, {
    method: "POST",
    headers: { "content-type": "application/json", authorization: `Bearer ${API_KEY}` },
    body: JSON.stringify({
      model: MODEL,
      messages: [{ role: "system", content: CONNECT_SYSTEM }, { role: "user", content: material }],
      temperature: 0.2,
    }),
  });
  if (!res.ok) {
    console.error(`[pack] synthesis ${res.status}: ${(await res.text().catch(() => "")).slice(0, 160)}`);
    return "_(connection synthesis failed)_";
  }
  return (await res.json() as any).choices?.[0]?.message?.content ?? "";
}

function verifyGrounding(text: string, valid: string[]): { text: string; total: number; bad: number } {
  let total = 0, bad = 0;
  const out = text.replace(/\[src:\s*([^\]]+)\]/g, (_full, raw) => {
    total++;
    const token = String(raw).trim();
    const ok = valid.some((v) => v.includes(token) || token.includes(v.split("/").pop() || v));
    if (ok) return `\`${token}\``;
    bad++;
    return `\`${token}\` ⚠UNVERIFIED`;
  });
  return { text: out, total, bad };
}

const slugify = (s: string) => s.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "").slice(0, 60);

export async function buildPack(
  engine: Engine,
  topic: string,
  opts: { ledger?: string; limit?: number; out?: string } = {},
): Promise<string> {
  const ledgerPath = opts.ledger || join(HOME, ".compound", "distill", "decision-ledger.generated.md");
  const out = opts.out || join(HOME, ".compound", "packs", `${slugify(topic)}.md`);
  const limit = opts.limit ?? 12;

  const { positions, source } = readLedger(ledgerPath);
  const durable = positions.filter((p) => p.durable);
  const dropped = positions.filter((p) => !p.durable);
  console.error(`[pack] ${durable.length} durable positions kept, ${dropped.length} operational dropped (source: ${source || "—"})`);

  const hits = await engine.query(topic, { limit });
  console.error(`[pack] ${hits.length} breadth hits via engine:${engine.name}`);

  const valid = [source, ...hits.map((h) => h.slug)].filter(Boolean);
  const material = [
    `TOPIC: ${topic}`, ``,
    `SOURCES (use ONLY these tokens in [src: ...]):`,
    `- ${source}`, ...hits.map((h) => `- ${h.slug}`), ``,
    `## Durable positions (from ${source}):`,
    ...durable.map((p, i) => `${i + 1}. ${p.claim}${p.quote ? `  — "${p.quote}"` : ""}`), ``,
    `## Prior writings (engine breadth):`,
    ...hits.map((h) => `- [${h.slug}] ${h.snippet}`),
  ].join("\n");

  const { text: connections, total, bad } = verifyGrounding(await synthesize(material), valid);
  const date = new Date().toISOString().slice(0, 10);

  const pack = [
    `---`, `type: context-pack`, `topic: "${topic}"`, `engine: ${engine.name}`,
    `generated_at: ${date}`, `depth_source: ${source}`, `breadth_hits: ${hits.length}`,
    `positions_kept: ${durable.length}`, `positions_dropped: ${dropped.length}`,
    `grounding: ${total - bad}/${total} verified`, `---`, ``,
    `# Context Pack — ${topic}`, ``,
    `> Reusable context from your own history. Inject before a task instead of re-explaining yourself.`, ``,
    `## A. Positions (evidence-pure)`, ``,
    `**Durable positions** (from \`${source}\`):`,
    ...(durable.length ? durable.map((p) => `- ${p.claim}${p.quote ? `\n  - _"${p.quote}"_` : ""}`) : ["- _(none — run distill first)_"]), ``,
    `**Prior writings** (engine breadth — each a recallable page):`,
    ...(hits.length ? hits.slice(0, 10).map((h) => `- \`${h.slug}\` — ${h.snippet}`) : ["- _(none)_"]), ``,
    `## B. Recurring connections (synthesized — grounded or flagged)`, ``,
    connections || "_(none)_", ``,
    `---`,
    `_Grounding: ${total - bad}/${total} citations matched a real source; ${bad} flagged ⚠UNVERIFIED._`,
    dropped.length ? `_Dropped as session-operational: ${dropped.map((p) => p.claim.slice(0, 40)).join(" · ")}_` : "",
    ``,
  ].join("\n");

  if (!existsSync(dirname(out))) mkdirSync(dirname(out), { recursive: true });
  writeFileSync(out, pack);
  console.error(`[pack] wrote ${out} — grounding ${total - bad}/${total}`);
  return out;
}

import { readdir, readFile } from "node:fs/promises";
import path from "node:path";

import { arr, COMPOUND_HOME, privateFallbacksEnabled, runJson, type Json, str } from "@/lib/compound";

const MEMORY_INBOX = path.resolve(COMPOUND_HOME, "inbox", "memory-candidates");
const KNOWLEDGE_EXTRACTION_INBOX = path.resolve(COMPOUND_HOME, "inbox", "knowledge-extractions");

export type MemoryCandidate = Json & {
  id: string;
  json_path?: string;
  console_source?: Json;
};

function asObject(value: unknown): Json {
  return value && typeof value === "object" && !Array.isArray(value) ? (value as Json) : {};
}

function evidenceRef(value: unknown): string {
  const s = str(value).trim();
  return /^ev_[a-z0-9]+$/.test(s) ? s : "";
}

function candidateWithId(item: Json): MemoryCandidate | null {
  const id = str(item.id);
  return id ? ({ ...item, id } as MemoryCandidate) : null;
}

export function isKnowledgeClassCandidate(candidate: Json): boolean {
  const qualifiers = asObject(candidate.qualifiers);
  const entity = str(candidate.entity).trim();
  const source = str(qualifiers.source).trim();
  return (
    entity.startsWith("knowledge:") ||
    source === "knowledge_import" ||
    source === "knowledge_extraction"
  );
}

export function isActionableMemoryCandidate(candidate: Json): boolean {
  return str(candidate.state) === "proposed" && !isKnowledgeClassCandidate(candidate);
}

async function consoleSourceFor(candidate: Json): Promise<Json | null> {
  const qualifiers = asObject(candidate.qualifiers);
  const traceRef = str(candidate.source_trace_ref);
  let extractionId = str(qualifiers.extraction_id);
  let evidence = evidenceRef(traceRef) || evidenceRef(qualifiers.evidence_ref);

  if (!extractionId && traceRef.startsWith("knowledge_extraction:")) {
    extractionId = traceRef.slice("knowledge_extraction:".length);
  }

  const out: Json = {};
  if (!privateFallbacksEnabled()) {
    if (evidence) out.evidence_ref = evidence;
    return Object.keys(out).length ? out : null;
  }

  if (extractionId && /^kx_[a-z0-9]+$/i.test(extractionId)) {
    const extractionPath = path.resolve(KNOWLEDGE_EXTRACTION_INBOX, `${extractionId}.json`);
    out.extraction_id = extractionId;
    out.extraction_path = extractionPath;
    try {
      const extraction = JSON.parse(await readFile(extractionPath, "utf8")) as Json;
      evidence = evidence || evidenceRef(extraction.evidence_ref);
      if (str(extraction.item_ref)) out.item_ref = str(extraction.item_ref);
      if (str(extraction.title)) out.title = str(extraction.title);
      if (str(extraction.evidence_ref)) out.evidence_ref = str(extraction.evidence_ref);
    } catch {
      /* details panel can still show the candidate without extraction source */
    }
  }

  if (evidence) out.evidence_ref = evidence;
  return Object.keys(out).length ? out : null;
}

async function loadFromProjection(filterState?: string): Promise<MemoryCandidate[] | null> {
  const args = ["memory", "candidates", "--json"];
  if (filterState) args.push("--state", filterState);
  args.push("--include-evidence");

  const data = await runJson(args);
  if (!data) return null;

  const items = arr(data.items)
    .map((item) => candidateWithId(asObject(item)))
    .filter((item): item is MemoryCandidate => Boolean(item));
  return items;
}

async function loadFromInbox(filterState?: string): Promise<MemoryCandidate[]> {
  if (!privateFallbacksEnabled()) return [];

  let entries: string[];
  try {
    entries = await readdir(MEMORY_INBOX);
  } catch {
    return [];
  }

  const out: MemoryCandidate[] = [];
  for (const entry of entries) {
    if (!entry.endsWith(".json")) continue;
    const candidatePath = path.resolve(MEMORY_INBOX, entry);
    if (!candidatePath.startsWith(MEMORY_INBOX + path.sep)) continue;

    try {
      const data = JSON.parse(await readFile(candidatePath, "utf8")) as Json;
      const id = str(data.id) || entry.replace(/\.json$/, "");
      if (filterState && str(data.state) !== filterState) continue;

      const candidate: MemoryCandidate = { ...data, id, json_path: candidatePath };
      const consoleSource = await consoleSourceFor(candidate);
      if (consoleSource) candidate.console_source = consoleSource;
      out.push(candidate);
    } catch {
      /* skip unreadable candidate files */
    }
  }

  return out;
}

export async function loadMemoryCandidates(filterState?: string): Promise<MemoryCandidate[]> {
  const projected = await loadFromProjection(filterState);
  const out = projected ?? (await loadFromInbox(filterState));
  out.sort((a, b) => str(b.created_at).localeCompare(str(a.created_at)));
  return out;
}

export async function loadActionableMemoryCandidates(): Promise<MemoryCandidate[]> {
  const proposed = await loadMemoryCandidates("proposed");
  return proposed.filter(isActionableMemoryCandidate);
}

export async function loadSourceEvidenceCandidates(state = "proposed"): Promise<MemoryCandidate[]> {
  const candidates = await loadMemoryCandidates(state);
  return candidates.filter(isKnowledgeClassCandidate);
}

/**
 * The /memory page needs both the actionable split and the source-evidence
 * split of the SAME "proposed" set. Calling loadActionableMemoryCandidates()
 * and loadSourceEvidenceCandidates("proposed") separately execs
 * `memory candidates --state proposed` twice; this fetches it once and
 * filters in memory for both.
 */
export async function loadProposedMemorySplit(): Promise<{
  actionable: MemoryCandidate[];
  sourceEvidence: MemoryCandidate[];
}> {
  const proposed = await loadMemoryCandidates("proposed");
  return {
    actionable: proposed.filter(isActionableMemoryCandidate),
    sourceEvidence: proposed.filter(isKnowledgeClassCandidate),
  };
}

export async function loadMemoryCounts(): Promise<Record<string, number>> {
  const all = await loadMemoryCandidates();
  const counts: Record<string, number> = {};
  for (const c of all) {
    const s = str(c.state) || "unknown";
    counts[s] = (counts[s] ?? 0) + 1;
  }
  return counts;
}

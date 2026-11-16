/*
 * Standing-law view mapper (ruling-harvest slice B) — the operator-facing twin
 * of what orient serves agents. Turns the `compound decisions standing-law
 * --json` projection into the model the /standing-law page and its client
 * search render.
 *
 * PURE by design (no node/server imports, only `import type`): the client
 * StandingLawBrowser and the selftest both import these shapes + the
 * buildStandingLaw/filterStandingLaw functions without pulling a runtime
 * dependency. The server loader (loadStandingLaw) lives in the page, which owns
 * the runJson call.
 */

import type { Json } from "@/lib/compound";
import { ruleKindWords } from "@/lib/plain-words";

/* local pure accessors so this module imports NO runtime value (mirrors
 * work-trace.ts) — @/lib/compound's str/arr pull node builtins at import. */
const s = (v: unknown): string => (typeof v === "string" ? v : "");
const a = (v: unknown): unknown[] => (Array.isArray(v) ? v : []);
const n = (v: unknown): number => (typeof v === "number" && Number.isFinite(v) ? v : 0);

export type StandingLawKind = "ruling" | "grant" | "freeze" | "policy" | string;

export type SupersededRecord = {
  decisionId: string;
  claimId: string;
  kind: string;
  title: string;
  date: string;
};

export type StandingLawRecord = {
  claimId: string;
  decisionId: string;
  kind: StandingLawKind;
  title: string;
  scope: string;
  owner: string;
  reviewedBy: string;
  date: string;
  falsifier: string;
  reviewTrigger: string;
  domains: string[];
  keywords: string[];
  sourcePath: string;
  corroborationCount: number;
  corroborationEvidence: string[];
  supersedes: SupersededRecord[];
  /** Precomputed lowercased haystack for client-side search (title+scope+kind+keywords+domains+id). */
  searchText: string;
};

export type StandingLawConflict = {
  candidateId: string;
  kind: string;
  restatement: string;
  verbatim: string;
  session: string;
  transcript: string;
  line: string;
  conflictsWithClaim: string;
  conflictsWithDecision: string;
  conflictingTitle: string;
};

export type StandingLawCounts = {
  /** rules in force, each decision once (after `duplicates` were folded) */
  records: number;
  rulings: number;
  grants: number;
  freezes: number;
  superseded: number;
  conflicts: number;
  /** rows the projection listed twice for one decision, folded into one */
  duplicates: number;
  /** rules in force by kind, largest first; adds up to `records` */
  byKind: { kind: string; count: number }[];
};

export type StandingLawModel = {
  records: StandingLawRecord[];
  conflicts: StandingLawConflict[];
  counts: StandingLawCounts;
};

function mapSuperseded(raw: unknown): SupersededRecord {
  const o = (raw ?? {}) as Json;
  return {
    decisionId: s(o.decision_id),
    claimId: s(o.claim_id),
    kind: s(o.kind),
    title: s(o.title),
    date: s(o.date),
  };
}

function mapRecord(raw: unknown): StandingLawRecord {
  const o = (raw ?? {}) as Json;
  const domains = a(o.domains).map(s).filter(Boolean);
  const keywords = a(o.keywords).map(s).filter(Boolean);
  const scope = s(o.scope);
  const kind = s(o.kind);
  const title = s(o.title);
  const decisionId = s(o.decision_id);
  const searchText = searchTextOf({ title, scope, kind, decisionId, keywords, domains });
  return {
    claimId: s(o.claim_id),
    decisionId,
    kind,
    title,
    scope,
    owner: s(o.owner),
    reviewedBy: s(o.reviewed_by),
    date: s(o.date),
    falsifier: s(o.falsifier),
    reviewTrigger: s(o.review_trigger),
    domains,
    keywords,
    sourcePath: s(o.source_path),
    corroborationCount: n(o.corroboration_count),
    corroborationEvidence: a(o.corroboration_evidence).map(s).filter(Boolean),
    supersedes: a(o.supersedes).map(mapSuperseded),
    searchText,
  };
}

function mapConflict(raw: unknown): StandingLawConflict {
  const o = (raw ?? {}) as Json;
  return {
    candidateId: s(o.candidate_id),
    kind: s(o.kind),
    restatement: s(o.restatement),
    verbatim: s(o.verbatim),
    session: s(o.session),
    transcript: s(o.transcript),
    line: s(o.line),
    conflictsWithClaim: s(o.conflicts_with_claim),
    conflictsWithDecision: s(o.conflicts_with_decision),
    conflictingTitle: s(o.conflicting_title),
  };
}

/** One decision's key: its decision id, or (for a row that carries the
 *  decision id in its claim slot) the claim id. */
function decisionKey(record: StandingLawRecord): string {
  return record.decisionId || record.claimId;
}

/** A real claim id (`claim_…`), not a decision id written in its place. */
const hasClaim = (record: StandingLawRecord) => record.claimId.startsWith("claim_");

function searchTextOf(r: Pick<StandingLawRecord, "title" | "scope" | "kind" | "decisionId" | "keywords" | "domains">): string {
  return [r.title, r.scope, r.kind, ruleKindWords(r.kind).many, r.decisionId, ...r.keywords, ...r.domains].join(" ").toLowerCase();
}

/** Fill the kept row's empty text fields from the row folded into it. */
function mergeRecord(kept: StandingLawRecord, other: StandingLawRecord): StandingLawRecord {
  const out = { ...kept };
  for (const field of ["kind", "title", "scope", "owner", "reviewedBy", "date", "falsifier", "reviewTrigger", "sourcePath", "decisionId"] as const) {
    if (!out[field] && other[field]) out[field] = other[field];
  }
  out.keywords = [...new Set([...out.keywords, ...other.keywords])];
  out.domains = [...new Set([...out.domains, ...other.domains])];
  out.corroborationCount = Math.max(out.corroborationCount, other.corroborationCount);
  out.corroborationEvidence = [...new Set([...out.corroborationEvidence, ...other.corroborationEvidence])];
  if (!out.supersedes.length) out.supersedes = other.supersedes;
  out.searchText = searchTextOf(out);
  return out;
}

/**
 * Each decision once. The projection can list one decision twice: once as
 * the decision record (its id in the claim slot, no claim) and once as the
 * accepted claim that carries the decision id. Rows with the same key fold
 * into the one with a real claim id; its empty fields are filled from the
 * other. Distinct decisions that share a title stay distinct.
 */
export function dedupeStandingLaw(records: readonly StandingLawRecord[]): { records: StandingLawRecord[]; duplicates: number } {
  const byKey = new Map<string, StandingLawRecord>();
  let duplicates = 0;
  for (const record of records) {
    const key = decisionKey(record);
    const seen = key ? byKey.get(key) : undefined;
    if (!seen) {
      byKey.set(key || `row:${byKey.size}`, record);
      continue;
    }
    duplicates += 1;
    const keep = !hasClaim(seen) && hasClaim(record) ? record : seen;
    byKey.set(key, mergeRecord(keep, keep === seen ? record : seen));
  }
  return { records: [...byKey.values()], duplicates };
}

/** Build the render model from the standing-law JSON projection. Defensive:
 *  a null/short payload yields an empty (never-crashing) model. Counts come
 *  from the rows after duplicates are folded, never from the projection's own
 *  total (which counts a duplicated decision twice). */
export function buildStandingLaw(data: Json | null): StandingLawModel {
  const { records: unique, duplicates } = dedupeStandingLaw(a(data?.records).map(mapRecord));
  const records = unique
    // Newest-first (the CLI already sorts; re-assert so the view never trusts
    // an unsorted upstream). ValidFrom is the record date; ties break on id.
    .sort((x, y) => (x.date !== y.date ? (x.date > y.date ? -1 : 1) : x.claimId > y.claimId ? -1 : 1));
  const conflicts = a(data?.conflicts).map(mapConflict);
  const countsRaw = (data?.counts ?? {}) as Json;
  const kinds = new Map<string, number>();
  for (const record of records) kinds.set(record.kind, (kinds.get(record.kind) ?? 0) + 1);
  const byKind = [...kinds.entries()]
    .map(([kind, count]) => ({ kind, count }))
    .sort((x, y) => y.count - x.count || x.kind.localeCompare(y.kind));
  const counts: StandingLawCounts = {
    records: records.length,
    rulings: kinds.get("ruling") ?? 0,
    grants: kinds.get("grant") ?? 0,
    freezes: kinds.get("freeze") ?? 0,
    superseded: n(countsRaw.superseded),
    conflicts: n(countsRaw.conflicts) || conflicts.length,
    duplicates,
    byKind,
  };
  return { records, conflicts, counts };
}

/** Client-side filter over title+scope+keywords (the <4-interaction journey:
 *  land → type → see the ruling). Empty query returns all records unchanged. */
export function filterStandingLaw(records: StandingLawRecord[], query: string, kind = ""): StandingLawRecord[] {
  const byKind = kind ? records.filter((r) => r.kind === kind) : records;
  const needle = query.trim().toLowerCase();
  if (!needle) return byKind;
  // Every whitespace-separated term must match (AND) so "opus execution" narrows.
  const terms = needle.split(/\s+/).filter(Boolean);
  return byKind.filter((r) => terms.every((t) => r.searchText.includes(t)));
}

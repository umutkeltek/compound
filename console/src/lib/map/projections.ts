import { readFile, readdir } from "node:fs/promises";
import path from "node:path";

import { COMPOUND_HOME, num, privateFallbacksEnabled, runJson, str, type Json } from "@/lib/compound";

/*
 * Real projections the System Map hydrates from. Every number on the map comes
 * from here — never hardcoded. Mirrors the memory-data.ts pattern for direct
 * JSONL reads; uses runJson for compound verbs.
 */

async function countLines(file: string): Promise<number> {
  if (!privateFallbacksEnabled()) return 0;

  try {
    const source = await readFile(file, "utf8");
    return source.split(/\r?\n/).filter((l) => l.trim().length > 0).length;
  } catch {
    return 0;
  }
}

export async function getCapabilityAuthority(): Promise<Json | null> {
  return runJson(["capability", "authority", "--json"]);
}

export async function getProductAuthority(): Promise<Json | null> {
  return runJson(["product", "authority", "--json"]);
}

export async function getRouteAuthority(): Promise<Json | null> {
  return runJson(["route", "evidence-authority", "--json"]);
}

export type LedgerStats = {
  entities: number;
  claims: number;
  edges: number;
  evidence: number;
  routeEvidence: number;
  noPromote: number;
  knowledgeImports: number;
};

export async function getLedgerStats(): Promise<LedgerStats> {
  const ledger = path.resolve(COMPOUND_HOME, "ledger");
  const [
    entities,
    claims,
    edges,
    evidence,
    routeEvidence,
    noPromote,
    knowledgeImports,
  ] = await Promise.all([
    countLines(path.join(ledger, "entities.jsonl")),
    countLines(path.join(ledger, "claims.jsonl")),
    countLines(path.join(ledger, "edges.jsonl")),
    countLines(path.join(ledger, "evidence.jsonl")),
    countLines(path.join(ledger, "execution-route-evidence.jsonl")),
    countLines(path.join(ledger, "no-promote-decisions.jsonl")),
    countLines(path.join(ledger, "knowledge-imports.jsonl")),
  ]);
  return { entities, claims, edges, evidence, routeEvidence, noPromote, knowledgeImports };
}

export async function getKnowledgeSourceCount(): Promise<number> {
  return countLines(path.resolve(COMPOUND_HOME, "registries", "knowledge-sources.jsonl"));
}

/** Names of the active capture sources (for the capture node's evidence list). */
export async function getKnowledgeSourceKinds(): Promise<string[]> {
  if (!privateFallbacksEnabled()) return [];

	try {
    const source = await readFile(
      path.resolve(COMPOUND_HOME, "registries", "knowledge-sources.jsonl"),
      "utf8",
    );
    const kinds = new Set<string>();
    for (const line of source.split(/\r?\n/)) {
      if (!line.trim()) continue;
      try {
        const row = JSON.parse(line) as Json;
        const k = str(row.source_kind) || str(row.kind);
        if (k) kinds.add(k);
      } catch {
        /* skip */
      }
    }
    return [...kinds];
  } catch {
    return [];
  }
}

export async function getIntakeRepoCount(): Promise<number> {
  if (!privateFallbacksEnabled()) return 0;

	try {
    const dir = path.resolve(COMPOUND_HOME, "inbox", "external-repo-intake");
    const entries = await readdir(dir, { withFileTypes: true });
    return entries.filter((e) => e.isDirectory()).length;
  } catch {
    return 0;
  }
}

export { num };

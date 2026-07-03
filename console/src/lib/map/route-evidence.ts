import { readFile, stat } from "node:fs/promises";
import path from "node:path";

import { COMPOUND_HOME } from "@/lib/compound";

/*
 * Execution route evidence — metadata only (no prompts/responses/secrets).
 * "Route evidence, not task success." Many rows have NO work_id and NO
 * cost_micros — only usage.total_tokens + latency_ms. Read defensively.
 */

export type RouteRecord = {
  id?: string;
  request_id?: string;
  work_id?: string;
  transition_id?: string;
  client_profile?: string;
  protocol?: string;
  selected_target?: { provider?: string; model?: string; protocol?: string };
  usage?: { total_tokens?: number };
  cost_micros?: number;
  latency_ms?: number;
  status?: string;
};

export async function readRouteEvidence(
  limit = 200,
): Promise<{ records: RouteRecord[]; mtime: string | null }> {
  const file = path.join(
    COMPOUND_HOME,
    "ledger",
    "execution-route-evidence.jsonl",
  );
  let text: string;
  let mtime: string | null = null;
  try {
    text = await readFile(file, "utf8");
    try {
      mtime = (await stat(file)).mtime.toISOString();
    } catch {
      /* mtime optional */
    }
  } catch {
    return { records: [], mtime: null };
  }
  const lines = text.split(/\r?\n/).filter((l) => l.trim());
  const tail = lines.slice(-limit);
  const records: RouteRecord[] = [];
  for (const line of tail) {
    try {
      records.push(JSON.parse(line) as RouteRecord);
    } catch {
      /* skip */
    }
  }
  return { records, mtime };
}

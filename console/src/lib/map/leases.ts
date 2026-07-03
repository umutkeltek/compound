import { readdir, readFile } from "node:fs/promises";
import path from "node:path";

import { COMPOUND_HOME } from "@/lib/compound";

/*
 * Work leases: one JSON file per claimed work_id under ~/.compound/work/leases.
 * Filename == work_id. Read-only.
 */

export type Lease = {
  work_id: string;
  by: string;
  claimed_at?: string | number;
};

export async function readLeases(): Promise<Lease[]> {
  const dir = path.join(COMPOUND_HOME, "work", "leases");
  let files: string[];
  try {
    files = await readdir(dir);
  } catch {
    return [];
  }
  const out: Lease[] = [];
  for (const f of files) {
    if (!f.endsWith(".json")) continue;
    try {
      const j = JSON.parse(await readFile(path.join(dir, f), "utf8"));
      out.push({
        work_id: String(j.work_id ?? f.replace(/\.json$/, "")),
        by: String(j.by ?? ""),
        claimed_at: j.claimed_at,
      });
    } catch {
      /* skip malformed lease */
    }
  }
  return out;
}

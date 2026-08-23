#!/usr/bin/env bun
/**
 * check_backup_freshness.ts — verify the agent-backup receipt is recent enough
 * to justify retention steps in the weekly reclaim.
 *
 * The weekly reclaim orchestrator calls this BEFORE it would prune session
 * history or any other "needs backup proof" path. The rule: a backup receipt
 * is fresh if the most recent file under ~/.hermes/cron/output/<job-id>/ is
 * less than 7 days old. JSON or markdown outputs both count.
 *
 * Returns:
 *   - exit 0 + status=fresh       (caller may proceed)
 *   - exit 0 + status=stale       (caller should report but skip destructive)
 *   - exit 0 + status=missing     (no receipts ever)
 */

import { existsSync, readdirSync, statSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

const HOME = homedir();
const RECEIPT_DIR = join(HOME, ".hermes/cron/output/1dc135fe0275");
const FRESHNESS_DAYS = 7;

type Status = "fresh" | "stale" | "missing";

function status(): { status: Status; latest: string | null; age_hours: number | null } {
  if (!existsSync(RECEIPT_DIR)) return { status: "missing", latest: null, age_hours: null };
  const files = readdirSync(RECEIPT_DIR)
    .map((f) => ({ f, mtimeMs: statSync(join(RECEIPT_DIR, f)).mtimeMs }))
    .sort((a, b) => b.mtimeMs - a.mtimeMs);
  if (files.length === 0) return { status: "missing", latest: null, age_hours: null };
  const top = files[0];
  const ageMs = Date.now() - top.mtimeMs;
  const ageHours = ageMs / (60 * 60 * 1000);
  return {
    status: ageMs < FRESHNESS_DAYS * 24 * 60 * 60 * 1000 ? "fresh" : "stale",
    latest: top.f,
    age_hours: Math.round(ageHours * 10) / 10,
  };
}

const s = status();
console.log(JSON.stringify(s, null, 2));
process.exit(0);

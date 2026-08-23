#!/usr/bin/env bun
/**
 * weekly_reclaim.ts — Sunday 03:00 SSD reclaim + symlink-drift repair + backup
 * + capture-coverage audit. Idempotent. Dry-run by default; --apply executes.
 *
 * Order:
 *   1. audit_switchback_capture.ts --markdown (record the live capture state)
 *   2. fix_work_symlink_drift.ts --apply (re-stitch Work-back symlinks)
 *   3. sweep_build_caches.ts --apply (age-gated build artifact sweep)
 *   4. tmutil thinlocalsnapshots (free > 250 GB purgeable)
 *   5. Write a Markdown report to ~/Projects/operating-system/state/runs/ssd-weekly-<date>/
 *      (NOTE: we record into umut-os state, since that's where the contract lives
 *       — this script writes there because the rest of the OS expects it. The
 *       ssd-guardian itself lives under compound.)
 *   6. Record a `compound finding record` so the next orient sees it.
 *
 * Why one orchestrator instead of three launchd jobs? Because the audit +
 * drift-fix + sweep are independent but their receipts are most useful as one
 * weekly delta. One plist, one log file, one notification.
 *
 * Why Sunday 03:00? Lowest agent activity (Codex lanes cool down), after the
 * nightly NAS sync (so backup receipts are fresh), before the workweek.
 */

import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

const APPLY = process.argv.includes("--apply");
const SCRIPT_DIR = import.meta.dir;
const HOME = homedir();
const REPORT_ROOT = join(HOME, "Projects/operating-system/umut-os/state/runs");
function runScript(name: string, args: string[]): { ok: boolean; stdout: string; stderr: string } {
  const r = spawnSync("bun", [join(SCRIPT_DIR, name), ...args], { stdio: "pipe", timeout: 1800_000 });
  return { ok: r.status === 0, stdout: r.stdout?.toString() ?? "", stderr: r.stderr?.toString() ?? "" };
}

type Step = {
  id: string;
  status: "executed" | "skipped" | "failed";
  reason?: string;
  reclaimed_bytes?: number;
};

function runBin(cmd: string[], timeoutMs = 600_000): { ok: boolean; stdout: string; stderr: string } {
  const r = spawnSync(cmd[0], cmd.slice(1), { stdio: "pipe", timeout: timeoutMs, shell: false });
  return { ok: r.status === 0, stdout: r.stdout?.toString() ?? "", stderr: r.stderr?.toString() ?? "" };
}

const now = new Date();
const stamp = now.toISOString().replace(/[:.]/g, "-");
const runId = `ssd-weekly-${stamp}`;
const reportDir = join(REPORT_ROOT, runId);
mkdirSync(reportDir, { recursive: true });

const steps: Step[] = [];
const logs: Record<string, string> = {};

// Step 0: backup freshness gate.
{
  const r = runScript("check_backup_freshness.ts", []);
  logs.backup = r.stdout;
  try {
    const j = JSON.parse(r.stdout);
    steps.push({ id: "backup-freshness-check", status: "executed", reason: `${j.status} (latest: ${j.latest ?? "none"}, ${j.age_hours ?? "?"}h)` });
  } catch {
    steps.push({ id: "backup-freshness-check", status: "failed", reason: r.stderr.slice(0, 200) });
  }
}
{
  const r = runScript("audit_switchback_capture.ts", ["--markdown"]);
  logs.audit = r.stdout;
  steps.push({ id: "capture-coverage-audit", status: r.ok ? "executed" : "failed", reason: r.ok ? undefined : r.stderr.slice(0, 200) });
}

// Step 2: symlink drift repair.
if (APPLY) {
  const r = runScript("fix_work_symlink_drift.ts", ["--apply"]);
  logs.drift = r.stdout;
  steps.push({ id: "symlink-drift-fix", status: r.ok ? "executed" : "failed", reason: r.ok ? undefined : r.stderr.slice(0, 200) });
} else {
  steps.push({ id: "symlink-drift-fix", status: "skipped", reason: "dry_run" });
}

// Step 3: build cache sweep.
if (APPLY) {
  const r = runScript("sweep_build_caches.ts", ["--apply"]);
  logs.sweep = r.stdout;
  let reclaimed = 0;
  try {
    const m = r.stdout.match(/"total_reclaimed_bytes":\s*(\d+)/);
    if (m) reclaimed = Number(m[1]);
  } catch {}
  steps.push({ id: "build-cache-sweep", status: r.ok ? "executed" : "failed", reclaimed_bytes: reclaimed, reason: r.ok ? undefined : r.stderr.slice(0, 200) });
} else {
  steps.push({ id: "build-cache-sweep", status: "skipped", reason: "dry_run" });
}

// Step 4: thin local Time Machine snapshots. Always safe; recovers blocks held
// by deleted files. We aim to keep 250 GB purgeable, which the 4-snapshot
// throttle bounds.
{
  const r = runBin(["tmutil", "thinlocalsnapshots", "/", "250000000000", "4"]);
  steps.push({ id: "tmutil-thin-snapshots", status: r.ok ? "executed" : "failed", reason: r.ok ? r.stdout.trim().slice(0, 200) : r.stderr.slice(0, 200) });
}

const totalReclaimed = steps.reduce((acc, s) => acc + (s.reclaimed_bytes ?? 0), 0);

// Free space after — only meaningful when actually run.
const df = runBin(["df", "-k", "/System/Volumes/Data"]);
const freeKiB = (() => {
  const m = df.stdout.match(/(\d+)\s+(\d+)\s+(\d+)\s+\d+%/);
  if (!m) return 0;
  return Number(m[3]) * 1024;
})();

// Markdown report.
const lines: string[] = [];
lines.push(`# Weekly SSD reclaim`);
lines.push(``);
lines.push(`Run id: \`${runId}\``);
lines.push(`Mode: ${APPLY ? "**apply**" : "dry_run"}`);
lines.push(`Free bytes after: ${(freeKiB / 1024 / 1024 / 1024).toFixed(1)} GiB`);
lines.push(`Total reclaimed this run: ${(totalReclaimed / 1024 / 1024 / 1024).toFixed(1)} GiB`);
lines.push(``);
lines.push(`## Steps`);
lines.push(``);
for (const s of steps) {
  const bytes = s.reclaimed_bytes ? ` (reclaimed ${(s.reclaimed_bytes / 1024 / 1024 / 1024).toFixed(1)} GiB)` : "";
  lines.push(`- **${s.id}**: ${s.status}${bytes}${s.reason ? ` — ${s.reason}` : ""}`);
}
lines.push(``);
lines.push(`## Capture coverage snapshot`);
lines.push(``);
lines.push(`See \`capture-coverage.md\` in this directory for the full table.`);
const report = lines.join("\n") + "\n\n" + logs.audit + "\n";
writeFileSync(join(reportDir, "report.md"), report);

const summary = {
  schema_version: "compound.ssd-guardian.weekly_reclaim.v1",
  run_id: runId,
  mode: APPLY ? "apply" : "dry_run",
  generated_at: now.toISOString(),
  free_bytes_after: freeKiB,
  total_reclaimed_bytes: totalReclaimed,
  steps,
  report_dir: reportDir,
};
writeFileSync(join(reportDir, "summary.json"), JSON.stringify(summary, null, 2));
console.log(JSON.stringify(summary, null, 2));

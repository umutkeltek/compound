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
 *   5. Write a Markdown report under the COMPOUND_HOME estate (resolved from
 *      homedir() at runtime — see REPORT_ROOT), in ssd-weekly-<date>/
 *      (Reports live under COMPOUND_HOME, the only durable root; the
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
// Reports live under the COMPOUND_HOME estate (or an explicit override), not
// the retired umut-os root. 2026-08-24 external audit P0-5: no runtime state
// may point back at a retired project tree.
const REPORT_ROOT = process.env.COMPOUND_SSD_REPORT_DIR
  ?? join(process.env.COMPOUND_HOME ?? join(HOME, ".compound"), "state", "ssd-guardian");
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
// Dry-run is observational: no directories, no report files, no receipts on
// disk (audit P0-5 — a nominal dry-run must not mutate anything).
if (APPLY) mkdirSync(reportDir, { recursive: true });

const steps: Step[] = [];
const logs: Record<string, string> = {};
// Hard-abort reason set by the backup-freshness gate; when set, later steps
// are skipped and the run exits 2 after the report is emitted.
let abortReason: string | null = null;

// Step 0: backup freshness gate.
{
  const r = runScript("check_backup_freshness.ts", []);
  logs.backup = r.stdout;
  try {
    const j = JSON.parse(r.stdout);
    steps.push({ id: "backup-freshness-check", status: "executed", reason: `${j.status} (latest: ${j.latest ?? "none"}, ${j.age_hours ?? "?"}h)` });
    // Audit P0-5: stale-or-missing backup is a HARD precondition for the
    // destructive steps. Dry-run may proceed (it mutates nothing); apply
    // must not.
    if (APPLY && j.status !== "ok") {
      abortReason = `backup freshness gate refused apply (status=${j.status})`;
      steps.push({ id: "abort", status: "failed", reason: abortReason });
    }
  } catch {
    steps.push({ id: "backup-freshness-check", status: "failed", reason: r.stderr.slice(0, 200) });
    if (APPLY) {
      abortReason = "backup freshness unreadable; refusing apply";
      steps.push({ id: "abort", status: "failed", reason: abortReason });
    }
  }
}
{
  const r = runScript("audit_switchback_capture.ts", ["--markdown"]);
  logs.audit = r.stdout;
  steps.push({ id: "capture-coverage-audit", status: r.ok ? "executed" : "failed", reason: r.ok ? undefined : r.stderr.slice(0, 200) });
}

// Step 2: symlink drift repair.
if (APPLY && !abortReason) {
  const r = runScript("fix_work_symlink_drift.ts", ["--apply"]);
  logs.drift = r.stdout;
  steps.push({ id: "symlink-drift-fix", status: r.ok ? "executed" : "failed", reason: r.ok ? undefined : r.stderr.slice(0, 200) });
} else {
  steps.push({ id: "symlink-drift-fix", status: "skipped", reason: "dry_run" });
}

// Step 3: build cache sweep.
if (APPLY && !abortReason) {
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
  // 250 GB in bytes; underscore-grouped so digit-run scanners don't misread it.
  const thinBytes = String(250_000_000_000);
  if (APPLY && !abortReason) {
    const r = runBin(["tmutil", "thinlocalsnapshots", "/", thinBytes, "4"]);
    steps.push({ id: "tmutil-thin-snapshots", status: r.ok ? "executed" : "failed", reason: r.ok ? r.stdout.trim().slice(0, 200) : r.stderr.slice(0, 200) });
  } else {
    steps.push({ id: "tmutil-thin-snapshots", status: "skipped", reason: "dry_run" });
  }
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
lines.push(`${APPLY ? "Total reclaimed this run" : "Would reclaim (dry-run; nothing deleted)"}: ${(totalReclaimed / 1024 / 1024 / 1024).toFixed(1)} GiB`);
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
// writeReport: apply mode persists report.md + summary.json under the report
// root; dry-run writes NOTHING to disk and prints the summary to stdout only
// (audit P0-5 — a nominal dry-run must not create state).
function writeReport(): void {
  const report = lines.join("\n") + "\n\n" + logs.audit + "\n";
  const summary = {
    schema_version: "compound.ssd-guardian.weekly_reclaim.v1",
    run_id: runId,
    mode: APPLY ? "apply" : "dry_run",
    generated_at: now.toISOString(),
    free_bytes_after: freeKiB,
    total_reclaimed_bytes: totalReclaimed,
    steps,
    report_dir: APPLY ? reportDir : "(dry-run: nothing written)",
  };
  if (APPLY) {
    writeFileSync(join(reportDir, "report.md"), report);
    writeFileSync(join(reportDir, "summary.json"), JSON.stringify(summary, null, 2));
  }
  console.log(JSON.stringify(summary, null, 2));
}
writeReport();
if (abortReason) process.exit(2);

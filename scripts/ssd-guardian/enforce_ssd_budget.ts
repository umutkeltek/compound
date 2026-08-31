#!/usr/bin/env bun
/**
 * enforce_ssd_budget.ts — declared-budget enforcement for the boot disk.
 *
 * The recurring failure on this machine was never one runaway directory. It was
 * that no directory had a declared ceiling, so every new producer claimed space
 * silently and was only discovered at 94% full. This reads ssd_budget.json,
 * holds each declared path to its policy, and — the part that actually prevents
 * a recurrence — reports any *undeclared* directory that has grown past the
 * alert threshold. That is how the next runaway producer gets caught at 5 GiB
 * instead of 39.
 *
 * It also carries a canary. Three separate jobs here logged success while
 * reclaiming nothing for weeks (an inverted gate, an unloaded agent, a stdout
 * buffer truncating a file listing). So a run that ends under the free-space
 * floor having reclaimed zero bytes is an ALARM, not a success: it exits 2 and
 * says which step did nothing.
 *
 * Dry-run by default; --apply enforces.
 */

import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, readdirSync, statSync, writeFileSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { join } from "node:path";
import { parseFlags, parseStatRecords, selectCacheDirEvictions, selectCacheEntryEvictions } from "./reclaim_selectors";

// Unknown flags are a hard error, not a silent no-op: a mistyped `--aply` would
// leave enforcement in dry-run while the launchd job logged a clean exit 0.
const ALLOWED_FLAGS = ["--apply", "--no-discovery", "--help"] as const;
const ARGS = process.argv.slice(2);
const FLAGS = parseFlags(ARGS, ALLOWED_FLAGS);
if (FLAGS.has("--help")) {
  console.log("usage: enforce_ssd_budget.ts [--apply] [--no-discovery]\n\n  --apply         enforce (default: dry-run)\n  --no-discovery  skip the undeclared-directory scan");
  process.exit(0);
}
if (FLAGS.unknown.length > 0) {
  console.error(`unknown argument(s): ${FLAGS.unknown.join(", ")}\nallowed: ${ALLOWED_FLAGS.join(", ")}`);
  process.exit(2);
}

const APPLY = FLAGS.has("--apply");
const SKIP_DISCOVERY = FLAGS.has("--no-discovery");
const HOME = homedir();
const GIB = 1024 ** 3;
// spawnSync truncates stdout at maxBuffer *without erroring*; a 12 MB file
// listing silently became 1 MB and a cap saw 7% of its cache. Never rely on the
// default here.
const MAX_BUFFER = 1024 * 1024 * 512;
const MANIFEST = process.env.SSD_BUDGET_MANIFEST ?? join(import.meta.dir, "ssd_budget.json");
// Gitignored operator overlay; absent in a fresh clone, which is the point.
const MANIFEST_OVERLAY = process.env.SSD_BUDGET_MANIFEST_OVERLAY
  ?? join(import.meta.dir, "..", "..", "docs_private", "ssd_budget.local.json");
const REPORT_ROOT = process.env.COMPOUND_SSD_REPORT_DIR
  ?? join(process.env.COMPOUND_HOME ?? join(HOME, ".compound"), "state", "ssd-guardian");

function sh(script: string, timeoutMs = 900_000) {
  const r = spawnSync("/bin/sh", ["-c", script], { stdio: "pipe", timeout: timeoutMs, maxBuffer: MAX_BUFFER });
  return { ok: r.status === 0, stdout: r.stdout?.toString() ?? "", stderr: r.stderr?.toString() ?? "" };
}

const q = (p: string) => JSON.stringify(p);

function expand(p: string): string {
  return p.replace("$HOME", HOME).replace("$TMPDIR", (process.env.TMPDIR ?? tmpdir()).replace(/\/$/, ""));
}

function dirSize(p: string): number {
  return Number(sh(`du -x -sk ${q(p)} 2>/dev/null | cut -f1`).stdout.trim() || 0) * 1024;
}

function removePaths(paths: string[]): void {
  if (!APPLY || paths.length === 0) return;
  // `--` terminates rm's option list; see daily_toolchain_reclaim.ts.
  const r = spawnSync("/usr/bin/xargs", ["-0", "rm", "-rf", "--"], {
    input: paths.join("\0") + "\0", stdio: ["pipe", "pipe", "pipe"], timeout: 900_000, maxBuffer: MAX_BUFFER,
  });
  if (r.status !== 0) throw new Error((r.stderr?.toString() ?? "").slice(0, 200));
}

type Entry = { mtime: number; size: number; path: string };

/** One stat(1) pass, with an explicit assertion that nothing was truncated. */
function listFiles(root: string, minDepth: number): Entry[] {
  const expected = Number(sh(`find ${q(root)} -type f -mindepth ${minDepth} 2>/dev/null | wc -l`).stdout.trim() || 0);
  if (expected === 0) return [];
  const r = sh(`find ${q(root)} -type f -mindepth ${minDepth} -print0 2>/dev/null | xargs -0 stat -f '%m %z %N' 2>/dev/null`);
  // Record boundaries, not newlines: this listing feeds rm. See parseStatRecords.
  const out: Entry[] = parseStatRecords(r.stdout);
  if (out.length < expected * 0.99) throw new Error(`listing truncated: ${out.length} of ${expected} under ${root}`);
  return out;
}

/** Files directly inside dir, never recursing: keep_newest owns a directory's
 *  own artifacts, not whatever a subdirectory happens to archive there. */
function listTopLevelFiles(root: string): Entry[] {
  const r = sh(`find ${q(root)} -mindepth 1 -maxdepth 1 -type f -print0 2>/dev/null | xargs -0 stat -f '%m %z %N' 2>/dev/null`);
  return parseStatRecords(r.stdout);
}

function newestMtime(dir: string): number {
  return Number(sh(`find ${q(dir)} -type f -print0 2>/dev/null | xargs -0 stat -f '%m' 2>/dev/null | sort -rn | head -1`).stdout.trim() || 0) * 1000;
}

type Rule = { path: string; policy: string; cap_gib: number; age_days?: number; keep?: number; why?: string };
type Result = { path: string; policy: string; status: string; size_gib: number; cap_gib: number; reclaimed_bytes: number; note?: string };

/**
 * Tracked manifest + optional gitignored operator overlay, merged by path.
 *
 * Operator-specific rows (a private tool's cache, a personal local project)
 * cannot live in the tracked manifest — this repo is destined to be public — but
 * they still have to be DECLARED, or the discovery scan reports them as
 * undeclared every single night and the alert goes back to being noise. Same
 * additive-overlay shape the doc-leak gate already uses for
 * docs_private/leak-tokens.txt: public core generic, private overlay additive.
 * The overlay wins on a path collision, so it can also retune a public cap.
 */
function loadManifest(): { manifest: any; overlaySource: string | null } {
  const base = JSON.parse(readFileSync(MANIFEST, "utf8"));
  if (!existsSync(MANIFEST_OVERLAY)) return { manifest: base, overlaySource: null };
  const overlay = JSON.parse(readFileSync(MANIFEST_OVERLAY, "utf8"));
  const byPath = new Map<string, Rule>(((base.paths ?? []) as Rule[]).map((r) => [r.path, r]));
  for (const r of ((overlay.paths ?? []) as Rule[])) byPath.set(r.path, r);
  return {
    manifest: { ...base, ...overlay, paths: [...byPath.values()] },
    overlaySource: MANIFEST_OVERLAY,
  };
}

const { manifest, overlaySource } = loadManifest();
const results: Result[] = [];

for (const rule of manifest.paths as Rule[]) {
  const p = expand(rule.path);
  const cap = rule.cap_gib * GIB;
  if (!existsSync(p)) {
    results.push({ path: rule.path, policy: rule.policy, status: "absent", size_gib: 0, cap_gib: rule.cap_gib, reclaimed_bytes: 0 });
    continue;
  }
  const before = dirSize(p);
  let reclaimed = 0, status = "ok", note: string | undefined;

  try {
    if (rule.policy === "lru_cap") {
      const entries = listFiles(p, 2);
      const cutoff = Date.now() - (rule.age_days ?? 2) * 86_400_000;
      const { doomed, freed } = selectCacheEntryEvictions(entries, cap, cutoff);
      removePaths(doomed.map((e) => e.path));
      reclaimed = freed;
      note = `${doomed.length} of ${entries.length} entries`;
    } else if (rule.policy === "dir_lru_cap") {
      const dirs = readdirSync(p)
        .map((n) => join(p, n))
        .filter((d) => { try { return statSync(d).isDirectory(); } catch { return false; } })
        .map((d) => ({ path: d, newest: newestMtime(d), size: dirSize(d) }));
      const cutoff = Date.now() - (rule.age_days ?? 2) * 86_400_000;
      const { doomed, freed } = selectCacheDirEvictions(dirs, cap, cutoff);
      removePaths(doomed.map((d) => d.path));
      reclaimed = freed;
      note = `${doomed.length} of ${dirs.length} sub-caches`;
    } else if (rule.policy === "age_prune") {
      const days = rule.age_days ?? 7;
      const victims = sh(`find ${q(p)} -mindepth 1 -maxdepth 1 -mtime +${days} 2>/dev/null`).stdout.split("\n").filter(Boolean);
      const bytes = victims.reduce((a, v) => a + dirSize(v), 0);
      removePaths(victims);
      reclaimed = bytes;
      note = `${victims.length} entries older than ${days}d`;
    } else if (rule.policy === "keep_newest") {
      const keep = rule.keep ?? 1;
      const files = listTopLevelFiles(p).sort((a, b) => b.mtime - a.mtime);
      const victims = files.slice(keep);
      removePaths(victims.map((v) => v.path));
      reclaimed = victims.reduce((a, v) => a + v.size, 0);
      note = `kept ${Math.min(keep, files.length)}, removed ${victims.length}`;
    } else {
      // report_only: measure, never delete. Over-cap is a signal to decide a
      // policy, not a licence to delete someone's history.
      status = before > cap ? "over_budget" : "ok";
      note = rule.why;
    }
    if (rule.policy !== "report_only") status = APPLY ? "enforced" : "dry_run";
  } catch (e) {
    status = "failed";
    note = String(e).slice(0, 200);
  }

  results.push({
    path: rule.path, policy: rule.policy, status,
    size_gib: Number((before / GIB).toFixed(2)), cap_gib: rule.cap_gib, reclaimed_bytes: reclaimed, note,
  });
}

// Undeclared growth. Anything large that nobody has declared is exactly the
// class of thing that reached 39 GiB unnoticed.
const declared = (manifest.paths as Rule[]).map((r) => expand(r.path));
const alertBytes = (manifest.undeclared_alert_gib ?? 5) * GIB;
const undeclared: { path: string; size_gib: number }[] = [];
for (const rawRoot of (SKIP_DISCOVERY ? [] : manifest.scan_roots) as string[]) {
  const root = expand(rawRoot);
  if (!existsSync(root)) continue;
  for (const line of sh(`du -x -k -d 2 ${q(root)} 2>/dev/null`).stdout.split("\n")) {
    if (!line) continue;
    const tab = line.indexOf("\t");
    if (tab < 0) continue;
    const bytes = Number(line.slice(0, tab)) * 1024;
    const path = line.slice(tab + 1);
    if (bytes < alertBytes || path === root) continue;
    // Skip anything already declared, and anything that is a parent or child of
    // a declared path — those are accounted for by their own rule.
    if (declared.some((d) => path === d || path.startsWith(d + "/") || d.startsWith(path + "/"))) continue;
    undeclared.push({ path, size_gib: Number((bytes / GIB).toFixed(1)) });
  }
}
undeclared.sort((a, b) => b.size_gib - a.size_gib);

const freeBytes = (() => {
  const m = sh(`df -k /System/Volumes/Data`).stdout.match(/(\d+)\s+(\d+)\s+(\d+)\s+\d+%/);
  return m ? Number(m[3]) * 1024 : 0;
})();
const totalReclaimed = results.reduce((a, r) => a + r.reclaimed_bytes, 0);
const floorBytes = (manifest.free_floor_gib ?? 150) * GIB;

// The canary: green logs that reclaim nothing are the failure mode that cost
// weeks here. Under the floor having freed nothing is an alarm, not a success.
const silentNoop = APPLY && freeBytes < floorBytes && totalReclaimed === 0;
const failures = results.filter((r) => r.status === "failed");
const overBudget = results.filter((r) => r.status === "over_budget");

const now = new Date();
const runId = `ssd-budget-${now.toISOString().replace(/[:.]/g, "-")}`;
const summary = {
  schema_version: "compound.ssd-guardian.budget_enforcement.v1",
  run_id: runId,
  mode: APPLY ? "apply" : "dry_run",
  generated_at: now.toISOString(),
  free_gib: Number((freeBytes / GIB).toFixed(1)),
  free_floor_gib: manifest.free_floor_gib,
  below_floor: freeBytes < floorBytes,
  total_reclaimed_gib: Number((totalReclaimed / GIB).toFixed(2)),
  alarm: silentNoop || failures.length > 0,
  alarm_reasons: [
    ...(silentNoop ? ["below free floor and reclaimed 0 bytes — enforcement is running but doing nothing"] : []),
    ...failures.map((f) => `${f.path}: ${f.note}`),
  ],
  over_budget: overBudget.map((r) => ({ path: r.path, size_gib: r.size_gib, cap_gib: r.cap_gib })),
  manifest_source: MANIFEST,
  manifest_overlay: overlaySource,
  declared_paths: (manifest.paths ?? []).length,
  discovery: SKIP_DISCOVERY ? "skipped (--no-discovery)" : "full scan",
  undeclared_over_threshold: undeclared.slice(0, 20),
  results,
};

if (APPLY) {
  const dir = join(REPORT_ROOT, "budget");
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, `${runId}.json`), JSON.stringify(summary, null, 2));
}
console.log(JSON.stringify(summary, null, 2));
process.exit(summary.alarm ? 2 : 0);

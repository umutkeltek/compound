#!/usr/bin/env bun
/**
 * daily_toolchain_reclaim.ts — nightly toolchain cache reclaim.
 *
 * Targets the four caches that regenerate faster than the weekly sweep can
 * bound them: the Go build cache, the per-proof Go caches, the Cargo/rustup
 * estate, Homebrew downloads, and idle CoreSimulator devices.
 *
 * Why a size cap and not only an age gate? Measured 2026-08-31: the 52 GiB
 * go-build cache had 4.1 GiB older than 1 day and 0 bytes older than 3 days —
 * the whole cache is hot working set churned at ~32 GiB/day. An age gate
 * reclaims nothing from it. Only an LRU cap bounds a cache like that, so each
 * Go step is age-gate-first, cap-second.
 *
 * Why per-hash whole-dir eviction for compound-proof-gocache? proof_runtime.go
 * mints a private GOCACHE per proof config hash. Go's own 5-day trimmer only
 * runs inside a cache that is still being used, so an abandoned hash dir is
 * never trimmed by anyone. Whole-dir eviction is the only thing that collects
 * them, and it is safe because each dir is an independent cache.
 *
 * Dry-run by default; --apply removes. Destructive steps self-skip while the
 * matching toolchain is live, so a nightly run cannot corrupt a build in flight.
 */

import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, readdirSync, rmSync, statSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { parseFlags, parseLimit, parseStatRecords, selectCacheDirEvictions, selectCacheEntryEvictions, summarizeAlarm } from "./reclaim_selectors";
import { join } from "node:path";

// A cleanup script that ignores a mistyped flag is a cleanup script that
// silently does nothing: `--aply` dry-runs forever while the nightly timer logs
// success. Unknown flags are a hard error, never a no-op.
const ALLOWED_FLAGS = ["--apply", "--force", "--help"] as const;
const ARGS = process.argv.slice(2);
const FLAGS = parseFlags(ARGS, ALLOWED_FLAGS);
if (FLAGS.has("--help")) {
  console.log("usage: daily_toolchain_reclaim.ts [--apply] [--force]\n\n  --apply  remove (default: dry-run, writes nothing)\n  --force  evict even while a matching toolchain is live");
  process.exit(0);
}
if (FLAGS.unknown.length > 0) {
  console.error(`unknown argument(s): ${FLAGS.unknown.join(", ")}\nallowed: ${ALLOWED_FLAGS.join(", ")}`);
  process.exit(2);
}

const APPLY = FLAGS.has("--apply");
// Operator override. The liveness guards protect a build from losing its cache
// mid-run; --force says the operator has accepted that cost. Safe in practice:
// a missing Go cache entry is a cache miss, not an error, and LRU eviction
// removes the oldest entries while a live build's are the newest. Recorded in
// the receipt so a forced run is never mistaken for a guarded one.
const FORCE = FLAGS.has("--force");
const HOME = homedir();
const WORK = "/Volumes/External";
const REPORT_ROOT = process.env.COMPOUND_SSD_REPORT_DIR
  ?? join(process.env.COMPOUND_HOME ?? join(HOME, ".compound"), "state", "ssd-guardian");

const GO_CACHE = process.env.SSD_GUARDIAN_GO_CACHE_DIR ?? join(HOME, "Library/Caches/go-build");
const PROOF_CACHE = process.env.SSD_GUARDIAN_PROOF_CACHE_DIR ?? join(HOME, "Library/Caches/compound-proof-gocache");
const WORK_PROOF_CACHE = process.env.SSD_GUARDIAN_WORK_PROOF_CACHE_DIR ?? join(WORK, "Caches/compound-proof-gocache");

const GIB = 1024 ** 3;
const num = (k: string, d: number) => {
  const r = parseLimit(k, process.env[k], d);
  if (!r.ok) {
    console.error(r.error);
    process.exit(2);
  }
  return r.value;
};

// Caps are per-cache ceilings, not targets: a run under the cap does nothing.
const GO_CAP_GIB = num("SSD_GUARDIAN_GO_CACHE_CAP_GIB", 20);
const GO_AGE_DAYS = num("SSD_GUARDIAN_GO_CACHE_AGE_DAYS", 2);
const PROOF_CAP_GIB = num("SSD_GUARDIAN_PROOF_CACHE_CAP_GIB", 20);
const PROOF_AGE_DAYS = num("SSD_GUARDIAN_PROOF_CACHE_AGE_DAYS", 2);
const WORK_PROOF_CAP_GIB = num("SSD_GUARDIAN_WORK_PROOF_CACHE_CAP_GIB", 80);
const CARGO_AGE_DAYS = num("SSD_GUARDIAN_CARGO_AGE_DAYS", 30);
const SIM_AGE_DAYS = num("SSD_GUARDIAN_SIM_AGE_DAYS", 14);

type Step = {
  id: string;
  status: "executed" | "skipped" | "failed";
  reason?: string;
  reclaimed_bytes?: number;
};
const steps: Step[] = [];

const MAX_BUFFER = 1024 * 1024 * 512;

function run(cmd: string[], timeoutMs = 900_000): { ok: boolean; stdout: string; stderr: string } {
  const r = spawnSync(cmd[0], cmd.slice(1), { stdio: "pipe", timeout: timeoutMs, shell: false, maxBuffer: MAX_BUFFER });
  return { ok: r.status === 0, stdout: r.stdout?.toString() ?? "", stderr: r.stderr?.toString() ?? "" };
}

function sh(script: string, timeoutMs = 900_000): { ok: boolean; stdout: string; stderr: string } {
  const r = spawnSync("/bin/sh", ["-c", script], { stdio: "pipe", timeout: timeoutMs, maxBuffer: MAX_BUFFER });
  return { ok: r.status === 0, stdout: r.stdout?.toString() ?? "", stderr: r.stderr?.toString() ?? "" };
}

/**
 * True when a real toolchain process matches, so we never evict a cache
 * mid-build.
 *
 * Agent harnesses embed their allowed-tool lists in argv, so a bare
 * `pgrep -f "go test"` matches every idle Claude and Codex session on the box
 * (measured 2026-08-31: 3 false positives, which would have skipped the Go
 * steps forever). Every match is therefore filtered against the harness argv
 * signature before it counts as live.
 */
const AGENT_ARGV_NOISE = /\/claude\/versions\/|\/\.local\/share\/claude\/|\/\.claude\/|\bcodex\s+--/;

function processLive(pattern: string): boolean {
  // pgrep -f for PIDs only: an argv containing newlines (agent shell snapshots
  // do) desynchronises line-oriented `pgrep -fl` parsing and inflates the match
  // count. Resolve each PID's command separately and read only its first line.
  const r = spawnSync("pgrep", ["-f", pattern], { stdio: "pipe", timeout: 5000 });
  // pgrep: 0 = matched, 1 = no match, >1 = error (and r.error covers timeout or
  // a missing binary). This guard exists to stop an eviction mid-build, so an
  // UNREADABLE process table must mean "live". Failing open here deletes a cache
  // out from under a running compile precisely when the machine is unhealthy.
  if (r.error || (r.status !== 0 && r.status !== 1)) return true;
  const self = String(process.pid);
  for (const pid of (r.stdout?.toString() ?? "").split("\n").map((l) => l.trim()).filter(Boolean)) {
    if (pid === self) continue;
    const ps = spawnSync("ps", ["-o", "command=", "-p", pid], { stdio: "pipe", timeout: 5000 });
    if (ps.error) return true;
    const cmd = (ps.stdout?.toString() ?? "").split("\n")[0] ?? "";
    // ps exit 1 with no output = the process died between pgrep and ps; that is
    // genuinely not live, and the only case where an empty read is safe.
    if (!cmd.trim()) continue;
    if (AGENT_ARGV_NOISE.test(cmd)) continue;
    return true;
  }
  return false;
}

function deviceName(udid: string): string {
  const plist = join(HOME, "Library/Developer/CoreSimulator/Devices", udid, "device.plist");
  const r = run(["plutil", "-extract", "name", "raw", plist], 10_000);
  return r.ok ? r.stdout.trim() : udid;
}

type Entry = { mtime: number; size: number; path: string };

/**
 * One stat(1) pass over a tree. Far cheaper than statSync per file in JS, and
 * the only part of this script that touches every inode.
 */
function listEntries(root: string): Entry[] {
  const expected = Number(sh(`find ${JSON.stringify(root)} -type f -mindepth 2 2>/dev/null | wc -l`).stdout.trim() || 0);
  const r = sh(`find ${JSON.stringify(root)} -type f -mindepth 2 -print0 2>/dev/null | xargs -0 stat -f '%m %z %N' 2>/dev/null`);
  const out: Entry[] = parseStatRecords(r.stdout);
  // Guard the truncation that made this step a no-op: evicting from a partial
  // listing silently under-reclaims instead of failing.
  if (expected > 0 && out.length < expected * 0.99) {
    throw new Error(`listing truncated: saw ${out.length} of ${expected} entries under ${root}`);
  }
  return out;
}

function removePaths(paths: string[]): void {
  if (!APPLY || paths.length === 0) return;
  // Batch through xargs; a 300k-path argv would blow E2BIG.
  // `--` terminates rm's option list: a cache path that begins with `-` is then
  // an operand, not a flag. Paths here are absolute today, but this is the one
  // destructive call in the script and it costs nothing to make it unambiguous.
  const r = spawnSync("/usr/bin/xargs", ["-0", "rm", "-rf", "--"], { input: paths.join("\0") + "\0", stdio: ["pipe", "pipe", "pipe"], timeout: 900_000 });
  if (r.status !== 0) throw new Error((r.stderr?.toString() ?? "").slice(0, 200));
}

/**
 * Age-gate first, then LRU-evict until under cap. Returns bytes freed.
 * Go treats a missing entry as a cache miss, so partial eviction is safe.
 */
function capCacheByLru(root: string, capBytes: number, ageDays: number): { freed: number; note: string } {
  const entries = listEntries(root);
  if (entries.length === 0) return { freed: 0, note: "empty or unreadable" };
  const { doomed, freed, total } = selectCacheEntryEvictions(entries, capBytes, Date.now() - ageDays * 86_400_000);
  removePaths(doomed.map((e) => e.path));
  return {
    freed,
    note: `${(total / GIB).toFixed(1)} GiB present, cap ${(capBytes / GIB).toFixed(0)} GiB, ${doomed.length} of ${entries.length} entries evicted`,
  };
}

/** Newest mtime anywhere under dir, or 0. Directory mtime alone is a lie for
 *  nested cache writes, so staleness has to be measured from the contents. */
function newestMtime(dir: string): number {
  const r = sh(`find ${JSON.stringify(dir)} -type f -print0 2>/dev/null | xargs -0 stat -f '%m' 2>/dev/null | sort -rn | head -1`);
  return Number(r.stdout.trim() || 0) * 1000;
}

function dirSize(dir: string): number {
  const r = sh(`du -x -sk ${JSON.stringify(dir)} 2>/dev/null | cut -f1`);
  return Number(r.stdout.trim() || 0) * 1024;
}

/** Evict whole abandoned per-hash caches: age gate first, then cap. */
function pruneProofCache(root: string, capBytes: number, ageDays: number, id: string): void {
  if (!existsSync(root)) {
    steps.push({ id, status: "skipped", reason: "cache absent" });
    return;
  }
  let dirs: { path: string; newest: number; size: number }[] = [];
  for (const name of readdirSync(root)) {
    const p = join(root, name);
    try {
      if (!statSync(p).isDirectory()) continue;
    } catch { continue; }
    dirs.push({ path: p, newest: newestMtime(p), size: dirSize(p) });
  }
  if (dirs.length === 0) {
    steps.push({ id, status: "skipped", reason: "no per-hash caches" });
    return;
  }
  const { doomed, freed, total } = selectCacheDirEvictions(dirs, capBytes, Date.now() - ageDays * 86_400_000);
  try {
    removePaths(doomed.map((d) => d.path));
    steps.push({
      id,
      status: "executed",
      reclaimed_bytes: freed,
      reason: `${(total / GIB).toFixed(1)} GiB in ${dirs.length} per-hash caches, cap ${(capBytes / GIB).toFixed(0)} GiB, ${doomed.length} evicted`,
    });
  } catch (e) {
    steps.push({ id, status: "failed", reason: String(e).slice(0, 200) });
  }
}

// Step 1: go-build cache.
if (!FORCE && processLive("(/pkg/tool/[^ ]*/(compile|link|asm|cgo)|^go (build|test|install|run))")) {
  steps.push({ id: "go-build-cap", status: "skipped", reason: "go toolchain live; refusing to evict mid-build" });
} else if (!existsSync(GO_CACHE)) {
  steps.push({ id: "go-build-cap", status: "skipped", reason: "GOCACHE absent" });
} else {
  try {
    const r = capCacheByLru(GO_CACHE, GO_CAP_GIB * GIB, GO_AGE_DAYS);
    steps.push({ id: "go-build-cap", status: "executed", reclaimed_bytes: r.freed, reason: r.note });
  } catch (e) {
    steps.push({ id: "go-build-cap", status: "failed", reason: String(e).slice(0, 200) });
  }
}

// Step 2: per-proof Go caches, on the SSD and on the Work volume.
if (!FORCE && processLive("compound (proof-check|work advance)")) {
  steps.push({ id: "proof-gocache-prune", status: "skipped", reason: "proof battery live" });
} else {
  pruneProofCache(PROOF_CACHE, PROOF_CAP_GIB * GIB, PROOF_AGE_DAYS, "proof-gocache-prune");
  if (existsSync(WORK)) {
    pruneProofCache(WORK_PROOF_CACHE, WORK_PROOF_CAP_GIB * GIB, PROOF_AGE_DAYS, "proof-gocache-prune-work");
  } else {
    steps.push({ id: "proof-gocache-prune-work", status: "skipped", reason: "Work volume not mounted" });
  }
}

// Step 3: Cargo registry. Sources and .crate downloads are both re-fetchable;
// rustup toolchains are reported, never auto-removed (a pinned toolchain is a
// project dependency, not a cache).
if (!FORCE && processLive("(/bin/(cargo|rustc)|^cargo |^rustc )")) {
  steps.push({ id: "cargo-prune", status: "skipped", reason: "cargo/rustc live" });
} else {
  const cargoSrc = join(HOME, ".cargo/registry/src");
  const cargoCache = join(HOME, ".cargo/registry/cache");
  let freed = 0;
  const removed: string[] = [];
  for (const root of [cargoSrc, cargoCache]) {
    if (!existsSync(root)) continue;
    const r = sh(`find ${JSON.stringify(root)} -mindepth 2 -maxdepth 2 -mtime +${CARGO_AGE_DAYS} -print0 2>/dev/null | xargs -0 stat -f '%z %N' 2>/dev/null`);
    for (const line of r.stdout.split("\n")) {
      if (!line) continue;
      const sp = line.indexOf(" ");
      if (sp < 0) continue;
      const p = line.slice(sp + 1);
      freed += dirSize(p);
      removed.push(p);
    }
  }
  try {
    removePaths(removed);
    const tc = existsSync(join(HOME, ".rustup/toolchains")) ? readdirSync(join(HOME, ".rustup/toolchains")) : [];
    steps.push({
      id: "cargo-prune",
      status: "executed",
      reclaimed_bytes: freed,
      reason: `${removed.length} registry entries older than ${CARGO_AGE_DAYS}d; ${tc.length} rustup toolchains present (${tc.join(", ")}) — not auto-removed`,
    });
  } catch (e) {
    steps.push({ id: "cargo-prune", status: "failed", reason: String(e).slice(0, 200) });
  }
}

// Step 4: Homebrew downloads.
{
  const before = dirSize(join(HOME, "Library/Caches/Homebrew"));
  if (!APPLY) {
    const dry = run(["brew", "cleanup", "--dry-run", "--prune=all"], 600_000);
    // A missing or broken brew reported "executed, reclaimed 0" — indistinguishable
    // from a clean cache. The exit status is the only thing that tells them apart.
    if (!dry.ok) {
      steps.push({ id: "brew-cleanup", status: "failed", reason: (dry.stderr || "brew cleanup --dry-run failed").slice(0, 200) });
    } else {
      const m = dry.stdout.match(/free approximately ([\d.]+)(\w+) of disk space/);
      const mult: Record<string, number> = { B: 1, KB: 1024, MB: 1024 ** 2, GB: GIB, TB: 1024 ** 4 };
      steps.push({ id: "brew-cleanup", status: "executed", reclaimed_bytes: m ? Math.round(Number(m[1]) * (mult[m[2]] ?? 1)) : 0, reason: "dry_run estimate" });
    }
  } else {
    const r = run(["brew", "cleanup", "--prune=all", "-s"], 900_000);
    const after = dirSize(join(HOME, "Library/Caches/Homebrew"));
    steps.push({ id: "brew-cleanup", status: r.ok ? "executed" : "failed", reclaimed_bytes: Math.max(0, before - after), reason: r.ok ? undefined : r.stderr.slice(0, 200) });
  }
}

// Step 5: CoreSimulator. `delete unavailable` only collects devices orphaned by
// a runtime removal — measured 2026-08-31 it matched nothing, so the age-gated
// erase below is the step that actually reclaims. Booted devices are never
// touched, and the age gate reads the newest file in the device's data tree.
if (processLive("(xcodebuild|Simulator\\.app/Contents/MacOS|CoreSimulatorBridge)")) {
  steps.push({ id: "simctl-prune", status: "skipped", reason: "Xcode or Simulator live" });
} else {
  const del = APPLY ? run(["xcrun", "simctl", "delete", "unavailable"], 600_000) : { ok: true, stdout: "", stderr: "" };
  const devicesRoot = join(HOME, "Library/Developer/CoreSimulator/Devices");
  const booted = run(["xcrun", "simctl", "list", "devices", "booted"]).stdout;
  let freed = 0;
  const erased: string[] = [];
  const eraseFailures: string[] = [];
  if (existsSync(devicesRoot)) {
    const cutoff = Date.now() - SIM_AGE_DAYS * 86_400_000;
    for (const udid of readdirSync(devicesRoot)) {
      const dataDir = join(devicesRoot, udid, "data");
      if (!existsSync(dataDir)) continue;
      if (booted.includes(udid)) continue;
      if (newestMtime(dataDir) >= cutoff) continue;
      const size = dirSize(join(devicesRoot, udid));
      if (APPLY) {
        // Count only what actually got erased: an ignored failure here reported
        // reclaimed bytes for a device that is still on disk.
        const er = run(["xcrun", "simctl", "erase", udid], 300_000);
        if (!er.ok) {
          eraseFailures.push(`${deviceName(udid)}: ${(er.stderr || "erase failed").slice(0, 120)}`);
          continue;
        }
      }
      freed += size;
      erased.push(udid);
    }
  }
  steps.push({
    id: "simctl-prune",
    status: del.ok && eraseFailures.length === 0 ? "executed" : "failed",
    reclaimed_bytes: freed,
    reason: `delete-unavailable${APPLY ? "" : " (skipped in dry-run)"}; ${erased.length} devices idle >${SIM_AGE_DAYS}d ${APPLY ? "erased" : "would be erased"}${erased.length ? `: ${erased.map((u) => deviceName(u)).join(", ")}` : ""}${eraseFailures.length ? `; ${eraseFailures.length} erase failure(s): ${eraseFailures.join("; ")}` : ""}${APPLY ? "" : " (reclaimed_bytes is an estimate: dry run)"}`,
  });
}

const totalReclaimed = steps.reduce((a, s) => a + (s.reclaimed_bytes ?? 0), 0);
const df = run(["df", "-k", "/System/Volumes/Data"]);
const freeBytes = (() => {
  const m = df.stdout.match(/(\d+)\s+(\d+)\s+(\d+)\s+\d+%/);
  return m ? Number(m[3]) * 1024 : 0;
})();

// The canary, same contract as enforce_ssd_budget.ts: a scheduled job that logs
// green while its steps failed is the exact failure this guardian exists to
// catch. A failed step is an alarm and a nonzero exit, never a silent success.
const verdict = summarizeAlarm(steps);

const now = new Date();
const runId = `ssd-daily-${now.toISOString().replace(/[:.]/g, "-")}`;
const summary = {
  schema_version: "compound.ssd-guardian.daily_toolchain_reclaim.v1",
  run_id: runId,
  mode: APPLY ? (FORCE ? "apply_forced" : "apply") : "dry_run",
  generated_at: now.toISOString(),
  free_bytes_after: freeBytes,
  total_reclaimed_bytes: totalReclaimed,
  alarm: verdict.alarm,
  alarm_reasons: verdict.alarm_reasons,
  steps,
};
// Dry-run writes nothing to disk (audit P0-5): a nominal dry-run must not
// create state.
if (APPLY) {
  const dir = join(REPORT_ROOT, "daily");
  mkdirSync(dir, { recursive: true });
  writeFileSync(join(dir, `${runId}.json`), JSON.stringify(summary, null, 2));
}
console.log(JSON.stringify(summary, null, 2));
process.exit(verdict.exit_code);

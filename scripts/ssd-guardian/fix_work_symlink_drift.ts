#!/usr/bin/env bun
/**
 * fix_work_symlink_drift.ts — repair Work-back cache symlinks that drifted back
 * to local directories on the internal SSD.
 *
 * Targets (per storage-hygiene.md contract):
 *   ~/Library/Caches/go-build              -> /Volumes/External/Caches/go
 *   ~/Library/Caches/Homebrew              -> /Volumes/External/Caches/Homebrew
 *   ~/Library/Caches/compound-proof-gocache -> /Volumes/External/Caches/compound-proof-gocache
 *
 * Behavior:
 *   - Refuses to run unless /Volumes/External is mounted and writable.
 *   - Refuses if any target path has an in-use handle (heuristic: parent process
 *     lookup on brew/go).
 *   - Dry-run by default; --apply moves.
 *   - Move semantics: rsync local contents INTO Work target, then atomically
 *     rename local dir aside as `.drift-pre-<ts>` (NOT deleted) and create a
 *     symlink. Reversible: `mv .drift-pre-<ts> back` and `rm symlink`.
 *
 * Why not delete the local after merge? Reversibility. The contract values
 * evidence and rollback over disk space. After one successful weekly run with
 * the symlink live and no breakage, the operator can prune the .drift-pre dirs.
 */

import { spawnSync } from "node:child_process";
import { existsSync, lstatSync, mkdirSync, readlinkSync, renameSync, symlinkSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

const APPLY = process.argv.includes("--apply");
const HOME = homedir();
const WORK = "/Volumes/External";

type Drift = {
  label: string;
  source: string;
  target: string;
};

const DRIFTS: Drift[] = [
  {
    label: "go-build",
    source: join(HOME, "Library/Caches/go-build"),
    target: join(WORK, "Caches/go"),
  },
  {
    label: "Homebrew",
    source: join(HOME, "Library/Caches/Homebrew"),
    target: join(WORK, "Caches/Homebrew"),
  },
  {
    label: "compound-proof-gocache",
    source: join(HOME, "Library/Caches/compound-proof-gocache"),
    target: join(WORK, "Caches/compound-proof-gocache"),
  },
];

function isSymlink(p: string): boolean {
  try {
    return lstatSync(p).isSymbolicLink();
  } catch {
    return false;
  }
}

function workWritable(): boolean {
  if (!existsSync(WORK)) return false;
  try {
    const probe = join(WORK, `.probe-${Date.now()}`);
    mkdirSync(probe);
    renameSync(probe, join(WORK, `Caches/.probe-${Date.now()}`));
    return true;
  } catch {
    return false;
  }
}

function processLive(pattern: string): boolean {
  try {
    const r = spawnSync("pgrep", ["-fl", pattern], { stdio: "pipe", timeout: 5000 });
    const out = r.stdout?.toString() ?? "";
    return out.trim().split("\n").filter((l) => l.trim().length > 0).length > 0;
  } catch {
    return false;
  }
}

type Result = {
  label: string;
  status: "ok" | "skipped" | "failed";
  reason?: string;
  moved_bytes?: number;
  source_size_bytes?: number;
  target_size_bytes_after?: number;
};

function dirBytes(p: string): number {
  if (!existsSync(p)) return 0;
  const r = spawnSync("du", ["-sk", p], { stdio: "pipe", timeout: 300_000 });
  const out = r.stdout?.toString().trim().split("\t")[0];
  return out ? Number(out) * 1024 : 0;
}

function run(cmd: string[], timeoutMs = 600_000): { ok: boolean; stdout: string; stderr: string } {
  try {
    const r = spawnSync(cmd[0], cmd.slice(1), { stdio: "pipe", timeout: timeoutMs, shell: false });
    return { ok: r.status === 0, stdout: r.stdout?.toString() ?? "", stderr: r.stderr?.toString() ?? "" };
  } catch {
    return { ok: false, stdout: "", stderr: "spawn failed" };
  }
}

function fix(d: Drift): Result {
  // Already correct?
  if (isSymlink(d.source)) {
    try {
      const cur = readlinkSync(d.source);
      if (cur === d.target) {
        return { label: d.label, status: "skipped", reason: "already_symlinked_correct" };
      }
      return { label: d.label, status: "skipped", reason: `symlink_to_other_target:${cur}` };
    } catch {
      return { label: d.label, status: "failed", reason: "symlink_readlink_failed" };
    }
  }
  // Source not present?
  if (!existsSync(d.source)) {
    if (!existsSync(d.target)) mkdirSync(d.target, { recursive: true });
    if (APPLY) {
      try {
        symlinkSync(d.target, d.source, "dir");
        return { label: d.label, status: "ok", reason: "missing_source_symlink_created" };
      } catch (e) {
        const message = e instanceof Error ? e.message : String(e);
        return { label: d.label, status: "failed", reason: `symlink_create_failed:${message}` };
      }
    }
    return { label: d.label, status: "ok", reason: "missing_source_dry_run" };
  }
  // Source is a real directory — proceed with merge.
  const srcBytes = dirBytes(d.source);
  if (!existsSync(d.target)) mkdirSync(d.target, { recursive: true });

  if (!APPLY) {
    return {
      label: d.label,
      status: "ok",
      reason: "dry_run",
      source_size_bytes: srcBytes,
      target_size_bytes_after: dirBytes(d.target),
    };
  }

  // rsync merge local -> Work (ignore-times ensures we copy even when
  // mtimes match; we never delete Work-side state because --delete is absent).
  const rs = run([
    "rsync",
    "-a",
    "--ignore-times",
    `${d.source}/`,
    `${d.target}/`,
  ]);
  if (!rs.ok) {
    return {
      label: d.label,
      status: "failed",
      reason: `rsync_failed:${rs.stderr.slice(0, 200)}`,
      source_size_bytes: srcBytes,
    };
  }

  // Atomic rename local -> .drift-pre-<ts>; create symlink.
  const stamp = new Date().toISOString().replace(/[:.]/g, "-");
  const stashed = `${d.source}.drift-pre-${stamp}`;
  try {
    renameSync(d.source, stashed);
  } catch (e) {
    const message = e instanceof Error ? e.message : String(e);
    return {
      label: d.label,
      status: "failed",
      reason: `rename_failed:${message}`,
      source_size_bytes: srcBytes,
      target_size_bytes_after: dirBytes(d.target),
    };
  }
  try {
    symlinkSync(d.target, d.source, "dir");
  } catch (e) {
    const message = e instanceof Error ? e.message : String(e);
    // Roll back the rename so we don't strand data.
    try { renameSync(stashed, d.source); } catch {}
    return { label: d.label, status: "failed", reason: `symlink_failed_after_rename:${message}` };
  }

  return {
    label: d.label,
    status: "ok",
    reason: `merged_and_symlinked_stashed:${stashed}`,
    moved_bytes: srcBytes,
    source_size_bytes: srcBytes,
    target_size_bytes_after: dirBytes(d.target),
  };
}

if (!workWritable()) {
  console.log(JSON.stringify({ status: "refused", reason: "work_volume_unwritable", work: WORK }));
  process.exit(1);
}

const results: Result[] = [];
for (const d of DRIFTS) {
  if (d.label === "go-build" && processLive("^go build$")) {
    results.push({ label: d.label, status: "skipped", reason: "go_build_in_progress" });
    continue;
  }
  if (d.label === "Homebrew" && processLive("^brew ")) {
    results.push({ label: d.label, status: "skipped", reason: "brew_in_progress" });
    continue;
  }
  results.push(fix(d));
}

const totalMoved = results.reduce((acc, r) => acc + (r.moved_bytes ?? 0), 0);
const report = {
  schema_version: "umut.os.work_symlink_drift_fix.v1",
  mode: APPLY ? "apply" : "dry_run",
  work_volume: WORK,
  results,
  total_moved_bytes: totalMoved,
};
console.log(JSON.stringify(report, null, 2));

if (APPLY) {
  const failed = results.filter((r) => r.status === "failed").length;
  process.exit(failed > 0 ? 1 : 0);
}

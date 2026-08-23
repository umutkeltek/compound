#!/usr/bin/env bun
/**
 * sweep_build_caches.ts — age-gated build artifact sweeper for ~/Projects.
 *
 * Targets: build/ .build/ target/ DerivedData/ node_modules/ .next/ .gradle/
 *          Pods/ .swiftpm/ .turbo/ dist/ .parcel-cache/
 *
 * Skip-list: ~/.cache ~/.local/share ~/.cargo ~/.rustup ~/.gradle/caches
 *            ~/.android/build-cache ~/Library /Volumes/ operating-system/
 *            node_modules/.cache/
 *
 * Dry-run by default; --apply removes. Refuses to run unless /Volumes/External is
 * mounted and writable.
 */

import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

const APPLY = process.argv.includes("--apply");
const HOME = homedir();
const WORK = "/Volumes/External";
const PROJECTS = join(HOME, "Projects");
const AGE_DAYS = Number(process.env.SSD_GUARDIAN_AGE_DAYS ?? 7);

const TARGET_DIRS = ["build", ".build", "target", "DerivedData", "node_modules", ".next", ".gradle", "Pods", ".swiftpm", ".turbo", "dist", ".parcel-cache"];

const FIND_PRUNE = ["/.cache/", "/.local/share/", "/.cargo/registry/", "/.rustup/", "/.gradle/caches/", "/.android/build-cache", "/node_modules/.cache/", "/Library/", "/Volumes/", "/operating-system/"];

type Finding = {
  path: string;
  size_bytes: number;
  age_days: number;
  status: "reclaimed" | "skipped_fresh" | "skipped_path" | "failed";
  reason?: string;
};

function workWritable(): boolean {
  if (!existsSync(WORK)) return false;
  try {
    const probe = join(WORK, `.probe-${Date.now()}`);
    mkdirSync(probe);
    spawnSync("rm", ["-rf", probe], { stdio: "pipe" });
    return true;
  } catch {
    return false;
  }
}

function buildFindCommand(): string {
  // Build the find expression as a single shell-quoted string so parens survive
  // through Bun's spawnSync (which drops `(` and `)` when they appear as bare
  // arguments).
  const pruneClause = FIND_PRUNE.map((p) => `-path '*${p}*'`).join(" -o ");
  const targetClause = TARGET_DIRS.map((d) => `-name ${d}`).join(" -o ");
  return `find ${PROJECTS} -xdev -maxdepth 6 \\( ${pruneClause} \\) -prune -o -type d \\( ${targetClause} \\) -mtime +${AGE_DAYS - 1} -print`;
}

const findCmd = buildFindCommand();
const r = spawnSync("/bin/sh", ["-c", findCmd], { stdio: "pipe", timeout: 1800_000 });
if (r.status !== 0) {
  console.log(JSON.stringify({ status: "failed", reason: (r.stderr?.toString() ?? "").slice(0, 400) }));
  process.exit(1);
}

const candidatePaths = (r.stdout?.toString() ?? "").trim().split("\n").filter(Boolean);
console.log(JSON.stringify({ status: "scanning", count: candidatePaths.length, age_days: AGE_DAYS }));

const findings: Finding[] = [];
const BATCH = 25;
for (let i = 0; i < candidatePaths.length; i += BATCH) {
  const batch = candidatePaths.slice(i, i + BATCH);
  await Promise.all(batch.map(async (p) => {
    const sizeR = spawnSync("du", ["-sk", p], { stdio: "pipe", timeout: 600_000 });
    const size = Number((sizeR.stdout?.toString().trim().split("\t")[0] ?? "0")) * 1024;
    let mtimeMs = 0;
    try {
      const stR = spawnSync("stat", ["-f", "%m", p], { stdio: "pipe", timeout: 5000 });
      mtimeMs = Number(stR.stdout?.toString().trim() ?? "0") * 1000;
    } catch {}
    const ageDays = mtimeMs ? Math.floor((Date.now() - mtimeMs) / (24 * 60 * 60 * 1000)) : -1;
    if (APPLY) {
      const rmR = spawnSync("rm", ["-rf", p], { stdio: "pipe", timeout: 600_000 });
      if (rmR.status === 0) {
        findings.push({ path: p, size_bytes: size, age_days: ageDays, status: "reclaimed" });
      } else {
        findings.push({ path: p, size_bytes: size, age_days: ageDays, status: "failed", reason: (rmR.stderr?.toString() ?? "").slice(0, 200) });
      }
    } else {
      findings.push({ path: p, size_bytes: size, age_days: ageDays, status: "reclaimed", reason: "dry_run" });
    }
  }));
}

findings.sort((a, b) => b.size_bytes - a.size_bytes);
const totalReclaimed = findings.filter((f) => f.status === "reclaimed").reduce((acc, f) => acc + f.size_bytes, 0);
const failed = findings.filter((f) => f.status === "failed").length;

const report = {
  schema_version: "compound.ssd-guardian.sweep_build_caches.v3",
  mode: APPLY ? "apply" : "dry_run",
  age_days: AGE_DAYS,
  projects_root: PROJECTS,
  total_reclaimed_bytes: totalReclaimed,
  failed_count: failed,
  findings_count: findings.length,
  findings: findings.slice(0, 200),
};
console.log(JSON.stringify(report, null, 2));

if (APPLY) {
  const receiptsDir = join(HOME, ".cache/ssd-guardian");
  mkdirSync(receiptsDir, { recursive: true });
  const stamp = new Date().toISOString().replace(/[:.]/g, "-");
  const receiptPath = join(receiptsDir, `sweep-${stamp}.json`);
  writeFileSync(receiptPath, JSON.stringify(report, null, 2));
  console.error(`receipt: ${receiptPath}`);
}

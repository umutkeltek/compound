#!/usr/bin/env bun
import {
  lstatSync,
  mkdirSync,
  readFileSync,
  readdirSync,
  renameSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { basename, join } from "node:path";
import { homedir } from "node:os";
import { spawnSync } from "node:child_process";

const GIB = 1024 ** 3;
const DEFAULT_FREE_FLOOR_BYTES = 140 * GIB;
const DEFAULT_GIANT_FILE_BYTES = 8 * GIB;
const DEFAULT_MIN_IDLE_SECONDS = 30 * 60;
const DEFAULT_TEMP_CACHE_MIN_AGE_SECONDS = 60 * 60;
const DEFAULT_TEMP_ROOT = "/private/tmp";
const GO_BUILD_CACHE_MARKER = "This directory holds cached build artifacts from the Go build system.";
const LM_STUDIO_PORT = 1234;

export type DeletedOpenFile = {
  pid: number;
  command: string;
  fd: string;
  sizeBytes: number;
  path: string;
};

export type ModelProcess = {
  identifier: string;
  status: string;
  ttlSeconds: number | null;
  lastUsedAtMs: number | null;
};

export type GuardDecision = {
  action: "none" | "unload_idle_lmstudio" | "report_only";
  reasons: string[];
};

export type TemporaryCacheCandidate = {
  path: string;
  sizeBytes: number;
  ageSeconds: number;
};

export type TemporaryCacheAction = TemporaryCacheCandidate & {
  status: "candidate" | "reclaimed" | "skipped_open" | "failed";
  error?: string;
};

type CommandResult = {
  ok: boolean;
  stdout: string;
  stderr: string;
};

function run(command: string[], timeoutMs = 30_000): CommandResult {
  const result = spawnSync(command[0], command.slice(1), {
    encoding: "utf8",
    timeout: timeoutMs,
    maxBuffer: 32 * 1024 * 1024,
  });
  return {
    ok: result.status === 0,
    stdout: result.stdout ?? "",
    stderr: result.stderr ?? "",
  };
}

export function isRebuildableTemporaryGoCache(name: string): boolean {
  return name === "go-cache"
    || name === "gocache"
    || name.startsWith("gocache-");
}

function hasGoBuildCacheMarker(path: string): boolean {
  try {
    return readFileSync(join(path, "README"), "utf8").includes(GO_BUILD_CACHE_MARKER);
  } catch {
    return false;
  }
}

export function temporaryGoCaches(
  root: string,
  minimumAgeSeconds: number,
  nowMs: number,
): TemporaryCacheCandidate[] {
  const candidates: TemporaryCacheCandidate[] = [];
  let entries;
  try {
    entries = readdirSync(root, { withFileTypes: true });
  } catch {
    return candidates;
  }
  for (const entry of entries) {
    if (!entry.isDirectory() || entry.isSymbolicLink() || !isRebuildableTemporaryGoCache(entry.name)) {
      continue;
    }
    const path = join(root, entry.name);
    try {
      const stat = lstatSync(path);
      const ageSeconds = Math.floor((nowMs - stat.mtimeMs) / 1000);
      if (
        !stat.isDirectory()
        || stat.isSymbolicLink()
        || ageSeconds < minimumAgeSeconds
        || !hasGoBuildCacheMarker(path)
      ) {
        continue;
      }
      const size = run(["du", "-sk", path], 120_000);
      const sizeKiB = Number(size.stdout.trim().split(/\s+/)[0]);
      candidates.push({
        path,
        sizeBytes: size.ok && Number.isFinite(sizeKiB) ? sizeKiB * 1024 : 0,
        ageSeconds,
      });
    } catch {
      // A raced removal is no longer a candidate.
    }
  }
  return candidates.sort((a, b) => b.sizeBytes - a.sizeBytes);
}

function reclaimTemporaryGoCaches(
  candidates: TemporaryCacheCandidate[],
  apply: boolean,
  minimumAgeSeconds: number,
  nowMs: number,
): TemporaryCacheAction[] {
  return candidates.map((candidate) => {
    if (!apply) return { ...candidate, status: "candidate" };
    try {
      const stat = lstatSync(candidate.path);
      const ageSeconds = Math.floor((nowMs - stat.mtimeMs) / 1000);
      if (
        !stat.isDirectory()
        || stat.isSymbolicLink()
        || ageSeconds < minimumAgeSeconds
        || !hasGoBuildCacheMarker(candidate.path)
      ) {
        return { ...candidate, status: "failed", error: "candidate changed before reclaim" };
      }
      const holders = run(["lsof", "-F", "p", "+D", candidate.path], 120_000);
      if (holders.stdout.trim().length > 0) {
        return { ...candidate, status: "skipped_open" };
      }
      if (!holders.ok && holders.stderr.trim().length > 0) {
        return {
          ...candidate,
          status: "failed",
          error: holders.stderr.trim().slice(0, 300),
        };
      }
      rmSync(candidate.path, { recursive: true });
      return { ...candidate, status: "reclaimed" };
    } catch (error) {
      return {
        ...candidate,
        status: "failed",
        error: error instanceof Error ? error.message.slice(0, 300) : String(error).slice(0, 300),
      };
    }
  });
}

export function parseDeletedOpenFiles(raw: string): DeletedOpenFile[] {
  const files: DeletedOpenFile[] = [];
  let pid = 0;
  let command = "";
  let current: Partial<DeletedOpenFile> | null = null;

  const flush = () => {
    if (
      current?.fd !== undefined
      && current.sizeBytes !== undefined
      && current.path !== undefined
      && pid > 0
    ) {
      files.push({
        pid,
        command,
        fd: current.fd,
        sizeBytes: current.sizeBytes,
        path: current.path,
      });
    }
    current = null;
  };

  for (const line of raw.split(/\r?\n/)) {
    if (!line) continue;
    const field = line[0];
    const value = line.slice(1);
    if (field === "p") {
      flush();
      pid = Number(value);
      command = "";
    } else if (field === "c") {
      command = value;
    } else if (field === "f") {
      flush();
      current = { fd: value };
    } else if (field === "s" && current) {
      current.sizeBytes = Number(value);
    } else if (field === "n" && current) {
      current.path = value;
    }
  }
  flush();
  return files;
}

export function parseElapsedSeconds(value: string): number | null {
  const match = value.trim().match(/^(?:(\d+)-)?(?:(\d+):)?(\d+):(\d+)$/);
  if (!match) return null;
  const days = Number(match[1] ?? 0);
  const hours = Number(match[2] ?? 0);
  const minutes = Number(match[3]);
  const seconds = Number(match[4]);
  return (((days * 24) + hours) * 60 + minutes) * 60 + seconds;
}

export function decideGuardAction(input: {
  freeBytes: number;
  freeFloorBytes: number;
  giantFiles: DeletedOpenFile[];
  lmStudioHolderPids: Set<number>;
  holderAgesSeconds: Map<number, number>;
  models: ModelProcess[];
  hasActiveClients: boolean;
  minimumIdleSeconds: number;
  nowMs: number;
}): GuardDecision {
  const reasons: string[] = [];
  if (input.freeBytes >= input.freeFloorBytes) {
    return { action: "none", reasons: ["free_space_healthy"] };
  }
  reasons.push("free_space_below_floor");

  const lmStudioGiants = input.giantFiles.filter((file) =>
    input.lmStudioHolderPids.has(file.pid)
  );
  if (lmStudioGiants.length === 0) {
    return { action: "report_only", reasons: [...reasons, "no_lmstudio_giant_deleted_file"] };
  }
  reasons.push("lmstudio_holds_giant_deleted_file");

  if (input.hasActiveClients) {
    return { action: "report_only", reasons: [...reasons, "active_local_model_clients"] };
  }
  const holderOldEnough = lmStudioGiants.every(
    (file) => (input.holderAgesSeconds.get(file.pid) ?? 0) >= input.minimumIdleSeconds,
  );
  if (!holderOldEnough) {
    return { action: "report_only", reasons: [...reasons, "holder_too_recent"] };
  }
  if (input.models.length === 0 || input.models.some((model) => model.status !== "idle")) {
    return { action: "report_only", reasons: [...reasons, "models_not_proven_idle"] };
  }
  const modelsOldEnough = input.models.every((model) =>
    model.lastUsedAtMs !== null
    && input.nowMs - model.lastUsedAtMs >= input.minimumIdleSeconds * 1000
  );
  if (!modelsOldEnough) {
    return { action: "report_only", reasons: [...reasons, "model_too_recent_or_unknown"] };
  }
  return { action: "unload_idle_lmstudio", reasons: [...reasons, "safe_idle_unload"] };
}

function freeBytes(): number {
  const result = run(["df", "-k", "/System/Volumes/Data"]);
  if (!result.ok) throw new Error(`df failed: ${result.stderr.trim()}`);
  const lines = result.stdout.trim().split(/\r?\n/);
  const fields = lines.at(-1)?.trim().split(/\s+/) ?? [];
  const availableKiB = Number(fields[3]);
  if (!Number.isFinite(availableKiB)) throw new Error("df returned no available block count");
  return availableKiB * 1024;
}

export function parseLoadedModels(raw: string): ModelProcess[] {
  try {
    const parsed = JSON.parse(raw);
    if (!Array.isArray(parsed)) return [];
    return parsed.flatMap((row): ModelProcess[] => {
      const identifier = row.identifier ?? row.modelIdentifier ?? row.modelKey;
      if (typeof identifier !== "string" || identifier.length === 0) return [];
      return [{
        identifier,
        status: String(row.status ?? "unknown").toLowerCase(),
        ttlSeconds: typeof row.ttlMs === "number"
          ? Math.floor(row.ttlMs / 1000)
          : typeof row.ttl === "number"
            ? row.ttl
            : typeof row.ttlSeconds === "number" ? row.ttlSeconds : null,
        lastUsedAtMs: typeof row.lastUsedTime === "number" ? row.lastUsedTime : null,
      }];
    });
  } catch {
    return [];
  }
}

function loadedModels(): ModelProcess[] {
  const result = run(["lms", "ps", "--json"]);
  return result.ok ? parseLoadedModels(result.stdout) : [];
}

function processFacts(pid: number): { lmStudio: boolean; ageSeconds: number | null } {
  const result = run(["ps", "-p", String(pid), "-o", "ppid=,etime=,command="]);
  if (!result.ok) return { lmStudio: false, ageSeconds: null };
  const match = result.stdout.trim().match(/^(\d+)\s+(\S+)\s+(.+)$/);
  if (!match) return { lmStudio: false, ageSeconds: null };
  const parent = run(["ps", "-p", match[1], "-o", "command="]);
  const identity = `${match[3]}\n${parent.stdout}`.toLowerCase();
  return {
    lmStudio: identity.includes("lm studio") || identity.includes(".lmstudio"),
    ageSeconds: parseElapsedSeconds(match[2]),
  };
}

function activeLocalClients(): boolean {
  const result = run([
    "lsof", "-nP", `-iTCP:${LM_STUDIO_PORT}`, "-sTCP:ESTABLISHED",
  ]);
  return result.ok && result.stdout.trim().length > 0;
}

function writeReceipt(receipt: object, archive: boolean): string {
  const root = process.env.COMPOUND_HOME ?? join(homedir(), ".compound");
  const dir = join(root, "state", "ssd-guardian", "pressure");
  mkdirSync(dir, { recursive: true, mode: 0o700 });
  const body = `${JSON.stringify(receipt, null, 2)}\n`;
  const latest = join(dir, "latest.json");
  const temp = join(dir, `.latest-${process.pid}.tmp`);
  writeFileSync(temp, body, { mode: 0o600 });
  renameSync(temp, latest);
  if (!archive) return latest;

  const timestamp = new Date().toISOString().replace(/[:.]/g, "-");
  const path = join(dir, `pressure-${timestamp}.json`);
  writeFileSync(path, body, { mode: 0o600 });
  return path;
}

async function main(): Promise<void> {
  const apply = process.argv.includes("--apply");
  const freeFloorBytes = Number(process.env.SSD_GUARDIAN_FREE_FLOOR_GIB ?? 140) * GIB;
  const giantThresholdBytes = Number(process.env.SSD_GUARDIAN_GIANT_FILE_GIB ?? 8) * GIB;
  const minimumIdleSeconds = Number(
    process.env.SSD_GUARDIAN_LMSTUDIO_MIN_IDLE_SECONDS ?? DEFAULT_MIN_IDLE_SECONDS,
  );
  const temporaryCacheMinimumAgeSeconds = Number(
    process.env.SSD_GUARDIAN_TEMP_CACHE_MIN_AGE_SECONDS
      ?? DEFAULT_TEMP_CACHE_MIN_AGE_SECONDS,
  );
  const temporaryRoot = process.env.SSD_GUARDIAN_TEMP_ROOT ?? DEFAULT_TEMP_ROOT;

  const beforeFreeBytes = freeBytes();
  const temporaryCacheCandidates = beforeFreeBytes < freeFloorBytes
    ? temporaryGoCaches(temporaryRoot, temporaryCacheMinimumAgeSeconds, Date.now())
    : [];
  const temporaryCacheActions = reclaimTemporaryGoCaches(
    temporaryCacheCandidates,
    apply,
    temporaryCacheMinimumAgeSeconds,
    Date.now(),
  );
  const decisionFreeBytes = apply ? freeBytes() : beforeFreeBytes;
  const lsof = run(["lsof", "+L1", "-F", "pcfsn"], 60_000);
  const deletedFiles = lsof.ok ? parseDeletedOpenFiles(lsof.stdout) : [];
  const giantFiles = deletedFiles
    .filter((file) => file.sizeBytes >= giantThresholdBytes)
    .sort((a, b) => b.sizeBytes - a.sizeBytes);
  const lmStudioHolderPids = new Set<number>();
  const holderAgesSeconds = new Map<number, number>();
  for (const pid of new Set(giantFiles.map((file) => file.pid))) {
    const facts = processFacts(pid);
    if (facts.lmStudio) lmStudioHolderPids.add(pid);
    if (facts.ageSeconds !== null) holderAgesSeconds.set(pid, facts.ageSeconds);
  }
  const models = loadedModels();
  const hasActiveClients = activeLocalClients();
  const nowMs = Date.now();
  const decision = decideGuardAction({
    freeBytes: decisionFreeBytes,
    freeFloorBytes,
    giantFiles,
    lmStudioHolderPids,
    holderAgesSeconds,
    models,
    hasActiveClients,
    minimumIdleSeconds,
    nowMs,
  });

  const actions: Array<{ identifier: string; ok: boolean; error?: string }> = [];
  let actionRecheck: GuardDecision | null = null;
  if (apply && decision.action === "unload_idle_lmstudio") {
    const refreshedModels = loadedModels();
    actionRecheck = decideGuardAction({
      freeBytes: freeBytes(),
      freeFloorBytes,
      giantFiles,
      lmStudioHolderPids,
      holderAgesSeconds,
      models: refreshedModels,
      hasActiveClients: activeLocalClients(),
      minimumIdleSeconds,
      nowMs: Date.now(),
    });
    if (actionRecheck.action !== "unload_idle_lmstudio") {
      actions.push({
        identifier: "*",
        ok: false,
        error: `readiness changed before unload: ${actionRecheck.reasons.join(",")}`,
      });
    } else {
      for (const model of refreshedModels) {
        const result = run(["lms", "unload", model.identifier], 120_000);
        actions.push({
          identifier: model.identifier,
          ok: result.ok,
          error: result.ok ? undefined : result.stderr.trim().slice(0, 300),
        });
      }
    }
  }
  const afterFreeBytes = freeBytes();
  const receipt = {
    schema_version: "compound.ssd-guardian.pressure.v1",
    generated_at: new Date().toISOString(),
    mode: apply ? "apply" : "dry_run",
    thresholds: {
      freeFloorBytes,
      giantThresholdBytes,
      minimumIdleSeconds,
      temporaryCacheMinimumAgeSeconds,
    },
    beforeFreeBytes,
    afterFreeBytes,
    reclaimedBytes: Math.max(0, afterFreeBytes - beforeFreeBytes),
    temporaryCacheCandidates,
    temporaryCacheActions,
    decision,
    actionRecheck,
    giantDeletedFiles: giantFiles.slice(0, 20).map((file) => ({
      pid: file.pid,
      command: file.command,
      fd: file.fd,
      sizeBytes: file.sizeBytes,
      name: basename(file.path),
      lmStudio: lmStudioHolderPids.has(file.pid),
      ageSeconds: holderAgesSeconds.get(file.pid) ?? null,
    })),
    models,
    hasActiveClients,
    actions,
  };
  const receiptPath = apply
    ? writeReceipt(
      receipt,
      decision.action !== "none" || temporaryCacheCandidates.length > 0,
    )
    : null;
  console.log(JSON.stringify({ ...receipt, receiptPath }, null, 2));

  const failedAction = actions.some((action) => !action.ok)
    || temporaryCacheActions.some((action) => action.status === "failed");
  if (failedAction || afterFreeBytes < freeFloorBytes) {
    process.exitCode = 2;
  }
}

if (import.meta.main) {
  await main();
}

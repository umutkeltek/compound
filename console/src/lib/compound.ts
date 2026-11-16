import { execFile } from "node:child_process";
import { existsSync } from "node:fs";
import { mkdir, readdir, readFile, rename, rm, stat, writeFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import { homedir } from "node:os";
import { tmpdir } from "node:os";
import { isAbsolute, join, resolve } from "node:path";
import { promisify } from "node:util";

import { consoleChildEnv } from "./console-child-env.ts";
import { buildWorkProjectIndex, catalogFromGrowth, inventoryFromJson } from "./work-project.ts";
import type { WorkProjectIndex } from "./work-project.ts";

const exec = promisify(execFile);

/** Runtime home: never repo. Resolved to an absolute path ONCE at module load
 * (process start) so it is the frozen instance pin — no per-request value can
 * retarget the home a mutation writes. Mirrors instance-pin.ts's resolution;
 * the instance-pin smoke asserts the two never drift. */
export const COMPOUND_HOME = resolve(
  (process.env.COMPOUND_HOME ?? "").trim() || `${homedir()}/.compound`,
);

/** Direct private readers are retired. The legacy fallback environment cannot
 * reactivate them, so every standard consumer remains projection-only. */
export function privateFallbacksEnabled(): boolean {
  return false;
}

function resolveBin(): string {
  const candidates = [
    process.env.COMPOUND_BIN,
    `${homedir()}/.local/bin/compound`,
    "compound",
  ].filter(Boolean) as string[];

  for (const candidate of candidates) {
    if (candidate === "compound" || existsSync(candidate)) return candidate;
  }
  return "compound";
}

export const COMPOUND_BIN = resolveBin();

export type Json = Record<string, any>;

export type InstalledBinaryFreshness = {
  status: "fresh" | "stale" | "missing" | "unknown";
  binary: string;
  installedBuild: string;
  head: string;
  message: string;
};

export type CompoundRunResult = {
  stdout: string;
  stderr: string;
};

export type CompoundRunFailure = CompoundRunResult & {
  message: string;
};

function firstJsonObject(text: string): string | null {
  const start = text.indexOf("{");
  if (start < 0) return null;

  let depth = 0;
  let inString = false;
  let escaped = false;

  for (let i = start; i < text.length; i++) {
    const ch = text[i];

    if (inString) {
      if (escaped) {
        escaped = false;
      } else if (ch === "\\") {
        escaped = true;
      } else if (ch === "\"") {
        inString = false;
      }
      continue;
    }

    if (ch === "\"") {
      inString = true;
      continue;
    }
    if (ch === "{") depth++;
    if (ch === "}") {
      depth--;
      if (depth === 0) return text.slice(start, i + 1);
    }
  }

  return null;
}

export async function runCompound(args: string[]): Promise<CompoundRunResult> {
  const { stdout, stderr } = await exec(COMPOUND_BIN, args, {
    env: consoleChildEnv({ COMPOUND_HOME }),
    timeout: 30_000,
    maxBuffer: 64 * 1024 * 1024,
  });
  return { stdout, stderr };
}

export function compoundFailure(error: unknown): CompoundRunFailure {
  const err = error as Partial<CompoundRunFailure> & {
    stdout?: unknown;
    stderr?: unknown;
  };
  return {
    message: typeof err.message === "string" ? err.message : "compound command failed",
    stdout: typeof err.stdout === "string" ? err.stdout : "",
    stderr: typeof err.stderr === "string" ? err.stderr : "",
  };
}

/** Why a read failed, in words: a timeout says so (node only reports
 * "Command failed"), otherwise the exit code and the first stderr line. */
export function readFailureReason(error: unknown, timeoutMs: number): string {
  const err = error as { killed?: boolean; signal?: string | null; code?: unknown; stderr?: unknown; message?: unknown };
  if (err?.killed && (err.signal === "SIGTERM" || err.signal === "SIGKILL")) {
    return `timed out after ${Math.round(timeoutMs / 1000)} s`;
  }
  const stderr = typeof err?.stderr === "string" ? err.stderr.split("\n").find((line) => line.trim()) ?? "" : "";
  if (typeof err?.code === "number") return `exited with code ${err.code}${stderr ? `: ${stderr.trim()}` : ""}`;
  if (typeof err?.code === "string") return `${err.code}${stderr ? `: ${stderr.trim()}` : ""}`;
  return compoundFailure(error).message;
}

async function execRunJson(args: string[], timeoutMs: number): Promise<Json | null> {
  const { stdout } = await exec(COMPOUND_BIN, args, {
    env: consoleChildEnv({ COMPOUND_HOME }),
    timeout: timeoutMs,
    maxBuffer: 64 * 1024 * 1024,
  });
  const json = firstJsonObject(stdout.trim());
  return json ? JSON.parse(json) : null;
}

/* ---------- runJson/runProjection memo: TTL cache + stale-while-revalidate +
 * in-flight de-dup + never-cache-null-over-good ----------
 * Every `compound ... --json` re-exec is a subprocess spawn; without this,
 * every request (and every N+1 caller within a request) pays that cost again.
 * Keyed by JSON.stringify(argv):
 *  - fresh success (<25s old, nothing has failed since): served straight from
 *    cache, no exec - `live`.
 *  - a last-good value exists but is past TTL, or the latest attempt to
 *    refresh it failed: served immediately from cache (never blocks the
 *    request) - `stale` - while at most one background exec refreshes it.
 *  - no last-good value has EVER landed for this argv: `unreachable`. A cold
 *    caller (no cache entry, no in-flight exec) awaits the first real attempt
 *    so it can report the genuine failure instead of an optimistic default.
 *  - cold + concurrent callers for the SAME argv (e.g. Promise.all fan-out):
 *    collapse into one in-flight exec when its timeout can satisfy the new
 *    caller. A caller with a longer deadline starts one replacement exec so a
 *    cheap 15s layout read cannot SIGTERM a 45s page projection. This still
 *    kills same-budget N+1 fan-outs (packets per work item, repeated stats).
 *  - invalidation advances a generation; execs that started before it may
 *    settle for their original caller but cannot repopulate the current cache.
 *
 * THE LAW (packet: console-honest-degradation, 2026-07-14 - the "clean seam"
 * bug): a failed or timed-out attempt NEVER overwrites a good cached value
 * with null, and NEVER stamps a fresh success timestamp on a failure. The old
 * code's cold-failure catch did `storeRunJsonEntry(key, null)`, which stamped
 * a fresh `storedAt` on a null value - for the next TTL window every reader
 * saw "no data" as a confirmed, current fact, and `null` mapped straight to
 * the doctrine empty state ("Clean seam. Nothing waits on you.")
 * pixel-identical to a genuinely healthy dashboard. Now a failed attempt only
 * ever updates `lastError`/`lastErrorAt`; `value`/`storedAt` (the last-good
 * facts) move forward ONLY on a real success.
 *
 * Bounded to ~50 entries (oldest-touched evicted first). Never throws - same
 * fail-soft contract as the pre-cache runJson. Mutations call
 * markRunJsonCacheOutOfDate(): values stay as last-good copies but are never
 * `live` again until a read that started after the action lands.
 * invalidateRunJsonCache() is the hard reset (memory, disk, queue).
 */
const RUN_JSON_TTL_MS = 25_000;
const RUN_JSON_MAX_ENTRIES = 50;
const DEFAULT_RUN_JSON_TIMEOUT_MS = 15_000;
/**
 * Budget for the heavy whole-home projections (attention, workbench, reviews,
 * coordination, memory candidates, home doctor). On the operator's live home
 * each costs 30-75s COLD SERIALLY, but the briefing fires several in parallel
 * and they thrash on home I/O together (measured: four doors concurrently =
 * ~117s wall on 16 cores), so any budget below the concurrent worst case never
 * lands a first success and the surface renders the unreachable seam forever.
 * The first landing populates the shared TTL cache; every later load serves
 * warm data (25s TTL) or last-good (stale-while-revalidate) without re-exec.
 */
export const HEAVY_PROJECTION_TIMEOUT_MS = 180_000;

type RunJsonCacheEntry = {
  /** last SUCCESSFUL payload; null only if a success has never landed. */
  value: Json | null;
  /** ms epoch of that last success; null only if a success has never landed. */
  storedAt: number | null;
  revalidating: boolean;
  /** message from the most recent failed attempt, if any (may postdate storedAt). */
  lastError: string | null;
  lastErrorAt: number | null;
  /** ms epoch the read that produced `value` STARTED (its snapshot moment). An
   * action marked after this moment makes the value out of date. */
  readStartedAt?: number | null;
  /** wall time of the most recent finished attempt (success or failure). */
  lastRunMs?: number | null;
  /** ms epoch the most recent attempt finished (success or failure). */
  lastAttemptAt?: number | null;
};

type RunJsonInflightEntry = {
  timeoutMs: number;
  token: symbol;
  promise: Promise<RunJsonCacheEntry>;
};

/**
 * Cache storage lives on globalThis, NOT module scope. This console's runtime
 * materializes a fresh module instance per server render (measured 2026-08-31:
 * every request saw an empty cache and re-executed every projection, and the
 * page+layout pair executed the same argv twice per render), so any
 * module-level memo is dead weight. globalThis is shared by every module
 * instance in the process, which both persists last-good data across requests
 * and collapses same-argv fan-outs (page+layout) through the in-flight map.
 */
type ReadLane = "heavy" | "light";

type ReadJob = { key: string; args: string[]; timeoutMs: number; lane: ReadLane; missing: boolean };

type RunJsonCacheGlobals = {
  cache: Map<string, RunJsonCacheEntry>;
  inflight: Map<string, RunJsonInflightEntry>;
  generation: number;
  diskWork: Promise<void>;
  binaryFreshness:
    | {
        storedAt: number;
        value: InstalledBinaryFreshness;
      }
    | null;
  /** the background read queue (non-blocking readers only). */
  queue: ReadJob[];
  queued: Set<string>;
  active: Record<ReadLane, number>;
  /** ms epoch of the last action that made every cached value out of date. */
  staleMarkAt: number;
  staleMarkLoaded: boolean;
  binaryFreshnessRefreshing: boolean;
};

const runJsonGlobals = ((globalThis as typeof globalThis & {
  __compoundConsoleRunJson?: RunJsonCacheGlobals;
}).__compoundConsoleRunJson ??= {
  cache: new Map<string, RunJsonCacheEntry>(),
  inflight: new Map<string, RunJsonInflightEntry>(),
  generation: 0,
  diskWork: Promise.resolve(),
  binaryFreshness: null,
  queue: [],
  queued: new Set<string>(),
  active: { heavy: 0, light: 0 },
  staleMarkAt: 0,
  staleMarkLoaded: false,
  binaryFreshnessRefreshing: false,
});

const runJsonCache = runJsonGlobals.cache;
const runJsonInflight = runJsonGlobals.inflight;

// Module reloads can retain an older global cache object without these fields.
runJsonGlobals.diskWork ??= Promise.resolve();
runJsonGlobals.queue ??= [];
runJsonGlobals.queued ??= new Set<string>();
runJsonGlobals.active ??= { heavy: 0, light: 0 };
runJsonGlobals.staleMarkAt ??= 0;
runJsonGlobals.staleMarkLoaded ??= false;
runJsonGlobals.binaryFreshnessRefreshing ??= false;

function queueRunJsonDiskWork<T>(work: () => Promise<T>): Promise<T> {
  const result = runJsonGlobals.diskWork.then(work);
  // A failed cache optimization must not poison later invalidations or reads.
  runJsonGlobals.diskWork = result.then(() => undefined, () => undefined);
  return result;
}


function pruneRunJsonCache() {
  while (runJsonCache.size > RUN_JSON_MAX_ENTRIES) {
    const oldestKey = runJsonCache.keys().next().value;
    if (oldestKey === undefined) break;
    runJsonCache.delete(oldestKey);
  }
}

/** A completion discarded by invalidation may observe a newer entry, but it
 * must never put its own pre-invalidation result back into the cache. */
function runJsonEntryAfterDiscard(key: string): RunJsonCacheEntry {
  return runJsonCache.get(key) ?? {
    value: null,
    storedAt: null,
    revalidating: false,
    lastError: null,
    lastErrorAt: null,
  };
}

/** A real success: the only path allowed to move `value`/`storedAt` forward. */
function commitRunJsonSuccess(
  key: string,
  value: Json | null,
  startedGeneration: number,
  startedAt: number = Date.now(),
): RunJsonCacheEntry {
  if (startedGeneration !== runJsonGlobals.generation) return runJsonEntryAfterDiscard(key);
  // Exit 0 but no parseable JSON object in stdout is not a confirmed empty
  // result - bookkeep it as a failure so never-cache-null holds even when the
  // exec itself didn't reject.
  if (value === null) {
    return commitRunJsonFailure(key, "compound produced no parseable JSON output", startedGeneration, startedAt);
  }
  // delete-then-set moves the key to the end of Map's iteration order, so the
  // eviction above naturally drops the least-recently-refreshed entry first.
  runJsonCache.delete(key);
  const storedAt = Date.now();
  const lastRunMs = Math.max(0, storedAt - startedAt);
  const entry: RunJsonCacheEntry = {
    value,
    storedAt,
    revalidating: false,
    lastError: null,
    lastErrorAt: null,
    readStartedAt: startedAt,
    lastRunMs,
    lastAttemptAt: storedAt,
  };
  runJsonCache.set(key, entry);
  pruneRunJsonCache();
  // Write-through to the disk tier so other render processes (and the next
  // server restart) inherit this last-good fact. Fire-and-forget: disk is an
  // optimization tier, never a correctness gate.
  void writeRunJsonDiskEntry(key, value, storedAt, startedAt, lastRunMs);
  return entry;
}

/** A failed (or parse-empty) attempt: NEVER touches `value`/`storedAt` - only
 * stamps the failure so status derivation can tell `stale` from `live`. */
function commitRunJsonFailure(
  key: string,
  message: string,
  startedGeneration: number,
  startedAt: number = Date.now(),
): RunJsonCacheEntry {
  if (startedGeneration !== runJsonGlobals.generation) return runJsonEntryAfterDiscard(key);
  const prev = runJsonCache.get(key);
  runJsonCache.delete(key);
  const now = Date.now();
  const entry: RunJsonCacheEntry = {
    value: prev?.value ?? null,
    storedAt: prev?.storedAt ?? null,
    revalidating: false,
    lastError: message,
    lastErrorAt: now,
    readStartedAt: prev?.readStartedAt ?? null,
    lastRunMs: Math.max(0, now - startedAt),
    lastAttemptAt: now,
  };
  runJsonCache.set(key, entry);
  pruneRunJsonCache();
  return entry;
}

/* ---------- process-surviving disk tier ----------
 * This console's runtime renders each dynamic request in a FRESH child process
 * (measured 2026-08-31: consecutive requests logged different pids), so no
 * in-process store — module scope or globalThis — can persist last-good data
 * between requests. The disk tier gives every process the same view:
 *  - write-through on every real success (atomic tmp+rename, 0600),
 *  - read on a memory miss, feeding the SAME TTL/stale machinery, so after the
 *    first-ever landing every request serves warm data instantly while one
 *    background exec revalidates,
 *  - a server restart degrades honestly to `stale` (last-good on disk) instead
 *    of `unreachable` — the value on disk IS the last-good fact.
 * Files live under the user's tmpdir (private to the uid) unless
 * COMPOUND_CONSOLE_CACHE_DIR moves them, keyed by sha256(bin, home, argv).
 * Actions leave them in place and persist an out-of-date mark beside them;
 * invalidateRunJsonCache() (the hard reset) clears the dir.
 */
/** Environment variable that moves the console's read cache folder. A cold
 * start can then be tested against a new, empty folder without touching (or
 * deleting) the operator's real cache. */
export const RUN_JSON_CACHE_DIR_ENV = "COMPOUND_CONSOLE_CACHE_DIR";
const RUN_JSON_DISK_MAX_FILES = 64;
/** Holds the ms epoch of the last action; not a `.json` file, so pruning
 * never removes it. */
const RUN_JSON_STALE_MARK_FILE = "_out-of-date-mark";

export function runJsonCacheDir(): string {
  const configured = (process.env[RUN_JSON_CACHE_DIR_ENV] ?? "").trim();
  return configured ? resolve(configured) : join(tmpdir(), "compound-console-run-json");
}

function runJsonDiskPath(key: string): string {
  return join(runJsonCacheDir(), createHash("sha256").update(key).digest("hex") + ".json");
}

const optionalNumber = (value: unknown): number | null => (typeof value === "number" && Number.isFinite(value) ? value : null);

async function readRunJsonDiskEntry(key: string): Promise<RunJsonCacheEntry | null> {
  return queueRunJsonDiskWork(async () => {
    try {
      const raw = await readFile(runJsonDiskPath(key), "utf8");
      const parsed = JSON.parse(raw) as { storedAt?: unknown; value?: unknown; readStartedAt?: unknown; lastRunMs?: unknown };
      if (typeof parsed.storedAt !== "number" || parsed.value == null || typeof parsed.value !== "object") {
        return null;
      }
      return {
        value: parsed.value as Json,
        storedAt: parsed.storedAt,
        revalidating: false,
        lastError: null,
        lastErrorAt: null,
        readStartedAt: optionalNumber(parsed.readStartedAt),
        lastRunMs: optionalNumber(parsed.lastRunMs),
        lastAttemptAt: null,
      };
    } catch {
      return null;
    }
  });
}

/** Load the persisted out-of-date mark once per process, so a restart (or a
 * second render process) never serves pre-action values as current. */
async function loadStaleMarkOnce(): Promise<void> {
  if (runJsonGlobals.staleMarkLoaded) return;
  runJsonGlobals.staleMarkLoaded = true;
  await queueRunJsonDiskWork(async () => {
    try {
      const raw = await readFile(join(runJsonCacheDir(), RUN_JSON_STALE_MARK_FILE), "utf8");
      const mark = Number(raw.trim());
      if (Number.isFinite(mark) && mark > runJsonGlobals.staleMarkAt) runJsonGlobals.staleMarkAt = mark;
    } catch {
      // No mark yet: nothing has been marked out of date.
    }
  });
}

function writeStaleMark(mark: number): void {
  void queueRunJsonDiskWork(async () => {
    try {
      const dir = runJsonCacheDir();
      await mkdir(dir, { recursive: true });
      const final = join(dir, RUN_JSON_STALE_MARK_FILE);
      const tmp = `${final}.${process.pid}.${Math.random().toString(36).slice(2)}.tmp`;
      await writeFile(tmp, String(mark), { mode: 0o600 });
      await rename(tmp, final);
    } catch {
      // The in-memory mark still holds for this process.
    }
  });
}

async function writeRunJsonDiskEntry(
  key: string,
  value: Json,
  storedAt: number,
  readStartedAt: number | null = null,
  lastRunMs: number | null = null,
): Promise<void> {
  return queueRunJsonDiskWork(async () => {
    try {
      const RUN_JSON_DISK_DIR = runJsonCacheDir();
      await mkdir(RUN_JSON_DISK_DIR, { recursive: true });
      const final = runJsonDiskPath(key);
      const tmp = `${final}.${process.pid}.${Math.random().toString(36).slice(2)}.tmp`;
      await writeFile(tmp, JSON.stringify({ storedAt, value, readStartedAt, lastRunMs }), { mode: 0o600 });
      await rename(tmp, final);
      // Bound the dir: once past the cap, unlink the oldest files. Cheap readdir,
      // best-effort — a failed prune only costs disk, never correctness.
      const names = await readdir(RUN_JSON_DISK_DIR);
      if (names.length <= RUN_JSON_DISK_MAX_FILES) return;
      const stats = await Promise.all(
        names.filter((n) => n.endsWith(".json")).map(async (n) => {
          const full = join(RUN_JSON_DISK_DIR, n);
          try {
            return { full, mtime: (await stat(full)).mtimeMs };
          } catch {
            return null;
          }
        }),
      );
      const ordered = stats.filter((s): s is { full: string; mtime: number } => s !== null).sort((a, b) => a.mtime - b.mtime);
      for (const victim of ordered.slice(0, ordered.length - RUN_JSON_DISK_MAX_FILES)) {
        await rm(victim.full, { force: true }).catch(() => undefined);
      }
    } catch {
      // Disk is an optimization tier; a failed write only costs warmth.
    }
  });
}

function clearRunJsonDiskEntries(): void {
  // Serialize removal after prior writes and before subsequent cold reads.
  // Fire-and-forget deletion alone lets an invalidated value return as live.
  void queueRunJsonDiskWork(async () => {
    try {
      const RUN_JSON_DISK_DIR = runJsonCacheDir();
      const names = await readdir(RUN_JSON_DISK_DIR);
      await Promise.all(names.map((name) => rm(join(RUN_JSON_DISK_DIR, name), { force: true }).catch(() => undefined)));
    } catch {
      // A missing or unreadable optimization tier still permits real execution.
    }
  });
}
function startRunJsonInflight(
  key: string,
  args: string[],
  timeoutMs: number,
): Promise<RunJsonCacheEntry> {
  const inflight = runJsonInflight.get(key);
  if (inflight && inflight.timeoutMs >= timeoutMs) return inflight.promise;

  const token = Symbol(key);
  const startedGeneration = runJsonGlobals.generation;
  const startedAt = Date.now();
  const promise = execRunJson(args, timeoutMs)
    .then((value) => commitRunJsonSuccess(key, value, startedGeneration, startedAt))
    .catch((error) => commitRunJsonFailure(key, readFailureReason(error, timeoutMs), startedGeneration, startedAt))
    .finally(() => {
      if (runJsonInflight.get(key)?.token === token) runJsonInflight.delete(key);
    });
  runJsonInflight.set(key, { timeoutMs, token, promise });
  return promise;
}

/**
 * The one place a `compound ... --json` argv is actually resolved: exec, TTL
 * cache, stale-while-revalidate, in-flight de-dup, never-cache-null. Both
 * `runJson` (value-only, back-compat) and `runProjection` (full honest-
 * degradation model below) read through this shared memo.
 */
/** Key includes COMPOUND_BIN so the disk tier cannot resurrect a last-good
 * payload written by a different executable into a process whose binary is
 * broken or pointed somewhere else (e.g. a smoke probing with COMPOUND_BIN=bun
 * must see a cold cache, not the operator's real success), and COMPOUND_HOME so
 * two homes can never share or mix a cached value. */
function runJsonKey(args: string[]): string {
  return `${COMPOUND_BIN}\0${COMPOUND_HOME}\0${JSON.stringify(args)}`;
}

/** Memory first, then the disk tier. Never runs compound. */
async function cachedRunJsonEntry(key: string): Promise<RunJsonCacheEntry | null | "retry"> {
  await loadStaleMarkOnce();
  const entry = runJsonCache.get(key) ?? null;
  if (entry) return entry;
  // Memory miss: this process has never seen the argv. The disk tier may
  // still carry a last-good fact from another process or a prior run.
  const diskGeneration = runJsonGlobals.generation;
  const disk = await readRunJsonDiskEntry(key);
  if (diskGeneration !== runJsonGlobals.generation) return "retry";
  // A completion may have landed while the disk read was pending; it is newer.
  const landed = runJsonCache.get(key);
  if (landed) return landed;
  if (disk) {
    runJsonCache.delete(key);
    runJsonCache.set(key, disk);
    pruneRunJsonCache();
    return disk;
  }
  return null;
}

/** True when an action happened after the read that produced this value
 * started: the value is a real last-good copy, but no longer current. */
function entryOutOfDate(entry: RunJsonCacheEntry): boolean {
  if (entry.storedAt === null) return false;
  const snapshotAt = entry.readStartedAt ?? entry.storedAt;
  return snapshotAt < runJsonGlobals.staleMarkAt;
}

async function readRunJsonEntry(args: string[], timeoutMs: number): Promise<RunJsonCacheEntry> {
  const key = runJsonKey(args);
  const cached = await cachedRunJsonEntry(key);
  if (cached === "retry") return readRunJsonEntry(args, timeoutMs);
  const entry = cached;

  if (entry) {
    const fresh = entry.storedAt !== null && Date.now() - entry.storedAt < RUN_JSON_TTL_MS && !entryOutOfDate(entry);
    if (fresh) return entry;

    // Stale values return immediately while they revalidate. A cached cold
    // failure still awaits its retry, because it has no truthful value to
    // serve. Track both paths in the shared in-flight map so a longer caller
    // can replace a shorter refresh instead of inheriting its timeout.
    entry.revalidating = true;
    const refresh = startRunJsonInflight(key, args, timeoutMs);
    return entry.storedAt === null ? refresh : entry;
  }

  return startRunJsonInflight(key, args, timeoutMs);
}

/**
 * Run read-only compound verb that emits JSON. The console surfaces mutation
 * commands but only API routes below execute allowlisted mutations.
 */
export async function runJson(args: string[]): Promise<Json | null> {
  const entry = await readRunJsonEntry(args, DEFAULT_RUN_JSON_TIMEOUT_MS);
  return entry.value;
}

export type ProjectionStatus = "live" | "stale" | "unreachable";

/**
 * The honest-degradation read model (packet: console-honest-degradation). A
 * door being down is a fact the console states, never something it papers
 * over with the last shape that happened to parse.
 *  - `live`: a fresh success landed within the TTL and nothing has failed since.
 *  - `stale`: a last-good value exists, but it is past TTL or the latest
 *    refresh attempt failed - render the last-good value, never invent a gap.
 *  - `unreachable`: no success has EVER landed for this argv - render nothing
 *    invented; the caller shows the degraded seam, not a doctrine empty state
 *    (that state is earned only by a genuinely empty `live` read).
 */
export type ProjectionRead = {
  status: ProjectionStatus;
  value: Json | null;
  /** the payload's own `generated_at` field, when the current value carries one. */
  generatedAt: string | null;
  /** ISO timestamp of the last successful fetch; null iff status is `unreachable`. */
  lastGoodAt: string | null;
  /** message from the most recent failed attempt, if any. */
  error: string | null;
  elapsedMs: number;
};

function projectionStatus(entry: RunJsonCacheEntry, now: number): ProjectionStatus {
  if (entry.storedAt === null) return "unreachable";
  const failedSinceSuccess = entry.lastErrorAt !== null && entry.lastErrorAt > entry.storedAt;
  const fresh = now - entry.storedAt < RUN_JSON_TTL_MS;
  return fresh && !failedSinceSuccess && !entryOutOfDate(entry) ? "live" : "stale";
}

/**
 * Run a read-only compound verb and return the full honest-degradation model
 * instead of a bare value - see `ProjectionRead`. Shares the exact cache
 * `runJson` uses (same key = JSON.stringify(argv)), so a page reading the same
 * argv through both never pays for two subprocess executions. `opts.timeoutMs`
 * overrides the default per call site (e.g. attention/workbench need ~45s
 * today; see console/src/app/page.tsx).
 */
export async function runProjection(args: string[], opts?: { timeoutMs?: number }): Promise<ProjectionRead> {
  const startedAt = Date.now();
  const entry = await readRunJsonEntry(args, opts?.timeoutMs ?? DEFAULT_RUN_JSON_TIMEOUT_MS);
  const now = Date.now();
  return {
    status: projectionStatus(entry, now),
    value: entry.value,
    generatedAt: typeof entry.value?.generated_at === "string" ? entry.value.generated_at : null,
    lastGoodAt: entry.storedAt !== null ? new Date(entry.storedAt).toISOString() : null,
    error: entry.lastError,
    elapsedMs: now - startedAt,
  };
}

/** Mutations advance the generation before clearing state so an exec already
 * running for the old snapshot cannot repopulate this cache after refresh. */
export function invalidateRunJsonCache(): void {
  runJsonGlobals.generation += 1;
  runJsonCache.clear();
  runJsonInflight.clear();
  runJsonGlobals.queue.length = 0;
  runJsonGlobals.queued.clear();
  runJsonGlobals.binaryFreshness = null;
  // A mutation wants the next render to read post-mutation truth: the disk
  // tier must not resurrect pre-mutation last-good values.
  clearRunJsonDiskEntries();
}

/**
 * What an action calls instead of `invalidateRunJsonCache`: every cached value
 * stays (memory and disk) as the last good copy, but is marked out of date, so
 * the next read serves it at once as "updating" and the queue refreshes it.
 * Deleting the cache made every page after an action wait for a cold read
 * (one to two minutes on a real home). The mark is persisted next to the disk
 * tier so a restart cannot serve a pre-action value as current.
 */
export function markRunJsonCacheOutOfDate(): void {
  const mark = Date.now();
  runJsonGlobals.staleMarkAt = Math.max(runJsonGlobals.staleMarkAt, mark);
  runJsonGlobals.binaryFreshness = null;
  writeStaleMark(runJsonGlobals.staleMarkAt);
}

/* ---------- non-blocking reads: a page never waits for compound ----------
 * `readProjection` returns at once from memory or disk and never awaits a
 * `compound` process:
 *  - a missing value returns as "loading" and its read starts in the background;
 *  - a value that exists returns with its real age, and a refresh is queued
 *    behind it when it is due;
 *  - a failed or timed-out read keeps the last good value and records the
 *    failure (when, why) - it never becomes zero, empty or "clean".
 * One queue runs those reads: at most 2 `compound` child processes at a time
 * in total; the same read is never started twice (an exec already running for
 * the key, from either path, is joined); and a read is due again no sooner
 * than max(25 s, 2 x its last run time). Order: values nobody has yet before
 * refreshes, and quick reads before heavy ones, so a cold page fills its
 * sub-second reads first instead of waiting two minutes behind heavy ones.
 */
export const READ_QUEUE_LIMIT = 2;
const READ_MIN_GAP_MS = 25_000;
/**
 * A queued read runs in the background, so a longer budget costs no page any
 * time. On a loaded machine `workbench --json` and `director propose` were
 * measured at just over 180 s and were killed at the heavy limit every time;
 * background heavy reads get 300 s instead. Blocking callers keep their own.
 */
export const BACKGROUND_HEAVY_TIMEOUT_MS = 300_000;
/** A read that took at least this long joins the heavy lane. */
const READ_HEAVY_RUN_MS = 10_000;
/** A copy older than this, or a failed refresh, earns a warning colour. */
export const READ_WARN_AGE_MS = 10 * 60_000;

export type ReadState = ProjectionStatus | "loading";

export type LiveRead = {
  /**
   * - `loading`: no copy yet and no failure: the first read is running or queued.
   * - `live` / `stale`: a copy exists (same meaning as `runProjection`: `live`
   *   only inside the 25 s TTL with no failure and no action since).
   * - `unreachable`: no copy, and the last attempt failed.
   */
  status: ReadState;
  value: Json | null;
  generatedAt: string | null;
  lastGoodAt: string | null;
  /** the most recent failure, when it happened after the last good copy. */
  error: string | null;
  failedAt: string | null;
  /** a read for this value is queued or running now. */
  updating: boolean;
  /** an action happened after this copy was read. */
  outOfDate: boolean;
  /** the copy is older than 10 minutes, or the last refresh failed. */
  warn: boolean;
  /** age of the copy (its own generated_at, else when its read started). */
  ageMs: number | null;
  /** how long the last finished attempt took; null when never measured. */
  lastRunMs: number | null;
  /** the command, for provenance. */
  source: string;
  elapsedMs: number;
};

function readLane(entry: RunJsonCacheEntry | null, timeoutMs: number): ReadLane {
  const ran = entry?.lastRunMs;
  if (typeof ran === "number") return ran >= READ_HEAVY_RUN_MS ? "heavy" : "light";
  return timeoutMs > DEFAULT_RUN_JSON_TIMEOUT_MS ? "heavy" : "light";
}

export function readRefreshGapMs(lastRunMs: number | null | undefined): number {
  return Math.max(READ_MIN_GAP_MS, 2 * (lastRunMs ?? 0));
}

function readIsDue(key: string, entry: RunJsonCacheEntry | null, now: number): boolean {
  if (runJsonInflight.has(key) || runJsonGlobals.queued.has(key)) return false;
  if (!entry) return true;
  const lastAttemptAt = entry.lastAttemptAt ?? entry.storedAt;
  if (lastAttemptAt === null) return true;
  // After an action, refresh once at once: unless an attempt already finished
  // after the mark (then the normal gap applies, so a failing read cannot spin).
  if (entryOutOfDate(entry) && lastAttemptAt < runJsonGlobals.staleMarkAt) return true;
  return now - lastAttemptAt >= readRefreshGapMs(entry.lastRunMs);
}

function activeReads(): number {
  return runJsonGlobals.active.heavy + runJsonGlobals.active.light;
}

/** Claim one of the 2 slots for work outside the queue (the version check).
 * Returns a release function, or null when every slot is busy. */
function claimReadSlot(lane: ReadLane): (() => void) | null {
  if (activeReads() >= READ_QUEUE_LIMIT) return null;
  runJsonGlobals.active[lane] += 1;
  let released = false;
  return () => {
    if (released) return;
    released = true;
    runJsonGlobals.active[lane] = Math.max(0, runJsonGlobals.active[lane] - 1);
    pumpReadQueue();
  };
}

function jobRank(job: ReadJob): number {
  return (job.missing ? 0 : 2) + (job.lane === "light" ? 0 : 1);
}

function pumpReadQueue(): void {
  const { queue, queued } = runJsonGlobals;
  while (queue.length > 0 && activeReads() < READ_QUEUE_LIMIT) {
    const job = queue.shift()!;
    queued.delete(job.key);
    // Never start the same read twice: a blocking caller may have started it.
    if (runJsonInflight.has(job.key)) continue;
    const release = claimReadSlot(job.lane)!;
    startRunJsonInflight(job.key, job.args, job.timeoutMs).then(release, release);
  }
}

function enqueueRead(key: string, args: string[], timeoutMs: number, entry: RunJsonCacheEntry | null): void {
  const { queue, queued } = runJsonGlobals;
  if (queued.has(key) || runJsonInflight.has(key)) return;
  const missing = !entry || entry.storedAt === null;
  const job: ReadJob = { key, args, timeoutMs, lane: readLane(entry, timeoutMs), missing };
  // Stable priority insert: missing before refresh, quick before heavy.
  const rank = jobRank(job);
  const at = queue.findIndex((queuedJob) => jobRank(queuedJob) > rank);
  if (at >= 0) queue.splice(at, 0, job);
  else queue.push(job);
  queued.add(key);
  pumpReadQueue();
}

function ageOf(entry: RunJsonCacheEntry, now: number): number | null {
  if (entry.storedAt === null) return null;
  const generated = typeof entry.value?.generated_at === "string" ? Date.parse(entry.value.generated_at) : NaN;
  const at = Number.isFinite(generated) ? generated : entry.readStartedAt ?? entry.storedAt;
  return Math.max(0, now - at);
}

/**
 * The non-blocking read. Returns at once with the cached copy (or "loading")
 * and queues a refresh when one is due. `enabled: false` returns the cached
 * copy (if any) without queueing anything - for sections hidden at the
 * current depth.
 */
export async function readProjection(
  args: string[],
  opts?: { timeoutMs?: number; enabled?: boolean },
): Promise<LiveRead> {
  const startedAt = Date.now();
  const timeoutMs = opts?.timeoutMs ?? DEFAULT_RUN_JSON_TIMEOUT_MS;
  const key = runJsonKey(args);
  let cached = await cachedRunJsonEntry(key);
  if (cached === "retry") cached = runJsonCache.get(key) ?? null;
  const entry = cached;
  const now = Date.now();
  const queueTimeoutMs = timeoutMs >= HEAVY_PROJECTION_TIMEOUT_MS ? Math.max(timeoutMs, BACKGROUND_HEAVY_TIMEOUT_MS) : timeoutMs;
  if (opts?.enabled !== false && readIsDue(key, entry, now)) enqueueRead(key, args, queueTimeoutMs, entry);

  const updating = runJsonInflight.has(key) || runJsonGlobals.queued.has(key);
  const source = `compound ${args.join(" ")}`;
  const hasValue = !!entry && entry.storedAt !== null;
  const failedSinceSuccess =
    !!entry && entry.lastErrorAt !== null && (entry.storedAt === null || entry.lastErrorAt > entry.storedAt);
  const ageMs = entry ? ageOf(entry, now) : null;
  const status: ReadState = hasValue
    ? projectionStatus(entry!, now)
    : failedSinceSuccess
      ? "unreachable"
      : "loading";
  return {
    status,
    value: hasValue ? entry!.value : null,
    generatedAt: hasValue && typeof entry!.value?.generated_at === "string" ? entry!.value.generated_at : null,
    lastGoodAt: hasValue ? new Date(entry!.storedAt!).toISOString() : null,
    error: failedSinceSuccess ? entry!.lastError : null,
    failedAt: failedSinceSuccess && entry!.lastErrorAt !== null ? new Date(entry!.lastErrorAt).toISOString() : null,
    updating,
    outOfDate: hasValue ? entryOutOfDate(entry!) : false,
    warn: failedSinceSuccess || (ageMs !== null && ageMs > READ_WARN_AGE_MS),
    ageMs,
    lastRunMs: entry?.lastRunMs ?? null,
    source,
    elapsedMs: Date.now() - startedAt,
  };
}

/**
 * Action resolution: always runs compound NOW and never answers from a cached
 * copy, because an action must re-resolve owner truth. (Before, actions got
 * this by deleting the whole cache, which made every page wait afterwards.)
 * A success is recorded in the cache as a normal last-good value; a failure
 * returns `unreachable` with no value - never a stale copy.
 */
export async function runProjectionFresh(args: string[], opts?: { timeoutMs?: number }): Promise<ProjectionRead> {
  const key = runJsonKey(args);
  const startedGeneration = runJsonGlobals.generation;
  const startedAt = Date.now();
  try {
    const value = await execRunJson(args, opts?.timeoutMs ?? DEFAULT_RUN_JSON_TIMEOUT_MS);
    const entry = commitRunJsonSuccess(key, value, startedGeneration, startedAt);
    if (value === null) {
      return {
        status: "unreachable", value: null, generatedAt: null, lastGoodAt: null,
        error: entry.lastError ?? "compound produced no parseable JSON output", elapsedMs: Date.now() - startedAt,
      };
    }
    return {
      status: "live",
      value,
      generatedAt: typeof value.generated_at === "string" ? value.generated_at : null,
      lastGoodAt: new Date().toISOString(),
      error: null,
      elapsedMs: Date.now() - startedAt,
    };
  } catch (error) {
    const message = readFailureReason(error, opts?.timeoutMs ?? DEFAULT_RUN_JSON_TIMEOUT_MS);
    commitRunJsonFailure(key, message, startedGeneration, startedAt);
    return { status: "unreachable", value: null, generatedAt: null, lastGoodAt: null, error: message, elapsedMs: Date.now() - startedAt };
  }
}

/** Value-only form of `runProjectionFresh`. */
export async function runJsonFresh(args: string[], opts?: { timeoutMs?: number }): Promise<Json | null> {
  return (await runProjectionFresh(args, opts)).value;
}

/** Start a read in the background (if due) without reading the value. */
export function prefetchProjection(args: string[], opts?: { timeoutMs?: number }): void {
  void readProjection(args, opts);
}

/** Introspection for tests and the proof: what the queue is doing right now. */
export function readQueueSnapshot(): { active: Record<ReadLane, number>; queued: number; inflight: number } {
  return {
    active: { ...runJsonGlobals.active },
    queued: runJsonGlobals.queue.length,
    inflight: runJsonInflight.size,
  };
}

/** Tests: wait until no read is queued or running. */
export async function settleProjectionReads(): Promise<void> {
  for (let i = 0; i < 1000; i += 1) {
    const running = [...runJsonInflight.values()].map((entry) => entry.promise);
    if (running.length === 0 && runJsonGlobals.queue.length === 0) return;
    if (running.length === 0) {
      pumpReadQueue();
      await new Promise((resolve) => setTimeout(resolve, 5));
      continue;
    }
    await Promise.allSettled(running);
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
}

/** Worktree inventory is scoped by an explicit operator repository binding.
 * Never substitute the console source checkout or cwd for the user's repo. */
export async function getWorktreeInventory(repo = process.env.COMPOUND_WORKTREE_REPO ?? ""): Promise<{
  repo: string | null; read: ProjectionRead | null;
}> {
  if (!repo || !isAbsolute(repo)) return { repo: null, read: null };
  return {
    repo,
    read: await runProjection(["executors", "worktrees", "--repo", repo, "--json"], { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS }),
  };
}

function repoRootFromConsole(): string {
  const candidates = [
    process.env.COMPOUND_REPO_ROOT,
    process.env.COMPOUND_REPO,
    resolve(process.cwd(), ".."),
    process.cwd(),
  ].filter(Boolean) as string[];
  for (const candidate of candidates) {
    if (existsSync(resolve(candidate, "go.mod"))) return candidate;
  }
  return resolve(process.cwd(), "..");
}

function parseBuildSHA(versionOutput: string): string {
  return versionOutput.match(/\(build ([0-9a-fA-F]+|dev)\)/)?.[1] ?? "";
}

function classifyBuild(installedBuild: string, head: string): InstalledBinaryFreshness["status"] {
  if (!installedBuild || installedBuild === "dev" || !head) return "unknown";
  if (head.startsWith(installedBuild) || installedBuild.startsWith(head)) return "fresh";
  return "stale";
}

/**
 * Never waits: returns the last known answer (or "unknown" before the first
 * check lands) and refreshes it in the background once it is older than the
 * TTL. The shell renders this on every page, so a slow `compound version`
 * under machine load must not hold the page.
 */
export async function getInstalledBinaryFreshness(): Promise<InstalledBinaryFreshness> {
  const known = runJsonGlobals.binaryFreshness;
  if (known && Date.now() - known.storedAt < RUN_JSON_TTL_MS) return known.value;
  if (!runJsonGlobals.binaryFreshnessRefreshing) {
    // `compound version` is a compound child too: it takes one of the read
    // queue's 2 slots, or waits for a later render when both are busy.
    const release = claimReadSlot("light");
    if (release) {
      runJsonGlobals.binaryFreshnessRefreshing = true;
      void checkInstalledBinaryFreshness().finally(() => {
        runJsonGlobals.binaryFreshnessRefreshing = false;
        release();
      });
    }
  }
  return known?.value ?? {
    status: "unknown",
    binary: COMPOUND_BIN,
    installedBuild: "",
    head: "",
    message: "Installed compound build freshness is being checked.",
  };
}

async function checkInstalledBinaryFreshness(): Promise<InstalledBinaryFreshness> {
  const binary = COMPOUND_BIN;
  const binaryLooksMissing = binary !== "compound" && !existsSync(binary);
  if (binaryLooksMissing) {
    const value: InstalledBinaryFreshness = {
      status: "missing",
      binary,
      installedBuild: "",
      head: "",
      message: `Installed compound binary is missing at ${binary}.`,
    };
    runJsonGlobals.binaryFreshness = { storedAt: Date.now(), value };
    return value;
  }

  try {
    const [{ stdout: versionOut }, { stdout: headOut }] = await Promise.all([
      exec(binary, ["version"], { env: consoleChildEnv(), timeout: 1_500 }),
      exec("git", ["-C", repoRootFromConsole(), "rev-parse", "--short", "HEAD"], {
        env: consoleChildEnv(),
        timeout: 1_500,
      }),
    ]);
    const installedBuild = parseBuildSHA(versionOut);
    const head = headOut.trim();
    const status = classifyBuild(installedBuild, head);
    const value: InstalledBinaryFreshness = {
      status,
      binary,
      installedBuild,
      head,
      message:
        status === "stale"
          ? `Installed compound is ${installedBuild}; source HEAD is ${head}.`
          : status === "fresh"
            ? `Installed compound matches source HEAD ${head}.`
            : "Installed compound build freshness is unknown.",
    };
    runJsonGlobals.binaryFreshness = { storedAt: Date.now(), value };
    return value;
  } catch {
    const value: InstalledBinaryFreshness = {
      status: "unknown",
      binary,
      installedBuild: "",
      head: "",
      message: "Installed compound build freshness is unknown.",
    };
    runJsonGlobals.binaryFreshness = { storedAt: Date.now(), value };
    return value;
  }
}

/* ---------- defensive accessors (schemas evolve; never crash surface) ---------- */

export const arr = (value: any): any[] => (Array.isArray(value) ? value : []);
export const num = (value: any): number =>
  typeof value === "number" ? value : 0;
export const str = (value: any): string =>
  typeof value === "string" ? value : "";

/** Relative-time helpers live in the client-safe `@/lib/time` module — import
 * them from there. (No re-export here: this file pulls in node: builtins and the
 * scripts/*.mjs smokes load it under node --experimental-strip-types, which
 * cannot resolve extensionless TS imports.) */

/* ---------- fetchers ---------- */

export async function getWorkbench(): Promise<Json | null> {
  return (await runProjection(["workbench", "--json"], { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS })).value;
}

export async function getReviews(opts?: { timeoutMs?: number }): Promise<Json | null> {
  const entry = await readRunJsonEntry(
    ["reviews", "--json"],
    opts?.timeoutMs ?? HEAVY_PROJECTION_TIMEOUT_MS,
  );
  return entry.value;
}

export async function getSourceConnections(): Promise<Json | null> {
  return runJson(["source", "connections", "--json"]);
}

export const EXECUTORS_ARGS = ["executors", "--json"];
export const DIRECTOR_PROPOSE_ARGS = ["director", "propose", "--dry-run", "--json"];

/** `executors --json` takes about a minute on a real home: it needs the heavy
 * timeout, or it never lands (the old 15 s limit killed every attempt). */
export async function getExecutors(): Promise<Json | null> {
  return (await runProjection(EXECUTORS_ARGS, { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS })).value;
}

/** Non-blocking executors read for pages (see `readProjection`). */
export async function readExecutors(opts?: { enabled?: boolean }): Promise<LiveRead> {
  return readProjection(EXECUTORS_ARGS, { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS, enabled: opts?.enabled });
}

/* ---------- open work grouped by app or codebase ----------
 * One shared read for every page that groups work by project (the app page,
 * a Work "by project" view). It joins three reads the pages already make, by
 * the same argv, so it shares their cache entries and costs no extra
 * `compound` run: the workbench's open work, the app catalog and the product
 * inventory. Like every read here it never waits for `compound`: it returns
 * at once with what the cache holds and queues refreshes (`readProjection`).
 */

export const WORK_PROJECT_INDEX_READS = {
  work: { args: ["workbench", "--json"], timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS },
  apps: { args: ["product", "growth", "--json"], timeoutMs: 60_000 },
  inventory: { args: ["system", "inventory", "--kind", "product", "--json"], timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS },
} as const;

/** A read's state without its (large) value: safe for the read-state kit and for client components. */
export type ReadInfo = Omit<LiveRead, "value">;

export type WorkProjectIndexRead = {
  /** the weakest of the three reads: unreachable, then loading, then stale, then live */
  status: ReadState;
  /**
   * Open work grouped by app or codebase (lib/work-project.ts), built from the
   * last good copy of each read. Null until all three reads hold a copy with
   * its list: unknown, never empty.
   */
  index: WorkProjectIndex | null;
  /** the reads that hold no usable list yet (no copy, or a copy without its list) */
  missing: Array<keyof typeof WORK_PROJECT_INDEX_READS>;
  /** each read's state, for ReadChip, ReadNotice and readsPending */
  reads: { work: ReadInfo; apps: ReadInfo; inventory: ReadInfo };
};

const READ_STATE_RANK: Record<ReadState, number> = { unreachable: 0, loading: 1, stale: 2, live: 3 };

/** The last index built, keyed by the three copies it was built from and the minute (stale counts move with the clock). */
let workProjectIndexMemo: { work: unknown; apps: unknown; inventory: unknown; minute: number; index: WorkProjectIndex } | null = null;

/**
 * Open work grouped by app or codebase, with per-project counts (need you,
 * in progress, waiting for a check, stale). Non-blocking. `nowMs` is the
 * clock for the stale count; pass it in tests.
 */
export async function readWorkProjectIndex(opts?: { enabled?: boolean; nowMs?: number }): Promise<WorkProjectIndexRead> {
  const { work: workArgs, apps: appArgs, inventory: inventoryArgs } = WORK_PROJECT_INDEX_READS;
  const [work, apps, inventory] = await Promise.all([
    readProjection([...workArgs.args], { timeoutMs: workArgs.timeoutMs, enabled: opts?.enabled }),
    readProjection([...appArgs.args], { timeoutMs: appArgs.timeoutMs, enabled: opts?.enabled }),
    readProjection([...inventoryArgs.args], { timeoutMs: inventoryArgs.timeoutMs, enabled: opts?.enabled }),
  ]);
  const nowMs = opts?.nowMs ?? Date.now();
  const minute = Math.floor(nowMs / 60_000);
  const open = Array.isArray(work.value?.open) ? (work.value!.open as Json[]) : null;
  const memo = workProjectIndexMemo;
  let index: WorkProjectIndex | null = null;
  const missing: WorkProjectIndexRead["missing"] = [];
  if (memo && memo.work === work.value && memo.apps === apps.value && memo.inventory === inventory.value && memo.minute === minute && open) {
    index = memo.index;
  } else {
    const catalog = apps.value ? catalogFromGrowth(apps.value) : null;
    const products = inventory.value ? inventoryFromJson(inventory.value) : null;
    if (!open) missing.push("work");
    if (!catalog) missing.push("apps");
    if (!products) missing.push("inventory");
    if (open && catalog && products) {
      index = buildWorkProjectIndex(open, catalog, products, nowMs);
      workProjectIndexMemo = { work: work.value, apps: apps.value, inventory: inventory.value, minute, index };
    }
  }
  const status = [work.status, apps.status, inventory.status].reduce((weakest, next) =>
    READ_STATE_RANK[next] < READ_STATE_RANK[weakest] ? next : weakest,
  );
  const info = ({ value: _value, ...rest }: LiveRead): ReadInfo => rest;
  return { status, index, missing, reads: { work: info(work), apps: info(apps), inventory: info(inventory) } };
}

export async function getWorkList(): Promise<Json | null> {
  return runJson(["work", "list", "--json"]);
}

export async function getWorkLeases(): Promise<Json | null> {
  return runJson(["work", "leases", "--json"]);
}

export async function getWork(id: string): Promise<Json | null> {
  // Work owns explicit IDs as well as generated ones; they are not hex-only.
  // Keep one safe argv/path element and let the owned read resolve its identity.
  if (!id.startsWith("work_") || id.length === 5 || id.trim() !== id || /[/\\\p{Cc}]/u.test(id)) return null;
  // Structured output includes lifecycle notes; human output prints them after JSON.
  return runJson(["work", "show", id, "--json"]);
}

/** Pull review rows out reviews report, whatever field is named. */
export function reviewItems(reviews: Json | null): Json[] {
  if (!reviews) return [];
  for (const key of ["items", "actions", "reviews", "queue", "review_actions"]) {
    const value = arr(reviews[key]);
    if (value.length) return value;
  }
  return [];
}

/** Human sentence for generic review rows. */
export function reviewSentence(item: Json): string {
  const area = str(item.area) || str(item.review_area);
  const action = str(item.action) || str(item.next) || str(item.reason) || str(item.next_action);
  if (action) return action;
  if (area === "external-repo-intake") {
    if (!privateFallbacksEnabled()) return "External repo awaits a projected review action.";
    return "External repo awaits decision: fill review YAML, then record it.";
  }
  return "Awaiting review.";
}

/** Exact command on generic review row: copied, never auto-run by card render. */
export function reviewCommand(item: Json): string {
  const area = str(item.area) || str(item.review_area);
  const id = str(item.id) || str(item.review_id);
  const path = str(item.path) || str(item.review_path);
  if (area === "external-repo-intake" && !privateFallbacksEnabled()) {
    return "compound reviews --area external-repo-intake";
  }
  if (area === "external-repo-intake" && (path || id)) {
    const yaml = path || `${COMPOUND_HOME}/reviews/external-repo-intake/${id}.review.yaml`;
    return `compound capability external-intake-review ${yaml.replace(/\.json$/, ".review.yaml")}`;
  }
  if (area) return `compound reviews --area ${area}`;
  return "compound reviews";
}

import { execFile } from "node:child_process";
import { existsSync } from "node:fs";
import { homedir } from "node:os";
import { resolve } from "node:path";
import { promisify } from "node:util";

const exec = promisify(execFile);

/** Runtime home: never repo. Resolved to an absolute path ONCE at module load
 * (process start) so it is the frozen instance pin — no per-request value can
 * retarget the home a mutation writes. Mirrors instance-pin.ts's resolution;
 * the instance-pin smoke asserts the two never drift. */
export const COMPOUND_HOME = resolve(
  (process.env.COMPOUND_HOME ?? "").trim() || `${homedir()}/.compound`,
);

export function privateFallbacksEnabled(): boolean {
  return process.env.COMPOUND_CONSOLE_PRIVATE_FALLBACKS === "1";
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
    env: { ...process.env, COMPOUND_HOME },
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

async function execRunJson(args: string[], timeoutMs: number): Promise<Json | null> {
  const { stdout } = await exec(COMPOUND_BIN, args, {
    env: { ...process.env, COMPOUND_HOME },
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
 * invalidateRunJsonCache() so router.refresh() always shows fresh truth.
 */
const RUN_JSON_TTL_MS = 25_000;
const RUN_JSON_MAX_ENTRIES = 50;
const DEFAULT_RUN_JSON_TIMEOUT_MS = 15_000;

type RunJsonCacheEntry = {
  /** last SUCCESSFUL payload; null only if a success has never landed. */
  value: Json | null;
  /** ms epoch of that last success; null only if a success has never landed. */
  storedAt: number | null;
  revalidating: boolean;
  /** message from the most recent failed attempt, if any (may postdate storedAt). */
  lastError: string | null;
  lastErrorAt: number | null;
};

type RunJsonInflightEntry = {
  timeoutMs: number;
  token: symbol;
  promise: Promise<RunJsonCacheEntry>;
};

const runJsonCache = new Map<string, RunJsonCacheEntry>();
const runJsonInflight = new Map<string, RunJsonInflightEntry>();

let binaryFreshnessCache:
  | {
      storedAt: number;
      value: InstalledBinaryFreshness;
    }
  | null = null;

function pruneRunJsonCache() {
  while (runJsonCache.size > RUN_JSON_MAX_ENTRIES) {
    const oldestKey = runJsonCache.keys().next().value;
    if (oldestKey === undefined) break;
    runJsonCache.delete(oldestKey);
  }
}

/** A real success: the only path allowed to move `value`/`storedAt` forward. */
function commitRunJsonSuccess(key: string, value: Json | null): RunJsonCacheEntry {
  // Exit 0 but no parseable JSON object in stdout is not a confirmed empty
  // result - bookkeep it as a failure so never-cache-null holds even when the
  // exec itself didn't reject.
  if (value === null) return commitRunJsonFailure(key, "compound produced no parseable JSON output");
  // delete-then-set moves the key to the end of Map's iteration order, so the
  // eviction above naturally drops the least-recently-refreshed entry first.
  runJsonCache.delete(key);
  const entry: RunJsonCacheEntry = { value, storedAt: Date.now(), revalidating: false, lastError: null, lastErrorAt: null };
  runJsonCache.set(key, entry);
  pruneRunJsonCache();
  return entry;
}

/** A failed (or parse-empty) attempt: NEVER touches `value`/`storedAt` - only
 * stamps the failure so status derivation can tell `stale` from `live`. */
function commitRunJsonFailure(key: string, message: string): RunJsonCacheEntry {
  const prev = runJsonCache.get(key);
  runJsonCache.delete(key);
  const entry: RunJsonCacheEntry = {
    value: prev?.value ?? null,
    storedAt: prev?.storedAt ?? null,
    revalidating: false,
    lastError: message,
    lastErrorAt: Date.now(),
  };
  runJsonCache.set(key, entry);
  pruneRunJsonCache();
  return entry;
}

/**
 * The one place a `compound ... --json` argv is actually resolved: exec, TTL
 * cache, stale-while-revalidate, in-flight de-dup, never-cache-null. Both
 * `runJson` (value-only, back-compat) and `runProjection` (full honest-
 * degradation model below) read through this shared memo.
 */
async function readRunJsonEntry(args: string[], timeoutMs: number): Promise<RunJsonCacheEntry> {
  const key = JSON.stringify(args);
  const cached = runJsonCache.get(key);

  if (cached) {
    const fresh = cached.storedAt !== null && Date.now() - cached.storedAt < RUN_JSON_TTL_MS;
    if (fresh) return cached;

    // Stale (or never-succeeded): serve what we have immediately; at most one
    // background exec refreshes it for the next reader.
    if (!cached.revalidating) {
      cached.revalidating = true;
      execRunJson(args, timeoutMs)
        .then((value) => commitRunJsonSuccess(key, value))
        .catch((error) => commitRunJsonFailure(key, compoundFailure(error).message));
    }
    return cached;
  }

  const inflight = runJsonInflight.get(key);
  if (inflight && inflight.timeoutMs >= timeoutMs) return inflight.promise;

  const token = Symbol(key);
  const promise = execRunJson(args, timeoutMs)
    .then((value) => commitRunJsonSuccess(key, value))
    .catch((error) => commitRunJsonFailure(key, compoundFailure(error).message))
    .finally(() => {
      if (runJsonInflight.get(key)?.token === token) runJsonInflight.delete(key);
    });
  runJsonInflight.set(key, { timeoutMs, token, promise });
  return promise;
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
  return fresh && !failedSinceSuccess ? "live" : "stale";
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

/** Mutations call this so the next read (router.refresh()) shows live truth. */
export function invalidateRunJsonCache(): void {
  runJsonCache.clear();
  runJsonInflight.clear();
  binaryFreshnessCache = null;
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

export async function getInstalledBinaryFreshness(): Promise<InstalledBinaryFreshness> {
  if (binaryFreshnessCache && Date.now() - binaryFreshnessCache.storedAt < RUN_JSON_TTL_MS) {
    return binaryFreshnessCache.value;
  }

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
    binaryFreshnessCache = { storedAt: Date.now(), value };
    return value;
  }

  try {
    const [{ stdout: versionOut }, { stdout: headOut }] = await Promise.all([
      exec(binary, ["version"], { timeout: 1_500 }),
      exec("git", ["-C", repoRootFromConsole(), "rev-parse", "--short", "HEAD"], { timeout: 1_500 }),
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
    binaryFreshnessCache = { storedAt: Date.now(), value };
    return value;
  } catch {
    const value: InstalledBinaryFreshness = {
      status: "unknown",
      binary,
      installedBuild: "",
      head: "",
      message: "Installed compound build freshness is unknown.",
    };
    binaryFreshnessCache = { storedAt: Date.now(), value };
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
  return runJson(["workbench", "--json"]);
}

export async function getReviews(): Promise<Json | null> {
  return runJson(["reviews", "--json"]);
}

export async function getSourceConnections(): Promise<Json | null> {
  return runJson(["source", "connections", "--json"]);
}

export async function getExecutors(): Promise<Json | null> {
  return runJson(["executors", "--json"]);
}

export async function getWorkList(): Promise<Json | null> {
  return runJson(["work", "list", "--json"]);
}

export async function getWorkLeases(): Promise<Json | null> {
  return runJson(["work", "leases", "--json"]);
}

export async function getWork(id: string): Promise<Json | null> {
  if (!/^work_[a-z0-9]+$/.test(id)) return null;
  return runJson(["work", "show", id]);
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

import { execFile } from "node:child_process";
import { existsSync } from "node:fs";
import { homedir } from "node:os";
import { promisify } from "node:util";

const exec = promisify(execFile);

/** Runtime home: never repo. */
export const COMPOUND_HOME =
  process.env.COMPOUND_HOME || `${homedir()}/.compound`;

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

/**
 * Run read-only compound verb that emits JSON. The console surfaces mutation
 * commands but only API routes below execute allowlisted mutations.
 */
export async function runJson(args: string[]): Promise<Json | null> {
  try {
    const { stdout } = await exec(COMPOUND_BIN, args, {
      env: { ...process.env, COMPOUND_HOME },
      timeout: 15_000,
      maxBuffer: 64 * 1024 * 1024,
    });
    const json = firstJsonObject(stdout.trim());
    if (!json) return null;
    return JSON.parse(json);
  } catch {
    return null;
  }
}

/* ---------- defensive accessors (schemas evolve; never crash surface) ---------- */

export const arr = (value: any): any[] => (Array.isArray(value) ? value : []);
export const num = (value: any): number =>
  typeof value === "number" ? value : 0;
export const str = (value: any): string =>
  typeof value === "string" ? value : "";

/** Timestamps arrive ISO strings, ms, us or ns since epoch. */
export function toDate(value: any): Date | null {
  if (typeof value === "string") {
    if (/^\d+$/.test(value)) return fromEpoch(Number(value));
    const d = new Date(value);
    return Number.isNaN(d.getTime()) ? null : d;
  }
  if (typeof value === "number") return fromEpoch(value);
  return null;
}

function fromEpoch(n: number): Date {
  if (n > 1e16) return new Date(n / 1e6); // ns
  if (n > 1e13) return new Date(n / 1e3); // us
  if (n > 1e11) return new Date(n); // ms
  return new Date(n * 1000); // s
}

export function ago(value: any): string {
  const d = toDate(value);
  if (!d) return "";
  const seconds = Math.max(0, (Date.now() - d.getTime()) / 1000);
  if (seconds < 90) return `${Math.round(seconds)}s ago`;
  if (seconds < 90 * 60) return `${Math.round(seconds / 60)}m ago`;
  if (seconds < 36 * 3600) return `${Math.round(seconds / 3600)}h ago`;
  return `${Math.round(seconds / 86400)}d ago`;
}

/* ---------- fetchers ---------- */

export async function getWorkbench(): Promise<Json | null> {
  return runJson(["workbench", "--json"]);
}

export async function getReviews(): Promise<Json | null> {
  return runJson(["reviews", "--json"]);
}

export async function getWorkList(): Promise<Json | null> {
  return runJson(["work", "list", "--json"]);
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

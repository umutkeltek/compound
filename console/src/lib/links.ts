/**
 * The one place that writes console URLs to Work, a work item, Decide, an
 * app, Ads, a Settings section, Memory and evidence history. Pages link
 * through these builders instead of writing URLs by hand, and the Work page
 * reads its parameters through `parseWorkListParams`, so the page that reads
 * a parameter and the pages that link to it cannot drift apart.
 *
 * Builders write one canonical URL per state: defaults are left out,
 * parameters come in a fixed order, values are URL-encoded.
 *
 * Client-safe: imports only dependency-free helpers, no reads.
 */

import { appSlug } from "@/lib/app-identity";
import { agentKey } from "@/lib/harness-label";
import { isWorkId } from "@/lib/work-id";

/* ---------- Work list: /work?lens=&filter=&owner=&app=&q=&trace= ---------- */

/**
 * Every view the Work page has. `board` (by state) is the default; `project`
 * is open work grouped by app, codebase or "not linked".
 */
export const WORK_LENSES = ["board", "table", "pipeline", "flow", "space", "offered", "project"] as const;
export type WorkLens = (typeof WORK_LENSES)[number];
export const DEFAULT_WORK_LENS: WorkLens = "board";

/** `list` is another name for the `table` view (the List). */
const WORK_LENS_ALIASES: Readonly<Record<string, WorkLens>> = { list: "table" };

/**
 * Every filter of the List (`lens=table`). `focus` is the default; `stale`
 * is in progress and picked up more than STALE_AFTER_DAYS days ago
 * (lib/work-state.ts `isStale`); `claimed` is picked up but not started;
 * `progress` is every item in progress (`isInProgress`).
 */
export const WORK_FILTERS = ["focus", "running", "blocked", "verifying", "stale", "all", "claimed", "progress"] as const;
export type WorkFilter = (typeof WORK_FILTERS)[number];
export const DEFAULT_WORK_FILTER: WorkFilter = "focus";

export type WorkListParams = {
  /** the view; `list` is accepted as `table` */
  lens?: WorkLens | "list";
  /** a List filter; a filter without a lens opens the List */
  filter?: WorkFilter;
  /** an agent, in any raw form; written as its agent key (lib/harness-label.ts) */
  owner?: string;
  /** an app slug or `product:` id; written as the slug */
  app?: string;
  /**
   * a group that is not an app (lib/work-project.ts ids): `codebase:<name>`,
   * `unlisted:<name>` or `none` (not linked). Link an app with `app`.
   */
  project?: string;
  /** search text */
  q?: string;
  /** a work item to open in the trace drawer */
  trace?: string;
};

/** What the Work page reads: every parameter present, defaults filled in, "" for absent text. */
export type ParsedWorkListParams = {
  lens: WorkLens;
  filter: WorkFilter;
  /** the agent key, or "" for no owner filter */
  owner: string;
  /** the app slug, or "" */
  app: string;
  /** a group id that is not an app (`codebase:<name>`, `unlisted:<name>`, `none`), or "" */
  project: string;
  q: string;
  /** a valid work id, or "" */
  trace: string;
};

/** Search parameters as a page receives them: Next's `searchParams` object or a URLSearchParams. */
export type SearchParamsInput =
  | { get(name: string): string | null }
  | Readonly<Record<string, string | readonly string[] | undefined>>;

function param(input: SearchParamsInput | null | undefined, name: string): string {
  if (!input) return "";
  if (typeof (input as { get?: unknown }).get === "function") return ((input as { get(name: string): string | null }).get(name) ?? "").trim();
  const value = (input as Readonly<Record<string, string | readonly string[] | undefined>>)[name];
  const first = Array.isArray(value) ? value[0] : value;
  return typeof first === "string" ? first.trim() : "";
}

function withQuery(path: string, pairs: ReadonlyArray<readonly [string, string]>, hash = ""): string {
  const query = pairs
    .filter(([, value]) => value !== "")
    .map(([key, value]) => `${key}=${encodeURIComponent(value)}`)
    .join("&");
  return `${path}${query ? `?${query}` : ""}${hash}`;
}

function workLensOf(raw: string): WorkLens | null {
  const value = raw.trim().toLowerCase();
  if ((WORK_LENSES as readonly string[]).includes(value)) return value as WorkLens;
  return WORK_LENS_ALIASES[value] ?? null;
}

function workFilterOf(raw: string): WorkFilter | null {
  const value = raw.trim().toLowerCase();
  return (WORK_FILTERS as readonly string[]).includes(value) ? (value as WorkFilter) : null;
}

/** An app slug from a slug or a `product:` id ("" when empty). */
function slugOf(app: string): string {
  return appSlug(app.trim()).trim();
}

/**
 * Link to the Work page. `workListHref({ filter: "stale" })` →
 * `/work?lens=table&filter=stale`; `workListHref({ owner: "Claude Code" })` →
 * `/work?owner=claude_code`.
 */
export function workListHref(params: WorkListParams = {}): string {
  const filter = params.filter ? workFilterOf(params.filter) : null;
  const lens = (params.lens ? workLensOf(params.lens) : null) ?? (filter ? "table" : DEFAULT_WORK_LENS);
  const owner = params.owner?.trim() ? agentKey(params.owner) : "";
  const trace = params.trace && isWorkId(params.trace.trim()) ? params.trace.trim() : "";
  return withQuery("/work", [
    ["lens", lens === DEFAULT_WORK_LENS ? "" : lens],
    ["filter", filter && filter !== DEFAULT_WORK_FILTER ? filter : ""],
    ["owner", owner],
    ["app", params.app ? slugOf(params.app) : ""],
    ["project", params.project?.trim() ?? ""],
    ["q", params.q?.trim() ?? ""],
    ["trace", trace],
  ]);
}

/**
 * Read the Work page's parameters. Unknown lens or filter values fall back to
 * the defaults; `owner` comes back as an agent key (compare it with
 * `agentKey(item.owner)`), `app` as a slug, `trace` only when it is a work id.
 */
export function parseWorkListParams(input: SearchParamsInput | null | undefined): ParsedWorkListParams {
  const ownerRaw = param(input, "owner");
  const trace = param(input, "trace");
  return {
    lens: workLensOf(param(input, "lens")) ?? DEFAULT_WORK_LENS,
    filter: workFilterOf(param(input, "filter")) ?? DEFAULT_WORK_FILTER,
    owner: ownerRaw ? agentKey(ownerRaw) : "",
    app: slugOf(param(input, "app")),
    project: param(input, "project"),
    q: param(input, "q"),
    trace: isWorkId(trace) ? trace : "",
  };
}

/**
 * Link to the List of one group's work (a row of the "by app" view): an app
 * by its slug (`app=`), any other group by its id (`project=`).
 */
export function workGroupHref(group: { readonly id: string; readonly kind: string }, filter: WorkFilter = "all"): string {
  return group.kind === "app"
    ? workListHref({ lens: "table", filter, app: group.id })
    : workListHref({ lens: "table", filter, project: group.id });
}

/* ---------- one work item ---------- */

/** Link to one work item's page: `/work/<id>`. An empty id links to the Work page. */
export function workItemHref(id: string): string {
  const value = (id ?? "").trim();
  return value ? `/work/${encodeURIComponent(value)}` : "/work";
}

/* ---------- Decide: /reviews?decision=&area=&q= ---------- */

export type DecideParams = {
  /** a decision key: opens that decision */
  decision?: string;
  /** an area id */
  area?: string;
  /** search text */
  q?: string;
};

/** Link to Decide: `/reviews`, `/reviews?decision=<key>`, `/reviews?area=<id>&q=<text>`. */
export function decideHref(params: DecideParams = {}): string {
  return withQuery("/reviews", [
    ["decision", params.decision?.trim() ?? ""],
    ["area", params.area?.trim() ?? ""],
    ["q", params.q?.trim() ?? ""],
  ]);
}

/* ---------- apps and ads ---------- */

/** Link to an app's page from its slug or `product:` id: `/apps/<slug>`. */
export function appHref(app: string): string {
  const slug = slugOf(app ?? "");
  return slug ? `/apps/${encodeURIComponent(slug)}` : "/apps";
}

export type AdsParams = {
  /** an app slug or `product:` id; written as `product:<slug>` */
  product?: string;
  /** a report window in days (`30` or `30d`); the Ads page decides which windows exist */
  window?: number | string;
};

/** Link to Ads: `/ads`, `/ads?product=product:<slug>&window=<days>`. */
export function adsHref(params: AdsParams = {}): string {
  const slug = params.product ? slugOf(params.product) : "";
  const rawWindow = typeof params.window === "number" ? String(params.window) : (params.window ?? "").trim();
  return withQuery("/ads", [
    ["product", slug ? `product:${slug}` : ""],
    ["window", /^\d+d?$/i.test(rawWindow) ? rawWindow : ""],
  ]);
}

/* ---------- settings, memory, evidence history ---------- */

/** Link that opens one Settings section and scrolls to it: `/settings?section=<key>#settings-<key>`. */
export function settingsHref(section: string): string {
  const key = (section ?? "").trim();
  return key ? withQuery("/settings", [["section", key]], `#settings-${encodeURIComponent(key)}`) : "/settings";
}

/** Link to the proposed memories waiting for review: `/memory#memory-review`. */
export function memoryReviewHref(): string {
  return "/memory#memory-review";
}

/** Link to one proposed memory on the Memory page: `/memory#<candidate id>`. */
export function memoryCandidateHref(candidateId: string): string {
  const id = (candidateId ?? "").trim();
  return id ? `/memory#${encodeURIComponent(id)}` : memoryReviewHref();
}

/** Link to a record's evidence history: `/map/lineage/<id>`. An empty id links to the map. */
export function evidenceHistoryHref(id: string): string {
  const value = (id ?? "").trim();
  return value ? `/map/lineage/${encodeURIComponent(value)}` : "/map";
}

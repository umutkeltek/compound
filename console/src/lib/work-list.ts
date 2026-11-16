/**
 * The Work List's rules: which rows a filter, an agent, an app or group and
 * a search keep, in what order, and where an old Projects address now goes.
 * The page reads its parameters with `parseWorkListParams` (lib/links.ts);
 * this file decides what they mean for a row, so the List, the counts that
 * link to it and the checks agree.
 *
 * Client-safe: no reads, no server imports.
 */

import { appSlug } from "@/lib/app-identity";
import { agentKey } from "@/lib/harness-label";
import {
  appHref,
  parseWorkListParams,
  workItemHref,
  workListHref,
  type ParsedWorkListParams,
  type SearchParamsInput,
  type WorkFilter,
} from "@/lib/links";
import { isInProgress, isOnHold, isWaitingForCheck } from "@/lib/work-state";

/** What the List needs from a row (lib/work-views.ts `WorkLensItem` carries it). */
export type WorkListRow = {
  readonly workId: string;
  readonly title: string;
  readonly state: string;
  /** raw owner or claimant ("" or "Unassigned" when none) */
  readonly owner: string;
  /** the agent's one name (lib/harness-label.ts `agentDisplayName`) */
  readonly ownerName?: string;
  /** lib/work-project.ts project id, or "" when not known yet */
  readonly projectId: string;
  readonly projectKind: string;
  readonly projectName: string;
  /** in progress and picked up more than STALE_AFTER_DAYS ago (lib/work-state.ts `isStale`) */
  readonly stale: boolean;
  /** pick-up time in epoch ms, or null when unknown */
  readonly pickedUpMs: number | null;
};

/** Does one row belong to a List filter? `focus` is what needs a look: on hold, or not moved in a week. */
export function matchesWorkFilter(row: Pick<WorkListRow, "state" | "stale">, filter: WorkFilter): boolean {
  switch (filter) {
    case "all":
      return true;
    case "focus":
      return isOnHold(row.state) || row.stale;
    case "blocked":
      return isOnHold(row.state);
    case "running":
      return row.state === "running" || row.state === "in_progress";
    case "claimed":
      return row.state === "claimed";
    case "verifying":
      return isWaitingForCheck(row.state);
    case "stale":
      return row.stale;
    case "progress":
      return isInProgress(row.state);
  }
}

/** On hold first, then the oldest pick-up; an unknown pick-up time sorts after known ones. */
function compareRows(a: WorkListRow, b: WorkListRow, oldestFirst: boolean): number {
  if (!oldestFirst) {
    const hold = Number(isOnHold(b.state)) - Number(isOnHold(a.state));
    if (hold) return hold;
    const stale = Number(b.stale) - Number(a.stale);
    if (stale) return stale;
  }
  const at = a.pickedUpMs ?? Number.POSITIVE_INFINITY;
  const bt = b.pickedUpMs ?? Number.POSITIVE_INFINITY;
  return at - bt || a.workId.localeCompare(b.workId);
}

export type WorkListResult<T extends WorkListRow> = {
  rows: T[];
  /**
   * The address asks for one app or group, but which app each item belongs
   * to is not read yet: no row can be shown honestly, so none is.
   */
  waitingForGroups: boolean;
};

/**
 * The rows the List shows for these parameters. `projectsKnown` says whether
 * each row's app or group has been read; without it an app or group filter
 * keeps nothing and says why (`waitingForGroups`), never "no work". The stale
 * list is oldest first; every other list leads with on hold, then not moved.
 */
export function filterWorkList<T extends WorkListRow>(
  items: readonly T[],
  params: Pick<ParsedWorkListParams, "filter" | "owner" | "app" | "project" | "q">,
  opts: { projectsKnown: boolean },
): WorkListResult<T> {
  const wantsGroup = !!(params.app || params.project);
  if (wantsGroup && !opts.projectsKnown) return { rows: [], waitingForGroups: true };
  const app = params.app.toLowerCase();
  const needle = params.q.trim().toLowerCase();
  const rows = items.filter((item) => {
    if (!matchesWorkFilter(item, params.filter)) return false;
    if (params.owner && agentKey(item.owner) !== params.owner) return false;
    if (app && !(item.projectKind === "app" && appSlug(item.projectId).toLowerCase() === app)) return false;
    if (params.project && item.projectId !== params.project) return false;
    if (needle) {
      const text = `${item.title} ${item.workId} ${item.owner} ${item.ownerName ?? ""} ${item.projectName}`.toLowerCase();
      if (!text.includes(needle)) return false;
    }
    return true;
  });
  rows.sort((a, b) => compareRows(a, b, params.filter === "stale"));
  return { rows, waitingForGroups: false };
}

/**
 * Where an old Projects address goes now that the page is retired: an app's
 * scope (`product:<id>`) to that app's page, an opened work item to its own
 * page, anything else to Work grouped by app.
 */
export function projectsRedirectHref(input: SearchParamsInput | null | undefined): string {
  const get = (name: string): string => {
    if (!input) return "";
    if (typeof (input as { get?: unknown }).get === "function") return ((input as { get(name: string): string | null }).get(name) ?? "").trim();
    const value = (input as Readonly<Record<string, string | readonly string[] | undefined>>)[name];
    const first = Array.isArray(value) ? value[0] : value;
    return typeof first === "string" ? first.trim() : "";
  };
  const scope = get("scope");
  if (/^product:[^\s]+$/i.test(scope)) return appHref(scope);
  const work = parseWorkListParams({ trace: get("work") }).trace;
  if (work) return workItemHref(work);
  return workListHref({ lens: "project" });
}

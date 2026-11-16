/**
 * An app's open work for its app page: every open work item the shared
 * resolver places in this app (lib/work-project.ts, through the index that
 * lib/compound.ts `readWorkProjectIndex` builds), not only the items whose
 * surface spells the app's id exactly. Work on hold comes first, then work
 * nobody has moved in a week (oldest first), then the rest (newest first).
 * Counts are the index's own, so the app page, the Work page "by app" view
 * and the Work List filtered to this app show the same numbers.
 *
 * Pure: no reads, no clock (pass `nowMs`).
 */

import { workGroupHref, workItemHref } from "../../lib/links.ts";
import { plainState } from "../../lib/plain-words.ts";
import { STALE_AFTER_DAYS, isOnHold, isStale, timestampMs } from "../../lib/work-state.ts";
import { workTitle } from "../../lib/work-title.ts";
import type { ProjectWorkCounts, WorkProjectIndex } from "../../lib/work-project.ts";

type Row = { readonly [field: string]: unknown };

export type AppWorkRow = {
  id: string;
  title: string;
  /** the state in everyday words ("Waiting for a proof check") */
  state: string;
  onHold: boolean;
  stale: boolean;
  /** whole days since the item was picked up (or created); null when the record carries no time */
  days: number | null;
  href: string;
};

export type AppWorkModel = {
  /** the counts for this app; all zero when the app has no open work */
  counts: ProjectWorkCounts;
  /** every open item of this app, in the one order (on hold, not moved, the rest) */
  rows: AppWorkRow[];
  /** the Work List filtered to this app: all, on hold, not moved, waiting for a check */
  hrefs: { all: string; onHold: string; stale: string; checking: string };
};

const str = (value: unknown): string => (typeof value === "string" ? value.trim() : "");
const DAY_MS = 86_400_000;

const ZERO: ProjectWorkCounts = { open: 0, needYou: 0, inProgress: 0, waitingForCheck: 0, stale: 0 };

/** Days since a work record was picked up, or created when nobody picked it up. */
export function workAgeDays(work: Row, nowMs: number): number | null {
  const at = timestampMs(work.claimed_at) ?? timestampMs(work.created_at);
  return at === null ? null : Math.max(0, Math.floor((nowMs - at) / DAY_MS));
}

/**
 * The app's open work. `open` is the workbench `open[]` list the index was
 * built from; `productId` is the catalog id of the app (the id the index
 * uses). Returns null while the index is not built: unknown, never empty.
 */
export function appWorkModel(
  open: readonly Row[],
  index: WorkProjectIndex | null,
  productId: string,
  nowMs: number,
): AppWorkModel | null {
  if (!index) return null;
  const group = { id: productId, kind: "app" } as const;
  const summary = index.projects.find((project) => project.id === productId);
  const seen = new Set<string>();
  const rows: (AppWorkRow & { at: number })[] = [];
  for (const work of open) {
    const id = str(work.work_id) || str(work.id);
    if (!id || seen.has(id) || index.byWork[id] !== productId) continue;
    seen.add(id);
    const state = str(work.state);
    rows.push({
      id,
      title: workTitle(work, 140),
      state: plainState(state),
      onHold: isOnHold(state),
      stale: isStale(work, nowMs),
      days: workAgeDays(work, nowMs),
      href: workItemHref(id),
      at: timestampMs(work.claimed_at) ?? timestampMs(work.created_at) ?? 0,
    });
  }
  const rank = (row: AppWorkRow) => (row.onHold ? 0 : row.stale ? 1 : 2);
  rows.sort((a, b) => rank(a) - rank(b) || (rank(a) === 1 ? a.at - b.at : b.at - a.at) || a.id.localeCompare(b.id));
  const counts = summary
    ? { open: summary.open, needYou: summary.needYou, inProgress: summary.inProgress, waitingForCheck: summary.waitingForCheck, stale: summary.stale }
    : { ...ZERO };
  return {
    counts,
    rows: rows.map(({ at: _at, ...row }) => row),
    hrefs: {
      all: workGroupHref(group, "all"),
      onHold: workGroupHref(group, "blocked"),
      stale: workGroupHref(group, "stale"),
      checking: workGroupHref(group, "verifying"),
    },
  };
}

/** "not moved in a week" for the one stale rule. */
export const NOT_MOVED_LABEL = `not moved in ${STALE_AFTER_DAYS === 7 ? "a week" : `${STALE_AFTER_DAYS} days`}`;

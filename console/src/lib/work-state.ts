/**
 * One rule for each question about a single work item's state: is it in
 * progress, on hold, waiting for a check, stale. `lib/counts.ts` counts with
 * these rules and re-exports them, so a page, a client component and the
 * read layer all ask the same question the same way.
 *
 * Client-safe and dependency-free: no reads, no imports. `lib/counts.ts`
 * itself cannot be imported by a client component (it re-exports the
 * attention reader), and `lib/compound.ts` loads its imports under the
 * node strip-types runner, so the rules live here.
 */

const DAY_MS = 86_400_000;

/**
 * Work picked up more than this many days ago and still in progress is
 * stale ("not moved"). The one threshold for every page.
 */
export const STALE_AFTER_DAYS = 7;

function text(value: unknown): string {
  return typeof value === "string" ? value : typeof value === "number" ? String(value) : "";
}

/**
 * A timestamp in epoch milliseconds. Work projections write `claimed_at` as
 * epoch nanoseconds in a string ("1794426104261463000"); other fields use
 * ISO text. Unknown or unparseable → null (never "now", never 0).
 */
export function timestampMs(value: unknown): number | null {
  const raw = text(value).trim();
  if (!raw) return null;
  if (/^\d+$/.test(raw)) {
    const n = Number(raw);
    if (!Number.isFinite(n) || n <= 0) return null;
    if (raw.length >= 16) return n / 1e6; // nanoseconds
    if (raw.length >= 12) return n; // milliseconds
    return n * 1000; // seconds
  }
  const parsed = Date.parse(raw);
  return Number.isFinite(parsed) ? parsed : null;
}

/**
 * "In progress" means an agent picked the item up and it is not finished:
 * it is running, picked up but not started, or finished running and waiting
 * for a proof check. These are the work states `claimed`, `running`
 * (`in_progress`) and `verifying` - the same set `executors --json` counts
 * as `running` and `workbench --json` counts as `in_progress`. Held and
 * blocked items are waiting, not in progress.
 */
export const IN_PROGRESS_STATES = ["claimed", "in_progress", "running", "verifying"] as const;

export function isInProgress(state: unknown): boolean {
  return (IN_PROGRESS_STATES as readonly string[]).includes(text(state));
}

/** On hold or blocked: the item waits for a decision. Not in progress. */
export const ON_HOLD_STATES = ["held", "blocked"] as const;

export function isOnHold(state: unknown): boolean {
  return (ON_HOLD_STATES as readonly string[]).includes(text(state));
}

/**
 * Picked up: an agent (or you) holds it. In progress or on hold, the same
 * work `executors --json` lists under each agent. Work suggested or ready
 * to start is open but not picked up yet: nobody holds it.
 */
export function isPickedUp(state: unknown): boolean {
  return isInProgress(state) || isOnHold(state);
}

/**
 * Which open work waits on whom, as the waiting list (`compound attention
 * --json`) sorts it: `you` are the work items held for your answer (the ones
 * Today and Decide count), `others` the ones blocked on something outside
 * you (another item, an account, a device). Ids only; lib/counts.ts
 * `waitingWork` builds it from the attention read.
 */
export type WaitingWorkIds = { readonly you: ReadonlySet<string>; readonly others: ReadonlySet<string> };

/** Whom an open work item waits on: you, something else, or nobody ("": it is not waiting). */
export type WaitsOn = "you" | "others" | "";

/**
 * Whom one open work item waits on. With the waiting list read, its own
 * sorting decides (the CLI reads more of the record than the open list
 * carries, so the console never re-derives it). Before that read lands, the
 * item's state is the only evidence: on hold or blocked counts as yours,
 * which is how every page counted it before the waiting list split it.
 */
export function waitsOn(item: { readonly work_id?: unknown; readonly state?: unknown }, waiting: WaitingWorkIds | null): WaitsOn {
  if (!waiting) return isOnHold(item.state) ? "you" : "";
  const id = text(item.work_id);
  if (waiting.you.has(id)) return "you";
  if (waiting.others.has(id)) return "others";
  return "";
}

/** Finished running and waiting for a proof check (`verifying`). Still in progress. */
export function isWaitingForCheck(state: unknown): boolean {
  return text(state) === "verifying";
}

/**
 * Stale: in progress, and picked up (`claimed_at`) more than
 * STALE_AFTER_DAYS days before `nowMs`. The projections carry no "last
 * changed" time for open work, so the pick-up time is the honest proxy. An
 * item with no readable pick-up time is not stale: its age is unknown, not
 * old. Pass the clock in; never read it here.
 */
export function isStale(item: { readonly state?: unknown; readonly claimed_at?: unknown }, nowMs: number): boolean {
  if (!isInProgress(item.state)) return false;
  const claimed = timestampMs(item.claimed_at);
  return claimed !== null && nowMs - claimed > STALE_AFTER_DAYS * DAY_MS;
}

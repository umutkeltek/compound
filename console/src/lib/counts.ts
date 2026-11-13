/**
 * One definition per number on the four main pages (Today, Work, Decide,
 * Live) and the sidebar. Each function names its unit, and every page that
 * shows the concept calls the function here instead of counting on its own.
 * Two pages may read different snapshots (Today reads `workbench --recent
 * 500`, Work reads `workbench --json`, Live reads `executors --json`), so the
 * numbers can differ for a minute; the definition never does.
 *
 * Pure functions, no reads. Server-side: "waiting for you" is re-exported from
 * the attention owner (lib/attention.ts), whose module reaches the read layer,
 * so client components receive these numbers as props instead of importing
 * this file. A count whose source has not been read is `null` at the call
 * site, never 0.
 *
 * Waiting for you (`waitingOnYou`): decisions (`groups`, one card each on
 * Decide), the items inside them (`items`), and the work items among them
 * (`workItems`, the sidebar Work badge). Units: decisions, items, work items.
 */

export { waitingOnYou, type WaitingOnYou } from "@/lib/attention";

type Row = Record<string, unknown>;

const DAY_MS = 86_400_000;

/** Work items older than this since they were picked up count as "not moved". */
export const STALE_AFTER_DAYS = 7;

/** `n` with its unit, singular or plural: `unit(22, "decision")` → "22 decisions". */
export function unit(n: number, one: string, many = `${one}s`): string {
  return `${n.toLocaleString("en-US")} ${n === 1 ? one : many}`;
}

function text(value: unknown): string {
  return typeof value === "string" ? value : typeof value === "number" ? String(value) : "";
}

/**
 * A timestamp as epoch milliseconds. The work projections write `claimed_at`
 * as epoch nanoseconds in a string ("1794426104261463000"); other fields use
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

/* ---------- open work ---------- */

/** Open work items: every item the work projection lists as not finished. Unit: work items. */
export function countOpenWork(open: readonly Row[]): number {
  return open.length;
}

/* ---------- in progress ---------- */

/**
 * "In progress" means an agent has picked the item up and it is not finished:
 * it is running, picked up but not started, or finished running and waiting
 * for a proof check. These are the work states `claimed`, `running`
 * (`in_progress`) and `verifying` - the same set `executors --json` counts as
 * `running` and `workbench --json` counts as `in_progress`. Held and blocked
 * items are waiting, not in progress.
 */
export const IN_PROGRESS_STATES = ["claimed", "in_progress", "running", "verifying"] as const;

export function isInProgress(state: unknown): boolean {
  return (IN_PROGRESS_STATES as readonly string[]).includes(text(state));
}

export type InProgressCount = {
  /** work items marked in progress */
  total: number;
  /** of those, the state says running */
  running: number;
  /** of those, picked up but not started (`claimed`) */
  pickedUp: number;
  /** of those, finished running and waiting for a proof check (`verifying`) */
  checking: number;
  /**
   * of those, picked up more than STALE_AFTER_DAYS ago. The projections carry
   * no "last changed" time for open work, only `claimed_at`, so this is the
   * honest proxy: an item picked up 7+ days ago and still open has not
   * finished in that time. Null when no item carries `claimed_at`.
   */
  pickedUpOver7Days: number | null;
  /** how many in-progress items carry a `claimed_at` time at all */
  withPickupTime: number;
};

/**
 * Count work items in progress. Takes any list of work rows with `state` and
 * `claimed_at` (workbench `open[]` or executors `current_work[]`); rows are
 * de-duplicated by `work_id`.
 */
export function countInProgress(items: readonly Row[], nowMs: number): InProgressCount {
  const seen = new Set<string>();
  const out: InProgressCount = { total: 0, running: 0, pickedUp: 0, checking: 0, pickedUpOver7Days: 0, withPickupTime: 0 };
  for (const item of items) {
    const id = text(item.work_id);
    if (id) {
      if (seen.has(id)) continue;
      seen.add(id);
    }
    const state = text(item.state);
    if (!isInProgress(state)) continue;
    out.total += 1;
    if (state === "claimed") out.pickedUp += 1;
    else if (state === "verifying") out.checking += 1;
    else out.running += 1;
    const claimed = timestampMs(item.claimed_at);
    if (claimed !== null) {
      out.withPickupTime += 1;
      if (nowMs - claimed > STALE_AFTER_DAYS * DAY_MS) out.pickedUpOver7Days = (out.pickedUpOver7Days ?? 0) + 1;
    }
  }
  if (out.withPickupTime === 0) out.pickedUpOver7Days = null;
  return out;
}

/**
 * The in-progress count in plain sentences, the same on every page:
 * headline ("515 work items are marked in progress"), detail (the split by
 * state) and age (how many were picked up 7+ days ago, or why that is
 * unknown). "Marked" is deliberate: the state is what the work record says,
 * not proof that an agent is running this minute.
 */
export function describeInProgress(c: InProgressCount): { headline: string; detail: string; age: string } {
  const headline = `${unit(c.total, "work item")} ${c.total === 1 ? "is" : "are"} marked in progress`;
  const parts = [
    c.running ? `${c.running.toLocaleString("en-US")} running` : "",
    c.pickedUp ? `${c.pickedUp.toLocaleString("en-US")} picked up but not started` : "",
    c.checking ? `${c.checking.toLocaleString("en-US")} waiting for a proof check` : "",
  ].filter(Boolean);
  const detail = parts.length ? `${parts.join(", ")}.` : "";
  let age = "";
  if (c.total > 0) {
    if (c.pickedUpOver7Days === null) {
      age = "The work records carry no pick-up time, so how long they have been open is unknown.";
    } else if (c.pickedUpOver7Days > 0) {
      age = `${c.pickedUpOver7Days.toLocaleString("en-US")} of them ${c.pickedUpOver7Days === 1 ? "was" : "were"} picked up more than ${STALE_AFTER_DAYS} days ago and ${c.pickedUpOver7Days === 1 ? "is" : "are"} still not finished.`;
    } else {
      age = `All of them were picked up in the last ${STALE_AFTER_DAYS} days.`;
    }
  }
  return { headline, detail, age };
}

/** Items on hold or blocked: waiting, not in progress. Unit: work items. */
export function countOnHold(items: readonly Row[]): number {
  const seen = new Set<string>();
  let n = 0;
  for (const item of items) {
    const id = text(item.work_id);
    if (id) {
      if (seen.has(id)) continue;
      seen.add(id);
    }
    const state = text(item.state);
    if (state === "held" || state === "blocked") n += 1;
  }
  return n;
}

/* ---------- agents ---------- */

/** The executors projection's bucket for work whose owner matches no agent. */
export const UNASSIGNED_AGENT_ID = "unassigned";

export type AgentCount = {
  /** agents (harnesses) that hold at least one open work item */
  withWork: number;
  /** agents that are set up but hold no work */
  idle: number;
  /** open work items held by an agent */
  heldItems: number;
  /** open work items whose owner matches no known agent */
  noAgentItems: number;
};

type AgentRow = { id: string; currentWork: readonly unknown[] };

/** Count agents from the normalized executors rows. Unit: agents; items are work items. */
export function countAgents(rows: readonly AgentRow[]): AgentCount {
  const out: AgentCount = { withWork: 0, idle: 0, heldItems: 0, noAgentItems: 0 };
  for (const row of rows) {
    const items = row.currentWork.length;
    if (row.id === UNASSIGNED_AGENT_ID) {
      out.noAgentItems += items;
      continue;
    }
    if (items > 0) {
      out.withWork += 1;
      out.heldItems += items;
    } else {
      out.idle += 1;
    }
  }
  return out;
}

/** The agent count in one plain sentence ("4 agents hold 444 work items. 90 more have no agent."). */
export function describeAgents(a: AgentCount): string {
  const held = a.withWork
    ? `${unit(a.withWork, "agent")} ${a.withWork === 1 ? "holds" : "hold"} ${unit(a.heldItems, "work item")}.`
    : "No agent holds any work.";
  const none = a.noAgentItems ? ` ${unit(a.noAgentItems, "more work item")} ${a.noAgentItems === 1 ? "has" : "have"} no agent.` : "";
  const idle = a.idle ? ` ${unit(a.idle, "agent")} ${a.idle === 1 ? "has" : "have"} nothing to do.` : "";
  return held + none + idle;
}

/* ---------- decisions ---------- */

export type DecisionCount = {
  /** decisions waiting (one card each on Decide; the system calls them groups) */
  decisions: number;
  /** the items inside those decisions */
  items: number;
  /** decisions you can answer on Decide right now (a ready action) */
  readyNow: number;
  /** decisions that cannot be answered yet, each with a reason */
  notYet: number;
  /**
   * What the last check of each decision found (its standing, lib/decide-model.ts),
   * whether or not that check is still inside the live window. Units: decisions.
   */
  found: {
    /** the last check found a button for it (a button shows only while that check is live) */
    decidable: number;
    /** no button exists for this kind of decision yet */
    noButton: number;
    /** an action exists but its approval record is incomplete */
    incomplete: number;
    /** the approval rules hold it */
    held: number;
    /** any other reason (details still loading, one review per repository) */
    other: number;
  };
  /** decisions whose last check is older than the live window */
  checkedEarlier: number;
};

/**
 * Count decisions on Decide. "Ready now" is the Decide card's own readiness
 * (`face.state === "ready"`): the owner offers an action and the console may
 * run it now. Everything else is "not yet". `found` splits every decision by
 * what its last check found, so the page can say why each one waits.
 */
export function countDecisions(
  groups: readonly { count: number; face: { state: string }; standing?: { kind: string; live: boolean } }[],
): DecisionCount {
  let items = 0;
  let readyNow = 0;
  let checkedEarlier = 0;
  const found = { decidable: 0, noButton: 0, incomplete: 0, held: 0, other: 0 };
  for (const group of groups) {
    items += group.count;
    if (group.face.state === "ready") readyNow += 1;
    const kind = group.standing?.kind ?? (group.face.state === "ready" ? "ready" : "other");
    if (kind === "ready") found.decidable += 1;
    else if (kind === "no_button") found.noButton += 1;
    else if (kind === "incomplete") found.incomplete += 1;
    else if (kind === "held") found.held += 1;
    else found.other += 1;
    if (group.standing && !group.standing.live) checkedEarlier += 1;
  }
  return { decisions: groups.length, items, readyNow, notYet: groups.length - readyNow, found, checkedEarlier };
}

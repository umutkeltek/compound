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
 * Waiting for you (`waitingOnYou`): only what is his. Decisions he can answer
 * (`groups`, one card each on Decide), the items inside them (`items`), and
 * the work items among them (`workItems`). Decisions an agent must word first,
 * agent housekeeping (skill proposals), what closed without him and what waits
 * on others are named apart and never added in. Units: decisions, items, work
 * items. Today, Decide and the sidebar read `groups` under one name.
 */

export { attentionShare, housekeepingGroups, waitingOnYou, yourDecisionGroups, type AttentionShare, type WaitingOnYou } from "@/lib/attention";
import type { WaitingOnYou } from "@/lib/attention";

import { isInProgress, isOnHold, isStale, isWaitingForCheck, STALE_AFTER_DAYS, timestampMs } from "@/lib/work-state";
import { NO_AGENT_KEY, OPERATOR_AGENT_KEY } from "@/lib/harness-label";

// The per-item rules (in progress, on hold, waiting for a check, stale and its
// threshold) live in the client-safe lib/work-state.ts; this file counts with
// them and re-exports them for the pages that already import them from here.
export {
  IN_PROGRESS_STATES,
  isInProgress,
  isOnHold,
  isStale,
  isWaitingForCheck,
  ON_HOLD_STATES,
  STALE_AFTER_DAYS,
  timestampMs,
} from "@/lib/work-state";

type Row = Record<string, unknown>;

/** `n` with its unit, singular or plural: `unit(22, "decision")` → "22 decisions". */
export function unit(n: number, one: string, many = `${one}s`): string {
  return `${n.toLocaleString("en-US")} ${n === 1 ? one : many}`;
}

function text(value: unknown): string {
  return typeof value === "string" ? value : typeof value === "number" ? String(value) : "";
}

/* ---------- open work ---------- */

/** Open work items: every item the work projection lists as not finished. Unit: work items. */
export function countOpenWork(open: readonly Row[]): number {
  return open.length;
}

/* ---------- in progress ---------- */

/*
 * "In progress" (IN_PROGRESS_STATES, isInProgress) is defined in
 * lib/work-state.ts: claimed, running (in_progress) and verifying. Held and
 * blocked items are waiting, not in progress.
 */

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
    else if (isWaitingForCheck(state)) out.checking += 1;
    else out.running += 1;
    const claimed = timestampMs(item.claimed_at);
    if (claimed !== null) {
      out.withPickupTime += 1;
      if (isStale(item, nowMs)) out.pickedUpOver7Days = (out.pickedUpOver7Days ?? 0) + 1;
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
    if (isOnHold(item.state)) n += 1;
  }
  return n;
}

/* ---------- agents ---------- */

/**
 * The executors projection's bucket for work whose owner matches no agent.
 * The same string as the agent key for "no agent" (lib/harness-label.ts).
 */
export const UNASSIGNED_AGENT_ID = NO_AGENT_KEY;

export type AgentCount = {
  /** agents (harnesses) that hold at least one open work item */
  withWork: number;
  /** agents that are set up but hold no work */
  idle: number;
  /** open work items held by an agent */
  heldItems: number;
  /** open work items whose owner matches no known agent */
  noAgentItems: number;
  /** open work items you hold yourself (the operator's row): yours, not an agent's */
  yourItems: number;
};

type AgentRow = { id: string; currentWork: readonly unknown[] };

/**
 * Count agents from the normalized executors rows (one row per agent key,
 * components/agents/executor-normalize.ts). Unit: agents; items are work
 * items. The no-agent row and your own row are not agents.
 */
export function countAgents(rows: readonly AgentRow[]): AgentCount {
  const out: AgentCount = { withWork: 0, idle: 0, heldItems: 0, noAgentItems: 0, yourItems: 0 };
  for (const row of rows) {
    const items = row.currentWork.length;
    if (row.id === UNASSIGNED_AGENT_ID) {
      out.noAgentItems += items;
      continue;
    }
    if (row.id === OPERATOR_AGENT_KEY) {
      out.yourItems += items;
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
  const yours = a.yourItems ? ` ${unit(a.yourItems, "more work item")} ${a.yourItems === 1 ? "is" : "are"} held by you.` : "";
  const idle = a.idle ? ` ${unit(a.idle, "agent")} ${a.idle === 1 ? "has" : "have"} nothing to do.` : "";
  return held + none + yours + idle;
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
    /** each item has its own action and the group has none: decided item by item */
    itemByItem: number;
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
  const found = { decidable: 0, noButton: 0, incomplete: 0, itemByItem: 0, held: 0, other: 0 };
  for (const group of groups) {
    items += group.count;
    if (group.face.state === "ready") readyNow += 1;
    const kind = group.standing?.kind ?? (group.face.state === "ready" ? "ready" : "other");
    if (kind === "ready") found.decidable += 1;
    else if (kind === "no_button") found.noButton += 1;
    else if (kind === "incomplete") found.incomplete += 1;
    else if (kind === "item_by_item") found.itemByItem += 1;
    else if (kind === "held") found.held += 1;
    else found.other += 1;
    if (group.standing && !group.standing.live) checkedEarlier += 1;
  }
  return { decisions: groups.length, items, readyNow, notYet: groups.length - readyNow, found, checkedEarlier };
}

/* ---------- waiting for you, in words ---------- */

export type WaitingWords = {
  /** "3 decisions wait for you" / "Nothing waits for your answer" */
  headline: string;
  /** quiet lines, each one plain sentence; "" when there is nothing to say */
  wording: string;
  housekeeping: string;
  closedWithoutYou: string;
  waitingOnOthers: string;
};

/**
 * The waiting count in plain sentences, the same on Today, Decide and the
 * sidebar's tooltip. Only `groups` is the number; every other line names what
 * it counts and says why it is not his. A line whose number the projection
 * did not give is "" (never "0 closed").
 */
export function describeWaiting(w: WaitingOnYou): WaitingWords {
  const be = (n: number) => (n === 1 ? "is" : "are");
  const headline = w.groups > 0
    ? `${unit(w.groups, "decision")} ${w.groups === 1 ? "waits" : "wait"} for you`
    : "Nothing waits for your answer";
  const wording = w.wording > 0
    ? `${unit(w.wording, "more decision")} ${be(w.wording)} yours once an agent words ${w.wording === 1 ? "it" : "them"} as a clear question.`
    : "";
  const housekeeping = w.housekeeping.groups > 0
    ? `${unit(w.housekeeping.groups, "proposal")} to update agent skills ${be(w.housekeeping.groups)} agent housekeeping, not decisions for you, so ${w.housekeeping.groups === 1 ? "it is" : "they are"} not counted.`
    : "";
  const closedWithoutYou = w.closedWithoutYou && w.closedWithoutYou > 0
    ? `${unit(w.closedWithoutYou, "record")} closed without you: nothing is left to decide.`
    : "";
  const waitingOnOthers = w.waitingOnOthers && w.waitingOnOthers.items > 0
    ? `${unit(w.waitingOnOthers.items, "work item")} ${w.waitingOnOthers.items === 1 ? "waits" : "wait"} on something outside you, not on you.`
    : "";
  return { headline, wording, housekeeping, closedWithoutYou, waitingOnOthers };
}

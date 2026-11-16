import {
  countAgents,
  countInProgress,
  countOnHold,
  type AgentCount,
  type InProgressCount,
} from "@/lib/counts";
import { plainAge } from "@/components/kit/ReadState";
import { NO_AGENT_KEY, OPERATOR_AGENT_KEY } from "@/lib/harness-label";
import { workItemHref, workListHref } from "@/lib/links";
import { plainState, plainWorkStep } from "@/lib/plain-words";
import { isInProgress, isOnHold, isStale, timestampMs } from "@/lib/work-state";
import { workTitle } from "@/lib/work-title";

import type { ExecutorRow, ExecutorWorkRef } from "./executor-model";

/**
 * Who holds what, one row per agent key, for the pages under Agents (Working
 * now, All agents, Waiting on each other). Every number comes from
 * lib/counts.ts over the normalized executors rows, which Today counts too,
 * and every number carries the Work list link that shows exactly those items
 * (`owner=<agent key>` with the matching filter). Server-side: counts.ts
 * re-exports the attention reader, so client components get the built rows.
 */

export type RosterWork = {
  workId: string;
  title: string;
  /** the state in everyday words */
  stateLabel: string;
  state: string;
  /** the next step in plain words, "" when none */
  next: string;
  /** when it was picked up (epoch ms), null when unknown */
  pickedUpMs: number | null;
  /** "3 days ago", "" when unknown */
  pickedUpAgo: string;
  stale: boolean;
  onHold: boolean;
  href: string;
};

export type RosterAgent = {
  /** the agent key (lib/harness-label.ts): what the Work filter compares */
  key: string;
  name: string;
  /** an agent, you, or the work nobody holds */
  kind: "agent" | "you" | "none";
  /** the raw names its work carries, for small secondary text */
  spellings: string[];
  items: number;
  inProgress: InProgressCount;
  onHold: number;
  /** in progress and picked up more than a week ago; null when no pick-up time is known */
  stale: number | null;
  /** the earliest pick-up among its in-progress items, null when unknown */
  oldestPickUpMs: number | null;
  /** the same as words ("73 days ago"), "" when unknown */
  oldestPickUpAgo: string;
  /** its most urgent items: on hold, then not moved longest, then running */
  top: RosterWork[];
  href: { all: string; progress: string; stale: string; onHold: string };
};

export type Roster = {
  /** agents, you and "no agent", each holding work, most held first */
  holders: RosterAgent[];
  /** agents set up but holding nothing */
  idle: { key: string; name: string }[];
  count: AgentCount;
  inProgress: InProgressCount;
  onHold: number;
};

function rowsFor(items: readonly ExecutorWorkRef[]) {
  return items.map((work) => ({ work_id: work.workId, state: work.state, claimed_at: work.claimedAt }));
}

function rank(work: RosterWork): number {
  if (work.onHold) return 0;
  if (work.stale) return 1;
  if (work.state === "running" || work.state === "in_progress") return 2;
  if (work.state === "claimed") return 3;
  if (work.state === "verifying") return 4;
  return 5;
}

function rosterWork(work: ExecutorWorkRef, nowMs: number): RosterWork {
  const pickedUpMs = timestampMs(work.claimedAt);
  return {
    workId: work.workId,
    title: workTitle({ title: work.title, work_id: work.workId }),
    stateLabel: plainState(work.state),
    state: work.state,
    next: work.nextAction ? plainWorkStep(work.nextAction) : "",
    pickedUpMs,
    pickedUpAgo: pickedUpMs !== null ? plainAge(Math.max(0, nowMs - pickedUpMs)) : "",
    stale: isStale({ state: work.state, claimed_at: work.claimedAt }, nowMs),
    onHold: isOnHold(work.state),
    href: workItemHref(work.workId),
  };
}

/** Build the roster from normalized executors rows. `nowMs` is the clock for "not moved in a week"; `top` caps each agent's listed items. */
export function buildRoster(rows: readonly ExecutorRow[], nowMs: number, top = 6): Roster {
  const holders: RosterAgent[] = [];
  const idle: Roster["idle"] = [];
  for (const row of rows) {
    if (!row.currentWork.length) {
      if (row.id !== NO_AGENT_KEY && row.id !== OPERATOR_AGENT_KEY) idle.push({ key: row.id, name: row.label });
      continue;
    }
    const work = row.currentWork.map((ref) => rosterWork(ref, nowMs));
    const sorted = work.slice().sort((a, b) => rank(a) - rank(b) || (a.pickedUpMs ?? Infinity) - (b.pickedUpMs ?? Infinity));
    const inProgress = countInProgress(rowsFor(row.currentWork), nowMs);
    const pickUps = work.filter((item) => isInProgress(item.state) && item.pickedUpMs !== null).map((item) => item.pickedUpMs as number);
    const spellings = [...new Set(row.currentWork.map((ref) => ref.claimedBy || ref.owner).filter(Boolean))].sort();
    holders.push({
      key: row.id,
      name: row.label,
      kind: row.id === NO_AGENT_KEY ? "none" : row.id === OPERATOR_AGENT_KEY ? "you" : "agent",
      spellings,
      items: row.currentWork.length,
      inProgress,
      onHold: countOnHold(rowsFor(row.currentWork)),
      stale: inProgress.pickedUpOver7Days,
      oldestPickUpMs: pickUps.length ? Math.min(...pickUps) : null,
      oldestPickUpAgo: pickUps.length ? plainAge(Math.max(0, nowMs - Math.min(...pickUps))) : "",
      top: sorted.slice(0, top),
      href: {
        all: workListHref({ filter: "all", owner: row.id }),
        progress: workListHref({ filter: "progress", owner: row.id }),
        stale: workListHref({ filter: "stale", owner: row.id }),
        onHold: workListHref({ filter: "blocked", owner: row.id }),
      },
    });
  }
  const all = rows.flatMap((row) => row.currentWork);
  return {
    holders,
    idle,
    count: countAgents(rows),
    inProgress: countInProgress(rowsFor(all), nowMs),
    onHold: countOnHold(rowsFor(all)),
  };
}

export type LatestWork = RosterWork & { agentName: string; agentKey: string };

/**
 * The most recently picked-up open items across every agent, newest first:
 * the only real time the projections carry for open work (`claimed_at`).
 * Items with no pick-up time come last. `total` is every open item held.
 */
export function latestPickedUp(rows: readonly ExecutorRow[], nowMs: number, limit: number): { rows: LatestWork[]; total: number } {
  const all = rows.flatMap((row) => row.currentWork.map((ref) => ({ ...rosterWork(ref, nowMs), agentName: row.label, agentKey: row.id })));
  all.sort((a, b) => (b.pickedUpMs ?? -Infinity) - (a.pickedUpMs ?? -Infinity));
  return { rows: all.slice(0, limit), total: all.length };
}

/** The open items picked up longest ago and still in progress past the stale threshold, oldest first. */
export function oldestNotMoved(rows: readonly ExecutorRow[], nowMs: number, limit: number): LatestWork[] {
  return rows
    .flatMap((row) => row.currentWork.map((ref) => ({ ...rosterWork(ref, nowMs), agentName: row.label, agentKey: row.id })))
    .filter((work) => work.stale)
    .sort((a, b) => (a.pickedUpMs ?? Infinity) - (b.pickedUpMs ?? Infinity))
    .slice(0, limit);
}

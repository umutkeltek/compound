import { laneGated, type CoordinationActor, type CoordinationView } from "@/lib/coordination-model";
import { readableDate } from "@/components/settings/grants";
import { agentDisplayName, agentKey } from "@/lib/harness-label";
import { workItemHref, workListHref } from "@/lib/links";
import { plainState } from "@/lib/plain-words";
import { workTitle } from "@/lib/work-title";

/**
 * "Who waits on what", reduced to what a person can read: how many agent
 * sessions wait, the one thing most of them wait for in plain words, and the
 * sessions that hold work, each linked to it. `compound coordination --json`
 * lists every session it has seen; most hold nothing and wait on the same
 * gate, so they are counted, not drawn. Pure and client-safe.
 */

export type WaitReason = { key: string; words: string };

const WAIT_WORDS: Readonly<Record<string, string>> = {
  capacity_confirmation_required: "a capacity check on its model that asks for a confirmation nobody gives yet",
  no_safe_continue_step: "no next step it may take on its own",
  proof_ready_close: "a proof check before its work can close",
};

function humanize(reason: string): string {
  const words = reason.replace(/[_-]+/g, " ").replace(/\s+/g, " ").trim();
  return words ? words[0].toLowerCase() + words.slice(1) : "no next step it may take on its own";
}

/** What one session waits for, in words; a session that can go on does not wait. */
export function waitReasonOf(actor: CoordinationActor): WaitReason | null {
  if (actor.canContinue) return null;
  const lane = actor.lane;
  if (lane?.refused) return { key: "lane-refused", words: "its model refusing new work" };
  if (lane?.confirmationRequired) return { key: "capacity_confirmation_required", words: WAIT_WORDS.capacity_confirmation_required };
  if (lane && laneGated(lane)) return { key: `lane-${lane.state}`, words: `its model being ${lane.state === "walled" ? "at its limit" : lane.state === "dead" ? "unavailable" : lane.state}` };
  const raw = actor.waitReason.trim();
  const key = raw.toLowerCase().replace(/[\s-]+/g, "_") || "no_safe_continue_step";
  return { key, words: WAIT_WORDS[key] ?? humanize(raw) };
}

export type WaitingHolder = {
  /** the session, raw, for small secondary text */
  session: string;
  /** the agent's one name */
  name: string;
  workId: string;
  title: string;
  stateLabel: string;
  href: string;
  /** what it waits for, or "" when it can go on */
  waits: string;
};

export type WaitingAgent = {
  key: string;
  name: string;
  /** its Work list (`owner=<key>`) */
  href: string;
  /** its sessions that hold work */
  holders: WaitingHolder[];
};

export type WaitingSummary = {
  sessions: number;
  waiting: number;
  /** what the sessions wait for, most common first */
  reasons: { key: string; words: string; count: number }[];
  /** sessions that hold work, each with its work item */
  holders: WaitingHolder[];
  /** the same sessions grouped by agent, most sessions first */
  agents: WaitingAgent[];
  /** "223 agent sessions are waiting. 220 of them wait for the same thing: …" */
  sentence: string;
};

export function summarizeWaiting(view: CoordinationView): WaitingSummary {
  const actors = [view.me, ...view.others].filter((actor) => actor.actor);
  const seen = new Set<string>();
  const unique = actors.filter((actor) => (seen.has(actor.actor) ? false : (seen.add(actor.actor), true)));
  const counts = new Map<string, { key: string; words: string; count: number }>();
  let waiting = 0;
  for (const actor of unique) {
    const reason = waitReasonOf(actor);
    if (!reason) continue;
    waiting += 1;
    const entry = counts.get(reason.key) ?? { ...reason, count: 0 };
    entry.count += 1;
    counts.set(reason.key, entry);
  }
  const reasons = [...counts.values()].sort((a, b) => b.count - a.count || a.words.localeCompare(b.words));
  const holders = unique
    .filter((actor) => actor.doing && actor.doing.workId)
    .map((actor) => ({
      session: actor.actor,
      name: agentDisplayName(actor.actor),
      workId: actor.doing!.workId,
      title: workTitle({ title: actor.doing!.title, work_id: actor.doing!.workId }),
      stateLabel: plainState(actor.doing!.state),
      href: workItemHref(actor.doing!.workId),
      waits: waitReasonOf(actor)?.words ?? "",
    }))
    .sort((a, b) => a.name.localeCompare(b.name) || a.title.localeCompare(b.title));
  const byAgent = new Map<string, WaitingAgent>();
  for (const holder of holders) {
    const key = agentKey(holder.session);
    const entry = byAgent.get(key) ?? { key, name: holder.name, href: workListHref({ filter: "all", owner: key }), holders: [] };
    entry.holders.push(holder);
    byAgent.set(key, entry);
  }
  const agents = [...byAgent.values()].sort((a, b) => b.holders.length - a.holders.length || a.name.localeCompare(b.name));
  // Since when: a capacity gate records the time of the last capacity reading it saw, when it has one.
  const readings = unique
    .filter((actor) => waitReasonOf(actor)?.key === "capacity_confirmation_required")
    .map((actor) => /data_row_timestamp=(\S+)/.exec(actor.lane?.message ?? "")?.[1] ?? "")
    .map((value) => (value && value !== "unknown" ? Date.parse(value) : NaN));
  const dated = readings.filter((ms) => Number.isFinite(ms));
  const sessions = unique.length;
  const n = (value: number, one: string, many: string) => `${value.toLocaleString("en-US")} ${value === 1 ? one : many}`;
  let sentence: string;
  if (sessions === 0) sentence = "No agent session is recorded.";
  else if (waiting === 0) sentence = `None of the ${n(sessions, "agent session", "agent sessions")} is waiting.`;
  else {
    sentence = `${n(waiting, "agent session is", "agent sessions are")} waiting${waiting < sessions ? ` (of ${sessions.toLocaleString("en-US")})` : ""}.`;
    const top = reasons[0];
    if (top && top.count > 1) sentence += ` ${top.count === waiting ? "All of them" : `${top.count.toLocaleString("en-US")} of them`} wait for the same thing: ${top.words}.`;
    else if (top) sentence += ` It waits for ${top.words}.`;
    if (readings.length) {
      sentence += dated.length
        ? ` Their last capacity readings date from ${readableDate(Math.min(...dated))}${readableDate(Math.min(...dated)) === readableDate(Math.max(...dated)) ? "" : ` to ${readableDate(Math.max(...dated))}`}${readings.length > dated.length ? `; ${(readings.length - dated.length).toLocaleString("en-US")} ${readings.length - dated.length === 1 ? "has" : "have"} none at all` : ""}.`
        : " None of them has any capacity reading, so the wait has no start time.";
    }
  }
  return { sessions, waiting, reasons, holders, agents, sentence };
}

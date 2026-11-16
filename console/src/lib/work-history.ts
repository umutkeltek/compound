/**
 * What happened to one work item, and when: its dates (created, picked up,
 * last changed) and its history, read from the `lifecycle` list that
 * `compound work show <id> --json` returns (`ts` in nanoseconds as text,
 * `from`, `to`, optional `by`, `note`). The item page and the preview drawer
 * both use these, so they cannot tell two different stories.
 *
 * Client-safe and pure: no reads; pass the clock in.
 */

import { agentDisplayName } from "@/lib/harness-label";
import { plainState } from "@/lib/plain-words";
import { isInProgress, isStale, timestampMs } from "@/lib/work-state";

type Row = { readonly [field: string]: unknown };

const DAY_MS = 86_400_000;

function text(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function steps(work: Row): Row[] {
  return Array.isArray(work.lifecycle) ? (work.lifecycle.filter((step) => step && typeof step === "object") as Row[]) : [];
}

/* ---------- dates ---------- */

export type WorkDates = {
  /** created, epoch ms; null when not recorded */
  createdMs: number | null;
  /** picked up by an agent, epoch ms; null when never picked up or not recorded */
  pickedUpMs: number | null;
  /** the latest recorded change, epoch ms; null when the record has no history */
  changedMs: number | null;
  /** in progress now (lib/work-state.ts) */
  inProgress: boolean;
  /** in progress and picked up more than STALE_AFTER_DAYS ago (lib/work-state.ts `isStale`) */
  stale: boolean;
};

/**
 * An item's dates. The pick-up time is the workbench row's `claimed_at` when
 * given (the same field every stale count uses), else the latest recorded
 * step into `claimed`, else the first step into `running`.
 */
export function workDates(work: Row, opts: { nowMs: number; claimedAt?: unknown }): WorkDates {
  const history = steps(work);
  const at = (step: Row | undefined) => (step ? timestampMs(step.ts) : null);
  const times = history.map((step) => timestampMs(step.ts)).filter((ms): ms is number => ms !== null);
  const pickedUpMs =
    timestampMs(opts.claimedAt) ??
    timestampMs(work.claimed_at) ??
    at([...history].reverse().find((step) => text(step.to) === "claimed")) ??
    at(history.find((step) => ["running", "in_progress"].includes(text(step.to))));
  const state = text(work.state);
  return {
    createdMs: timestampMs(work.created_at) ?? (times.length ? Math.min(...times) : null),
    pickedUpMs,
    changedMs: times.length ? Math.max(...times) : null,
    inProgress: isInProgress(state),
    stale: isStale({ state, claimed_at: pickedUpMs === null ? undefined : String(Math.round(pickedUpMs)) }, opts.nowMs),
  };
}

/** A day in plain words, the same everywhere: "12 Nov 2026" (UTC). */
export function plainDay(ms: number): string {
  return new Date(ms).toLocaleDateString("en-GB", { day: "numeric", month: "short", year: "numeric", timeZone: "UTC" });
}

/** How long ago, in whole days: "today", "1 day ago", "40 days ago". */
export function daysAgo(ms: number, nowMs: number): string {
  const days = Math.floor((nowMs - ms) / DAY_MS);
  if (days <= 0) return "today";
  return days === 1 ? "1 day ago" : `${days.toLocaleString("en-US")} days ago`;
}

/* ---------- history ---------- */

export type WorkHistoryEntry = {
  /** epoch ms; null when the step has no readable time */
  atMs: number | null;
  /** what changed, in plain words: "Created as Ready to start", "Running → Waiting for a proof check" */
  what: string;
  /** who made the change, by the agent's one name; "" when not recorded */
  who: string;
  /** the note recorded with the step, without policy suffixes; "" when there is none worth showing */
  note: string;
};

/** A step's note without the tool's bookkeeping: "(policy …)" suffixes, bare "created" or "claimed". */
export function plainHistoryNote(note: unknown): string {
  const value = text(note).replace(/\s*\(policy [^)]*\)\s*$/i, "").trim();
  if (!value || /^(created|claimed)$/i.test(value)) return "";
  const recorded = /^action recorded:\s*(.+)$/i.exec(value);
  if (recorded) return `Recorded the step "${recorded[1].replace(/[_-]+/g, " ").trim()}".`;
  return value;
}

/** The item's history, newest first, in plain words. A record with no `lifecycle` has none (an empty list). */
export function workHistory(work: Row): WorkHistoryEntry[] {
  const entries = steps(work).map((step, index) => {
    const from = text(step.from);
    const to = text(step.to);
    const what = !from
      ? `Created as ${plainState(to || "unknown")}`
      : from === to
        ? `Updated while ${plainState(to).toLowerCase()}`
        : `${plainState(from)} → ${plainState(to || "unknown")}`;
    return {
      index,
      atMs: timestampMs(step.ts),
      what,
      who: text(step.by) ? agentDisplayName(text(step.by)) : "",
      note: plainHistoryNote(step.note),
    };
  });
  entries.sort((a, b) => (b.atMs ?? Number.NEGATIVE_INFINITY) - (a.atMs ?? Number.NEGATIVE_INFINITY) || b.index - a.index);
  return entries.map(({ index: _index, ...entry }) => entry);
}

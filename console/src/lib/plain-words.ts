/**
 * Plain words for the four main pages (Today, Work, Decide, Live). One thing
 * has one name on every page: a person who has never seen this system should
 * understand each label without a glossary. System words (state ids, area
 * ids, owner wording) are turned into everyday words here, and only here.
 * Ids stay available as small secondary text; they are never the main text.
 *
 * Client-safe: no reads, no `node:` imports.
 */

import { blockedReasonPlain } from "@/lib/glossary";

/* ---------- names ---------- */

/**
 * The named things on the four pages, with the one-line definition a tooltip
 * or a "what does this mean" line shows. Pages use these names verbatim.
 */
export const TERMS = {
  decision: {
    name: "decision",
    plural: "decisions",
    means: "Something only you can answer. One decision can hold several items of the same kind.",
  },
  item: {
    name: "item",
    plural: "items",
    means: "One record inside a decision.",
  },
  workItem: {
    name: "work item",
    plural: "work items",
    means: "One piece of work with a goal and a proof of done.",
  },
  agent: {
    name: "agent",
    plural: "agents",
    means: "A tool that does the work: Codex, Claude Code, scripts or an API lane.",
  },
  inProgress: {
    name: "in progress",
    plural: "in progress",
    means: "An agent picked the work item up and it is not finished: running, picked up but not started, or waiting for a proof check.",
  },
  onHold: {
    name: "on hold",
    plural: "on hold",
    means: "Stopped until you decide what happens next.",
  },
  checking: {
    name: "waiting for a proof check",
    plural: "waiting for a proof check",
    means: "The agent says it is done; the system has not accepted the proof yet.",
  },
  pickedUp: {
    name: "picked up, not started",
    plural: "picked up, not started",
    means: "An agent took the work item but has not started running it.",
  },
  filed: {
    name: "filed",
    plural: "filed",
    means: "Already answered and stored. Nothing to do.",
  },
  watching: {
    name: "watching",
    plural: "watching",
    means: "Noticed, but it does not need you yet.",
  },
} as const;

/* ---------- work states ---------- */

const STATE_LABEL: Record<string, string> = {
  proposed: "Suggested",
  suggested: "Suggested",
  candidate: "Suggested",
  admitted: "Accepted",
  ready: "Ready to start",
  claimed: "Picked up, not started",
  in_progress: "Running",
  running: "Running",
  verifying: "Waiting for a proof check",
  blocked: "Blocked",
  held: "On hold",
  needs_review: "Needs your review",
  completed: "Done",
  failed: "Failed",
  cancelled: "Cancelled",
};

/** A work state id in everyday words ("verifying" → "Waiting for a proof check"). */
export function plainState(state: string): string {
  return STATE_LABEL[state] ?? sentence(state.replace(/[_-]+/g, " "));
}

/* ---------- areas (what a decision is about) ---------- */

const AREA_LABEL: Record<string, string> = {
  "workbench-blocked-on-you": "Work on hold",
  "workbench-needs-claim": "Work nobody picked up",
  "decision-candidates": "Proposed rules",
  "product-hypotheses": "Product ideas",
  "product-reviews": "Product results",
  "product-content-queue": "Product content",
  "product-action-cards": "Product next steps",
  "launchd-repair": "Background service repair",
  "git-index-lock": "Stuck git lock files",
  "source-control-cleanup": "Repository cleanup",
  "harness-edit-proposals": "Agent setup changes",
  "route-promotions": "Model upgrades",
  "route-comparisons": "Model comparisons",
  "route-remorse": "Model choices to undo",
  "worktree-retirement": "Old worktrees to remove",
  "distiller-eval": "Extraction checks",
  "distiller-improvements": "Extraction improvements",
  "distiller-requests": "Extraction requests",
  bench: "Benchmark results",
  parity: "Parity checks",
  "job-health": "Background job health",
  "autonomy-grant-mint": "Permission to run on its own",
  "panel-verdicts": "Judge panel results",
  "synthesis-contradictions": "Facts that disagree",
  "synthesis-sweeps": "Memory sweep reports",
  "seed-corroborations": "Old notes that proved right",
  "memory-adjudicate": "Memory to confirm",
  "capability-lifecycle-proposals": "Skill changes",
  "capability-evaluation-intake": "Tools to evaluate",
  "external-repo-intake": "Shared repositories",
  "observation-candidates": "New observations",
  "source-observation-verdicts": "Source checks",
  source: "Sources",
};

/**
 * What a decision is about, in everyday words. Area ids can be folder paths
 * ("source-control-cleanup/scr_4b6d91fd142c.preimage"): only the first part
 * names the kind; the rest is an id and is never used as a label.
 */
export function plainArea(area: string): string {
  const head = (area || "").split("/")[0].trim();
  if (!head) return "Other";
  return AREA_LABEL[head] ?? sentence(head.replace(/[_-]+/g, " "));
}

/** The id part of an area path, for small secondary text ("" when none). */
export function areaDetail(area: string): string {
  const at = (area || "").indexOf("/");
  return at >= 0 ? area.slice(at + 1) : "";
}

/* ---------- reasons and next steps ---------- */

const REASON_PLAIN: Array<[RegExp, string]> = [
  [/owner has not exposed a safe action/i, "There is no button for this kind of decision yet. Open it to read the items, or set it aside."],
  [/actions are item-specific/i, "Each item needs its own answer. Open it and decide one at a time."],
  [/authority (?:bundle|stamp)|No complete current owner authority|owner authority bundle is not current/i, "The approval record behind it is incomplete, so it cannot be answered with a button yet."],
  [/resolve (?:the )?policy\/source-control hold/i, "Each held work item needs you to let it continue, start it again, or cancel it."],
  [/Owner authority evidence is (?:stale|unreachable|loading)|live status feed is/i, "Buttons pause while the list is read again. They come back when the new copy lands."],
  [/Current owner authority evidence is not live/i, "Buttons pause while the list is read again. They come back when the new copy lands."],
  [/no executable adapter|no effect-compatible workflow|no one-click action built/i, "There is no button for this kind of decision yet. Open it to read the items, or set it aside."],
  [/requires its own reviewed owner artifact/i, "Each repository needs its own review. Open it and decide one at a time."],
];

/**
 * The system's own reason a decision cannot be answered now, in plain words.
 * Unknown reasons pass through the existing glossary (lib/glossary.ts
 * `blockedReasonPlain`) and are otherwise shown as written: the reason is the
 * owner's, never invented here.
 */
export function plainReason(reason: string): string {
  const trimmed = (reason || "").trim();
  if (!trimmed) return "";
  for (const [pattern, plain] of REASON_PLAIN) if (pattern.test(trimmed)) return plain;
  return sentence(blockedReasonPlain(trimmed));
}

/**
 * What answering a decision unlocks ("why it matters"), in plain words. An
 * unlock that only points at a command or "the review queue" says nothing
 * about why, so it becomes "" (the row's own button already opens it).
 */
export function plainWhy(unlock: string): string {
  const trimmed = (unlock || "").trim();
  if (!trimmed) return "";
  if (/\bcompound\s+[a-z]/i.test(trimmed) || /--[a-z]/.test(trimmed)) return "";
  if (/^open (?:it|the review queue)\b/i.test(trimmed)) return "";
  return sentence(plainText(trimmed).replace(/\.+$/, "")) + ".";
}

const JARGON: Array<[RegExp, string]> = [
  [/\boperator review\b/g, "your review"],
  [/\bthe operator\b/g, "you"],
  [/\bexecutors\b/g, "agents"],
  [/\bExecutors\b/g, "Agents"],
  [/\bexecutor\b/g, "agent"],
  [/\bExecutor\b/g, "Agent"],
];

/**
 * A sentence the system wrote (a decision summary or unlock), with its few
 * internal words swapped for everyday ones ("executors" → "agents",
 * "operator review" → "your review"). Meaning is never changed.
 */
export function plainText(value: string): string {
  let out = (value || "").replace(/\s+/g, " ").trim();
  for (const [pattern, plain] of JARGON) out = out.replace(pattern, plain);
  return out;
}

/** Capitalise the first letter; leave the rest as written. */
export function sentence(value: string): string {
  const v = (value || "").trim();
  return v ? v[0].toUpperCase() + v.slice(1) : v;
}

/* ---------- banned words ---------- */

/**
 * System words that must never appear in the text a person reads on Today,
 * Work, Decide or Live (checked against rendered pages by
 * scripts/plain-pages-cases.mjs, run by the wired work-legibility smoke).
 */
export const BANNED_PHRASES: readonly string[] = [
  "projected lanes",
  "heartbeat-backed",
  "paused at a seam",
  "Harness workfield",
  "active WorkEnvelope",
  "next seam",
  "Operations evidence",
  "execution detail unavailable",
  "Clean seam",
  "Propose-only holds the line",
  "Highest leverage seam",
  "leverage seam",
  "operator seam",
  "blast radius",
  "World Δ",
  "Mind Δ",
  "Current owner projection",
  "claimed by this face",
  "Manual owner command",
  "Projected unlock",
  "No spend projection wired",
];

/** Area ids that are folder paths with an id tail, used as a label. */
const RAW_PATH = /\b[a-z]+(?:-[a-z]+)+\/(?:scr_[0-9a-f]+|[\w-]+\.preimage)\b/;

/** Every banned phrase (and raw area path) found in `text`, case-insensitive. */
export function findBannedPhrases(text: string): string[] {
  const lower = text.toLowerCase();
  const hits = BANNED_PHRASES.filter((phrase) => lower.includes(phrase.toLowerCase()));
  const raw = text.match(RAW_PATH);
  if (raw) hits.push(`raw path label: ${raw[0]}`);
  return hits;
}

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
import { agentKey } from "@/lib/harness-label";

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
    means: "Waiting for a dependency or decision before work can continue.",
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

/**
 * How a decision moves today, said once per page (Today and Decide), never on
 * every row. The browser cannot record his answer yet, and no control may
 * pretend it can: no button, no "connect" step, no command for him to run.
 */
export const HOW_DECISIONS_MOVE =
  "To decide, tell an agent in chat. The agent records your answer and carries it out. Deciding on this page is being built.";

/* ---------- areas (what a decision is about) ---------- */

/**
 * The waiting list's own kinds that are not review-queue areas: each source
 * that lists his items one by one (`compound attention --json` groups with
 * `items[]`). Kept apart so the review areas below stay one list.
 */
const WAITING_LIST_AREA_LABEL: Record<string, string> = {
  "memory-proposals": "Proposed memories",
  "product-results-due": "Did a change work?",
  "hook-approval-requests": "Agent actions held for you",
  "data-source-setup": "Data source setup",
};

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
  return AREA_LABEL[head] ?? WAITING_LIST_AREA_LABEL[head] ?? sentence(head.replace(/[_-]+/g, " "));
}

/** The id part of an area path, for small secondary text ("" when none). */
export function areaDetail(area: string): string {
  const at = (area || "").indexOf("/");
  return at >= 0 ? area.slice(at + 1) : "";
}

const REACH: Record<string, string> = {
  read: "Only reads; changes nothing",
  local_reversible: "This machine only",
  external_side_effect: "Something outside this machine",
  public: "Something the public can see",
  destructive: "Deletes or destroys something",
  financial: "Money",
  security: "Security settings or secrets",
};

/** What a decision can touch if acted on (the system's blast-radius id), in plain words. */
export function plainReach(blastRadius: string): string {
  return REACH[blastRadius] ?? (blastRadius ? sentence(blastRadius.replace(/_/g, " ")) : "Not stated");
}

/* ---------- reasons and next steps ---------- */

const REASON_PLAIN: Array<[RegExp, string]> = [
  [/owner has not exposed a safe action/i, "There is no button for this kind of decision yet. Open it to read the items, or set it aside."],
  [/actions are item-specific/i, "Each item needs its own answer. Open it and decide one at a time."],
  [/authority (?:bundle|stamp)|No complete current owner authority|owner authority bundle is not current/i, "The approval record behind it is incomplete, so it cannot be answered with a button yet."],
  [/resolve (?:the )?policy\/source-control hold/i, "Each held work item needs you to let it continue, start it again, or cancel it."],
  // The list's copy is older than the 25-second live window. That is a fact
  // about the copy's age, not a pause: opening the decision checks it again.
  [/Owner authority evidence is stale|Current owner authority evidence is not live/i, "This was last checked more than 25 seconds ago. Opening it checks it again in a few seconds."],
  [/Owner authority evidence is (?:unreachable|loading)|live status feed is/i, "The list could not be read yet, so nothing can be decided from it."],
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

const DECISION_STATE_PLAIN: Record<string, string> = {
  "Owner evidence not live": "Checked more than 25 seconds ago",
  "Projection detail not aligned": "Details still loading",
  "Authority unavailable": "No approval record",
  "Action adapter unavailable": "Needs follow-up",
  "Needs context": "Read it first",
};

/** A Decide card's state label in plain words ("Authority unavailable" → "No approval record"). */
export function plainDecisionState(label: string): string {
  return DECISION_STATE_PLAIN[label] ?? label;
}

const NEXT_STEP_PLAIN: Array<[RegExp, string]> = [
  [/^Refresh current owner evidence before acting\.?$/, "It is checked again while it is open; that takes a few seconds and the page updates by itself."],
  [/^Wait for (?:the )?owner projection to emit (?:a )?complete current authority bundle\.?$/, "An agent needs to refresh the approval record before applying this."],
  [/^Use the manual owner command below, or wait for (?:the )?owner to expose (?:a )?governed workflow\.?$/, "Continue with an agent to check the blocker and prepare the next step."],
  [/^Use (?:the )?owner surface (?:after it |that )?exposes an authority-bound adapter\.?$/, "Continue with the agent responsible for this review."],
  [/^Park or dismiss this review with a reason; (?:the )?underlying authority stays unchanged\.?$/, "Set it aside or dismiss it, with a reason; nothing else changes."],
  [/^Refresh until Attention membership and Reviews claim detail align\.?$/, "The details are still loading; look again in a moment."],
];

/** A Decide card's "what unblocks it" line in plain words; unknown text passes through. */
export function plainNextStep(text: string): string {
  const trimmed = (text || "").trim();
  for (const [pattern, plain] of NEXT_STEP_PLAIN) if (pattern.test(trimmed)) return plain;
  return trimmed;
}

/* ---------- where a decision stands (Decide rows, banner, answer) ---------- */

/**
 * What the evidence about one decision says, judged by the same rules the
 * Decide gate uses (lib/decide-model.ts `standing`). `live` is the gate's own
 * test: only live evidence can show a button. Client-safe: the page computes
 * the standing and its age on the server and sends them as data.
 */
export type DecisionStanding = {
  kind: "ready" | "no_button" | "incomplete" | "item_by_item" | "held" | "other";
  /** the tool's own reason, as written (shown through `plainToolReason`) */
  reason: string;
  /** the evidence is inside the live window, so the gate may show a button */
  live: boolean;
  /** this decision's own read is running now and no live copy is here yet */
  checking: boolean;
  /** "3 min ago": when that evidence was read; "" when unknown */
  checkedAgo: string;
};

const TOOL_REASON_PLAIN: Array<[RegExp, string]> = [
  [/^owner authority bundle is incomplete or invalid/i, "parts of it are missing or do not match. Read the item before acting."],
  [/^owner-work authority stamp blocked/i, "the tool that raised it has not recorded what the action would do and who may approve it."],
  [/^actions are item-specific/i, "each item has its own action; there is no single action for all of them."],
  [/^owner has not exposed a safe action/i, "the tool offers no safe action for it."],
  [/^resolve (?:the )?policy\/source-control hold/i, "each held work item needs you to let it continue, start it again, or cancel it."],
  [/^The owner authority bundle is not current/i, "it is out of date."],
  [/^No complete current owner authority bundle/i, "no complete approval record came with it."],
];

/**
 * The tool's reason as the end of a sentence ("The approval record is
 * incomplete: <this>"). An unknown reason is shown as the tool wrote it.
 */
export function plainToolReason(reason: string): string {
  const trimmed = (reason || "").trim();
  if (!trimmed) return "";
  for (const [pattern, plain] of TOOL_REASON_PLAIN) if (pattern.test(trimmed)) return plain;
  return trimmed;
}

/** The standing as one line, plus a tone for its dot. Never says "paused". */
export function standingLine(s: DecisionStanding): { text: string; tone: "good" | "seam" | "muted" } {
  if (s.checking) return { text: "Checking… (takes a few seconds)", tone: "muted" };
  const age = !s.live && s.checkedAgo ? ` · checked ${s.checkedAgo}` : "";
  switch (s.kind) {
    case "ready":
      return s.live
        ? { text: "Ready for review", tone: "good" }
        : { text: `Could be decided at the last check; open it to check again${age}`, tone: "good" };
    case "no_button":
      return { text: `Needs follow-up before it can be applied${age}`, tone: "muted" };
    case "item_by_item":
      // Nothing is incomplete here: the tool offers one action per item and
      // none for the group, so the group is decided one item at a time.
      return { text: `Each item has its own action. Open it to decide them one by one.${age}`, tone: "muted" };
    case "incomplete": {
      const why = plainToolReason(s.reason);
      return { text: `The approval record is incomplete${why ? `: ${why.replace(/\.$/, "")}` : ""}${age}`, tone: "seam" };
    }
    case "held":
      return { text: `The approval rules hold this${s.reason ? `: ${s.reason}` : ""}${age}`, tone: "seam" };
    default:
      return { text: `${plainReason(s.reason) || "It cannot be decided here yet."}${age}`, tone: "muted" };
  }
}

/* ---------- Today: why a decision matters ---------- */

/** The "why it matters" lines `compound attention --json` writes today, in plain words. */
const WHY_PLAIN: Array<[RegExp, string]> = [
  [/^Resolve the hold before executors can continue\.?$/i, "Agents cannot go on until you lift the hold."],
  [/^decision source changes only after reviewed approval\.?$/i, "The rule is only added after it has been reviewed and approved."],
  [/^the product loop gets an evidence-shaped hypothesis\.?$/i, "The product gets an idea to test that rests on evidence."],
  [/^the router surfaces the right capability more often\.?$/i, "Agents find the right tool more often."],
  [/^the router starts trusting auto\/judge with classify extract work\.?$/i, "The model picker starts giving sorting and extraction work to the automatic judge model."],
  [/^the repo stops blocking follow-up work\.?$/i, "The repository stops holding up later work."],
  [/^the hypothesis gets a reviewed product result\.?$/i, "The product idea gets a reviewed result."],
  [/^regenerate dirty-plan from the current read-only source-control snapshot before reviewing\.?$/i, "The cleanup plan must be made again from a fresh look at the repository before it can be reviewed."],
  [/^restore repository visibility, then retry the read-only review projection\.?$/i, "The repository must be readable again; then the review can be read again."],
];

/* ---------- Work: the next step on a card ---------- */

/** The next-step instructions `compound workbench --json` writes today, in plain words. */
const WORK_STEP_PLAIN: Array<[RegExp, string]> = [
  [/^close completed or failed, or return (?:to )?running$/i, "Mark it done or failed, or send it back to running."],
  [/^attach (?:the )?final verified candidate (?:as )?commit:<full-sha>$/i, "Record the final checked commit for it."],
  [/^advance (?:to )?verifying, completed, blocked, held, or failed$/i, "Move it on: to the proof check, done, blocked, on hold or failed."],
  [/^advance (?:to )?running or release to ready$/i, "Start running it, or put it back as ready to start."],
  [/^run a new certifying proof battery in the WorkEnvelope repository$/i, "Run the full proof checks again in its repository."],
  [/^resolve policy\/source-control hold, then ready or cancel$/i, "Lift the hold, then make it ready again or cancel it."],
  [/^admit or cancel after owner review$/i, "Accept it or cancel it after review."],
  [/^close with the attached current Git delivery proof$/i, "Close it with the proof that its change landed."],
  [/^claim before touching$/i, "An agent must pick it up before working on it."],
  [/^replace this incomplete code work with a WorkEnvelope carrying a complete git_delivery contract$/i, "Replace it with a work item that says where its code change will land."],
  [/^resolve blocker, then running, failed, or cancel$/i, "Clear what blocks it, then run it, fail it or cancel it."],
  [/^check out \S+ in the explicit target worktree, then rerun land-check$/i, "Switch its target folder to the right branch, then check the landing again."],
  [/^wait for a green verifier verdict covering commit:/i, "Waiting for the proof check to pass on its final commit."],
  [/^rebase candidate \S+ onto (?:the )?current main tip/i, "Bring its change up to date with main, then check the landing again."],
  [/^repair the bound Git delivery target/i, "Fix where its change should land, then check the landing again."],
  [/^run (?:a )?fresh canonical certifying proof for (?:the )?attached candidate/i, "Run the full proof checks again on its final commit, then check the landing again."],
  [/^repository fingerprint changed/i, "Its repository looks different than before, for example a renamed address. Confirm it before going on."],
  [/^record the current delivery proof/i, "Record the proof that its change landed."],
  [/^rebuild candidate \S+ from expected base/i, "Rebuild its change from the right starting point; this version cannot be accepted."],
  [/^recover candidate tip \S+ into this repository/i, "Bring its final commit back into the repository, then check the landing again."],
  [/^prepare ready with reviewed action context; do not execute action until claimed\/running$/i, "Get it ready with a reviewed plan; nothing runs until an agent picks it up."],
];

/** A work item's next step in plain words; an unknown instruction is shown as written. */
export function plainWorkStep(text: string): string {
  const trimmed = (text || "").trim();
  for (const [pattern, plain] of WORK_STEP_PLAIN) if (pattern.test(trimmed)) return plain;
  return trimmed;
}

/* ---------- Today: names and levels ---------- */

/**
 * A title that starts with a product id ("product:fixture-app has a product
 * hypothesis…") in plain words: the product's name leads, and the id is
 * returned apart for small secondary text. Other titles pass through.
 */
export function productTitle(text: string, nameOf: (productId: string) => string): { text: string; id: string } {
  const match = /^(product:[\w.-]+)\b/.exec(text || "");
  if (!match) return { text: text || "", id: "" };
  return { text: `${nameOf(match[1])}${text.slice(match[1].length)}`, id: match[1] };
}

/**
 * The agent behind an owner or lane id ("claude_code:subtract-lessons"), named
 * the way Live names agents: the executors projection's label for that agent
 * id ("Claude Code"). `names` maps agent id → label. The full id comes back
 * as `lane` for small text. An id no agent matches keeps its own text.
 * Spellings of one agent (`harness:claude-code`, `Claude_Code:lane`) find
 * its label through the one agent key (lib/harness-label.ts `agentKey`).
 */
export function agentNameFor(owner: string, names: ReadonlyMap<string, string>): { name: string; lane: string } {
  const raw = (owner || "").trim();
  if (!raw) return { name: "", lane: "" };
  const head = raw.split(/[:/]/)[0];
  const key = agentKey(raw);
  let name = names.get(raw) ?? names.get(head) ?? "";
  if (!name) for (const [id, label] of names) if (agentKey(id) === key) { name = label; break; }
  return name ? { name, lane: raw === head ? "" : raw } : { name: raw, lane: "" };
}

const AUTONOMY_PLAIN: Record<string, string> = {
  L0: "Agents only suggest; every change waits for you.",
  L1: "Agents suggest changes and ask you before they change files on this machine.",
  L2: "Agents may make local, undoable changes without asking; everything else waits for you.",
  L3: "Agents may make local changes and work inside the projects you have granted without asking; destructive actions still wait for you.",
};

/** What an autonomy level lets agents do without asking, in one sentence ("" when the level is unknown). */
export function plainAutonomy(level: string): string {
  return AUTONOMY_PLAIN[(level || "").trim().toUpperCase()] ?? "";
}

/**
 * What answering a decision unlocks ("why it matters"), in plain words. An
 * unlock that only points at a command or "the review queue" says nothing
 * about why, so it becomes "" (the row's own button already opens it).
 */
export function plainWhy(unlock: string): string {
  const trimmed = (unlock || "").trim();
  if (!trimmed) return "";
  for (const [pattern, plain] of WHY_PLAIN) if (pattern.test(trimmed)) return plain;
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

/* ---------- knowledge: Memory, Rules, Sources ---------- */

/**
 * One name per thing on the Knowledge pages. A proposed rule is called a
 * proposed rule on Memory and on Rules (not "standing law candidate" or
 * "proposed standing rule"); a proposed memory is a note agents found that
 * no agent is told until he accepts it.
 */
export const KNOWLEDGE_TERMS = {
  memory: {
    name: "memory",
    plural: "memories",
    means: "A statement agents can be told when it is relevant.",
  },
  rule: {
    name: "rule",
    plural: "rules",
    means: "A statement every agent is told at the start of each task.",
  },
  proposedMemory: {
    name: "proposed memory",
    plural: "proposed memories",
    means: "A note agents took from a document or past work. No agent is told it until you accept it.",
  },
  proposedRule: {
    name: "proposed rule",
    plural: "proposed rules",
    means: "A decision on record that could become a rule every agent follows. It waits for your choice.",
  },
} as const;

/** Kinds of rule in force (`decisions standing-law` record kinds), in everyday words. */
const RULE_KIND: Record<string, { one: string; many: string }> = {
  ruling: { one: "your ruling", many: "your rulings" },
  grant: { one: "permission", many: "permissions" },
  freeze: { one: "freeze", many: "freezes" },
  policy: { one: "policy", many: "policies" },
  operational: { one: "operating note", many: "operating notes" },
  doctrine: { one: "principle", many: "principles" },
};

export function ruleKindWords(kind: string): { one: string; many: string } {
  const known = RULE_KIND[kind];
  if (known) return known;
  const word = (kind || "other").replace(/[_-]+/g, " ");
  return { one: word, many: word.endsWith("s") ? word : `${word}s` };
}

/**
 * Who recorded a rule (`reviewed_by`), in words: "operator" or
 * "operator:<name>" is "you"; "operator:<name>-via-<agent>" is "you through
 * <agent>"; anything else is shown as recorded. `agentName` turns an agent
 * key into its name. Empty when nothing is on file.
 */
export function ruleRecordedBy(reviewedBy: string, agentName: (raw: string) => string): string {
  const raw = (reviewedBy || "").trim();
  if (!raw) return "";
  const via = /^operator(?::[^\s]*?)?-via-(.+)$/.exec(raw);
  if (via) return `you through ${agentName(via[1])}`;
  if (raw === "operator" || raw.startsWith("operator:")) return "you";
  return raw;
}

/** Kinds of accepted memory (the `memory canon` classes), plural, in everyday words. */
const MEMORY_KIND: Record<string, string> = {
  decision: "decisions",
  principle: "principles and preferences",
  behavior: "ways of working",
  product_fact: "app facts",
  product_metric: "app numbers",
  research_fact: "research findings",
  status: "status notes",
  unclassified: "not sorted by kind",
};

export function memoryKindPlural(kind: string): string {
  return MEMORY_KIND[kind] ?? kind.replace(/[_-]+/g, " ");
}

/** States of a proposal record (`memory candidates` state), in everyday words. */
const MEMORY_RECORD_STATE: Record<string, { label: string; means: string }> = {
  proposed: { label: "Waiting for your choice", means: "No agent is told these yet." },
  needs_review: { label: "Need another look", means: "Flagged for a second review." },
  parked: { label: "Set aside", means: "Kept on file; agents are not told." },
  no_promote: { label: "Not to be asked again", means: "Kept on file; their source is not put to you again." },
  rejected: { label: "Discarded", means: "Marked wrong or noise; never used." },
  promoted: { label: "Accepted through review", means: "Agents can be told these." },
};

export function memoryRecordState(state: string): { label: string; means: string } {
  return MEMORY_RECORD_STATE[state] ?? { label: state ? sentence(state.replace(/[_-]+/g, " ")) : "State not recorded", means: "" };
}

/** The order the record states are listed in. */
export const MEMORY_RECORD_STATE_ORDER = ["proposed", "needs_review", "parked", "no_promote", "rejected", "promoted"] as const;

/**
 * The four choices on a proposed memory or a proposed rule
 * (`memory adjudicate --verdict promote|park|reject|no_promote`): what each
 * is called and what happens after it, in his words.
 */
export type MemoryChoiceFor = "memory" | "rule";

const MEMORY_CHOICE: Record<string, { memory: string; rule: string; means: { memory: string; rule: string } }> = {
  promote: {
    memory: "Accept",
    rule: "Make it a rule",
    means: {
      memory: "Future agents will be told this when it is relevant.",
      rule: "Every agent will be told this at the start of each task.",
    },
  },
  park: {
    memory: "Set aside",
    rule: "Not now",
    means: { memory: "Keep it on file; agents won't be told.", rule: "Keep it on file; agents won't be told." },
  },
  reject: {
    memory: "Discard",
    rule: "Discard",
    means: { memory: "Wrong or noise; never use it.", rule: "Wrong or noise; never use it." },
  },
  no_promote: {
    memory: "Stop asking",
    rule: "Stop asking",
    means: { memory: "Don't ask me about this source again.", rule: "Don't ask me about this source again." },
  },
};

/**
 * What happens next while the browser cannot record a choice: who records
 * it. The exact commands sit behind "For agents" on each card.
 */
export const MEMORY_CHOICE_HANDOFF =
  "Choosing from this page is not built yet. Tell an agent which choice you want; it records it for you.";

export function memoryChoice(verdict: string, kind: MemoryChoiceFor): { label: string; means: string } {
  const choice = MEMORY_CHOICE[verdict];
  if (!choice) return { label: sentence(verdict.replace(/[_-]+/g, " ")), means: "" };
  return { label: choice[kind], means: choice.means[kind] };
}

/* ---------- Work: the records an item points to ---------- */

/** A record reference in words: what it is, and its id as small secondary text. */
export type RecordRefWords = { kind: string; id: string };

/**
 * What a record reference is, by the shape its owner writes: `kind:` prefix
 * or id prefix. Order matters: the first match names it. Unknown shapes are
 * "Record", never a guess.
 */
const RECORD_KINDS: ReadonlyArray<readonly [RegExp, string]> = [
  [/^ratt_/, "Agent run"],
  [/^(?:land-check:|landcheck_)/, "Landing check"],
  [/^commit:/, "Commit"],
  [/^(?:proof-check:|pfrs_)/, "Proof check result"],
  [/^presubmit-execution:/, "Test run before landing"],
  [/^(?:verifier-verdict:|verification:)/, "Review verdict"],
  [/^(?:receipt:|rec_)/, "Result record"],
  [/^(?:transition:|tr_)/, "Recorded change"],
  [/^ctx_/, "What the agent was given"],
  [/^(?:finding:|find_)/, "Finding"],
  [/^worktree-branch:/, "Branch"],
  [/^artifact:/, "Saved file"],
  [/^design-sketch:/, "Design sketch"],
  [/^source-observation:/, "Observation"],
  [/^(?:evidence:|ev_)/, "Evidence"],
  [/^cand_/, "Proposed memory"],
  [/^claim_/, "Memory"],
  [/^(?:work:|work_)/, "Work item"],
  [/^(?:check:|live:)/, "Check"],
  [/^https?:\/\//i, "Web page"],
  [/^(?:\/|~\/|file:)/, "File"],
];

/**
 * A record reference in words: "Landing check" with its id, "Commit" with
 * the short hash, "File" with the file's name. The kind leads; the id stays
 * small, to find the record.
 */
export function recordRefWords(ref: string): RecordRefWords {
  const value = (ref ?? "").trim();
  const kind = RECORD_KINDS.find(([pattern]) => pattern.test(value))?.[1] ?? "Record";
  const bare = /^https?:\/\//i.test(value) ? value.replace(/^https?:\/\//i, "") : value.replace(/^[a-z][a-z-]*:(?!\/\/)/i, "");
  if (kind === "Commit") return { kind, id: bare.slice(0, 12) };
  if (kind === "File") return { kind, id: bare.split("/").filter(Boolean).pop() ?? bare };
  return { kind, id: bare };
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
  // Decide said these while its list copy was older than 25 seconds, which is
  // nearly always: a pause that never clears, hiding the real reason.
  "Paused while the list refreshes",
  "Buttons pause while the list is read again",
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

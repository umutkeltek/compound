import { arr, type Json, str } from "@/lib/compound";
import { ago } from "@/lib/time";
import { type StateTone, toneForState } from "@/components/kit/StateBadge";
import { WORK_TRANSITIONS, workOptionsFor } from "@/lib/decision-registry";
import { shortText, workId } from "@/lib/work-data";
import { agentNameFor, plainWorkStep } from "@/lib/plain-words";
import { workTitle } from "@/lib/work-title";
import { agentDisplayName } from "@/lib/harness-label";
import { isStale } from "@/lib/work-state";

/**
 * Name each card's owner the way Live names agents: the executor lane that
 * reports the work item (`byWork`: work id → agent name) wins, else the
 * owner id's agent (`names`: agent id → name, from the executors projection),
 * else the owner id as written. The full owner/lane id stays as small text.
 */
export function nameCardOwners(
  columns: BoardColumnModel[],
  byWork: ReadonlyMap<string, string>,
  names: ReadonlyMap<string, string>,
): BoardColumnModel[] {
  return columns.map((column) => ({
    ...column,
    cards: column.cards.map((card) => {
      if (!card.subtitle) return card;
      const joined = card.workId ? byWork.get(card.workId) : undefined;
      // The lane id adds something only when it names more than the agent ("claude_code:<lane>").
      if (joined) return { ...card, agentName: joined, lane: /[:/]/.test(card.subtitle) ? card.subtitle : "" };
      const named = agentNameFor(card.subtitle, names);
      return { ...card, agentName: named.name, lane: named.lane };
    }),
  }));
}

/*
 * Pipeline lens data model — maps the live workbench / suggestion / source
 * projections onto the seven visible stages the operator scans left to right:
 *
 *   Signal → Suggest → Add to work → Start → Run → Proof → Learn
 *
 *
 * Every card that carries a real work_id is `traceable` — clicking it opens the
 * Trace drawer. Pure-ish server module (reads projections already fetched by
 * the page); returns only serializable shapes so a client lens can render them.
 */

/**
 * Envelope-stage position for the SegmentedProgress on a card (§9.3): where in
 * the seven-stage spine (Signal→Suggest→Add to work→Start→Run→Proof→Learn) the
 * item sits. `done` = stages complete, `active` = the current stage (null when
 * terminal/stalled — nothing pulses). Derived from the SAME state buckets
 * buildPipeline scans, so the strip and the progress can never disagree.
 */
export type StageProgress = { total: number; done: number; active: number | null };

const STAGE_TOTAL = 7;

function progressAt(active: number, opts: { complete?: boolean; stalled?: boolean } = {}): StageProgress {
  if (opts.complete) return { total: STAGE_TOTAL, done: STAGE_TOTAL, active: null };
  const a = Math.max(0, Math.min(STAGE_TOTAL - 1, active));
  return { total: STAGE_TOTAL, done: a, active: opts.stalled ? null : a };
}

/** Map a lifecycle state onto its envelope-stage progress (§9.3). */
export function stageProgressForState(state: string): StageProgress {
  switch (state) {
    case "proposed":
    case "suggested":
    case "candidate":
      return progressAt(2); // completed Signal+Suggest, waiting to be added
    case "admitted":
    case "ready":
      return progressAt(3); // Start
    case "claimed":
      return progressAt(4, { stalled: true }); // in the Execute lane but not yet running
    case "in_progress":
    case "running":
    case "verifying":
      return progressAt(4); // Execute — the breathing stage
    case "blocked":
    case "held":
    case "needs_review":
      return progressAt(4, { stalled: true }); // stalled on the operator at Execute
    case "completed":
    case "promoted":
    case "filed":
    case "allow":
      return progressAt(6, { complete: true }); // through Proof + Learn
    case "failed":
    case "deny":
    case "rejected":
      return progressAt(4, { stalled: true });
    case "cancelled":
    case "demoted":
    case "no_promote":
      return progressAt(2, { stalled: true });
    default:
      return progressAt(2);
  }
}

export type BoardCard = {
  key: string;
  workId: string; // "" for a pure suggestion with no work yet
  displayId: string;
  title: string;
  subtitle: string;
  state: string;
  tone: StateTone;
  ageLabel: string;
  moveType: string;
  note: string;
  traceable: boolean;
  running: boolean;
  /** envelope-stage position for the card's SegmentedProgress (§9.3). */
  progress: StageProgress;
  /** canonical surface (e.g. `product:<id>`), so the board can show which app a card belongs to. */
  surface: string;
  /** the owner's agent, named as Live names agents ("Claude Code"); set by `nameCardOwners` */
  agentName?: string;
  /** the full owner/lane id, small text beside the agent name ("" when the name is the whole id) */
  lane?: string;
  /** its app, codebase or "not linked" group (lib/work-project.ts); set by `decorateBoard` */
  projectId?: string;
  projectName?: string;
  projectKind?: string;
  /** in progress and not moved for more than a week (lib/work-state.ts `isStale`); set by `decorateBoard` */
  stale?: boolean;
  /** how to tell this card apart when its title repeats in its column; set by `decorateBoard` */
  repeatHint?: string;
};

export type BoardColumnModel = {
  key: string;
  label: string;
  description: string;
  tone: StateTone;
  emptyCopy: string;
  cards: BoardCard[];
};

/** One contextual governed transition offered on the work-advance state machine. */
export type ActionOption = {
  value: string;
  label: string;
  tone: string;
  description: string;
  argv: string[] | null;
  terminal: boolean;
  needsSummary: boolean;
  needsMoveType: boolean;
};

export type WorkActionRowData = {
  area: string;
  itemId: string;
  state: string;
  hasTransitions: boolean;
  options: ActionOption[];
};

const TERMINAL_TARGETS = new Set(["completed", "failed", "cancelled"]);

/**
 * Build the contextual action row for a work id + state directly from the
 * EXISTING work-advance state machine (WORK_TRANSITIONS / workOptionsFor in
 * decision-registry — consumed, never edited). Shared by the Trace API and the
 * /work/[id] page so the drawer and the detail page can never diverge on what
 * moves are legal.
 */
export function buildActionRow(id: string, state: string): WorkActionRowData {
  const options: ActionOption[] = workOptionsFor({ work_id: id, state }).map((option) => ({
    value: option.value,
    label: option.label,
    tone: option.tone,
    description: option.description,
    argv: option.argv ?? null,
    terminal: TERMINAL_TARGETS.has(option.value),
    needsSummary: TERMINAL_TARGETS.has(option.value),
    needsMoveType: option.value === "completed" || option.value === "failed",
  }));
  return {
    area: "work-advance",
    itemId: id,
    state,
    hasTransitions: (WORK_TRANSITIONS[state] ?? []).length > 0,
    options,
  };
}

/** The card shows the item's one title (lib/work-title.ts), shortened for the card. */
function cardTitle(item: Json): string {
  return workTitle(item);
}

function workCard(item: Json, note = "", progress?: StageProgress): BoardCard {
  const id = workId(item);
  const state = str(item.state);
  const age = str(item.claimed_at) || str(item.created_at) || str(item.updated_at);
  return {
    key: id || cardTitle(item),
    workId: id,
    displayId: id,
    title: cardTitle(item),
    subtitle: str(item.claimed_by) || str(item.owner) || "",
    state,
    tone: toneForState(state),
    ageLabel: age ? ago(age) : "",
    moveType: str(item.move_type),
    // The tool's next-step instruction in plain words (lib/plain-words.ts); an
    // unknown instruction is shown as written.
    note: note || shortText(plainWorkStep(str(item.next_action)) || str(item.desired_outcome), 150),
    traceable: Boolean(id),
    running: state === "running",
    progress: progress ?? stageProgressForState(state),
    surface: str(item.canonical_surface),
  };
}

/**
 * What a card needs to say which app it is for, who has it, whether it has
 * stopped moving, and how it differs from a card with the same title in its
 * column. `groupOf` comes from the shared index (lib/compound.ts
 * `readWorkProjectIndex`); without it a card names no app (unknown). Pass the
 * clock in.
 */
export function decorateBoard(
  columns: BoardColumnModel[],
  open: Json[],
  opts: { nowMs: number; groupOf?: (workId: string) => { id: string; name: string; kind: string } | null },
): BoardColumnModel[] {
  const rows = new Map(open.map((row) => [workId(row), row] as const));
  return columns.map((column) => {
    const seen = new Map<string, number>();
    for (const card of column.cards) seen.set(card.title, (seen.get(card.title) ?? 0) + 1);
    return {
      ...column,
      cards: column.cards.map((card) => {
        const group = card.workId ? opts.groupOf?.(card.workId) ?? null : null;
        const row = card.workId ? rows.get(card.workId) : undefined;
        const named = card.agentName && card.agentName !== card.subtitle;
        const repeats = seen.get(card.title) ?? 0;
        return {
          ...card,
          agentName: named ? card.agentName : card.subtitle ? agentDisplayName(card.subtitle) : "",
          lane: named ? card.lane : card.subtitle && agentDisplayName(card.subtitle) !== card.subtitle ? card.subtitle : "",
          projectId: group?.id ?? "",
          projectName: group?.name ?? "",
          projectKind: group?.kind ?? "",
          stale: row ? isStale(row, opts.nowMs) : false,
          repeatHint: repeats > 1 ? `One of ${repeats} with this title · ${card.subtitle || "no agent"} · ${card.workId.slice(-6) || card.key}` : "",
        };
      }),
    };
  });
}

export function buildBoard(open: Json[], recent: Json[]): BoardColumnModel[] {
  const byStates = (states: string[]) => open.filter((w) => states.includes(str(w.state)));
  const columns: { key: string; label: string; description: string; tone: StateTone; states: string[]; empty: string }[] = [
    // Column names follow lib/plain-words.ts. "In progress" (lib/counts.ts) is
    // the two middle columns together: running or picked up, plus waiting for
    // a proof check. "On hold" is held or blocked work; "waiting for your
    // decision" is a different, larger set (it also holds suggested work).
    { key: "inbox", label: "Suggested", description: "Proposed work waiting to be accepted.", tone: "draft", states: ["proposed", "admitted"], empty: "Nothing suggested." },
    { key: "ready", label: "Ready to start", description: "Waiting for an agent to pick it up.", tone: "queued", states: ["ready"], empty: "Nothing ready to start." },
    { key: "running", label: "Running or picked up", description: "In progress: an agent picked it up; it may not have started.", tone: "live", states: ["claimed", "in_progress", "running"], empty: "Nothing running." },
    { key: "needs-me", label: "On hold", description: "Stopped: held for your answer, or blocked on something outside you. The List's \"Waiting for you\" and \"Waiting on something else\" split them.", tone: "seam", states: ["blocked", "held"], empty: "Nothing is on hold." },
    { key: "review", label: "Waiting for a proof check", description: "In progress: the agent says it is done; the proof is not accepted yet.", tone: "live", states: ["verifying"], empty: "Nothing waits for a proof check." },
  ];
  const board: BoardColumnModel[] = columns.map((col) => ({
    key: col.key,
    label: col.label,
    description: col.description,
    tone: col.tone,
    emptyCopy: col.empty,
    cards: byStates(col.states).map((w) => workCard(w)),
  }));
  const done = recent.filter((w) => ["completed", "failed", "cancelled"].includes(str(w.state)));
  board.push({
    key: "done",
    label: "Done",
    description: "Recently closed, with a receipt.",
    tone: "done",
    emptyCopy: "Nothing closed recently.",
    cards: done.map((w) => workCard(w)),
  });
  // What waits for you is the first thing on the board when there is any:
  // on hold moves to the front instead of sitting off-screen to the right.
  const hold = board.findIndex((column) => column.key === "needs-me");
  if (hold > 0 && board[hold].cards.length) board.unshift(...board.splice(hold, 1));
  return board;
}

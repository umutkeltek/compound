import { arr, HEAVY_PROJECTION_TIMEOUT_MS, type LiveRead, num, readProjection, runProjection, str, type Json, type ProjectionRead } from "@/lib/compound";
import {
  normalizeOperatorCoordination,
  type OperatorCoordinationProjection,
} from "@/lib/coordination-model";

/**
 * The ONE operator-attention projection (`compound attention --json`,
 * schema `compound.attention.v0.1`). Part 1 of the console redesign: a single
 * server-side, deterministically-ranked projection that composes the reviews
 * queue + workbench needs-claim/dispatch + executor job-health into one
 * "what needs me" surface.
 *
 * THE LAW (why this file exists): every "what needs you" surface — nav badges,
 * the briefing headline + Decide list, the Reviews/Decide band ordering — reads
 * THIS projection. No consumer may re-derive a second client-side "needs me"
 * heuristic; that drift is exactly what Phase 2 deletes. If a surface needs a
 * number, it comes from here.
 */

export type AttentionBand = "needs_you" | "watching" | "filed" | (string & {});
export type AttentionSource = "reviews" | "workbench" | "executor" | (string & {});
export type AttentionActionKind = "one_click" | "batch" | "copy_only" | (string & {});
export type AttentionBlastRadius =
  | "read"
  | "local_reversible"
  | "external_side_effect"
  | "public"
  | "destructive"
  | "financial"
  | "security"
  | (string & {});

export interface AttentionAction {
  id: string;
  label: string;
  argv: string[];
  kind: AttentionActionKind;
  requires_confirmation: boolean;
  /** Go-owned execution authority. Preserved byte-for-shape; parsed by the consumer. */
  action_intent?: Json;
  policy_decision?: Json;
  policy_authority?: Json;
}

export interface AttentionFreshness {
  generated_at: string;
  source: string;
}

export interface AttentionGroup {
  key: string;
  area: string;
  band: AttentionBand;
  source: AttentionSource;
  kind: string;
  count: number;
  operator_summary: string;
  operator_unlock: string;
  blast_radius: AttentionBlastRadius;
  reversibility: "reversible" | "irreversible" | (string & {});
  leverage: number;
  oldest_created_at: string;
  /** null when the owner has exposed no safe one-click/batch act (terminal_reason says why). */
  action: AttentionAction | null;
  terminal_reason: string;
  item_ids: string[];
  freshness: AttentionFreshness;
  /**
   * The owner wrote no clear question for this record: it is his, but an agent
   * must word it before it is put to him. False when the projection does not say.
   */
  needs_wording?: boolean;
  /**
   * Each item inside the group, from the sources that list them one by one
   * (proposed memories, product results, held agent actions, data-source
   * setup), so a page can name and link each one. Empty when the source does
   * not list items (the review queue, held work).
   */
  items?: AttentionItemRef[];
}

/** One item of a group: what it is, what it is about (`context`), and where it lives. */
export interface AttentionItemRef {
  id: string;
  title: string;
  /** what it is about: `product:<id>` for a product result, an agent or a source label otherwise */
  context: string;
  ref: string;
  path: string;
  createdAt: string;
}

/** One reason the projection set records aside instead of counting them as his. */
export interface AttentionSetAsideReason {
  reason: string;
  reasonText: string;
  count: number;
  /** Nothing is left for anyone to decide. */
  closed: boolean;
  nextStep: string;
}

/**
 * What the projection set aside, and why. Null when the projection does not
 * carry these fields (an older tool): the page then says nothing about them,
 * never zero.
 */
export interface AttentionTotals {
  closedWithoutYou: number;
  /** items a standing rule of his already allows, listed apart from his count; null when the source does not say */
  coveredByRule: number | null;
  setAside: number;
  waitingOnOthers: number;
  reasons: AttentionSetAsideReason[];
}

export interface AttentionCounts {
  needs_you: number;
  needs_you_groups: number;
  actionable_now: number;
  watching: number;
  blocked_on_you: number;
  fallback_groups: number;
}

export interface AttentionReport {
  schema_version: string;
  generated_at: string;
  counts: AttentionCounts;
  /** Highest-leverage group — replaces the old client `selectInsight`. */
  top: AttentionGroup | null;
  /** Deterministically ranked, server-side; consume in order, never re-sort. */
  groups: AttentionGroup[];
  /** Unresolved authority references; never folded into counts, top, or groups. */
  operatorAttention: OperatorCoordinationProjection;
  /** What was set aside and why; null when the projection does not say. */
  totals: AttentionTotals | null;
  /** batches a standing rule of his already allows; null when the source does not send the list (an older tool) */
  coveredByRule: AttentionCoveredBatch[] | null;
  /** whether his standing rules were read; null when the source does not say */
  rules: AttentionRules | null;
  /** what was set aside, batch by batch (with the decision a duplicate is counted under); null when not sent */
  setAside: AttentionSetAsideBatch[] | null;
  /** every recorded answer, newest first; null when the source does not send them */
  answered: AttentionAnswer[] | null;
}

/** A standing rule (grant) of his that covers a batch. */
export interface AttentionCoveringRule {
  grantId: string;
  capability: string;
  expiresAt: string;
}

/** One batch of items a standing rule of his already allows: not counted as his. */
export interface AttentionCoveredBatch {
  key: string;
  area: string;
  source: string;
  kind: string;
  count: number;
  summary: string;
  itemIds: string[];
  rules: AttentionCoveringRule[];
  /** what works these items; "" when nothing does yet */
  worker: string;
  /** the source's own sentence: what happens next and who does it */
  nextStep: string;
}

export interface AttentionRules {
  read: boolean;
  activeStandingGrants: number;
}

/** One batch the source set aside, and why. */
export interface AttentionSetAsideBatch {
  key: string;
  area: string;
  kind: string;
  reason: string;
  reasonText: string;
  closed: boolean;
  nextStep: string;
  itemIds: string[];
  /** a duplicate: the decision key the same question is still counted under */
  duplicateOf: string;
}

/** One recorded answer and what came of it. */
export interface AttentionAnswer {
  area: string;
  itemId: string;
  question: string;
  decision: string;
  decidedAt: string;
  decidedBy: string;
  workId: string;
  /** the work's state now; "" when there is no work, "unknown" when it could not be read */
  workState: string;
  stillWaiting: boolean;
  /** what came of it, in one sentence */
  outcome: string;
}

/* ---------- defensive normalizer (schemas evolve; never crash a surface) ---------- */

function toAction(raw: unknown): AttentionAction | null {
  if (!raw || typeof raw !== "object") return null;
  const a = raw as Json;
  const objectField = (value: unknown): Json | undefined =>
    value != null && typeof value === "object" && !Array.isArray(value) ? value as Json : undefined;
  return {
    id: str(a.id),
    label: str(a.label),
    argv: arr(a.argv).map(String),
    kind: str(a.kind) as AttentionActionKind,
    requires_confirmation: Boolean(a.requires_confirmation),
    action_intent: objectField(a.action_intent),
    policy_decision: objectField(a.policy_decision),
    policy_authority: objectField(a.policy_authority),
  };
}

function toGroup(raw: unknown): AttentionGroup {
  const g = (raw && typeof raw === "object" ? raw : {}) as Json;
  const freshness = (g.freshness && typeof g.freshness === "object" ? g.freshness : {}) as Json;
  return {
    key: str(g.key),
    area: str(g.area),
    band: (str(g.band) || "needs_you") as AttentionBand,
    source: str(g.source) as AttentionSource,
    kind: str(g.kind),
    count: num(g.count),
    operator_summary: str(g.operator_summary),
    operator_unlock: str(g.operator_unlock),
    blast_radius: str(g.blast_radius) as AttentionBlastRadius,
    reversibility: str(g.reversibility) as AttentionGroup["reversibility"],
    leverage: num(g.leverage),
    oldest_created_at: str(g.oldest_created_at),
    action: toAction(g.action),
    terminal_reason: str(g.terminal_reason),
    item_ids: arr(g.item_ids).map(String),
    freshness: { generated_at: str(freshness.generated_at), source: str(freshness.source) },
    needs_wording: g.needs_wording === true,
    items: arr(g.items).filter((item) => item && typeof item === "object").map((item: Json) => ({
      id: str(item.id),
      title: str(item.title),
      context: str(item.context),
      ref: str(item.ref),
      path: str(item.path),
      createdAt: str(item.created_at),
    })),
  };
}

function toTotals(raw: unknown): AttentionTotals | null {
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) return null;
  const t = raw as Json;
  if (typeof t.closed_without_you !== "number") return null;
  return {
    closedWithoutYou: num(t.closed_without_you),
    coveredByRule: typeof t.covered_by_rule === "number" ? t.covered_by_rule : null,
    setAside: num(t.set_aside),
    waitingOnOthers: num(t.waiting_on_others),
    reasons: arr(t.set_aside_reasons).map((r: Json) => ({
      reason: str(r.reason),
      reasonText: str(r.reason_text),
      count: num(r.count),
      closed: r.closed === true,
      nextStep: str(r.next_step),
    })).filter((r) => r.count > 0),
  };
}

function isObject(value: unknown): value is Json {
  return Boolean(value) && typeof value === "object" && !Array.isArray(value);
}

function toCoveredBatch(raw: Json): AttentionCoveredBatch {
  return {
    key: str(raw.key),
    area: str(raw.area),
    source: str(raw.source),
    kind: str(raw.kind),
    count: num(raw.count) || arr(raw.item_ids).length,
    summary: str(raw.operator_summary),
    itemIds: arr(raw.item_ids).map(String),
    rules: arr(raw.rules).filter(isObject).map((rule: Json) => ({
      grantId: str(rule.grant_id),
      capability: str(rule.capability),
      expiresAt: str(rule.expires_at),
    })),
    worker: str(raw.worker),
    nextStep: str(raw.next_step),
  };
}

function toSetAsideBatch(raw: Json): AttentionSetAsideBatch {
  return {
    key: str(raw.key),
    area: str(raw.area),
    kind: str(raw.kind),
    reason: str(raw.reason),
    reasonText: str(raw.reason_text),
    closed: raw.closed === true,
    nextStep: str(raw.next_step),
    itemIds: arr(raw.item_ids).map(String),
    duplicateOf: str(raw.duplicate_of),
  };
}

function toAnswer(raw: Json): AttentionAnswer {
  return {
    area: str(raw.area),
    itemId: str(raw.item_id),
    question: str(raw.question),
    decision: str(raw.decision),
    decidedAt: str(raw.decided_at),
    decidedBy: str(raw.decided_by),
    workId: str(raw.work_id),
    workState: str(raw.work_state),
    stillWaiting: raw.still_waiting === true,
    outcome: str(raw.outcome),
  };
}

export function toAttentionReport(raw: Json | null): AttentionReport | null {
  if (!raw || typeof raw !== "object") return null;
  const counts = (raw.counts && typeof raw.counts === "object" ? raw.counts : {}) as Json;
  return {
    schema_version: str(raw.schema_version),
    generated_at: str(raw.generated_at),
    counts: {
      needs_you: num(counts.needs_you),
      needs_you_groups: num(counts.needs_you_groups),
      actionable_now: num(counts.actionable_now),
      watching: num(counts.watching),
      blocked_on_you: num(counts.blocked_on_you),
      fallback_groups: num(counts.fallback_groups),
    },
    top: raw.top && typeof raw.top === "object" ? toGroup(raw.top) : null,
    groups: arr(raw.groups).map(toGroup),
    operatorAttention: normalizeOperatorCoordination(raw.operator_attention, true),
    totals: toTotals(raw.totals),
    coveredByRule: Array.isArray(raw.covered_by_rule) ? raw.covered_by_rule.filter(isObject).map(toCoveredBatch) : null,
    rules: isObject(raw.rules) && typeof raw.rules.read === "boolean"
      ? { read: raw.rules.read, activeStandingGrants: num(raw.rules.active_standing_grants) }
      : null,
    setAside: Array.isArray(raw.set_aside) ? raw.set_aside.filter(isObject).map(toSetAsideBatch) : null,
    answered: Array.isArray(raw.answered) ? raw.answered.filter(isObject).map(toAnswer) : null,
  };
}

/**
 * Fetch + normalize the attention projection. Goes through the existing
 * `runJson` TTL memo (the same subprocess-collapsing cache every other read
 * uses). `runJson` enforces no argv allowlist — it is read-only by contract
 * (see its doc comment), so no command needs registering there.
 */
export async function getAttention(): Promise<AttentionReport | null> {
  return toAttentionReport(await runProjection(["attention", "--json"], { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS }).then((r) => r.value));
}

/**
 * ProjectionRead-aware companion to `getAttention` (packet:
 * console-honest-degradation, passthrough). Same normalizer, but exposes
 * status/lastGoodAt/error so a caller renders the honest-degradation model -
 * never assume a non-null report means "live"; check `read.status`. Only the
 * Every mutation-capable attention consumer uses this form so action
 * readiness is bound to the read's actual live/stale/unreachable status.
 */
export async function getAttentionProjection(opts?: { timeoutMs?: number }): Promise<{
  read: ProjectionRead;
  report: AttentionReport | null;
}> {
  const read = await runProjection(["attention", "--json"], { timeoutMs: opts?.timeoutMs ?? HEAVY_PROJECTION_TIMEOUT_MS });
  return { read, report: toAttentionReport(read.value) };
}

/**
 * Non-blocking companion (lib/compound.ts `readProjection`): returns the
 * cached report at once, or `loading`, and queues a refresh when due. The
 * pages that must never wait for compound (Today, Decide, the sidebar) use it.
 */
export async function readAttention(): Promise<{ read: LiveRead; report: AttentionReport | null }> {
  const read = await readProjection(["attention", "--json"], { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS });
  return { read, report: toAttentionReport(read.value) };
}

/**
 * Kinds of request that are agent housekeeping, not decisions for him:
 * proposals to update agent skills, mined from agent logs. Until the
 * projection moves the kind out, Today and Decide fold the whole kind into one
 * quiet line outside every count.
 */
const AGENT_HOUSEKEEPING_AREAS = new Set(["capability-lifecycle-proposals"]);

/** Whose a needs-you group is: his to answer, his once an agent words it, or agent housekeeping. */
export type AttentionShare = "yours" | "wording" | "housekeeping" | "covered";

export function attentionShare(
  group: Pick<AttentionGroup, "area" | "needs_wording"> & { item_ids?: readonly string[] },
  covered?: ReadonlySet<string>,
): AttentionShare {
  // The source's own answer first: a group whose every item a standing rule
  // of his already covers is listed under that rule, never as his and never
  // in the console's housekeeping fold below.
  const ids = group.item_ids ?? [];
  if (covered && covered.size > 0 && ids.length > 0 && ids.every((id) => covered.has(id))) return "covered";
  // Stopgap until the source reports skill proposals under a rule: the
  // console folds that kind out of his count itself. Once the source lists
  // them in `covered_by_rule`, they leave the needs-you band, this rule
  // matches nothing, and it can be deleted without a visible change.
  const head = (group.area || "").split("/")[0];
  if (AGENT_HOUSEKEEPING_AREAS.has(head)) return "housekeeping";
  return group.needs_wording ? "wording" : "yours";
}

/** Every item id a standing rule of his already covers (empty when the source sends none). */
export function coveredItemIds(report: AttentionReport | null): Set<string> {
  return new Set((report?.coveredByRule ?? []).flatMap((batch) => batch.itemIds));
}

/** A group's items that are not covered by a rule (a group is counted once, its items never twice). */
function uncoveredCount(group: AttentionGroup, covered: ReadonlySet<string>): number {
  if (!covered.size) return group.count;
  const overlap = group.item_ids.filter((id) => covered.has(id)).length;
  return Math.max(0, group.count - overlap);
}

/**
 * THE one definition of "waiting for you", used by the Today sentence, the
 * sidebar badge, Decide's header and Live so they can never disagree. Only
 * what is his counts:
 * - `groups`: decisions he can answer (one card each on Decide), `items` the
 *   records inside them, `workItems` the work items among them;
 * - `wording`: decisions that are his but carry no clear question yet, so an
 *   agent must word them first (shown, never counted as waiting);
 * - `housekeeping`: proposals to update agent skills (one quiet line, never counted);
 * - `closedWithoutYou` / `waitingOnOthers`: what the projection set aside,
 *   null when the projection does not say (an older tool).
 * Returns null when attention has not been read: unknown is never zero.
 */
export type WaitingOnYou = {
  groups: number;
  items: number;
  workItems: number;
  wording: number;
  housekeeping: { groups: number; items: number };
  closedWithoutYou: number | null;
  /** everything set aside (closed or not); null when the source does not say */
  setAside: number | null;
  waitingOnOthers: { items: number; itemIds: string[] } | null;
  /**
   * Items a standing rule of his already allows (`covered_by_rule`), never
   * counted as his; null when the source does not send the list.
   */
  coveredByRule: { items: number; batches: AttentionCoveredBatch[] } | null;
  /** false when his standing rules could not be read; null when the source does not say */
  rulesRead: boolean | null;
};

export function waitingOnYou(report: AttentionReport | null): WaitingOnYou | null {
  if (!report) return null;
  const covered = coveredItemIds(report);
  const needsYou = attentionNeedsYouGroups(report);
  const share = (group: AttentionGroup) => attentionShare(group, covered);
  const yours = needsYou.filter((group) => share(group) === "yours");
  const housekeeping = needsYou.filter((group) => share(group) === "housekeeping");
  const others = arr(report.groups).filter((group) => group.kind === "waiting_on_others");
  const batches = report.coveredByRule;
  return {
    groups: yours.length,
    items: yours.reduce((sum, group) => sum + uncoveredCount(group, covered), 0),
    workItems: yours.filter((group) => group.source === "workbench").reduce((sum, group) => sum + uncoveredCount(group, covered), 0),
    wording: needsYou.filter((group) => share(group) === "wording").length,
    housekeeping: { groups: housekeeping.length, items: housekeeping.reduce((sum, group) => sum + uncoveredCount(group, covered), 0) },
    coveredByRule: batches ? { items: batches.reduce((sum, batch) => sum + batch.count, 0), batches } : null,
    rulesRead: report.rules ? report.rules.read : null,
    setAside: report.totals ? report.totals.setAside : null,
    closedWithoutYou: report.totals ? report.totals.closedWithoutYou : null,
    waitingOnOthers: others.length || report.totals
      ? {
          items: others.length ? others.reduce((sum, group) => sum + group.count, 0) : report.totals?.waitingOnOthers ?? 0,
          itemIds: others.flatMap((group) => group.item_ids),
        }
      : null,
  };
}

/**
 * His decisions in the one order Today and Decide share: the projection's
 * own order, with the ones an agent must word first after the ones he can
 * answer. Agent housekeeping is left out (see `housekeepingGroups`).
 */
export function yourDecisionGroups(report: AttentionReport | null): AttentionGroup[] {
  const covered = coveredItemIds(report);
  const needsYou = attentionNeedsYouGroups(report);
  return [
    ...needsYou.filter((group) => attentionShare(group, covered) === "yours"),
    ...needsYou.filter((group) => attentionShare(group, covered) === "wording"),
  ];
}

/** The needs-you groups that are agent housekeeping, folded into one quiet line. */
export function housekeepingGroups(report: AttentionReport | null): AttentionGroup[] {
  const covered = coveredItemIds(report);
  return attentionNeedsYouGroups(report).filter((group) => attentionShare(group, covered) === "housekeeping");
}

/* ---------- a product result: is his verdict due? ---------- */

/** The waiting list's kind for "a product change's result waits for his verdict". */
export const PRODUCT_RESULT_KIND = "product_result";

/**
 * Whether his verdict on one app's change is due, by the waiting list's own
 * answer (the one definition: `/loops`, the app page, Today and Decide agree):
 * - `due`: the list asks him (a `product_result` item for this app or one of
 *   its changes); `href` opens that decision;
 * - `asked_elsewhere`: the same question is counted once under another
 *   decision (for example the idea itself); `href` opens that one;
 * - `set_aside`: the list set it aside, with its own reason;
 * - `not_listed`: the list does not ask him about this app;
 * - `unknown`: the list has not been read yet.
 */
export type ProductVerdict =
  | { state: "due"; decisionKey: string; itemId: string }
  | { state: "asked_elsewhere"; decisionKey: string }
  | { state: "set_aside"; reasonText: string; nextStep: string }
  | { state: "not_listed" }
  | { state: "unknown" };

export function productVerdict(
  report: AttentionReport | null,
  productIds: readonly string[],
  changeIds: readonly string[] = [],
): ProductVerdict {
  if (!report) return { state: "unknown" };
  const products = new Set(productIds.filter(Boolean));
  const changes = new Set(changeIds.filter(Boolean));
  for (const group of attentionNeedsYouGroups(report)) {
    if (group.kind !== PRODUCT_RESULT_KIND) continue;
    const item = (group.items ?? []).find((entry) => products.has(entry.context) || changes.has(entry.id));
    if (item) return { state: "due", decisionKey: group.key, itemId: item.id };
  }
  for (const batch of report.setAside ?? []) {
    if (batch.kind !== PRODUCT_RESULT_KIND || !batch.itemIds.some((id) => changes.has(id))) continue;
    if (batch.duplicateOf) return { state: "asked_elsewhere", decisionKey: batch.duplicateOf };
    // A draft nobody wrote is already said by the results card itself.
    if (batch.reason !== "placeholder_draft") return { state: "set_aside", reasonText: batch.reasonText, nextStep: batch.nextStep };
  }
  return { state: "not_listed" };
}

export function plural(n: number, one: string, many: string): string {
  return `${n.toLocaleString("en-US")} ${n === 1 ? one : many}`;
}

/**
 * The needs-you groups IN THE PROJECTION'S ORDER (already leverage-ranked,
 * server-side). Consumers slice this; they must not re-rank it — the ranking is
 * the projection's job, not the client's.
 */
export function attentionNeedsYouGroups(report: AttentionReport | null): AttentionGroup[] {
  return arr(report?.groups).filter((g): g is AttentionGroup => (g as AttentionGroup).band === "needs_you");
}

/**
 * The high-danger blast radii — a gate carrying one of these (or an irreversible
 * one) is CLASS-A: the operator's most consequential seat. The briefing hero
 * wears `--heat-seam` (amber ambience) when any Class-A gate waits, `--heat-work`
 * otherwise (DESIGN.md §9.2 / §9.4). `external_side_effect` stays mid-tier — a
 * side effect is not, by itself, irreversible or destructive.
 */
const CLASS_A_BLAST = new Set<AttentionBlastRadius>(["destructive", "financial", "security", "public"]);

export function hasClassAGate(groups: AttentionGroup[]): boolean {
  return groups.some((g) => CLASS_A_BLAST.has(g.blast_radius) || g.reversibility === "irreversible");
}

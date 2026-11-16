/**
 * Normalized executor model for the Agents screen (Phase 4 Wave B1).
 *
 * This module is PURE and client-safe on purpose: the executor table and the
 * hygiene band are client components, so nothing here may import
 * `@/lib/compound` (it pulls `node:path`/`node:child_process` and would break
 * the client bundle). The server-only projection parser lives in
 * `./executor-normalize`; this file owns the shared types, the lease-staleness
 * rule both surfaces read, and the small formatting helpers.
 */

import { agentDisplayName, agentKey, NO_AGENT_KEY } from "@/lib/harness-label";
import { isInProgress, isOnHold } from "@/lib/work-state";

/** DESIGN.md / plan Part 3: a lease older than this (and no projection-supplied
 *  threshold) is stale — a lane-health hygiene class. 48h in seconds. */
export const LEASE_STALE_AFTER_SECONDS = 48 * 60 * 60;

export interface ExecutorLease {
  workId: string;
  ageSeconds: number | null;
}

export interface ExecutorWorkRef {
  workId: string;
  title: string;
  state: string;
  nextAction: string;
  /** the record's accountable owner, raw ("" when none) */
  owner: string;
  /** who picked it up, raw ("" when nobody) */
  claimedBy: string;
  /** when it was picked up, as the projection wrote it ("" when unknown) */
  claimedAt: string;
}

export interface ExecutorRow {
  id: string;
  label: string;
  /** other names the projection lists for this agent */
  aliases: string[];
  status: string;
  statusSource: string;
  waitReason: string;
  bestFor: string[];
  canUseTools: string[];
  costLimits: string[];
  running: number;
  ready: number;
  assigned: number;
  needsMe: number;
  leaseCount: number;
  oldestLeaseAgeSeconds: number | null;
  leases: ExecutorLease[];
  routeEvidenceCount: number;
  routeEvidencePresent: boolean;
  lastHook: { hook: string; status: string; createdAt: string } | null;
  currentWork: ExecutorWorkRef[];
  nextAssigned: ExecutorWorkRef | null;
  projectionNote: string;
}

export interface ExecutorCounts {
  total: number;
  open: number;
  running: number;
  ready: number;
  needsMe: number;
  assigned: number;
  /** ACTIVE leases (work still live) — the population the threshold band reads. */
  leases: number;
  /** Residue leases whose work is terminal/archived/missing — the "unarchived
   *  lease history" the briefing self-healing row reports (== `home doctor`
   *  operational_hygiene.stale_lease_count). Age-independent, disjoint from
   *  `leases`. Surfaced so the Agents band can name both numbers side by side. */
  staleHistory: number;
}

export interface ExecutorProjection {
  generatedAt: string;
  authorityRef: string;
  sourceRefs: string[];
  nextCommand: string[];
  staleAfterSeconds: number;
  counts: ExecutorCounts;
  executors: ExecutorRow[];
  delegatedSessions: DelegatedSessionRow[];
  delegatedCounts: DelegatedSessionCounts;
}

/* ---------- one agent per holder key ----------
 * `compound executors --json` sorts open work into its own fixed agent
 * profiles by a loose name match, and puts every holder it does not know
 * (a scheduler, another tool, you) into one `unassigned` bucket. The Work
 * page's agent filter, Today and Live must count the same population, so
 * the console sorts each open work item by the one agent key
 * (lib/harness-label.ts `agentKey` of who picked it up, else its owner),
 * the same key the Work filter `owner=` compares. A row's numbers then equal
 * the list its link opens, and "no agent" means no holder at all.
 */

const READY_STATES = new Set(["ready", "admitted", "proposed"]);

/** The agent key a work item belongs to: who picked it up, else its owner. */
export function holderKey(work: Pick<ExecutorWorkRef, "claimedBy" | "owner">): string {
  return agentKey(work.claimedBy || work.owner);
}

function emptyRow(id: string): ExecutorRow {
  return {
    id,
    label: agentDisplayName(id),
    aliases: [],
    status: "",
    statusSource: "",
    waitReason: "",
    bestFor: [],
    canUseTools: [],
    costLimits: [],
    running: 0,
    ready: 0,
    assigned: 0,
    needsMe: 0,
    leaseCount: 0,
    oldestLeaseAgeSeconds: null,
    leases: [],
    routeEvidenceCount: 0,
    routeEvidencePresent: false,
    lastHook: null,
    currentWork: [],
    nextAssigned: null,
    projectionNote: "",
  };
}

/**
 * Rows by holder key. A profile row keeps its configured facts (tools, cost
 * limits, last hook) when its id is the key; its counts are recounted from
 * the work it holds with lib/work-state.ts (in progress, on hold) and a lease
 * follows the work item it is for. Profiles that hold nothing stay as idle
 * rows. Order: holders by how much they hold, then idle profiles, and the
 * no-agent row last.
 */
export function regroupByHolder(rows: readonly ExecutorRow[]): ExecutorRow[] {
  const profiles = new Map<string, ExecutorRow>();
  for (const row of rows) {
    const key = agentKey(row.id);
    if (!profiles.has(key)) profiles.set(key, row);
  }
  const work = new Map<string, ExecutorWorkRef[]>();
  const keyOfWork = new Map<string, string>();
  for (const row of rows) {
    for (const ref of row.currentWork) {
      const key = holderKey(ref);
      if (ref.workId) {
        if (keyOfWork.has(ref.workId)) continue;
        keyOfWork.set(ref.workId, key);
      }
      work.set(key, [...(work.get(key) ?? []), ref]);
    }
  }
  const leases = new Map<string, ExecutorLease[]>();
  for (const row of rows) {
    for (const lease of row.leases) {
      const key = keyOfWork.get(lease.workId) ?? agentKey(row.id);
      leases.set(key, [...(leases.get(key) ?? []), lease]);
    }
  }
  const keys = new Set<string>([...work.keys(), ...leases.keys()]);
  const out: ExecutorRow[] = [];
  for (const key of keys) {
    const profile = profiles.get(key);
    const items = work.get(key) ?? [];
    const held = leases.get(key) ?? [];
    const ages = held.map((lease) => lease.ageSeconds).filter((age): age is number => age !== null);
    const next = profile?.nextAssigned && keyOfWork.get(profile.nextAssigned.workId) === key ? profile.nextAssigned : null;
    out.push({
      ...(profile ?? emptyRow(key)),
      id: key,
      label: profile?.label && key !== NO_AGENT_KEY ? profile.label : agentDisplayName(key),
      currentWork: items,
      assigned: items.length,
      running: items.filter((ref) => isInProgress(ref.state)).length,
      ready: items.filter((ref) => READY_STATES.has(ref.state)).length,
      needsMe: items.filter((ref) => isOnHold(ref.state)).length,
      leases: held,
      leaseCount: held.length,
      oldestLeaseAgeSeconds: ages.length ? Math.max(...ages) : null,
      nextAssigned: next,
    });
  }
  for (const [id, profile] of profiles) {
    if (keys.has(id) || id === NO_AGENT_KEY) continue;
    out.push({ ...profile, id, currentWork: [], assigned: 0, running: 0, ready: 0, needsMe: 0, leases: [], leaseCount: 0, oldestLeaseAgeSeconds: null, nextAssigned: null });
  }
  const rank = (row: ExecutorRow) => (row.id === NO_AGENT_KEY ? 2 : row.currentWork.length ? 0 : 1);
  return out.sort((a, b) => rank(a) - rank(b) || b.currentWork.length - a.currentWork.length || a.label.localeCompare(b.label));
}

/* ---------- delegated supervised-session board (pure) ----------
 * The reduced view of `compound executors --json` `delegated_sessions`: one
 * safe row per work+lane, folded server-side from work.delegate.{spawned,
 * steered,refused,exited}. Privacy law: NO prompt text, pane/stdout content,
 * credentials, argv, or provider env ever reaches these fields — only the
 * binding shape, the reduced status, the last steer TIME, and the exact
 * RunAttempt id/status. This file owns the shared type + the pure status→tone
 * mapping the board renders and the smoke asserts. */

export type DelegatedSessionStatus = "running" | "exited" | "refused" | "unknown";

export interface DelegatedProgress {
  done: number;
  empty: number;
  failed: number;
  remaining: number;
}

export interface DelegatedSessionRow {
  workId: string;
  lane: string;
  session: string;
  status: DelegatedSessionStatus;
  holder: string;
  runAttemptId: string;
  runAttemptStatus: string;
  progress: DelegatedProgress | null;
  refusedReason: string;
  spawnedAt: string;
  lastSteerAt: string;
  exitedAt: string;
  observedExit: string;
}

export interface DelegatedSessionCounts {
  total: number;
  running: number;
  exited: number;
  refused: number;
  unknown: number;
}

export type DelegatedTone = "live" | "done" | "seam" | "faint";

/** The rendered view of one delegated-session row: the status→tone/label
 *  mapping the board draws. Pure so the smoke reasons about the same values. */
export interface DelegatedSessionView {
  workId: string;
  status: DelegatedSessionStatus;
  statusLabel: string;
  tone: DelegatedTone;
  lane: string;
  session: string;
  holder: string;
  runAttemptId: string;
  /** exact "<ratt> · <status>" when both present, else whichever exists. */
  runAttemptLabel: string;
  progressLabel: string;
  /** the single most-relevant right-hand detail for the row's status. */
  detail: string;
  lastSteerAt: string;
}

const DELEGATED_STATUS_META: Record<DelegatedSessionStatus, { label: string; tone: DelegatedTone }> = {
  running: { label: "Running", tone: "live" },
  exited: { label: "Exited", tone: "done" },
  refused: { label: "Refused", tone: "seam" },
  unknown: { label: "Unknown", tone: "faint" },
};

function delegatedStatusOf(value: string): DelegatedSessionStatus {
  return value === "running" || value === "exited" || value === "refused" ? value : "unknown";
}

/** Pure per-row render model. The `detail` column names the status-relevant
 *  fact (refusal reason, observed exit, or run-attempt status) WITHOUT ever
 *  reaching for prompt/argv/message content. */
export function delegatedSessionView(row: DelegatedSessionRow): DelegatedSessionView {
  const status = delegatedStatusOf(row.status);
  const meta = DELEGATED_STATUS_META[status];
  const runAttemptLabel = row.runAttemptId
    ? row.runAttemptStatus
      ? `${row.runAttemptId} · ${row.runAttemptStatus}`
      : row.runAttemptId
    : "";
  const progressLabel = row.progress
    ? `${row.progress.done} done · ${row.progress.empty} empty · ${row.progress.failed} failed · ${row.progress.remaining} left`
    : "";
  let detail = "";
  if (status === "refused") detail = row.refusedReason;
  else if (status === "exited") detail = row.observedExit || row.runAttemptStatus;
  else if (status === "running") detail = row.runAttemptStatus;
  // An exit is green only when it finished: abandoned, vanished, failed or killed is not done.
  const unfinished = status === "exited" && /abandon|vanish|fail|kill|lost|crash|timeout|timed out/i.test(detail);
  return {
    workId: row.workId,
    status,
    statusLabel: unfinished ? "Ended unfinished" : meta.label,
    tone: unfinished ? "seam" : meta.tone,
    lane: row.lane,
    session: row.session,
    holder: row.holder,
    runAttemptId: row.runAttemptId,
    runAttemptLabel,
    progressLabel,
    detail,
    lastSteerAt: row.lastSteerAt,
  };
}

/** The board's ordered render models — server order preserved (running,
 *  unknown, refused, exited; ties by work id). */
export function delegatedSessionViews(rows: DelegatedSessionRow[]): DelegatedSessionView[] {
  return rows.map(delegatedSessionView);
}

/** A single stale lease surfaced to the hygiene band. */
export interface StaleLease {
  executorId: string;
  executorLabel: string;
  workId: string;
  ageSeconds: number;
}

export interface HygieneSummary {
  staleLeases: StaleLease[];
  oldestAgeSeconds: number;
  affectedExecutors: string[];
  totalLeaseCount: number;
}

/**
 * The lease-hygiene rule, shared by the band and any future consumer. A lease
 * is stale when its age exceeds the threshold. When the projection reports a
 * per-executor `oldest_lease_age_seconds` past the threshold but no per-lease
 * ages, we still surface the executor as affected (age known, ids not) so a
 * real staleness never hides behind a missing sub-array.
 */
export function summarizeHygiene(projection: ExecutorProjection): HygieneSummary {
  const threshold = projection.staleAfterSeconds;
  const staleLeases: StaleLease[] = [];
  const affected = new Set<string>();
  let oldest = 0;
  let totalLeaseCount = 0;

  for (const executor of projection.executors) {
    totalLeaseCount += executor.leaseCount;
    let executorHasStale = false;

    for (const lease of executor.leases) {
      if (lease.ageSeconds !== null && lease.ageSeconds > threshold) {
        staleLeases.push({
          executorId: executor.id,
          executorLabel: executor.label,
          workId: lease.workId,
          ageSeconds: lease.ageSeconds,
        });
        oldest = Math.max(oldest, lease.ageSeconds);
        executorHasStale = true;
      }
    }

    // Age known at the executor level but no per-lease ids: still affected.
    if (
      !executorHasStale &&
      executor.oldestLeaseAgeSeconds !== null &&
      executor.oldestLeaseAgeSeconds > threshold &&
      executor.leaseCount > 0
    ) {
      oldest = Math.max(oldest, executor.oldestLeaseAgeSeconds);
      executorHasStale = true;
    }

    if (executorHasStale) affected.add(executor.label);
  }

  return {
    staleLeases,
    oldestAgeSeconds: oldest,
    affectedExecutors: [...affected],
    totalLeaseCount,
  };
}

/** Compact duration label for lease age / hook recency. Mono-rendered. */
export function durationLabel(seconds: number | null): string {
  if (seconds === null) return "";
  if (seconds < 90) return `${Math.round(seconds)}s`;
  if (seconds < 90 * 60) return `${Math.round(seconds / 60)}m`;
  if (seconds < 36 * 3600) return `${Math.round(seconds / 3600)}h`;
  return `${Math.round(seconds / 86400)}d`;
}

/** Client-safe truncation (mirrors work-data.shortText, without its
 *  compound.ts dependency, so the client table can trim titles). */
export function truncate(value: string, max = 150): string {
  const clean = value.trim();
  if (clean.length <= max) return clean;
  return `${clean.slice(0, Math.max(0, max - 1)).trimEnd()}…`;
}

/* ---------- the two DISTINCT stale-lease definitions ----------
 * The fleet reports "stale leases" two different ways and the operator must
 * never see the two numbers side by side without the UI naming which is which.
 * Both labels live HERE (one source) so the briefing self-healing row (history)
 * and the Agents lease-hygiene band (threshold) can never render the same
 * conflatable phrase. The consistency smoke imports these and asserts each names
 * its own population.
 *
 *  - HISTORY / residue: lease files still on disk whose work is terminal,
 *    archived, or missing. Age-INDEPENDENT. This is `home doctor`
 *    operational_hygiene.stale_lease_count === `executors` counts.stale_leases
 *    (one population, two doors). Cleared by `home archive-history --kind lease`.
 *  - THRESHOLD / live: among ACTIVE (non-terminal) leases, those held longer
 *    than the staleness threshold. Age-DEPENDENT. This is what summarizeHygiene
 *    counts and the Agents band renders.
 */

/** Label for the HISTORY / residue population (briefing self-healing row). */
export const LEASE_HISTORY_LABEL = "Unarchived lease history";

/** Plain-language sub-line for the HISTORY / residue population. Not rendered by
 *  the briefing panel (label-only), but the Agents band shows it so the two
 *  numbers coexist and the operator sees why they differ. */
export const LEASE_HISTORY_SUB =
  "lease files left behind by finished work — not a live executor problem; archive-history clears them";

/** Label for the THRESHOLD / live population (Agents lease-hygiene band). */
export function leaseThresholdLabel(staleAfterSeconds: number): string {
  const hours = Math.max(1, Math.round(staleAfterSeconds / 3600));
  return `live leases past the ${hours}h threshold`;
}

/** Plain-language sub-line for the THRESHOLD / live population. */
export function leaseThresholdSub(staleAfterSeconds: number): string {
  const hours = Math.max(1, Math.round(staleAfterSeconds / 3600));
  return `A live lease held longer than ${hours}h may be stuck — clear it to let the fleet read true.`;
}

/** The exact count the hygiene band renders, kept here so the band and the
 *  consistency smoke agree byte-for-byte on the rule (per-lease stale ids, or
 *  the executor-level fallback when only ages are known). */
export function hygieneStaleCount(summary: HygieneSummary): number {
  return summary.staleLeases.length || summary.affectedExecutors.length;
}

/* ---------- lease-pressure viz model (pure) ----------
 * The bar geometry + severity for the Agents "Lease pressure" capsule column.
 * Kept pure and testable so the saturation falsifier (fixture ages that must NOT
 * collapse to identical full-height bars) reasons about the same numbers the
 * component draws.
 */

export type LeasePressureTone = "crit" | "seam" | "work";

export interface LeasePressureBar {
  id: string;
  /** full executor label (tooltip holder). */
  label: string;
  /** short axis label under the capsule. */
  shortLabel: string;
  ageSeconds: number;
  /** fill value in hours (CapsuleColumn `value`). */
  ageHours: number;
  /** true age, mono-rendered as the visible value label. */
  ageLabel: string;
  /** bounded-scale ceiling in hours (CapsuleColumn `max`). */
  ceilingHours: number;
  /** age / threshold — drives severity color. */
  ratio: number;
  tone: LeasePressureTone;
  overThreshold: boolean;
  leaseCount: number;
  /** the oldest lease's work id (tooltip). */
  oldestLeaseWorkId: string;
}

export interface LeasePressureModel {
  bars: LeasePressureBar[];
  thresholdHours: number;
}

function leaseToneFor(ratio: number): LeasePressureTone {
  return ratio >= 1 ? "crit" : ratio >= 0.75 ? "seam" : "work";
}

/** Which of an executor's active leases is the oldest (its age matches
 *  oldestLeaseAgeSeconds); the tooltip names it. */
function oldestLeaseWorkId(executor: ExecutorRow): string {
  let bestId = "";
  let bestAge = -1;
  for (const lease of executor.leases) {
    if (lease.ageSeconds !== null && lease.ageSeconds > bestAge) {
      bestAge = lease.ageSeconds;
      bestId = lease.workId;
    }
  }
  return bestId;
}

export function leasePressureBars(executors: ExecutorRow[], staleAfterSeconds: number): LeasePressureModel {
  const thresholdHours = Math.max(1, Math.round(staleAfterSeconds / 3600));
  const leased = executors.filter((e) => e.leaseCount > 0 && e.oldestLeaseAgeSeconds !== null);

  // Bounded scale: the track ceiling is the OLDEST age on screen (never below
  // the threshold), so the bars keep their relative shape when every age far
  // exceeds the threshold — instead of all pinning full-height. Severity color
  // still comes from the threshold ratio, so a young lease reads calm and an
  // over-threshold one reads hot regardless of the scale.
  const maxAgeSeconds = leased.reduce((max, e) => Math.max(max, e.oldestLeaseAgeSeconds ?? 0), 0);
  const ceilingHours = Math.max(thresholdHours, Math.ceil(maxAgeSeconds / 3600));

  const bars: LeasePressureBar[] = leased.map((executor) => {
    const ageSeconds = executor.oldestLeaseAgeSeconds ?? 0;
    const ageHours = Math.round((ageSeconds / 3600) * 10) / 10;
    const ratio = ageSeconds / staleAfterSeconds;
    return {
      id: executor.id || executor.label,
      label: executor.label,
      shortLabel: executor.label.length > 6 ? `${executor.label.slice(0, 6)}…` : executor.label,
      ageSeconds,
      ageHours,
      ageLabel: durationLabel(ageSeconds),
      ceilingHours,
      ratio,
      tone: leaseToneFor(ratio),
      overThreshold: ratio >= 1,
      leaseCount: executor.leaseCount,
      oldestLeaseWorkId: oldestLeaseWorkId(executor),
    };
  });

  return { bars, thresholdHours };
}

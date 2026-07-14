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
}

export interface ExecutorRow {
  id: string;
  label: string;
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
  leases: number;
}

export interface ExecutorProjection {
  generatedAt: string;
  authorityRef: string;
  sourceRefs: string[];
  nextCommand: string[];
  staleAfterSeconds: number;
  counts: ExecutorCounts;
  executors: ExecutorRow[];
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
export const LEASE_HISTORY_LABEL = "Stale executor leases";

/** Label for the THRESHOLD / live population (Agents lease-hygiene band). */
export function leaseThresholdLabel(_staleAfterSeconds: number): string {
  return "stale leases";
}

/** Plain-language sub-line for the THRESHOLD / live population. */
export function leaseThresholdSub(_staleAfterSeconds: number): string {
  return "A stale lease holds work no one is running — clear it to let the fleet read true.";
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
      // Saturating scale (measured against the threshold): every age past the
      // threshold pins full-height. Bounded-scale fix lands in the viz repair.
      ceilingHours: thresholdHours,
      ratio,
      tone: leaseToneFor(ratio),
      overThreshold: ratio >= 1,
      leaseCount: executor.leaseCount,
      oldestLeaseWorkId: oldestLeaseWorkId(executor),
    };
  });

  return { bars, thresholdHours };
}

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
 *  threshold) is stale — the amber "needs you" hygiene class. 48h in seconds. */
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

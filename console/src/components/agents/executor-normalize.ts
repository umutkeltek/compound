import { arr, num, str, type Json } from "@/lib/compound";
import { workTitleFull } from "@/lib/work-title";

import {
  LEASE_STALE_AFTER_SECONDS,
  regroupByHolder,
  type DelegatedSessionCounts,
  type DelegatedSessionRow,
  type DelegatedSessionStatus,
  type ExecutorLease,
  type ExecutorProjection,
  type ExecutorRow,
  type ExecutorWorkRef,
} from "./executor-model";

/**
 * Server-only parser for `compound executors --json`
 * (schema `compound.executors_projection.v0.1`). Imports `@/lib/compound`
 * (which pulls node built-ins), so this file must never be imported by a
 * client component — only the server page composes it. The pure model + shared
 * rules live in `./executor-model`.
 */

function optionalNumber(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function stringList(value: unknown): string[] {
  return arr(value)
    .map((item) => str(item))
    .filter(Boolean);
}

function workRef(value: unknown): ExecutorWorkRef | null {
  if (!value || typeof value !== "object") return null;
  const work = value as Json;
  const workId = str(work.work_id);
  const title = str(work.title) || str(work.success_predicate) || str(work.desired_outcome) || workId;
  if (!workId && !title) return null;
  return {
    workId,
    title,
    state: str(work.state),
    nextAction: str(work.next_action),
    owner: str(work.owner),
    claimedBy: str(work.claimed_by),
    claimedAt: str(work.claimed_at),
  };
}

/**
 * The Work page's open list (`workbench --json` `open[]`) as work refs: the
 * population every Agents number is counted from, so a number and the Work
 * List it opens come from one read. Titles use the List's own wording
 * (lib/work-title.ts), so an item reads the same on both pages.
 */
export function workRefsFromWorkbench(open: readonly unknown[]): ExecutorWorkRef[] {
  return open
    .map((value) => {
      const ref = workRef(value);
      return ref ? { ...ref, title: workTitleFull(value as Json) } : null;
    })
    .filter((ref): ref is ExecutorWorkRef => ref !== null);
}

function leaseList(value: unknown): ExecutorLease[] {
  return arr(value)
    .map((raw) => {
      const lease = raw as Json;
      const workId = str(lease.work_id);
      if (!workId) return null;
      return { workId, ageSeconds: optionalNumber(lease.age_seconds) } satisfies ExecutorLease;
    })
    .filter((lease): lease is ExecutorLease => lease !== null);
}

function normalizeRow(raw: Json): ExecutorRow {
  const lastHookEvent =
    raw.last_hook_event && typeof raw.last_hook_event === "object" ? (raw.last_hook_event as Json) : null;

  return {
    id: str(raw.id),
    label: str(raw.label) || str(raw.id),
    aliases: stringList(raw.aliases),
    status: str(raw.status) || "unknown",
    statusSource: str(raw.status_source),
    waitReason: str(raw.wait_reason),
    bestFor: stringList(raw.best_for),
    canUseTools: stringList(raw.can_use_tools),
    costLimits: stringList(raw.cost_limits),
    running: num(raw.running_count),
    ready: num(raw.ready_count),
    assigned: num(raw.assigned_count),
    needsMe: num(raw.needs_me_count),
    leaseCount: num(raw.lease_count),
    oldestLeaseAgeSeconds: optionalNumber(raw.oldest_lease_age_seconds),
    leases: leaseList(raw.leases),
    routeEvidenceCount: num(raw.route_evidence_count),
    routeEvidencePresent: raw.route_evidence_present === true,
    lastHook: lastHookEvent
      ? {
          hook: str(lastHookEvent.hook),
          status: str(lastHookEvent.status),
          createdAt: str(lastHookEvent.created_at),
        }
      : null,
    currentWork: arr(raw.current_work)
      .map(workRef)
      .filter((work): work is ExecutorWorkRef => work !== null),
    nextAssigned: workRef(raw.next_assigned),
    projectionNote: str(raw.projection_note),
  };
}

function delegatedStatus(value: unknown): DelegatedSessionStatus {
  const raw = str(value);
  return raw === "running" || raw === "exited" || raw === "refused" ? raw : "unknown";
}

function delegatedProgress(value: unknown): DelegatedSessionRow["progress"] {
  if (!value || typeof value !== "object") return null;
  const raw = value as Json;
  const count = (input: unknown) => Math.max(0, Math.trunc(num(input)));
  return {
    done: count(raw.done),
    empty: count(raw.empty),
    failed: count(raw.failed),
    remaining: count(raw.remaining),
  };
}

/**
 * Whitelist parser for one delegated-session row. It reads ONLY the safe,
 * reduced fields the projection exposes — never any prompt/argv/message key,
 * even if a future server accidentally emitted one, because it never spreads
 * the raw object. This is the privacy seam on the console side.
 */
function delegatedRow(value: unknown): DelegatedSessionRow | null {
  if (!value || typeof value !== "object") return null;
  const raw = value as Json;
  const workId = str(raw.work_id);
  if (!workId) return null;
  return {
    workId,
    lane: str(raw.lane),
    session: str(raw.session),
    status: delegatedStatus(raw.status),
    holder: str(raw.holder),
    runAttemptId: str(raw.run_attempt_id),
    runAttemptStatus: str(raw.run_attempt_status),
    progress: delegatedProgress(raw.progress),
    refusedReason: str(raw.refused_reason),
    spawnedAt: str(raw.spawned_at),
    lastSteerAt: str(raw.last_steer_at),
    exitedAt: str(raw.exited_at),
    observedExit: str(raw.observed_exit),
  };
}

function delegatedCounts(rows: DelegatedSessionRow[], raw: unknown): DelegatedSessionCounts {
  const source = (raw && typeof raw === "object" ? raw : {}) as Json;
  // Prefer the server's counts; fall back to a recount of the parsed rows so a
  // stale/absent counts block never disagrees with what the board renders.
  const tally = { total: rows.length, running: 0, exited: 0, refused: 0, unknown: 0 };
  for (const row of rows) tally[row.status] += 1;
  const has = typeof source.total === "number";
  return has
    ? {
        total: num(source.total),
        running: num(source.running),
        exited: num(source.exited),
        refused: num(source.refused),
        unknown: num(source.unknown),
      }
    : tally;
}

/**
 * Parse `executors --json`. Pass `openWork` (the Work page's open list,
 * `workbench --json` `open[]`) to count every agent's work from the read the
 * Work List shows; the executors read then gives only each agent's profile
 * (name, tools, leases, last hook). Without it the projection's own
 * `current_work` is used.
 */
export function normalizeExecutorProjection(projection: Json | null, openWork?: readonly unknown[] | null): ExecutorProjection | null {
  if (!projection) return null;
  const counts = (projection.counts && typeof projection.counts === "object" ? projection.counts : {}) as Json;
  // The projection carries no staleness threshold today (verified against the
  // live schema 2026-07-09); read it defensively if a future projection adds
  // one, else fall back to the 48h doctrine default.
  const staleAfterSeconds =
    optionalNumber(projection.lease_stale_after_seconds) ??
    optionalNumber(counts.lease_stale_after_seconds) ??
    LEASE_STALE_AFTER_SECONDS;

  const delegatedSessions = arr(projection.delegated_sessions)
    .map(delegatedRow)
    .filter((row): row is DelegatedSessionRow => row !== null);

  return {
    generatedAt: str(projection.generated_at),
    authorityRef: str(projection.authority_ref),
    sourceRefs: stringList(projection.source_refs),
    nextCommand: stringList(projection.next_command),
    staleAfterSeconds,
    counts: {
      total: num(counts.total),
      open: num(counts.open),
      running: num(counts.running),
      ready: num(counts.ready),
      needsMe: num(counts.needs_me),
      assigned: num(counts.assigned),
      leases: num(counts.leases),
      staleHistory: num(counts.stale_leases),
    },
    executors: regroupByHolder(arr(projection.executors).map(normalizeRow), openWork ? workRefsFromWorkbench(openWork) : undefined),
    delegatedSessions,
    delegatedCounts: delegatedCounts(delegatedSessions, projection.delegated_counts),
  };
}

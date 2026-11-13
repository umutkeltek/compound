import { type LiveRead, readExecutors } from "@/lib/compound";
import { normalizeExecutorProjection } from "@/components/agents/executor-normalize";
import {
  type ExecutorProjection,
  type ExecutorRow,
  type ExecutorWorkRef,
} from "@/components/agents/executor-model";
import { getRunAttempts, retryDepth, type RunAttempt } from "@/lib/run-attempts";

/*
 * Traffic projection — the real-time board's SEED/RECONCILE data plane.
 * Joins the executor fleet projection (who is on what) with the run-attempt
 * evidence (how each in-flight work is being executed: harness/status/actor/
 * transcript/retry depth). buildTraffic is PURE (synthetic inputs in the
 * selftest); getTraffic is the server I/O wrapper. The live push plane
 * (lib/stream/useLiveFleet.ts) overlays stream moves on top of this seed.
 *
 * Honesty: when the run-attempt door is unreachable (attempts === null,
 * e.g. an older binary without the verb), runAttemptsPresent is false and the
 * board renders "execution detail unavailable" — never an invented harness.
 */

export type TrafficWork = {
  workId: string;
  title: string;
  state: string;
  nextAction: string;
  /** execution layer — "" means "unknown" (no run-attempt joined). */
  harness: string;
  runStatus: string;
  actor: string;
  transcriptRef: string;
  retryDepth: number;
  startedAt: string;
  lastHeartbeatAt: string;
};

export type TrafficLane = {
  agentId: string;
  agentLabel: string;
  agentStatus: string;
  running: number;
  needsMe: number;
  inFlight: TrafficWork[];
};

export type TrafficReport = {
  generatedAt: string;
  lanes: TrafficLane[];
  inFlightCount: number;
  runningCount: number;
  needsMeCount: number;
  /** false => run-attempt door unreachable; board shows "execution detail unavailable". */
  runAttemptsPresent: boolean;
  /**
   * The executors read behind this report (getTraffic only; null from the
   * pure builder). While it has no copy (`loading` / `unreachable`) every
   * count above is UNKNOWN, not zero: callers must render the read state, not
   * the numbers.
   */
  read: LiveRead | null;
};

/** True when the counts are real (an executors copy exists). */
export function trafficKnown(report: TrafficReport): boolean {
  return report.read === null || report.read.status === "live" || report.read.status === "stale";
}

/** Run-attempt statuses that are terminal (everything else is in flight). */
const RUN_STATUS_TERMINAL = new Set(["succeeded", "failed", "cancelled", "canceled", "swept", "abandoned", "superseded"]);

/** Pick the attempt that best represents HOW a work is executing: one that
 *  actually ran (has a harness/command), prefer still-running, else latest. */
function representativeAttempt(attempts: RunAttempt[]): RunAttempt | null {
  if (attempts.length === 0) return null;
  const withExec = attempts.filter((a) => a.harness || a.command);
  const pool = withExec.length ? withExec : attempts;
  const nonTerminal = pool.filter((a) => !RUN_STATUS_TERMINAL.has(a.status));
  const candidates = nonTerminal.length ? nonTerminal : pool;
  let best = candidates[0];
  for (const c of candidates) {
    if ((c.createdAt || "") > (best.createdAt || "")) best = c;
  }
  return best ?? null;
}

function toTrafficWork(
  work: ExecutorWorkRef,
  rep: RunAttempt | null,
  byId: Map<string, RunAttempt>,
): TrafficWork {
  return {
    workId: work.workId,
    title: work.title || work.workId,
    state: work.state,
    nextAction: work.nextAction,
    harness: rep?.harness ?? "",
    runStatus: rep?.status ?? "",
    actor: rep?.actor ?? "",
    transcriptRef: rep?.transcriptRef ?? "",
    retryDepth: rep ? retryDepth(rep, byId) : 0,
    startedAt: rep?.createdAt ?? "",
    lastHeartbeatAt: rep?.lastHeartbeatAt ?? "",
  };
}

function toLane(
  row: ExecutorRow,
  byWork: Map<string, RunAttempt[]>,
  byId: Map<string, RunAttempt>,
): TrafficLane {
  return {
    agentId: row.id,
    agentLabel: row.label || row.id,
    agentStatus: row.status,
    running: row.running,
    needsMe: row.needsMe,
    inFlight: row.currentWork.map((work) => {
      const rep = representativeAttempt(byWork.get(work.workId) ?? []);
      return toTrafficWork(work, rep, byId);
    }),
  };
}

export function buildTraffic(
  projection: ExecutorProjection | null,
  attempts: RunAttempt[] | null,
): TrafficReport {
  const byId = new Map<string, RunAttempt>();
  const byWork = new Map<string, RunAttempt[]>();
  for (const a of attempts ?? []) {
    byId.set(a.id, a);
    if (!a.workId) continue;
    const list = byWork.get(a.workId) ?? [];
    list.push(a);
    byWork.set(a.workId, list);
  }

  const lanes = (projection?.executors ?? []).map((row) => toLane(row, byWork, byId));
  const inFlightCount = lanes.reduce((n, lane) => n + lane.inFlight.length, 0);
  const runningCount = lanes.reduce((n, lane) => n + lane.running, 0);
  const needsMeCount = lanes.reduce((n, lane) => n + lane.needsMe, 0);

  return {
    generatedAt: projection?.generatedAt ?? "",
    lanes,
    inFlightCount,
    runningCount,
    needsMeCount,
    runAttemptsPresent: attempts !== null,
    read: null,
  };
}

/** Never waits for compound: the executors read is the non-blocking
 * `readExecutors` (a cached copy, or `loading`). */
export async function getTraffic(): Promise<TrafficReport> {
  const [read, attempts] = await Promise.all([readExecutors(), getRunAttempts()]);
  const projection = normalizeExecutorProjection(read.value);
  return { ...buildTraffic(projection, attempts), read };
}

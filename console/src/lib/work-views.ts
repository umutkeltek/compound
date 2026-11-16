import type { Json } from "@/lib/compound";
import { str } from "@/lib/compound";
import type { TrafficReport, TrafficWork } from "@/lib/traffic";
import { stageProgressForState, type StageProgress } from "@/lib/work-pipeline";
import { ago } from "@/lib/time";
import { shortText, workId } from "@/lib/work-data";
import { agentDisplayName, harnessDisplayName } from "@/lib/harness-label";
import { plainWorkStep } from "@/lib/plain-words";
import { isStale, timestampMs } from "@/lib/work-state";
import { workTitle } from "@/lib/work-title";

/** Three presentations over one WorkEnvelope + executor + RunAttempt join. */
export type WorkLensItem = {
  workId: string;
  title: string;
  state: string;
  owner: string;
  nextAction: string;
  canonicalSurface: string;
  ageLabel: string;
  laneId: string;
  laneLabel: string;
  harness: string;
  harnessLabel: string;
  runStatus: string;
  actor: string;
  retryDepth: number;
  startedAt: string;
  lastHeartbeatAt: string;
  progress: StageProgress;
  /** the agent's one name (lib/harness-label.ts) */
  ownerName: string;
  /** its app, codebase or "not linked" group (lib/work-project.ts), "" while not read */
  projectId: string;
  projectKind: string;
  projectName: string;
  /** in progress and picked up more than a week ago (lib/work-state.ts `isStale`) */
  stale: boolean;
  /** pick-up time in epoch ms, null when unknown */
  pickedUpMs: number | null;
};

/** Which group a work item is in, from the shared index (lib/compound.ts `readWorkProjectIndex`). */
export type WorkGroupOf = (workId: string) => { id: string; name: string; kind: string } | null;

export type WorkLensGroup = { id: string; label: string; status: string; items: WorkLensItem[] };

/** Claimant first (who holds it now), then the accountable owner. */
function ownerOf(item: Json): string {
  return str(item.claimed_by) || str(item.owner);
}

/** "claude:cf1" and "claude:cf2" share the "claude" lane; empty = unassigned. */
function ownerLaneId(item: Json): string {
  const owner = ownerOf(item);
  return owner ? `owner:${owner.split(":")[0]}` : "unassigned";
}

function ownerLaneLabel(item: Json): string {
  const owner = ownerOf(item);
  return owner ? owner.split(":")[0] : "Unassigned";
}

function byWork(report: TrafficReport): Map<string, { laneId: string; laneLabel: string; work: TrafficWork }> {
  const out = new Map<string, { laneId: string; laneLabel: string; work: TrafficWork }>();
  for (const lane of report.lanes) {
    for (const work of lane.inFlight) {
      if (!out.has(work.workId)) out.set(work.workId, { laneId: lane.agentId, laneLabel: lane.agentLabel, work });
    }
  }
  return out;
}

/**
 * One List row per open work item. Pass the clock (`nowMs`) for the stale
 * mark and the group lookup when the shared index is read; without it a
 * row's group is "" (unknown), never "not linked".
 */
export function buildWorkLensItems(open: Json[], traffic: TrafficReport, opts: { nowMs: number; groupOf?: WorkGroupOf }): WorkLensItem[] {
  const execution = byWork(traffic);
  return open.map((item) => {
    const id = workId(item);
    const joined = execution.get(id);
    const run = joined?.work;
    const group = opts.groupOf?.(id) ?? null;
    const age = str(item.claimed_at) || str(item.created_at) || str(item.updated_at);
    return {
      workId: id,
      title: workTitle(item),
      state: str(item.state) || "unknown",
      owner: ownerOf(item) || "Unassigned",
      // The tool's step in the board's words (lib/plain-words.ts), mapped
      // before it is shortened: the patterns match the whole instruction.
      nextAction: shortText(plainWorkStep(str(item.next_action)), 200),
      canonicalSurface: str(item.canonical_surface),
      ageLabel: age ? ago(age) : "age unavailable",
      // The lane is the executor lane that runs the item. When the executor
      // join has no row for it (executors not read yet, or no lane runs it),
      // fall back to the item's own claimant/owner: the data has owners, so
      // "Unassigned" is only for an item that truly has neither.
      laneId: joined?.laneId ?? ownerLaneId(item),
      laneLabel: joined?.laneLabel ?? ownerLaneLabel(item),
      harness: run?.harness ?? "",
      harnessLabel: harnessDisplayName(run?.harness ?? ""),
      runStatus: run?.runStatus ?? "",
      actor: run?.actor ?? "",
      retryDepth: run?.retryDepth ?? 0,
      startedAt: run?.startedAt ?? "",
      lastHeartbeatAt: run?.lastHeartbeatAt ?? "",
      progress: stageProgressForState(str(item.state)),
      ownerName: agentDisplayName(ownerOf(item)),
      projectId: group?.id ?? "",
      projectKind: group?.kind ?? "",
      projectName: group?.name ?? "",
      stale: isStale(item, opts.nowMs),
      pickedUpMs: timestampMs(item.claimed_at),
    };
  });
}

export function groupWorkLensItems(items: WorkLensItem[], traffic: TrafficReport): WorkLensGroup[] {
  const groups = traffic.lanes.map((lane) => ({
    id: lane.agentId,
    label: lane.agentLabel,
    status: lane.agentStatus,
    items: items.filter((item) => item.laneId === lane.agentId),
  }));
  const assigned = new Set(groups.flatMap((group) => group.items.map((item) => item.workId)));
  // Items no executor lane reports are grouped by their own owner (same
  // fallback as the item's lane), and only items with no owner at all are
  // "Unassigned".
  const byOwner = new Map<string, WorkLensGroup>();
  for (const item of items) {
    if (assigned.has(item.workId)) continue;
    const group = byOwner.get(item.laneId) ?? { id: item.laneId, label: item.laneLabel, status: "waiting", items: [] };
    group.items.push(item);
    byOwner.set(item.laneId, group);
  }
  const rest = [...byOwner.values()].sort((a, b) => (a.id === "unassigned" ? 1 : b.id === "unassigned" ? -1 : b.items.length - a.items.length));
  return [...groups, ...rest].filter((group) => group.items.length > 0);
}

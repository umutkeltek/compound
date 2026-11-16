import { buildRoster, oldestNotMoved } from "@/components/agents/agent-roster";
import { AgentsAnswer } from "@/components/agents/AgentsAnswer";
import { CostLanes } from "@/components/agents/CostLanes";
import { DelegatedSessions } from "@/components/agents/DelegatedSessions";
import type { ExecutorProjection } from "@/components/agents/executor-model";
import { normalizeExecutorProjection } from "@/components/agents/executor-normalize";
import { ExecutorTable } from "@/components/agents/ExecutorTable";
import { HygieneBand } from "@/components/agents/HygieneBand";
import { LocalModelsTile } from "@/components/agents/LocalModelsTile";
import { hasCopy, PageHeader, ReadChip, ReadFailedBanner, ReadNotice, readsPending } from "@/components/kit";
import { PendingRefresh } from "@/components/PendingRefresh";
import { getWorktreeInventory } from "@/lib/compound";
import { getTraffic } from "@/lib/traffic";
import { WorktreeInventory } from "@/components/agents/WorktreeInventory";

export const dynamic = "force-dynamic";

/**
 * All agents: who holds what, how much is in progress, what has not moved,
 * what needs you. The numbers are the ones Today and Working now show
 * (components/agents/agent-roster.ts over lib/counts.ts), one row per agent
 * key, and each number opens the Work list of exactly those items. The read
 * is non-blocking (`readExecutors` through getTraffic): a cold page says it
 * is reading, never shows an empty fleet.
 */

function Provenance({ projection }: { projection: ExecutorProjection }) {
  return (
    <div className="rounded-lg border border-hair bg-soft px-4 py-3 text-2xs leading-relaxed text-faint">
      <span className="font-bold uppercase tracking-wide">Authority</span>{" "}
      <span className="mono text-muted">{projection.authorityRef || "—"}</span>
      {projection.sourceRefs.length ? (
        <>
          {" · "}
          <span className="font-bold uppercase tracking-wide">Sources</span>{" "}
          <span className="mono text-muted">{projection.sourceRefs.join(", ")}</span>
        </>
      ) : null}
      {projection.nextCommand.length ? (
        <>
          {" · "}
          <span className="mono text-muted">{projection.nextCommand.join(" ")}</span>
        </>
      ) : null}
    </div>
  );
}

export default async function AgentsPage() {
  const [traffic, worktrees] = await Promise.all([getTraffic(), getWorktreeInventory()]);
  const read = traffic.read!;
  const projection = hasCopy(read) ? normalizeExecutorProjection(read.value) : null;
  const nowMs = Date.now();
  const roster = projection ? buildRoster(projection.executors, nowMs, 6) : null;

  return (
    <div className="flex flex-col gap-6">
      <PendingRefresh pending={readsPending(read)} stamp={Date.now()} />
      <PageHeader
        name="All agents"
        answer={roster ? <AgentsAnswer roster={roster} runRecordsVisible={traffic.runAttemptsPresent} /> : <ReadNotice read={read} label="who works on what" />}
        right={<ReadChip read={read} />}
      />
      <ReadFailedBanner read={read} label="who works on what" />
      <WorktreeInventory {...worktrees} />

      {projection && roster ? (
        <>
          <HygieneBand
            staleCount={roster.inProgress.pickedUpOver7Days}
            oldest={oldestNotMoved(projection.executors, nowMs, 10)}
            staleHistory={projection.counts.staleHistory}
          />

          {roster.holders.length ? (
            <ExecutorTable agents={roster.holders} />
          ) : (
            <div className="rounded-lg border border-dashed border-hair bg-page px-6 py-12 text-center">
              <p className="text-sm text-muted">No agent holds any work right now.</p>
            </div>
          )}
          {roster.idle.length ? (
            <p className="text-sm text-muted">
              Set up but holding no work: {roster.idle.map((agent) => agent.name).join(", ")}.
            </p>
          ) : null}

          <DelegatedSessions rows={projection.delegatedSessions} counts={projection.delegatedCounts} />

          <div className="grid gap-4 lg:grid-cols-2">
            <CostLanes executors={projection.executors} generatedAt={projection.generatedAt} />
            <LocalModelsTile />
          </div>

          <Provenance projection={projection} />
        </>
      ) : null}
    </div>
  );
}

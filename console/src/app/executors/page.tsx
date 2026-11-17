import { buildRoster, oldestNotMoved } from "@/components/agents/agent-roster";
import { AgentsAnswer } from "@/components/agents/AgentsAnswer";
import { DelegatedSessions } from "@/components/agents/DelegatedSessions";
import type { ExecutorProjection } from "@/components/agents/executor-model";
import { ExecutorTable } from "@/components/agents/ExecutorTable";
import { HygieneBand } from "@/components/agents/HygieneBand";
import Link from "next/link";

import { ForAgents } from "@/components/settings/primitives";
import { hasCopy, PageHeader, ReadChip, ReadFailedBanner, ReadNotice, readsPending } from "@/components/kit";
import { PendingRefresh } from "@/components/PendingRefresh";
import { getWorktreeInventory } from "@/lib/compound";
import { readAttention } from "@/lib/attention";
import { waitingWork } from "@/lib/counts";
import { readAgentsWork } from "@/lib/traffic";
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
    <ForAgents cmds={projection.nextCommand.length ? [projection.nextCommand.join(" ")] : []} label="For agents: where these numbers come from">
      <p className="mono text-2xs text-faint">{projection.authorityRef || "—"}{projection.sourceRefs.length ? ` · ${projection.sourceRefs.join(", ")}` : ""}</p>
    </ForAgents>
  );
}

export default async function AgentsPage() {
  const [agentsWork, worktrees, attention] = await Promise.all([readAgentsWork(), getWorktreeInventory(), readAttention()]);
  const { traffic, executorsRead: read, workRead, open, projection } = agentsWork;
  const nowMs = Date.now();
  // Every number is counted over the Work page's own open list, so it equals the Work list it opens.
  // "Need you" is the waiting list's own sorting (lib/counts.ts `waitingWork`), as on Today, Decide and Work.
  const roster = projection ? buildRoster(projection.executors, nowMs, 6, { openWork: open, waiting: waitingWork(attention.report) }) : null;
  // The read still missing when the numbers cannot be counted yet: the open work first.
  const missingRead = hasCopy(workRead) ? read : workRead;

  return (
    <div className="flex flex-col gap-6">
      <PendingRefresh pending={readsPending(read, workRead, attention.read)} stamp={Date.now()} />
      <PageHeader
        name="All agents"
        answer={roster ? <AgentsAnswer roster={roster} runRecordsVisible={traffic.runAttemptsPresent} /> : <ReadNotice read={missingRead} label="who works on what" />}
        right={<ReadChip read={workRead} />}
      />
      <ReadFailedBanner read={workRead} label="open work" />
      <ReadFailedBanner read={read} label="who is set up" />
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

          <p className="text-sm text-muted">
            What each model costs is on{" "}
            <Link href="/routing?tab=traffic" className="font-semibold text-work hover:underline">Models and cost</Link>.
          </p>

          <DelegatedSessions rows={projection.delegatedSessions} counts={projection.delegatedCounts} />

          <Provenance projection={projection} />
        </>
      ) : null}
    </div>
  );
}

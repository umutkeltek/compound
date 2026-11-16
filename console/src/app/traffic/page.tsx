import { buildRoster } from "@/components/agents/agent-roster";
import { AgentsSentence, InProgressSentence } from "@/components/agents/AgentsAnswer";
import { WaitingPanel } from "@/components/coordination/WaitingPanel";
import { hasCopy, PageHeader, ReadChip, ReadFailedBanner, ReadNotice, readsPending } from "@/components/kit";
import { PendingRefresh } from "@/components/PendingRefresh";
import { readCoordination } from "@/lib/coordination";
import { readAgentsWork } from "@/lib/traffic";

export const dynamic = "force-dynamic";

/**
 * Waiting on each other: how many agent sessions wait, what most of them wait
 * for in plain words, and the agents whose sessions hold work, each linked to
 * its Work list. It reads `compound coordination --json` through the
 * non-blocking reader (a cold read says it is reading, never an empty list),
 * shows one freshness chip, and closes with the same agent and in-progress
 * numbers as Working now and All agents (lib/counts.ts), so the three pages
 * agree. The old per-session cards and the "view as" switch are gone: every
 * card said the same thing and nothing on them could be acted on.
 */
export default async function WaitingPage() {
  const [coordination, agentsWork] = await Promise.all([readCoordination(), readAgentsWork()]);
  const { executorsRead: executors, workRead, open, projection } = agentsWork;
  // Counted over the Work page's own open list, as Working now and All agents count.
  const roster = projection ? buildRoster(projection.executors, Date.now(), 0, { openWork: open }) : null;

  return (
    <div className="flex flex-col gap-6">
      <PendingRefresh pending={readsPending(coordination.read, executors, workRead)} stamp={Date.now()} />
      <PageHeader name="Waiting on each other" right={<ReadChip read={coordination.read} />} />
      {!hasCopy(coordination.read) ? (
        <ReadNotice read={coordination.read} label="who waits on whom" />
      ) : (
        <>
          <ReadFailedBanner read={coordination.read} label="who waits on whom" />
          <WaitingPanel view={coordination.view} />
        </>
      )}
      {roster ? (
        <p className="max-w-read border-t border-hair pt-4 text-sm leading-relaxed text-muted">
          <AgentsSentence count={roster.count} /> <InProgressSentence c={roster.inProgress} />
        </p>
      ) : null}
    </div>
  );
}

import Link from "next/link";

import { buildRoster, latestPickedUp } from "@/components/agents/agent-roster";
import { AgentsAnswer } from "@/components/agents/AgentsAnswer";
import { DelegatedSessions } from "@/components/agents/DelegatedSessions";
import { WaitingPanel } from "@/components/coordination/WaitingPanel";
import { LiveHarnessCanvas, type LiveLane } from "@/components/live/LiveHarnessCanvas";
import { LiveOperationsTimeline, type TimelineRow } from "@/components/live/LiveOperationsTimeline";
import { hasCopy, PageHeader, PulseIcon, ReadChip, ReadFailedBanner, ReadNotice, readsPending, SectionHeader, StatusChip } from "@/components/kit";
import { PendingRefresh } from "@/components/PendingRefresh";
import { readAttention } from "@/lib/attention";
import { readCoordination } from "@/lib/coordination";
import { countWaitingWork, unit, waitingWork } from "@/lib/counts";
import { depthShows, requestDepth } from "@/lib/depth";
import { workListHref } from "@/lib/links";
import { readAgentsWork } from "@/lib/traffic";

export const dynamic = "force-dynamic";

/** The most agents the canvas draws; it still fits the page at that size. */
const CANVAS_LANES = 8;

async function LiveCoordination() {
  const { view, read } = await readCoordination();
  return (
    <section data-depth-min="technical" className="flex flex-col gap-3">
      <SectionHeader tone="route" icon={<PulseIcon />} label="Who waits on whom" sub="agent sessions and what they wait for" right={<ReadChip read={read} />} />
      {read.status === "loading" || read.status === "unreachable" ? (
        <ReadNotice read={read} label="who waits on whom" />
      ) : (
        <>
          <ReadFailedBanner read={read} label="who waits on whom" />
          <WaitingPanel view={view} />
        </>
      )}
    </section>
  );
}

/**
 * Live answers one question first: "Who is working on what right now?" The
 * answer is built from the executors read with the shared definitions
 * (lib/counts.ts): agents holding work, work items marked in progress and
 * their split, and how many were picked up more than a week ago. It says
 * plainly when the console cannot see live run records, so "running" is the
 * work record's word, not proof an agent is busy this minute.
 */
export default async function LivePage() {
  // Non-blocking: readAgentsWork reads the cached executors and open work copies (or "loading").
  // The coordination section shows only at the Technical depth; its read is
  // skipped entirely when hidden.
  const [agentsWork, depth, attentionLive] = await Promise.all([readAgentsWork(), requestDepth(), readAttention()]);
  const { traffic, executorsRead, workRead, open, projection } = agentsWork;
  // Which open work waits for you and which on something else: the waiting
  // list's own sorting (Today and Decide count it), counted over the same open
  // list the Work filters show, so each number equals the list it opens.
  const waitingIds = waitingWork(attentionLive.report);
  const waits = waitingIds && open ? countWaitingWork(open, waitingIds) : null;
  const showCoordination = depthShows(depth, "technical");
  const known = projection !== null;
  // The read still missing when the numbers cannot be counted yet: the open work first.
  const missingRead = hasCopy(workRead) ? executorsRead : workRead;
  const nowMs = Date.now();
  // One row per agent key, counted with lib/counts.ts (components/agents/agent-roster.ts)
  // over the Work page's own open list, so each number equals the Work list it opens.
  const roster = projection ? buildRoster(projection.executors, nowMs, 2, { openWork: open, waiting: waitingIds }) : null;
  const runOf = new Map(traffic.lanes.flatMap((lane) => lane.inFlight.map((work) => [work.workId, work] as const)));
  // The canvas holds the agents that hold the most; the rest are on All agents.
  const shown = (roster?.holders ?? []).slice(0, CANVAS_LANES);
  const hiddenHolders = (roster?.holders.length ?? 0) - shown.length;
  const lanes: LiveLane[] = shown.map((agent) => ({
    id: agent.key,
    label: agent.name,
    totalWork: agent.items,
    running: agent.inProgress.total,
    needsMe: agent.onHold,
    stale: agent.stale,
    href: agent.href,
    work: agent.top.map((work) => {
      const run = runOf.get(work.workId);
      return {
        workId: work.workId,
        title: work.title,
        state: work.state,
        stateLabel: work.stateLabel,
        nextAction: work.next,
        pickedUpAgo: work.pickedUpAgo,
        stale: work.stale,
        href: work.href,
        harness: run?.harness ?? "",
        runStatus: run?.runStatus ?? "",
        actor: run?.actor ?? "",
        retryDepth: run?.retryDepth ?? 0,
      };
    }),
  }));
  const latest = known && projection ? latestPickedUp(projection.executors, nowMs, 6) : null;
  const timelineRows: TimelineRow[] = (latest?.rows ?? []).map((work) => ({
    workId: work.workId,
    title: work.title,
    state: work.state,
    stateLabel: work.stateLabel,
    next: work.next,
    pickedUpAgo: work.pickedUpAgo,
    stale: work.stale,
    href: work.href,
    agentName: work.agentName,
    runStatus: runOf.get(work.workId)?.runStatus ?? "",
    harness: runOf.get(work.workId)?.harness ?? "",
  }));



  const delegatedRunning = projection?.delegatedCounts.running ?? 0;
  const pending = readsPending(executorsRead, workRead, attentionLive.read);

  const answer = !roster ? (
    <ReadNotice read={missingRead} label="who works on what" />
  ) : (
    <AgentsAnswer
      roster={roster}
      runRecordsVisible={traffic.runAttemptsPresent}
      extra={waits && waits.you + waits.others > 0 ? (
        <p className="text-sm text-muted">
          {waits.you > 0 ? (
            <Link href={workListHref({ filter: "blocked" })} className="font-semibold text-work hover:underline">
              {unit(waits.you, "work item")} {waits.you === 1 ? "waits" : "wait"} for your decision.
            </Link>
          ) : null}
          {waits.you > 0 && waits.others > 0 ? " " : null}
          {waits.others > 0 ? (
            <Link href={workListHref({ filter: "others" })} className="font-semibold text-work hover:underline">
              {unit(waits.others, waits.you > 0 ? "more work item" : "work item", waits.you > 0 ? "more work items" : "work items")} {waits.others === 1 ? "waits" : "wait"} on something else, not on you.
            </Link>
          ) : null}
        </p>
      ) : null}
    />
  );

  return (
    <div className="console-stage flex flex-col gap-6">
      <PendingRefresh pending={pending} stamp={Date.now()} />
      <PageHeader
        name="Live"
        lead="Follow the latest recorded activity from your agents."
        answer={answer}
        right={<ReadChip read={workRead} />}
      />

      {known ? <><ReadFailedBanner read={workRead} label="open work" /><ReadFailedBanner read={executorsRead} label="who is set up" /></> : null}

      <section data-depth-min="understand" className="flex flex-col gap-3">
        <SectionHeader
          tone="work"
          icon={<PulseIcon />}
          label="Who holds what"
          sub="each agent and its two most urgent work items · pick one for details"
        />
        {known ? <LiveHarnessCanvas lanes={lanes} /> : <ReadNotice read={missingRead} label="who holds what" />}
        {hiddenHolders > 0 ? (
          <p className="text-sm text-muted">
            {unit(hiddenHolders, "more agent")} {hiddenHolders === 1 ? "holds" : "hold"} less work and {hiddenHolders === 1 ? "is" : "are"} listed on{" "}
            <Link href="/executors" className="font-semibold text-work hover:underline">All agents</Link>.
          </p>
        ) : null}
      </section>

      <section data-depth-min="investigate" className="flex flex-col gap-3">
        <SectionHeader
          tone="work"
          icon={<PulseIcon />}
          label="Latest work picked up"
          sub="newest first · who holds it and what comes next"
        />
        {known ? <LiveOperationsTimeline rows={timelineRows} total={latest?.total ?? 0} allHref={workListHref({ filter: "picked" })} /> : <ReadNotice read={missingRead} label="the latest work picked up" />}
      </section>

      {showCoordination ? (
        <LiveCoordination />
      ) : null}

      {/* Technical depth only, like coordination: the list holds every session
       * ever started (hundreds), so it is not rendered while hidden. */}
      {projection && showCoordination ? (
        <div data-depth-min="technical" className="flex flex-col gap-3">
          <div className="flex items-center justify-between gap-3">
            <div>
              <div className="section-label">Sessions started by agents</div>
              <p className="mt-1 text-xs text-muted">Each session and its run id. What was asked is never shown here.</p>
            </div>
            <StatusChip tone={delegatedRunning > 0 ? "good" : "muted"} label={`${delegatedRunning} running`} />
          </div>
          <DelegatedSessions rows={projection.delegatedSessions} counts={projection.delegatedCounts} />
        </div>
      ) : null}
    </div>
  );
}

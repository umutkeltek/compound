import Link from "next/link";

import { buildRoster, latestPickedUp } from "@/components/agents/agent-roster";
import { AgentsAnswer } from "@/components/agents/AgentsAnswer";
import { DelegatedSessions } from "@/components/agents/DelegatedSessions";
import { normalizeExecutorProjection } from "@/components/agents/executor-normalize";
import { CoordinationBoard, type ExecDetail } from "@/components/coordination/CoordinationBoard";
import { LiveHarnessCanvas, type LiveLane } from "@/components/live/LiveHarnessCanvas";
import { LiveOperationsTimeline, type TimelineRow } from "@/components/live/LiveOperationsTimeline";
import { PageHeader, PulseIcon, ReadChip, ReadFailedBanner, ReadNotice, readsPending, SectionHeader, StatusChip } from "@/components/kit";
import { PendingRefresh } from "@/components/PendingRefresh";
import { readAttention } from "@/lib/attention";
import { readCoordination } from "@/lib/coordination";
import { unit, waitingOnYou } from "@/lib/counts";
import { depthShows, requestDepth } from "@/lib/depth";
import { workListHref } from "@/lib/links";
import { getTraffic, trafficKnown } from "@/lib/traffic";

export const dynamic = "force-dynamic";

/** The most agents the canvas draws; it still fits the page at that size. */
const CANVAS_LANES = 8;

async function LiveCoordination({ execDetail, runAttemptsPresent }: { execDetail: Record<string, ExecDetail>; runAttemptsPresent: boolean }) {
  const { view, read } = await readCoordination();
  return (
    <section data-depth-min="technical" className="flex flex-col gap-3">
      <SectionHeader tone="route" icon={<PulseIcon />} label="Who waits on whom" sub="this session · other sessions · what each waits for" right={<ReadChip read={read} />} />
      {read.status === "loading" || read.status === "unreachable" ? (
        <ReadNotice read={read} label="who waits on whom" />
      ) : (
        <>
          <ReadFailedBanner read={read} label="who waits on whom" />
          <CoordinationBoard
            view={view}
            doorStatus={read.status}
            doorError={read.error}
            execDetail={execDetail}
            runAttemptsPresent={runAttemptsPresent}
          />
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
  // Non-blocking: getTraffic reads the cached executors copy (or "loading").
  // The coordination section shows only at the Technical depth; its read is
  // skipped entirely when hidden.
  const [traffic, depth, attentionLive] = await Promise.all([getTraffic(), requestDepth(), readAttention()]);
  const waiting = waitingOnYou(attentionLive.report);
  const showCoordination = depthShows(depth, "technical");
  const executorsRead = traffic.read!;
  const known = trafficKnown(traffic);
  const projection = normalizeExecutorProjection(executorsRead.value);
  const nowMs = Date.now();
  // One row per agent key, counted with lib/counts.ts (components/agents/agent-roster.ts):
  // the same rows Today counts, and each row's numbers equal its Work list.
  const roster = known && projection ? buildRoster(projection.executors, nowMs, 2) : null;
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

  const execDetail: Record<string, ExecDetail> = {};
  for (const lane of traffic.lanes) {
    for (const work of lane.inFlight) {
      if (execDetail[work.workId]) continue;
      execDetail[work.workId] = {
        harness: work.harness,
        runStatus: work.runStatus,
        actor: work.actor,
        retryDepth: work.retryDepth,
      };
    }
  }

  const delegatedRunning = projection?.delegatedCounts.running ?? 0;
  const pending = readsPending(executorsRead, attentionLive.read);

  const answer = !roster ? (
    <ReadNotice read={executorsRead} label="who works on what" />
  ) : (
    <AgentsAnswer
      roster={roster}
      runRecordsVisible={traffic.runAttemptsPresent}
      extra={waiting && waiting.workItems > 0 ? (
        <p className="text-sm text-muted">
          <Link href={workListHref({ filter: "blocked" })} className="font-semibold text-work hover:underline">
            {unit(waiting.workItems, "work item")} {waiting.workItems === 1 ? "waits" : "wait"} for your decision.
          </Link>
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
        right={<ReadChip read={executorsRead} />}
      />

      {known ? <ReadFailedBanner read={executorsRead} label="who works on what" /> : null}

      <section data-depth-min="understand" className="flex flex-col gap-3">
        <SectionHeader
          tone="work"
          icon={<PulseIcon />}
          label="Who holds what"
          sub="each agent and its two most urgent work items · pick one for details"
        />
        {known ? <LiveHarnessCanvas lanes={lanes} /> : <ReadNotice read={executorsRead} label="who holds what" />}
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
        {known ? <LiveOperationsTimeline rows={timelineRows} total={latest?.total ?? 0} allHref={workListHref({ filter: "all" })} /> : <ReadNotice read={executorsRead} label="the latest work picked up" />}
      </section>

      {showCoordination ? (
        <LiveCoordination execDetail={execDetail} runAttemptsPresent={traffic.runAttemptsPresent} />
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

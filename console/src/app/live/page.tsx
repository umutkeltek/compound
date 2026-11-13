import Link from "next/link";

import { DelegatedSessions } from "@/components/agents/DelegatedSessions";
import { normalizeExecutorProjection } from "@/components/agents/executor-normalize";
import { CoordinationBoard, type ExecDetail } from "@/components/coordination/CoordinationBoard";
import { LiveHarnessCanvas, type LiveLane } from "@/components/live/LiveHarnessCanvas";
import { LiveOperationsTimeline } from "@/components/live/LiveOperationsTimeline";
import { PulseIcon, QuestionHeader, ReadChip, ReadFailedBanner, ReadNotice, readsPending, SectionHeader, StatusChip } from "@/components/kit";
import { PendingRefresh } from "@/components/PendingRefresh";
import { readAttention } from "@/lib/attention";
import { arr, type Json } from "@/lib/compound";
import { readCoordination } from "@/lib/coordination";
import { countAgents, countInProgress, describeAgents, describeInProgress, unit, waitingOnYou } from "@/lib/counts";
import { depthShows, requestDepth } from "@/lib/depth";
import { getTraffic, newestInFlight, trafficKnown } from "@/lib/traffic";

export const dynamic = "force-dynamic";

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
  const lanes: LiveLane[] = traffic.lanes.map((lane) => {
    const ranked = lane.inFlight.slice().sort((a, b) => {
      const rank = (value: typeof a) => value.runStatus === "running" || value.state === "running" ? 0 : value.state === "verifying" ? 1 : value.state === "blocked" || value.state === "held" ? 2 : 3;
      return rank(a) - rank(b);
    });
    return {
      id: lane.agentId,
      label: lane.agentLabel,
      status: lane.agentStatus,
      running: lane.running,
      needsMe: lane.needsMe,
      totalWork: lane.inFlight.length,
      work: ranked.slice(0, 2).map((work) => ({
        workId: work.workId,
        title: work.title,
        state: work.state,
        nextAction: work.nextAction,
        harness: work.harness,
        runStatus: work.runStatus,
        actor: work.actor,
        retryDepth: work.retryDepth,
      })),
    };
  });

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

  // One definition per number (lib/counts.ts), the same Today and Work use.
  // In-progress counts take the raw executors rows: they carry `claimed_at`.
  const rawWork: Json[] = known
    ? arr(executorsRead.value?.executors).flatMap((row: Json) => arr(row.current_work))
    : [];
  const agents = known && projection ? countAgents(projection.executors) : null;
  const inProgress = known ? countInProgress(rawWork, Date.now()) : null;
  const progress = inProgress ? describeInProgress(inProgress) : null;

  // The browser gets only the six rows the timeline shows, not every item.
  const newest = newestInFlight(traffic, 6);
  const delegatedRunning = projection?.delegatedCounts.running ?? 0;
  const pending = readsPending(executorsRead, attentionLive.read);

  const answer = !known || !agents || !progress ? (
    <ReadNotice read={executorsRead} label="who works on what" />
  ) : (
    <div className="flex flex-col gap-1.5">
      <p>{describeAgents(agents)}</p>
      <p className="text-base text-muted">
        <span className="text-ink">{progress.headline}</span>
        {progress.detail ? <>: {progress.detail}</> : "."}
      </p>
      {progress.age ? <p className="text-base text-seam">{progress.age}</p> : null}
      {!traffic.runAttemptsPresent ? (
        <p className="text-sm text-muted">
          This page cannot see the live run records, so it cannot tell which of these an agent is running this minute.
          &quot;Running&quot; is what the work record says.
        </p>
      ) : null}
      {waiting && waiting.workItems > 0 ? (
        <p className="text-sm text-muted">
          {unit(waiting.workItems, "work item")} {waiting.workItems === 1 ? "waits" : "wait"} for your decision.{" "}
          <Link href="/reviews" className="font-semibold text-work hover:underline">Open Decide →</Link>
        </p>
      ) : null}
    </div>
  );

  return (
    <div className="console-stage flex flex-col gap-6">
      <PendingRefresh pending={pending} stamp={Date.now()} />
      <QuestionHeader
        name="Live"
        question="Who is working on what right now?"
        answer={answer}
        right={
          <>
            <ReadChip read={executorsRead} />
            <Link href="/traffic" className="text-xs font-semibold text-work hover:underline">Who waits on whom →</Link>
          </>
        }
      />

      {known ? <ReadFailedBanner read={executorsRead} label="who works on what" /> : null}

      <section data-depth-min="understand" className="flex flex-col gap-3">
        <SectionHeader
          tone="work"
          icon={<PulseIcon />}
          label="Who holds what"
          sub="each agent and its two most active work items · pick one for details"
        />
        {known ? <LiveHarnessCanvas lanes={lanes} /> : <ReadNotice read={executorsRead} label="who holds what" />}
      </section>

      <section data-depth-min="investigate" className="flex flex-col gap-3">
        <SectionHeader
          tone="work"
          icon={<PulseIcon />}
          label="Latest work steps"
          sub="newest first · when each was last seen and what comes next"
        />
        {known ? <LiveOperationsTimeline report={newest.report} total={newest.total} /> : <ReadNotice read={executorsRead} label="the latest work steps" />}
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

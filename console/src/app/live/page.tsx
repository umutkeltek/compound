import Link from "next/link";

import { DelegatedSessions } from "@/components/agents/DelegatedSessions";
import { normalizeExecutorProjection } from "@/components/agents/executor-normalize";
import { CoordinationBoard, type ExecDetail } from "@/components/coordination/CoordinationBoard";
import { LiveHarnessCanvas, type LiveLane } from "@/components/live/LiveHarnessCanvas";
import { LiveOperationsTimeline } from "@/components/live/LiveOperationsTimeline";
import { PageHeader, PulseIcon, ReadChip, ReadFailedBanner, ReadNotice, readsPending, SectionHeader, StatusChip } from "@/components/kit";
import { PendingRefresh } from "@/components/PendingRefresh";
import { readAttention, waitingOnYou } from "@/lib/attention";
import { readCoordination } from "@/lib/coordination";
import { depthShows, requestDepth } from "@/lib/depth";
import { getTraffic, trafficKnown } from "@/lib/traffic";

export const dynamic = "force-dynamic";

/** `value` null = not read yet: shown as "…", never 0. */
function Metric({ label, value, note, tone = "ink" }: { label: string; value: number | null; note: string; tone?: "ink" | "good" | "seam" }) {
  const color = value === null ? "text-faint" : tone === "good" ? "text-good" : tone === "seam" ? "text-seam" : "text-ink";
  return (
    <div className="rounded-lg border border-hair bg-panel px-3 py-2.5 shadow-sm">
      <div className="section-label">{label}</div>
      <div className={`mono mt-1 text-xl tabular-nums ${color}`}>{value === null ? "…" : value.toLocaleString("en-US")}</div>
      <div className="mt-0.5 text-2xs text-faint">{note}</div>
    </div>
  );
}

async function LiveCoordination({ execDetail, runAttemptsPresent }: { execDetail: Record<string, ExecDetail>; runAttemptsPresent: boolean }) {
  const { view, read } = await readCoordination();
  return (
    <section data-depth-min="technical" className="flex flex-col gap-3">
      <SectionHeader tone="route" icon={<PulseIcon />} label="Coordination" sub="me · others · waiting-on" right={<ReadChip read={read} />} />
      {read.status === "loading" || read.status === "unreachable" ? (
        <ReadNotice read={read} label="coordination" />
      ) : (
        <>
          <ReadFailedBanner read={read} label="coordination" />
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

export default async function LivePage() {
  // Non-blocking: getTraffic reads the cached executors copy (or "loading").
  // The coordination section shows only at the Technical depth; its read is
  // skipped entirely when hidden.
  // "Waiting on you" uses the ONE definition (lib/attention.ts waitingOnYou)
  // the sidebar, Today and Work use; attention is already cached for the sidebar.
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

  const delegatedRunning = projection?.delegatedCounts.running ?? 0;
  const pending = readsPending(executorsRead, attentionLive.read);
  return (
    <div className="console-stage flex flex-col gap-6">
      <PendingRefresh pending={pending} stamp={Date.now()} />
      <PageHeader
        breadcrumb="live / overview"
        title="Live"
        right={
          <div className="flex items-center gap-2">
            {known ? (
              <StatusChip tone={traffic.runningCount > 0 ? "good" : "muted"} label={traffic.runningCount > 0 ? "activity now" : "quiet now"} pulse={traffic.runningCount > 0} />
            ) : null}
            <ReadChip read={executorsRead} />
          </div>
        }
      />
      <div className="flex flex-wrap items-end justify-between gap-3">
        <p className="max-w-2xl text-sm leading-relaxed text-muted">
          See which harness owns each unit of work, what is genuinely active, and where the system is waiting. Motion
          appears only when heartbeat or transition evidence says something is moving.
        </p>
        <Link href="/traffic" className="text-xs font-semibold text-work hover:underline">Open coordination detail</Link>
      </div>

      {known ? <ReadFailedBanner read={executorsRead} label="the harness projection" /> : null}

      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <Metric label="Harnesses" value={known ? traffic.lanes.length : null} note="projected lanes" />
        <Metric label="Running" value={known ? traffic.runningCount : null} note="heartbeat-backed" tone="good" />
        <Metric label="In flight" value={known ? traffic.inFlightCount : null} note="work assignments" />
        <Metric label="Waiting on you" value={waiting ? waiting.workItems : null} note="work items" tone="seam" />
      </div>

      <section data-depth-min="understand" className="flex flex-col gap-3">
        <SectionHeader
          tone="work"
          icon={<PulseIcon />}
          label="Harness workfield"
          sub="ownership → active WorkEnvelope → next seam"
          right={<span className="mono text-2xs text-faint">{traffic.runAttemptsPresent ? "RunAttempt joined" : "execution detail unavailable"}</span>}
        />
        {known ? <LiveHarnessCanvas lanes={lanes} /> : <ReadNotice read={executorsRead} label="the harness projection" />}
      </section>

      <section data-depth-min="investigate" className="flex flex-col gap-3">
        <SectionHeader
          tone="work"
          icon={<PulseIcon />}
          label="Operations evidence"
          sub="observed → executing → current seam · motion requires live evidence"
        />
        {known ? <LiveOperationsTimeline report={traffic} /> : <ReadNotice read={executorsRead} label="operations evidence" />}
      </section>

      {showCoordination ? (
        <LiveCoordination execDetail={execDetail} runAttemptsPresent={traffic.runAttemptsPresent} />
      ) : null}

      {projection ? (
        <div data-depth-min="technical" className="flex flex-col gap-3">
          <div className="flex items-center justify-between gap-3">
            <div>
              <div className="section-label">Supervised delegated sessions</div>
              <p className="mt-1 text-xs text-muted">Exact session and RunAttempt identity; prompt content never reaches this projection.</p>
            </div>
            <StatusChip tone={delegatedRunning > 0 ? "good" : "muted"} label={`${delegatedRunning} running`} />
          </div>
          <DelegatedSessions rows={projection.delegatedSessions} counts={projection.delegatedCounts} />
        </div>
      ) : null}
    </div>
  );
}

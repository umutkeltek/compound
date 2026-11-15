import Link from "next/link";

import { hasCopy, PageHeader, ReadChip, ReadFailedBanner, ReadNotice, readsPending, aboutDuration } from "@/components/kit";
import { PendingRefresh } from "@/components/PendingRefresh";
import { WorkLenses } from "@/components/work/WorkLenses";
import { readAttention } from "@/lib/attention";
import { arr, DIRECTOR_PROPOSE_ARGS, HEAVY_PROJECTION_TIMEOUT_MS, readProjection, str } from "@/lib/compound";
import { consoleMutationsEnabled } from "@/lib/console-mutations";
import { countInProgress, countOnHold, countOpenWork, describeInProgress, STALE_AFTER_DAYS, UNASSIGNED_AGENT_ID, unit, waitingOnYou } from "@/lib/counts";
import { computeConsoleMutationReadiness } from "@/lib/instance-pin";
import { buildLaneTraffic } from "@/lib/lane-traffic";
import { getTraffic } from "@/lib/traffic";
import { isWorkId } from "@/lib/work-id";
import { buildBoard, buildOffered, buildPipeline, nameCardOwners } from "@/lib/work-pipeline";
import { buildWorkLensItems, groupWorkLensItems } from "@/lib/work-views";
import type { WorkLensKey } from "@/components/work/WorkLenses";

export const dynamic = "force-dynamic";


/**
 * Work answers one question first: "What is being worked on, and what is
 * stuck?" The answer counts with the shared definitions (lib/counts.ts): open
 * work items, how many are marked in progress and how that splits, and what
 * is stuck (picked up more than a week ago and still open, or on hold). The
 * board by state is the default view; the other views sit beside it, quieter.
 * Every read is non-blocking (`readProjection`): the page renders at once from
 * cached copies, or says what is still being read; it never waits for compound.
 */
export default async function WorkPage({
  searchParams,
}: {
  searchParams: Promise<{ trace?: string; lens?: string }>;
}) {
  const { trace, lens } = await searchParams;
  const [workbenchRead, sourcesRead, suggestionsRead, packetsRead, laneTraffic, traffic, attentionLive] = await Promise.all([
    readProjection(["workbench", "--json"], { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS }),
    readProjection(["source", "connections", "--json"]),
    // Read-only: `director propose --dry-run` creates nothing; suggestions feed
    // the Suggest stage. It takes about two minutes on a real home, so it runs
    // under the heavy timeout (the old 15 s limit meant it never landed).
    readProjection(DIRECTOR_PROPOSE_ARGS, { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS }),
    readProjection(["packets", "list", "--json"]),
    buildLaneTraffic(200, { nonBlocking: true }),
    getTraffic(),
    readAttention(),
  ]);
  const workbench = workbenchRead.value;
  const suggestions = suggestionsRead.value;

  const open = arr(workbench?.open);
  const recent = arr(workbench?.recent);
  const proposals = arr(suggestions?.proposals ?? suggestions?.proposed ?? suggestions?.items);

  // A stage whose source has not been read says so, instead of "none".
  const stages = buildPipeline(workbench, sourcesRead.value, proposals).map((stage) => {
    if (stage.key === "signal" && !hasCopy(sourcesRead)) {
      return { ...stage, emptyCopy: sourcesRead.status === "unreachable" ? "Could not read sources." : "Reading sources…" };
    }
    if (stage.key === "propose" && !hasCopy(suggestionsRead)) {
      const how = suggestionsRead.lastRunMs ? ` (last time ${aboutDuration(suggestionsRead.lastRunMs)})` : "";
      return { ...stage, emptyCopy: suggestionsRead.status === "unreachable" ? "Could not read suggestions." : `Reading suggestions${how}…` };
    }
    return stage;
  });
  // Card owners named as Live names agents (executors projection labels); the
  // "unassigned" bucket is not an agent, so it never names an owned card.
  const agentLanes = traffic.lanes.filter((lane) => lane.agentId !== UNASSIGNED_AGENT_ID);
  const agentNames = new Map(agentLanes.map((lane) => [lane.agentId, lane.agentLabel] as const));
  const agentByWork = new Map(agentLanes.flatMap((lane) => lane.inFlight.map((work) => [work.workId, lane.agentLabel] as const)));
  const columns = nameCardOwners(buildBoard(open, recent), agentByWork, agentNames);
  const offered = buildOffered(arr(packetsRead.value?.items));

  const initialTrace = isWorkId(str(trace)) ? str(trace) : null;
  const mutationsReady = computeConsoleMutationReadiness(consoleMutationsEnabled()).ready;
  const items = buildWorkLensItems(open, traffic);
  const groups = groupWorkLensItems(items, traffic);
  const allowedLenses = new Set<WorkLensKey>(["pipeline", "table", "flow", "space", "board", "offered"]);
  const defaultLens = allowedLenses.has(lens as WorkLensKey) ? lens as WorkLensKey : "board";
  const workbenchKnown = hasCopy(workbenchRead);
  // One definition per number (lib/counts.ts), shared with Today, Live and the sidebar.
  const waiting = waitingOnYou(attentionLive.report);
  const inProgress = workbenchKnown ? countInProgress(open, Date.now()) : null;
  const progress = inProgress ? describeInProgress(inProgress) : null;
  const onHold = workbenchKnown ? countOnHold(open) : null;
  const pending = readsPending(workbenchRead, sourcesRead, suggestionsRead, packetsRead, traffic.read, attentionLive.read);

  const stuck = [
    inProgress?.pickedUpOver7Days
      ? `${inProgress.pickedUpOver7Days.toLocaleString("en-US")} of them were picked up more than ${STALE_AFTER_DAYS} days ago and are still not finished`
      : "",
    onHold ? `${unit(onHold, "work item")} ${onHold === 1 ? "is" : "are"} on hold` : "",
  ].filter(Boolean);

  const answer = !workbenchKnown || !inProgress || !progress ? (
    <ReadNotice read={workbenchRead} label="work" />
  ) : (
    <div className="flex flex-col gap-1.5">
      <p><strong className="font-semibold">{inProgress.total.toLocaleString("en-US")} in progress</strong> · {countOpenWork(open).toLocaleString("en-US")} open tasks</p>
      <p className="text-sm text-muted">{onHold || 0} on hold{inProgress.pickedUpOver7Days ? ` · ${inProgress.pickedUpOver7Days.toLocaleString("en-US")} picked up over a week ago` : ""}</p>
      <details className="text-sm text-muted"><summary className="cursor-pointer">Progress breakdown</summary>
      <p className="mt-2">{progress.headline}, out of {unit(countOpenWork(open), "open work item")}.</p>
      {progress.detail ? <p className="text-sm text-muted">{progress.detail}</p> : null}
      {stuck.length ? (
        <p className="text-base text-seam">Stuck: {stuck.join("; ")}.</p>
      ) : inProgress.total > 0 && inProgress.pickedUpOver7Days === null ? (
        <p className="text-sm text-muted">{progress.age}</p>
      ) : (
        <p className="text-base text-good">Nothing looks stuck.</p>
      )}
      </details>
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
      <PageHeader
        name="Work"
        lead="Follow progress and resolve what is holding work up."
        answer={answer}
        right={
          <>
            <ReadChip read={workbenchRead} />

          </>
        }
      />

      {workbenchKnown ? (
        <>
          <ReadFailedBanner read={workbenchRead} label="work" />
          <WorkLenses
            stages={stages}
            columns={columns}
            offered={offered}
            openCount={open.length}
            initialTrace={initialTrace}
            laneTraffic={laneTraffic}
            items={items}
            groups={groups}
            defaultLens={defaultLens}
            mutationsReady={mutationsReady}
          />
        </>
      ) : null}
    </div>
  );
}

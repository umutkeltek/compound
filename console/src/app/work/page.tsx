import Link from "next/link";

import { hasCopy, PageHeader, ReadChip, ReadFailedBanner, ReadNotice, readsPending, aboutDuration } from "@/components/kit";
import { PendingRefresh } from "@/components/PendingRefresh";
import { WorkLenses } from "@/components/work/WorkLenses";
import { arr, DIRECTOR_PROPOSE_ARGS, HEAVY_PROJECTION_TIMEOUT_MS, readProjection, readWorkProjectIndex, str } from "@/lib/compound";
import type { WorkByAppModel } from "@/components/work/WorkByAppLens";
import { workListHref, type WorkFilter } from "@/lib/links";
import { consoleMutationsEnabled } from "@/lib/console-mutations";
import { countInProgress, countOnHold, countOpenWork, STALE_AFTER_DAYS, UNASSIGNED_AGENT_ID } from "@/lib/counts";
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
  const nowMs = Date.now();
  const [workbenchRead, sourcesRead, suggestionsRead, packetsRead, laneTraffic, traffic, projectIndex] = await Promise.all([
    readProjection(["workbench", "--json"], { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS }),
    readProjection(["source", "connections", "--json"]),
    // Read-only: `director propose --dry-run` creates nothing; suggestions feed
    // the Suggest stage. It takes about two minutes on a real home, so it runs
    // under the heavy timeout (the old 15 s limit meant it never landed).
    readProjection(DIRECTOR_PROPOSE_ARGS, { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS }),
    readProjection(["packets", "list", "--json"]),
    buildLaneTraffic(200, { nonBlocking: true }),
    getTraffic(),
    // Which app, codebase or "not linked" group each item is in. It shares the
    // workbench read above (same argv) and adds the app list and inventory.
    readWorkProjectIndex({ nowMs }),
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
  const index = projectIndex.index;
  const groupById = new Map((index?.projects ?? []).map((group) => [group.id, group] as const));
  const items = buildWorkLensItems(open, traffic, {
    nowMs,
    groupOf: index ? (id) => groupById.get(index.byWork[id] ?? "") ?? null : undefined,
  });
  const readLabels = { work: "work", apps: "your app list", inventory: "where each app's code lives" } as const;
  const byApp: WorkByAppModel = {
    groups: index ? index.projects.map((group) => ({ ...group })) : null,
    missing: projectIndex.missing.map((key) => ({ label: readLabels[key], read: projectIndex.reads[key] })),
    reads: (Object.keys(readLabels) as Array<keyof typeof readLabels>).map((key) => ({ label: readLabels[key], read: projectIndex.reads[key] })),
  };
  const groups = groupWorkLensItems(items, traffic);
  const allowedLenses = new Set<WorkLensKey>(["pipeline", "table", "flow", "space", "board", "offered", "project"]);
  const defaultLens = allowedLenses.has(lens as WorkLensKey) ? lens as WorkLensKey : "board";
  const workbenchKnown = hasCopy(workbenchRead);
  // One definition per number (lib/counts.ts), shared with Today, Live and the sidebar.
  const inProgress = workbenchKnown ? countInProgress(open, Date.now()) : null;
  const onHold = workbenchKnown ? countOnHold(open) : null;
  const pending = readsPending(workbenchRead, sourcesRead, suggestionsRead, packetsRead, traffic.read, projectIndex.reads.apps, projectIndex.reads.inventory);

  // The header leads with what is stopped: on hold (waits for a decision) and
  // not moved (in progress, picked up more than a week ago). In progress is
  // split into its three parts. Every number opens the List of exactly those
  // items: the counts and the List filters share the rules in lib/work-state.ts.
  const countLink = (n: number, label: string, filter: WorkFilter, tone = "text-ink") => (
    n > 0
      ? <Link href={workListHref({ filter })} className={`font-semibold hover:underline ${tone}`}>{n.toLocaleString("en-US")} {label}</Link>
      : <span className="text-muted">{n.toLocaleString("en-US")} {label}</span>
  );
  const answer = !workbenchKnown || !inProgress ? (
    <ReadNotice read={workbenchRead} label="work" />
  ) : (
    <div className="flex flex-col gap-1.5">
      <p className="text-lg">
        {countLink(onHold ?? 0, `on hold, waiting for you`, "blocked", "text-seam")}
        {" · "}
        {inProgress.pickedUpOver7Days === null
          ? <span className="text-muted">how long work has sat is unknown: the records carry no pick-up time</span>
          : countLink(inProgress.pickedUpOver7Days, `not moved in over ${STALE_AFTER_DAYS} days`, "stale", "text-crit")}
      </p>
      <p className="text-sm text-muted">
        {countLink(inProgress.total, "in progress", "progress")}:{" "}
        {countLink(inProgress.running, "running", "running")}
        {" · "}
        {countLink(inProgress.pickedUp, "picked up, not started", "claimed")}
        {" · "}
        {countLink(inProgress.checking, "waiting for a proof check", "verifying")}
      </p>
      <p className="text-sm text-muted">
        {countLink(countOpenWork(open), countOpenWork(open) === 1 ? "open work item" : "open work items", "all")} in all.
        {!onHold && !inProgress.pickedUpOver7Days ? <span className="text-good"> Nothing is on hold or stuck.</span> : null}
      </p>
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
            <Link href={workListHref({ lens: "project" })} className="text-sm font-medium text-work hover:underline">Work by app →</Link>

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
            byApp={byApp}
          />
        </>
      ) : null}
    </div>
  );
}

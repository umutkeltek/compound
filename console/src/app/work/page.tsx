import Link from "next/link";

import { hasCopy, PageHeader, ReadChip, ReadFailedBanner, ReadNotice, readsPending } from "@/components/kit";
import { PendingRefresh } from "@/components/PendingRefresh";
import { WorkLenses } from "@/components/work/WorkLenses";
import type { WorkByAppModel } from "@/components/work/WorkByAppLens";
import { arr, readWorkProjectIndex } from "@/lib/compound";
import { consoleMutationsEnabled } from "@/lib/console-mutations";
import { readAttention } from "@/lib/attention";
import { countInProgress, countOpenWork, countWaitingWork, STALE_AFTER_DAYS, UNASSIGNED_AGENT_ID, waitingWork } from "@/lib/counts";
import { computeConsoleMutationReadiness } from "@/lib/instance-pin";
import { parseWorkListParams, workListHref, type WorkFilter } from "@/lib/links";
import { getTraffic, readOpenWork } from "@/lib/traffic";
import { buildBoard, decorateBoard, nameCardOwners } from "@/lib/work-pipeline";
import { buildWorkLensItems } from "@/lib/work-views";

export const dynamic = "force-dynamic";

/**
 * Work answers one question first: "What is stopped, and what is moving?"
 * The header leads with work on hold and work that has not moved in a week,
 * then splits what is in progress; every number opens the List of exactly
 * those items (lib/work-state.ts rules, lib/counts.ts counts). Three views sit
 * below: the board by state (default), the List, and work grouped by app.
 * Every read is non-blocking (`readProjection`): the page renders at once
 * from cached copies, or says what is still being read.
 */
export default async function WorkPage({
  searchParams,
}: {
  searchParams: Promise<Record<string, string | string[] | undefined>>;
}) {
  const params = parseWorkListParams(await searchParams);
  const nowMs = Date.now();
  const [workbenchRead, traffic, projectIndex, attention] = await Promise.all([
    // The open work read every Agents page counts from too (lib/traffic.ts).
    readOpenWork(),
    getTraffic(),
    // Which app, codebase or "not linked" group each item is in. It shares
    // the workbench read above (same argv) and adds the app list and inventory.
    readWorkProjectIndex({ nowMs }),
    // Which work waits for you and which waits on something else: the
    // waiting list's own sorting, the one Today and Decide count.
    readAttention(),
  ]);
  const waiting = waitingWork(attention.report);
  const workbench = workbenchRead.value;
  const open = arr(workbench?.open);
  const recent = arr(workbench?.recent);
  const index = projectIndex.index;
  const groupById = new Map((index?.projects ?? []).map((group) => [group.id, group] as const));
  const groupOf = index ? (id: string) => groupById.get(index.byWork[id] ?? "") ?? null : undefined;

  // Card owners named as Live names agents (executors projection labels); the
  // "unassigned" bucket is not an agent, so it never names an owned card.
  const agentLanes = traffic.lanes.filter((lane) => lane.agentId !== UNASSIGNED_AGENT_ID);
  const agentNames = new Map(agentLanes.map((lane) => [lane.agentId, lane.agentLabel] as const));
  const agentByWork = new Map(agentLanes.flatMap((lane) => lane.inFlight.map((work) => [work.workId, lane.agentLabel] as const)));
  const columns = decorateBoard(nameCardOwners(buildBoard(open, recent), agentByWork, agentNames), open, { nowMs, groupOf });

  const mutationsReady = computeConsoleMutationReadiness(consoleMutationsEnabled()).ready;
  const items = buildWorkLensItems(open, traffic, { nowMs, groupOf, waiting });
  const readLabels = { work: "work", apps: "your app list", inventory: "where each app's code lives" } as const;
  const byApp: WorkByAppModel = {
    groups: index ? index.projects.map((group) => ({ ...group })) : null,
    missing: projectIndex.missing.map((key) => ({ label: readLabels[key], read: projectIndex.reads[key] })),
    reads: (Object.keys(readLabels) as Array<keyof typeof readLabels>).map((key) => ({ label: readLabels[key], read: projectIndex.reads[key] })),
  };
  const workbenchKnown = hasCopy(workbenchRead);
  // One definition per number (lib/counts.ts), shared with Today, Live and the sidebar.
  const inProgress = workbenchKnown ? countInProgress(open, nowMs) : null;
  // Counted with the rule the List filters by (lib/work-state.ts `waitsOn`), over the same rows.
  const waits = workbenchKnown ? countWaitingWork(open, waiting) : null;
  const pending = readsPending(workbenchRead, traffic.read, projectIndex.reads.apps, projectIndex.reads.inventory, attention.read);

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
        {/* Before the waiting list is read, on hold is counted by state and not called "waiting for you". */}
        {countLink(waits?.you ?? 0, waiting ? "on hold, waiting for you" : "on hold", "blocked", "text-seam")}
        {waiting && waits && waits.others > 0 ? (
          <>
            {" · "}
            {countLink(waits.others, waits.others === 1 ? "waits on something else, not on you" : "wait on something else, not on you", "others")}
          </>
        ) : null}
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
        {!waits?.you && !inProgress.pickedUpOver7Days ? <span className="text-good"> Nothing is on hold or stuck.</span> : null}
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
            columns={columns}
            openCount={open.length}
            initialTrace={params.trace || null}
            items={items}
            defaultLens={params.lens}
            mutationsReady={mutationsReady}
            byApp={byApp}
          />
        </>
      ) : null}
    </div>
  );
}

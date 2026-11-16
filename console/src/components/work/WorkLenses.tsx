"use client";

import * as Tabs from "@radix-ui/react-tabs";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";

import { BoardIcon, GridIcon, InboxIcon, LayersIcon, ListIcon, RouteIcon, TimelineIcon } from "@/components/kit/icons";
import { BoardLens } from "@/components/work/BoardLens";
import { WorkByAppLens, type WorkByAppModel } from "@/components/work/WorkByAppLens";
import { CapsuleColumns } from "@/components/work/CapsuleColumns";
import { OfferedLens } from "@/components/work/OfferedLens";
import { PipelineLens } from "@/components/work/PipelineLens";
import { TraceDrawer } from "@/components/work/TraceDrawer";
import { TransitionsTicker } from "@/components/work/TransitionsTicker";
import { WorkFlowLens } from "@/components/work/WorkFlowLens";
import { WorkSpaceLens } from "@/components/work/WorkSpaceLens";
import { WorkTableLens } from "@/components/work/WorkTableLens";
import type { LaneTrafficReport } from "@/lib/lane-traffic";
import { parseWorkListParams, workListHref, type ParsedWorkListParams, type WorkLens } from "@/lib/links";
import type { BoardColumnModel, OfferedPacketModel, PipelineStageModel } from "@/lib/work-pipeline";
import type { WorkLensGroup, WorkLensItem } from "@/lib/work-views";

/**
 * Work screen shell (redesign plan Part 3): three lenses over the same units of
 * work — Table (default, the needs-first operational view), then Flow, Space,
 * Pipeline, Board, and Offered packets — with ONE Trace drawer mounted once
 * and opened by any card in any lens. The open trace is mirrored into
 * `?trace=<id>` so it is
 * deep-linkable and shareable (Briefing's pipeline strip links straight here).
 *
 * Above the lenses: the DESIGN.md §9.6 "information flow" ambient row — the
 * live Transitions ticker (streams over /api/stream) and CapsuleColumns lane
 * traffic — visible regardless of which lens tab is active, since both are
 * ambient organism-is-alive signal, not lens-scoped data.
 */

export type WorkLensKey = WorkLens;

const TAB_CLASS =
  "inline-flex shrink-0 items-center gap-1.5 rounded-md border border-transparent px-3 py-1.5 text-sm font-medium text-muted transition-colors hover:text-ink " +
  "data-[state=active]:border-hair data-[state=active]:bg-panel data-[state=active]:text-ink data-[state=active]:shadow-sm";

/** The other views: quieter text tabs after the one default view. */
const ALT_TAB_CLASS =
  "inline-flex shrink-0 items-center gap-1 rounded-md px-2 py-1 text-xs font-medium text-faint transition-colors hover:text-ink " +
  "data-[state=active]:bg-soft data-[state=active]:text-ink";

export function WorkLenses({
  stages,
  columns,
  offered,
  openCount,
  initialTrace,
  laneTraffic,
  items,
  groups,
  defaultLens,
  mutationsReady,
  byApp,
}: {
  stages: PipelineStageModel[];
  columns: BoardColumnModel[];
  offered: OfferedPacketModel[];
  openCount: number;
  initialTrace: string | null;
  laneTraffic: LaneTrafficReport;
  items: WorkLensItem[];
  groups: WorkLensGroup[];
  defaultLens: WorkLensKey;
  mutationsReady: boolean;
  byApp: WorkByAppModel;
}) {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const paramsRef = useRef(searchParams.toString());
  const [trace, setTrace] = useState<string | null>(initialTrace);
  const [lens, setLens] = useState<WorkLensKey>(defaultLens);
  useEffect(() => { setLens(defaultLens); }, [defaultLens]);

  useEffect(() => {
    paramsRef.current = searchParams.toString();
  }, [searchParams]);

  // The address owns the view, the List filter, the agent, the app or group
  // and the search text (lib/links.ts). A view, filter or drawer change is a
  // step Back can return to (push); typing replaces the current step.
  const replaceParams = useCallback((next: { trace?: string | null } & Partial<Omit<ParsedWorkListParams, "trace">>, mode: "push" | "replace" = "replace") => {
    // Keep a synchronous query snapshot. A close followed immediately by a
    // lens switch must not resurrect the trace while the router settles.
    const current = parseWorkListParams(new URLSearchParams(paramsRef.current));
    const merged = { ...current, ...next, trace: next.trace === null ? "" : next.trace ?? current.trace };
    const href = workListHref(merged);
    paramsRef.current = href.includes("?") ? href.slice(href.indexOf("?") + 1) : "";
    const target = pathname === "/work" ? href : `${pathname}${href.slice("/work".length)}`;
    if (mode === "push") router.push(target, { scroll: false });
    else router.replace(target, { scroll: false });
  }, [pathname, router]);
  const listParams = parseWorkListParams(searchParams);

  const openTrace = useCallback(
    (workId: string) => {
      setTrace(workId);
      replaceParams({ trace: workId });
    },
    [replaceParams],
  );

  const closeTrace = useCallback(() => {
    setTrace(null);
    replaceParams({ trace: null });
  }, [replaceParams]);

  return (
    <>
      <Tabs.Root value={lens} onValueChange={(value) => {
        const next = value as WorkLensKey;
        setLens(next);
        replaceParams({ lens: next }, "push");
      }} className="flex min-w-0 flex-col gap-4">
        {/* One default view (the board by state) and quieter alternates over
         * the same work items. Every view stays one click away. */}
        <Tabs.List
          aria-label="Ways to view the work"
          className="flex w-full max-w-full flex-wrap items-center gap-x-1 gap-y-2 overflow-x-auto"
        >
          <span className="inline-flex rounded-lg border border-hair bg-soft p-0.5">
            <Tabs.Trigger value="board" className={TAB_CLASS}>
              <BoardIcon size={14} />
              By state
              <span className="tabular-nums text-faint">{openCount}</span>
            </Tabs.Trigger>
          </span>
          <span className="ml-2 text-2xs font-semibold uppercase tracking-wide text-faint">Other views</span>
          <Tabs.Trigger value="table" className={ALT_TAB_CLASS}>
            <ListIcon size={12} />
            List
            <span className="tabular-nums">{items.length}</span>
          </Tabs.Trigger>
          <Tabs.Trigger value="project" className={ALT_TAB_CLASS}>
            <GridIcon size={12} />
            By app
            {byApp.groups ? <span className="tabular-nums">{byApp.groups.length}</span> : null}
          </Tabs.Trigger>
          <Tabs.Trigger value="pipeline" className={ALT_TAB_CLASS}>
            <TimelineIcon size={12} />
            Stages
          </Tabs.Trigger>
          <Tabs.Trigger value="flow" className={ALT_TAB_CLASS}>
            <RouteIcon size={12} />
            Flow
          </Tabs.Trigger>
          <Tabs.Trigger value="space" className={ALT_TAB_CLASS}>
            <LayersIcon size={12} />
            By agent
          </Tabs.Trigger>
          <Tabs.Trigger value="offered" className={ALT_TAB_CLASS}>
            <InboxIcon size={12} />
            Work packets
            <span className="tabular-nums">{offered.length}</span>
          </Tabs.Trigger>
        </Tabs.List>

        <Tabs.Content value="pipeline" className="min-w-0 focus-visible:outline-none">
          <PipelineLens stages={stages} onOpenTrace={openTrace} />
        </Tabs.Content>
        <Tabs.Content value="table" className="min-w-0 focus-visible:outline-none">
          <WorkTableLens
            items={items}
            onOpenTrace={openTrace}
            params={listParams}
            groups={byApp.groups}
            onParamsChange={(next, mode) => replaceParams(next, mode)}
          />
        </Tabs.Content>
        <Tabs.Content value="project" className="min-w-0 focus-visible:outline-none">
          <WorkByAppLens model={byApp} />
        </Tabs.Content>
        <Tabs.Content value="flow" className="min-w-0 focus-visible:outline-none">
          <WorkFlowLens stages={stages} groups={groups} onOpenTrace={openTrace} />
        </Tabs.Content>
        <Tabs.Content value="space" className="min-w-0 focus-visible:outline-none">
          <WorkSpaceLens stages={stages} groups={groups} onOpenTrace={openTrace} />
        </Tabs.Content>
        <Tabs.Content value="board" className="min-w-0 focus-visible:outline-none">
          <BoardLens columns={columns} onOpenTrace={openTrace} />
        </Tabs.Content>
        <Tabs.Content value="offered" className="min-w-0 focus-visible:outline-none">
          <OfferedLens packets={offered} />
        </Tabs.Content>
      </Tabs.Root>

      <section data-depth-min="investigate" className="mt-2 flex min-w-0 flex-col gap-3">
        <div>
          <div className="section-label">Recent activity</div>
          <p className="mt-1 text-xs text-muted">The latest recorded changes and which model lanes carried the work, for context.</p>
        </div>
        <div className="grid min-w-0 gap-4 lg:grid-cols-[3fr_2fr]">
          <TransitionsTicker onOpenTrace={openTrace} />
          <CapsuleColumns report={laneTraffic} />
        </div>
      </section>

      <TraceDrawer workId={trace} onClose={closeTrace} mutationsReady={mutationsReady} />
    </>
  );
}

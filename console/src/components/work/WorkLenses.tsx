"use client";

import * as Tabs from "@radix-ui/react-tabs";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useCallback, useEffect, useRef, useState } from "react";

import { BoardIcon, GridIcon, ListIcon } from "@/components/kit/icons";
import { BoardLens } from "@/components/work/BoardLens";
import { rememberWorkList } from "@/components/work/WorkBackLink";
import { WorkByAppLens, type WorkByAppModel } from "@/components/work/WorkByAppLens";
import { TraceDrawer } from "@/components/work/TraceDrawer";
import { WorkTableLens } from "@/components/work/WorkTableLens";
import { parseWorkListParams, workListHref, type ParsedWorkListParams, type WorkLens } from "@/lib/links";
import type { BoardColumnModel } from "@/lib/work-pipeline";
import type { WorkLensItem } from "@/lib/work-views";

/**
 * The Work page's three views over the same open work: the board by state
 * (default), the List, and work grouped by app. One preview drawer is mounted
 * once and opened from any card or row; it is mirrored into `?trace=<id>`.
 *
 * The address owns the view, the List filter, the agent, the app or group and
 * the search text (lib/links.ts), so a filtered view survives reload and is a
 * link. A view, filter or preview change is a step Back returns to; typing
 * replaces the current step. Retired views (Stages, Flow, By agent, Work
 * packets) open the board.
 */

export type WorkLensKey = WorkLens;

/** The views Work shows; any other `lens` value (the retired ones) opens the board. */
const SHOWN_LENSES: ReadonlySet<WorkLens> = new Set<WorkLens>(["board", "table", "project"]);

function shownWorkLens(lens: WorkLens): WorkLens {
  return SHOWN_LENSES.has(lens) ? lens : "board";
}

const TAB_CLASS =
  "inline-flex shrink-0 items-center gap-1.5 rounded-md border border-transparent px-3 py-1.5 text-sm font-medium text-muted transition-colors hover:text-ink " +
  "data-[state=active]:border-hair data-[state=active]:bg-panel data-[state=active]:text-ink data-[state=active]:shadow-sm";

export function WorkLenses({
  columns,
  openCount,
  initialTrace,
  items,
  defaultLens,
  mutationsReady,
  byApp,
}: {
  columns: BoardColumnModel[];
  openCount: number;
  initialTrace: string | null;
  items: WorkLensItem[];
  defaultLens: WorkLensKey;
  mutationsReady: boolean;
  byApp: WorkByAppModel;
}) {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const paramsRef = useRef(searchParams.toString());
  const [trace, setTrace] = useState<string | null>(initialTrace);
  const [lens, setLens] = useState<WorkLensKey>(shownWorkLens(defaultLens));
  useEffect(() => { setLens(shownWorkLens(defaultLens)); }, [defaultLens]);
  const listParams = parseWorkListParams(searchParams);
  // Back and Forward move the address; the open preview follows it.
  useEffect(() => { setTrace(listParams.trace || null); }, [listParams.trace]);

  useEffect(() => {
    paramsRef.current = searchParams.toString();
    // The item page's "Back" returns here (components/work/WorkBackLink.tsx).
    rememberWorkList(searchParams.toString());
  }, [searchParams]);

  const replaceParams = useCallback((next: { trace?: string | null } & Partial<Omit<ParsedWorkListParams, "trace">>, mode: "push" | "replace" = "replace") => {
    // Keep a synchronous query snapshot. A close followed immediately by a
    // view switch must not resurrect the preview while the router settles.
    const current = parseWorkListParams(new URLSearchParams(paramsRef.current));
    const merged = { ...current, ...next, trace: next.trace === null ? "" : next.trace ?? current.trace };
    const href = workListHref(merged);
    paramsRef.current = href.includes("?") ? href.slice(href.indexOf("?") + 1) : "";
    const target = `${pathname}${href.slice("/work".length)}`;
    if (mode === "push") router.push(target, { scroll: false });
    else router.replace(target, { scroll: false });
  }, [pathname, router]);

  // Opening a preview is a step Back closes; closing it never leaves Work.
  const openTrace = useCallback((workId: string) => {
    setTrace(workId);
    replaceParams({ trace: workId }, "push");
  }, [replaceParams]);

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
        <Tabs.List aria-label="Ways to view the work" className="flex w-full max-w-full flex-wrap items-center gap-x-1 gap-y-2 overflow-x-auto">
          <span className="inline-flex rounded-lg border border-hair bg-soft p-0.5">
            <Tabs.Trigger value="board" className={TAB_CLASS}>
              <BoardIcon size={14} />
              By state
              <span className="tabular-nums text-faint">{openCount}</span>
            </Tabs.Trigger>
            <Tabs.Trigger value="table" className={TAB_CLASS}>
              <ListIcon size={14} />
              List
              <span className="tabular-nums text-faint">{items.length}</span>
            </Tabs.Trigger>
            <Tabs.Trigger value="project" className={TAB_CLASS}>
              <GridIcon size={14} />
              By app
              {byApp.groups ? <span className="tabular-nums text-faint">{byApp.groups.length}</span> : null}
            </Tabs.Trigger>
          </span>
        </Tabs.List>

        <Tabs.Content value="board" className="min-w-0 focus-visible:outline-none">
          <BoardLens
            columns={columns}
            openCount={openCount}
            onOpenTrace={openTrace}
            params={listParams}
            onParamsChange={(next, mode) => replaceParams(next, mode)}
          />
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
      </Tabs.Root>

      <TraceDrawer workId={trace} onClose={closeTrace} mutationsReady={mutationsReady} />
    </>
  );
}

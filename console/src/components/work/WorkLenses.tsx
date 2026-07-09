"use client";

import * as Tabs from "@radix-ui/react-tabs";
import { usePathname, useRouter } from "next/navigation";
import { useCallback, useState } from "react";

import { BoardLens } from "@/components/work/BoardLens";
import { OfferedLens } from "@/components/work/OfferedLens";
import { PipelineLens } from "@/components/work/PipelineLens";
import { TraceDrawer } from "@/components/work/TraceDrawer";
import type { BoardColumnModel, OfferedPacketModel, PipelineStageModel } from "@/lib/work-pipeline";

/**
 * Work screen shell (redesign plan Part 3): three lenses over the same units of
 * work — Pipeline (default, the envelope stages), Board (by-state kanban), and
 * Offered packets — with ONE Trace drawer mounted once and opened by any card
 * in any lens. The open trace is mirrored into `?trace=<id>` so it is
 * deep-linkable and shareable (Briefing's pipeline strip links straight here).
 */

type TabKey = "pipeline" | "board" | "offered";

const TAB_CLASS =
  "rounded-md px-3 py-1.5 text-xs font-semibold text-muted data-[state=active]:bg-panel data-[state=active]:text-ink " +
  "data-[state=active]:shadow-sm";

export function WorkLenses({
  stages,
  columns,
  offered,
  openCount,
  initialTrace,
}: {
  stages: PipelineStageModel[];
  columns: BoardColumnModel[];
  offered: OfferedPacketModel[];
  openCount: number;
  initialTrace: string | null;
}) {
  const router = useRouter();
  const pathname = usePathname();
  const [trace, setTrace] = useState<string | null>(initialTrace);

  const openTrace = useCallback(
    (workId: string) => {
      setTrace(workId);
      router.replace(`${pathname}?trace=${encodeURIComponent(workId)}`, { scroll: false });
    },
    [pathname, router],
  );

  const closeTrace = useCallback(() => {
    setTrace(null);
    router.replace(pathname, { scroll: false });
  }, [pathname, router]);

  return (
    <>
      <Tabs.Root defaultValue="pipeline" className="flex flex-col gap-4">
        <Tabs.List className="inline-flex w-fit items-center gap-1 rounded-lg border border-hair bg-soft p-1">
          <Tabs.Trigger value={"pipeline" satisfies TabKey} className={TAB_CLASS}>
            Pipeline
          </Tabs.Trigger>
          <Tabs.Trigger value={"board" satisfies TabKey} className={TAB_CLASS}>
            Board
            <span className="mono ml-1.5 tabular-nums text-faint">{openCount}</span>
          </Tabs.Trigger>
          <Tabs.Trigger value={"offered" satisfies TabKey} className={TAB_CLASS}>
            Offered packets
            <span className="mono ml-1.5 tabular-nums text-faint">{offered.length}</span>
          </Tabs.Trigger>
        </Tabs.List>

        <Tabs.Content value="pipeline" className="focus-visible:outline-none">
          <PipelineLens stages={stages} onOpenTrace={openTrace} />
        </Tabs.Content>
        <Tabs.Content value="board" className="focus-visible:outline-none">
          <BoardLens columns={columns} onOpenTrace={openTrace} />
        </Tabs.Content>
        <Tabs.Content value="offered" className="focus-visible:outline-none">
          <OfferedLens packets={offered} />
        </Tabs.Content>
      </Tabs.Root>

      <TraceDrawer workId={trace} onClose={closeTrace} />
    </>
  );
}

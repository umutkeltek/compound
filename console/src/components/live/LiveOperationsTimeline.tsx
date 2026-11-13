"use client";

import { useMemo, useState } from "react";

import { Drawer, EmptyState, IdRef, StateBadge } from "@/components/kit";
import { harnessDisplayName } from "@/lib/harness-label";
import { ago } from "@/lib/time";
import type { TrafficReport, TrafficWork } from "@/lib/traffic";

type Selected = { laneLabel: string; work: TrafficWork };

/**
 * `report` may hold only the rows to show (lib/traffic.ts `newestInFlight`);
 * `total` then carries the full in-flight count for "showing 6 of N".
 */
export function LiveOperationsTimeline({ report, total }: { report: TrafficReport; total?: number }) {
  const rows = useMemo(() => report.lanes.flatMap((lane) => lane.inFlight.map((work) => ({ laneLabel: lane.agentLabel, laneId: lane.agentId, work }))).sort((a, b) => (b.work.lastHeartbeatAt || b.work.startedAt).localeCompare(a.work.lastHeartbeatAt || a.work.startedAt)), [report]);
  const visibleRows = rows.slice(0, 6);
  const [selected, setSelected] = useState<Selected | null>(null);
  if (!rows.length) return <EmptyState>No agent holds any work right now.</EmptyState>;

  return (
    <>
      <section className="min-w-0 overflow-hidden rounded-xl border border-hair bg-panel shadow-sm">
        <div className="overflow-x-auto">
          <div className="min-w-[620px]">
            <div className="grid grid-cols-[128px_minmax(240px,1fr)_minmax(180px,.7fr)] border-b border-hair bg-soft/70 text-2xs font-bold uppercase tracking-wide text-faint">
              <div className="px-3 py-2.5">Last seen</div><div className="border-l border-hair px-3 py-2.5">Work and agent</div><div className="border-l border-hair px-3 py-2.5">Next step</div>
            </div>
            <div>
          {visibleRows.map(({ laneLabel, laneId, work }) => {
            const active = work.runStatus === "running" || work.state === "running";
            const blocked = work.state === "blocked" || work.state === "held";
            const observedAt = work.lastHeartbeatAt || work.startedAt;
            return <button key={`${laneId}:${work.workId}`} type="button" onClick={() => setSelected({ laneLabel, work })} className={`grid w-full grid-cols-[128px_minmax(240px,1fr)_minmax(180px,.7fr)] border-b border-hair text-left last:border-b-0 hover:bg-soft ${blocked ? "bg-seam-soft/25" : ""}`}>
              <div className="px-3 py-3"><div className="mono text-xs text-ink">{observedAt ? ago(observedAt) : "not recorded"}</div><div className="mt-1 text-2xs text-faint">{work.lastHeartbeatAt ? "last sign of life" : work.startedAt ? "run started" : "no run record"}</div></div>
              <div className="border-l border-hair px-3 py-3">
                <div className="flex items-start justify-between gap-3"><div><div className="line-clamp-2 text-sm font-semibold leading-snug text-ink">{work.title}</div><div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-2xs text-faint"><span className="font-semibold text-muted">{laneLabel}</span><span title={work.harness ? `Recorded harness: ${work.harness}` : undefined}>{harnessDisplayName(work.harness)}</span>{work.actor ? <span>by {work.actor}</span> : null}</div></div><StateBadge state={work.runStatus || work.state} /></div>
                {active ? <div className="runbar mt-2" aria-hidden /> : null}
              </div>
              <div className="border-l border-hair px-3 py-3"><p className={`line-clamp-3 text-xs leading-relaxed ${blocked ? "text-seam" : "text-muted"}`}>{work.nextAction || "No next step recorded."}</p>{work.retryDepth > 0 ? <div className="mono mt-1 text-2xs text-seam">tried {work.retryDepth + 1} times</div> : null}</div>
            </button>;
          })}
            </div>
          </div>
        </div>
        <div className="flex flex-wrap items-center justify-between gap-2 border-t border-hair px-3 py-2 text-2xs text-faint"><span>Showing {visibleRows.length} of {(total ?? rows.length).toLocaleString("en-US")} work items agents hold</span><a href="/work?lens=table" className="font-semibold text-work hover:underline">See all in Work →</a><span>A moving bar means the record says running.</span></div>
      </section>
      <Drawer open={selected !== null} onOpenChange={(open) => { if (!open) setSelected(null); }} title={selected?.work.title || "Live work"} meta={selected?.work.workId} footer={selected ? <a href={`/work?trace=${selected.work.workId}`} className="text-xs font-semibold text-work hover:underline">Open full trace →</a> : undefined}>
        {selected ? <div className="grid gap-4">
          <div className="grid grid-cols-2 gap-3"><div className="rounded-lg border border-hair bg-soft px-3 py-2"><div className="section-label">Work state</div><div className="mt-1"><StateBadge state={selected.work.state} /></div></div><div className="rounded-lg border border-hair bg-soft px-3 py-2"><div className="section-label">Last run</div><div className="mt-1"><StateBadge state={selected.work.runStatus || "unknown"} /></div></div></div>
          <div><div className="section-label">Agent</div><p className="mt-1 text-sm text-ink">{selected.laneLabel}</p><p className="mt-0.5 text-xs text-muted" title={selected.work.harness ? `Recorded harness: ${selected.work.harness}` : undefined}>{harnessDisplayName(selected.work.harness)}</p></div>
          <div><div className="section-label">Next step</div><p className="mt-1 text-sm leading-relaxed text-muted">{selected.work.nextAction || "No next step recorded."}</p></div>
          <IdRef id={selected.work.workId} kind="work" plain />
        </div> : null}
      </Drawer>
    </>
  );
}

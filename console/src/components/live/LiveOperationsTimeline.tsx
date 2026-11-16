"use client";

import Link from "next/link";
import { useState } from "react";

import { Drawer, EmptyState, IdRef, StateBadge } from "@/components/kit";
import { harnessDisplayName } from "@/lib/harness-label";

/** One row: a work item, who holds it, when it was picked up, its next step, and any run record. */
export type TimelineRow = {
  workId: string;
  title: string;
  state: string;
  stateLabel: string;
  next: string;
  pickedUpAgo: string;
  stale: boolean;
  href: string;
  agentName: string;
  /** from a run record; "" when this page sees none */
  runStatus: string;
  harness: string;
};

/**
 * The latest work picked up, newest first: pick-up time is the only real
 * time the work records carry for open work, so it is the order. Next steps
 * are in plain words (lib/plain-words.ts). Nothing moves unless a run record
 * says an agent is running it now.
 */
export function LiveOperationsTimeline({ rows, total, allHref }: { rows: TimelineRow[]; total: number; allHref: string }) {
  const [selected, setSelected] = useState<TimelineRow | null>(null);
  if (!rows.length) return <EmptyState>No agent holds any work right now.</EmptyState>;

  return (
    <>
      <section className="min-w-0 overflow-hidden rounded-xl border border-hair bg-panel shadow-sm">
        <div className="overflow-x-auto">
          <div className="min-w-[620px]">
            <div className="grid grid-cols-[128px_minmax(240px,1fr)_minmax(180px,.7fr)] border-b border-hair bg-soft/70 text-2xs font-bold uppercase tracking-wide text-faint">
              <div className="px-3 py-2.5">Picked up</div><div className="border-l border-hair px-3 py-2.5">Work and agent</div><div className="border-l border-hair px-3 py-2.5">Next step</div>
            </div>
            <div>
              {rows.map((row) => {
                const running = row.runStatus === "running";
                return (
                  <div key={row.workId} className={`grid w-full grid-cols-[128px_minmax(240px,1fr)_minmax(180px,.7fr)] border-b border-hair text-left last:border-b-0 hover:bg-soft ${row.state === "held" || row.state === "blocked" ? "bg-seam-soft/25" : ""}`}>
                    <div className="px-3 py-3"><div className={`text-xs ${row.stale ? "text-seam" : "text-ink"}`}>{row.pickedUpAgo || "not recorded"}</div>{row.stale ? <div className="mt-1 text-2xs text-seam">not moved in a week</div> : null}</div>
                    <div className="border-l border-hair px-3 py-3">
                      <div className="flex items-start justify-between gap-3"><div><Link href={row.href} className="line-clamp-2 text-sm font-semibold leading-snug text-ink hover:underline">{row.title}</Link><div className="mt-1 text-2xs text-faint"><span className="font-semibold text-muted">{row.agentName}</span>{row.runStatus ? <> · run record: {row.runStatus}</> : null}</div></div><StateBadge state={row.state} /></div>
                      {running ? <div className="runbar mt-2" aria-hidden /> : null}
                    </div>
                    <div className="border-l border-hair px-3 py-3"><p className="line-clamp-3 text-xs leading-relaxed text-muted">{row.next || "No next step recorded."}</p><button type="button" onClick={() => setSelected(row)} className="mt-1 text-2xs font-semibold text-work hover:underline">Preview</button></div>
                  </div>
                );
              })}
            </div>
          </div>
        </div>
        <div className="flex flex-wrap items-center justify-between gap-2 border-t border-hair px-3 py-2 text-2xs text-faint">
          <span>The {rows.length} picked up most recently, of {total.toLocaleString("en-US")} open work items</span>
          <Link href={allHref} className="font-semibold text-work hover:underline">See all {total.toLocaleString("en-US")} in Work →</Link>
        </div>
      </section>
      <Drawer open={selected !== null} onOpenChange={(open) => { if (!open) setSelected(null); }} title={selected?.title || "Work item"} meta={selected?.workId} footer={selected ? <Link href={selected.href} className="text-xs font-semibold text-work hover:underline">Open work page →</Link> : undefined}>
        {selected ? <div className="grid gap-4">
          <div className="grid grid-cols-2 gap-3"><div className="rounded-lg border border-hair bg-soft px-3 py-2"><div className="section-label">Work state</div><div className="mt-1"><StateBadge state={selected.state} /></div></div><div className="rounded-lg border border-hair bg-soft px-3 py-2"><div className="section-label">Picked up</div><div className={`mt-1 text-sm ${selected.stale ? "text-seam" : "text-ink"}`}>{selected.pickedUpAgo || "unknown"}</div></div></div>
          <div><div className="section-label">Agent</div><p className="mt-1 text-sm text-ink">{selected.agentName}</p><p className="mt-0.5 text-xs text-muted">{selected.runStatus ? `Latest run record: ${selected.runStatus}${selected.harness ? ` (${harnessDisplayName(selected.harness)})` : ""}.` : "This page sees no run record for it."}</p></div>
          <div><div className="section-label">Next step</div><p className="mt-1 text-sm leading-relaxed text-muted">{selected.next || "No next step recorded."}</p></div>
          <IdRef id={selected.workId} kind="work" plain />
        </div> : null}
      </Drawer>
    </>
  );
}

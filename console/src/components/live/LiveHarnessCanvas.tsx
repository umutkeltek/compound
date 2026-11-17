"use client";

import { Background, Controls, Handle, Position, ReactFlow, ReactFlowProvider } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import Link from "next/link";
import type { CSSProperties } from "react";
import { memo, useMemo, useState } from "react";

import { IdRef, StateBadge } from "@/components/kit";
import { harnessDisplayName } from "@/lib/harness-label";

export type LiveWork = {
  workId: string;
  title: string;
  state: string;
  /** the state in everyday words */
  stateLabel: string;
  /** the next step in plain words, "" when none */
  nextAction: string;
  /** "3 days ago", "" when unknown */
  pickedUpAgo: string;
  /** in progress and picked up more than a week ago */
  stale: boolean;
  /** the item's own page */
  href: string;
  /** from a run record; "" when this page sees none */
  harness: string;
  runStatus: string;
  actor: string;
  retryDepth: number;
};

export type LiveLane = {
  id: string;
  label: string;
  /** work items it holds */
  totalWork: number;
  /** of those, marked in progress */
  running: number;
  /** of those, on hold: waiting for you */
  needsMe: number;
  /** in progress and not moved in a week; null when no pick-up time is known */
  stale: number | null;
  /** Work list links for each number (lib/links.ts) */
  href: { all: string; progress: string; stale: string; onHold: string };
  work: LiveWork[];
};

type LaneData = { kind: "lane"; lane: LiveLane; color: string };
type WorkData = { kind: "work"; work: LiveWork; laneLabel: string; laneId: string; active: boolean; color: string };
type LiveData = LaneData | WorkData;
/** Run-record statuses that mean an agent is running it now. Only these animate (DESIGN.md §10.2). */
const ACTIVE_RUN = new Set(["running", "pending", "started"]);

/** Moving only when a run record says so; the work record's own state never animates. */
export function isActiveRun(work: Pick<LiveWork, "runStatus">): boolean {
  return ACTIVE_RUN.has(work.runStatus);
}

function laneColor(lane: Pick<LiveLane, "id" | "label">): string {
  const identity = `${lane.id} ${lane.label}`.toLowerCase();
  if (identity.includes("codex")) return "var(--product-fg)";
  if (identity.includes("claude")) return "var(--cap-fg)";
  if (identity.includes("script")) return "var(--memory-fg)";
  if (identity.includes("switchback") || identity.includes("api")) return "var(--good-fg)";
  return "var(--work-fg)";
}

const LaneNode = memo(function LaneNode({ data }: { data: LaneData }) {
  const live = data.lane.work.some(isActiveRun);
  const lane = data.lane;
  return (
    <button
      type="button"
      aria-label={`Inspect ${lane.label}: ${lane.totalWork} work items, ${lane.running} in progress, ${lane.needsMe} need you`}
      className="live-node w-56 rounded-lg border px-3 py-2.5 text-left shadow-lg"
      data-live={live}
      style={{ "--lane-color": data.color } as CSSProperties}
    >
      <Handle type="source" position={Position.Right} className="!border-0" style={{ background: data.color }} />
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0 truncate text-sm font-semibold text-[var(--canvas-ink)]">{lane.label}</div>
        {live ? <span className="live-presence text-good"><span className="live-dot" aria-hidden />running now</span> : null}
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-2xs text-[var(--canvas-muted)]">
        <span className="whitespace-nowrap">{lane.totalWork} items</span><span>·</span><span className="whitespace-nowrap">{lane.running} in progress</span>
        {lane.stale ? <span className="whitespace-nowrap text-seam">{lane.stale} not moved in a week</span> : null}
        {lane.needsMe > 0 ? <span className="whitespace-nowrap text-seam">{lane.needsMe} need you</span> : null}
      </div>
    </button>
  );
});

const WorkNode = memo(function WorkNode({ data }: { data: WorkData }) {
  const work = data.work;
  return (
    <button
      type="button"
      aria-label={`Inspect ${work.title}: ${work.stateLabel}`}
      className="live-node w-64 rounded-lg border px-3 py-2.5 text-left shadow-lg"
      data-live={data.active}
      style={{ "--lane-color": data.color } as CSSProperties}
    >
      <Handle type="target" position={Position.Left} className="!border-0" style={{ background: data.color }} />
      <div className="line-clamp-2 text-sm font-semibold leading-snug text-[var(--canvas-ink)]">{work.title}</div>
      <div className="mt-1.5 flex flex-wrap gap-x-2 gap-y-0.5 text-2xs text-[var(--canvas-muted)]">
        <span>{data.active ? "Running now" : work.stateLabel}</span>
        {work.pickedUpAgo ? <span>· picked up {work.pickedUpAgo}</span> : null}
        {work.stale ? <span className="text-seam">· not moved in a week</span> : null}
        {work.retryDepth > 0 ? <span className="text-seam">· retry {work.retryDepth}</span> : null}
      </div>
    </button>
  );
});

const nodeTypes = { lane: LaneNode, work: WorkNode };

function graphFor(lanes: LiveLane[]) {
  const nodes: any[] = [];
  const edges: any[] = [];
  let cursorY = 28;
  lanes.forEach((lane, laneIndex) => {
    const laneId = `lane-${lane.id || laneIndex}`;
    const color = laneColor(lane);
    const rows = Math.max(1, lane.work.length);
    nodes.push({ id: laneId, type: "lane", position: { x: 28, y: cursorY + Math.max(0, (rows - 1) * 38) }, data: { kind: "lane", lane, color } });
    lane.work.forEach((work, workIndex) => {
      const id = `work-${work.workId}-${laneIndex}-${workIndex}`;
      const active = isActiveRun(work);
      nodes.push({ id, type: "work", position: { x: 390, y: cursorY + workIndex * 82 }, data: { kind: "work", work, laneLabel: lane.label, laneId: lane.id, active, color } });
      edges.push({ id: `${laneId}-${id}`, source: laneId, target: id, animated: active, style: { stroke: active ? color : "var(--canvas-border)", strokeWidth: active ? 2 : 1.2 } });
    });
    cursorY += Math.max(118, rows * 82 + 24);
  });
  return { nodes, edges };
}

function initialSelection(lanes: LiveLane[]): LiveData | null {
  for (const lane of lanes) {
    const work = lane.work.find(isActiveRun);
    if (work) return { kind: "work", work, laneLabel: lane.label, laneId: lane.id, active: true, color: laneColor(lane) };
  }
  const lane = lanes[0];
  return lane ? { kind: "lane", lane, color: laneColor(lane) } : null;
}

const linkClass = "font-semibold text-work hover:underline";

/**
 * One count in the agent panel, in the canvas's own colours (ink on the
 * canvas background, like every other tile on it): a link number is
 * underlined, not blue on grey, so it reads on the dark canvas.
 */
function LaneCount({ label, value, href }: { label: string; value: number | null; href: string }) {
  return (
    <div className="rounded-lg border border-[var(--canvas-border)] bg-[var(--canvas-bg)] px-2 py-2">
      <div className="text-2xs font-bold uppercase text-[var(--canvas-muted)]">{label}</div>
      {value === null ? (
        <div className="mt-1 text-sm text-[var(--canvas-muted)]">unknown</div>
      ) : value > 0 ? (
        <Link href={href} className="mono mt-1 block w-fit text-lg font-semibold text-[var(--canvas-ink)] underline decoration-[var(--canvas-muted)] underline-offset-4 hover:decoration-[var(--canvas-ink)]">{value}</Link>
      ) : (
        <div className="mono mt-1 text-lg text-[var(--canvas-ink)]">0</div>
      )}
    </div>
  );
}

function Canvas({ lanes }: { lanes: LiveLane[] }) {
  const graph = useMemo(() => graphFor(lanes), [lanes]);
  const [selected, setSelected] = useState<LiveData | null>(() => initialSelection(lanes));
  if (!lanes.length) {
    return <div className="live-canvas flex h-[240px] items-center justify-center rounded-xl border text-center text-sm text-[var(--canvas-muted)]">No agent holds any work right now.</div>;
  }
  const work = selected?.kind === "work" ? selected.work : null;
  const lane = selected?.kind === "lane" ? selected.lane : null;
  const selectedColor = selected?.color ?? "var(--work-fg)";
  return (
    <div className="grid min-w-0 gap-3 xl:grid-cols-[minmax(0,1fr)_300px]">
      <div className="live-canvas live-canvas-stage min-w-0 overflow-hidden rounded-xl border">
        <ReactFlow
          nodes={graph.nodes}
          edges={graph.edges}
          nodeTypes={nodeTypes}
          onNodeClick={(_, node) => setSelected(node.data as LiveData)}
          onPaneClick={() => setSelected(initialSelection(lanes))}
          fitView
          fitViewOptions={{ padding: 0.08 }}
          minZoom={0.2}
          maxZoom={1.6}
          zoomOnScroll={false}
          panOnScroll={false}
          preventScrolling={false}
          nodesDraggable={false}
          nodesConnectable={false}
          proOptions={{ hideAttribution: true }}
        >
          <Background color="var(--canvas-border)" gap={24} size={1} />
          <Controls showInteractive={false} />
        </ReactFlow>
      </div>
      <aside
        className="live-inspector min-w-0 rounded-xl p-4 text-[var(--canvas-ink)]"
        style={{ "--lane-color": selectedColor } as CSSProperties}
        aria-live="polite"
      >
        <div className="flex items-center justify-between gap-3 border-b border-[var(--canvas-border)] pb-3">
          <div className="text-2xs font-bold uppercase tracking-wide text-[var(--canvas-muted)]">
            {work ? "Selected work" : "Agent"}
          </div>
          <span className="text-2xs text-[var(--canvas-muted)]">from the work records</span>
        </div>
        {work ? (
          <div className="mt-4 flex flex-col gap-4">
            <h3 className="line-clamp-6 text-lg font-semibold leading-snug" title={work.title}>{work.title}</h3>
            <div className="grid grid-cols-2 gap-2">
              <div className="rounded-lg border border-[var(--canvas-border)] bg-soft/30 px-3 py-2"><div className="text-2xs font-bold uppercase text-[var(--canvas-muted)]">Work state</div><div className="mt-1"><StateBadge state={work.state} /></div></div>
              <div className="rounded-lg border border-[var(--canvas-border)] bg-soft/30 px-3 py-2"><div className="text-2xs font-bold uppercase text-[var(--canvas-muted)]">Picked up</div><div className={`mt-1 text-sm ${work.stale ? "text-seam" : ""}`}>{work.pickedUpAgo || "unknown"}</div></div>
            </div>
            <p className="text-sm leading-relaxed text-[var(--canvas-muted)]">
              {selected?.kind === "work" ? <>Held by <span className="font-semibold text-[var(--canvas-ink)]">{selected.laneLabel}</span>. </> : null}
              {work.runStatus ? <>The latest run record says {plainRun(work.runStatus)}{work.harness ? ` (${harnessDisplayName(work.harness)})` : ""}.</> : "This page sees no run record for it, so whether an agent is on it this minute is unknown."}
              {work.stale ? " It was picked up more than a week ago and has not finished." : ""}
            </p>
            <div><div className="text-2xs font-bold uppercase text-[var(--canvas-muted)]">Next step</div><p className="mt-1 text-sm leading-relaxed text-[var(--canvas-muted)]">{work.nextAction || "No next step recorded."}</p></div>
            <Link href={work.href} className="rounded-md border border-work-line bg-work-soft px-3 py-2 text-center text-xs font-semibold text-work transition-colors hover:bg-work-soft/70">Open work page →</Link>
            <IdRef id={work.workId} kind="work" plain />
          </div>
        ) : lane ? (
          <div className="mt-4 flex flex-col gap-4">
            <h3 className="text-lg font-semibold">{lane.label}</h3>
            <div className="grid grid-cols-2 gap-2">
              <LaneCount label="Work items" value={lane.totalWork} href={lane.href.all} />
              <LaneCount label="In progress" value={lane.running} href={lane.href.progress} />
              <LaneCount label="Not moved in a week" value={lane.stale} href={lane.href.stale} />
              <LaneCount label="Need you" value={lane.needsMe} href={lane.href.onHold} />
            </div>
            {lane.totalWork > lane.work.length ? (
              <Link href={lane.href.all} className={`text-xs ${linkClass}`}>+{(lane.totalWork - lane.work.length).toLocaleString("en-US")} more: all its work in Work →</Link>
            ) : null}
            <p className="text-sm leading-relaxed text-[var(--canvas-muted)]">The canvas shows its two most urgent items: on hold first, then the ones not moved longest.</p>
          </div>
        ) : null}
      </aside>
    </div>
  );
}

/** A run-record status in words. */
function plainRun(status: string): string {
  if (ACTIVE_RUN.has(status)) return "it is running now";
  if (status === "succeeded") return "its last run finished";
  if (status === "failed") return "its last run failed";
  return `"${status.replace(/_/g, " ")}"`;
}

export function LiveHarnessCanvas({ lanes }: { lanes: LiveLane[] }) {
  return <ReactFlowProvider><Canvas lanes={lanes} /></ReactFlowProvider>;
}

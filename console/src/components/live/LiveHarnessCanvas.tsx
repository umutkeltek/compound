"use client";

import { Background, Controls, Handle, MiniMap, Position, ReactFlow, ReactFlowProvider } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import Link from "next/link";
import { memo, useMemo, useState } from "react";

import { Drawer, StateBadge } from "@/components/kit";
import { harnessDisplayName } from "@/lib/harness-label";

export type LiveWork = {
  workId: string;
  title: string;
  state: string;
  nextAction: string;
  harness: string;
  runStatus: string;
  actor: string;
  retryDepth: number;
};

export type LiveLane = {
  id: string;
  label: string;
  status: string;
  running: number;
  needsMe: number;
  totalWork: number;
  work: LiveWork[];
};

type LaneData = { kind: "lane"; lane: LiveLane };
type WorkData = { kind: "work"; work: LiveWork; laneLabel: string; active: boolean };
type LiveData = LaneData | WorkData;
const ACTIVE_RUN = new Set(["running", "pending", "started"]);

const LaneNode = memo(function LaneNode({ data }: { data: LaneData }) {
  const live = data.lane.running > 0;
  return (
    <div className="live-node w-52 rounded-lg border px-3 py-2.5 shadow-lg" data-live={live}>
      <Handle type="source" position={Position.Right} className="!border-0 !bg-work" />
      <div className="flex items-center justify-between gap-3">
        <div className="min-w-0">
          <div className="truncate text-sm font-semibold text-[var(--canvas-ink)]">{data.lane.label}</div>
          <div className="mono mt-0.5 text-2xs text-[var(--canvas-muted)]">{data.lane.id || "unbound lane"}</div>
        </div>
        <span className={`live-presence ${live ? "text-good" : "text-[var(--canvas-muted)]"}`}>
          <span className={live ? "live-dot" : "h-1.5 w-1.5 rounded-full bg-current"} aria-hidden />
          {live ? "live" : "idle"}
        </span>
      </div>
      <div className="mt-3 flex items-center gap-2 text-2xs text-[var(--canvas-muted)]">
        <span>{data.lane.running} running</span><span>·</span><span>{data.lane.totalWork} work</span>
        {data.lane.needsMe > 0 ? <><span>·</span><span className="text-seam">{data.lane.needsMe} paused</span></> : null}
      </div>
    </div>
  );
});

const WorkNode = memo(function WorkNode({ data }: { data: WorkData }) {
  return (
    <div className="live-node w-64 rounded-lg border px-3 py-2.5 shadow-lg" data-live={data.active}>
      <Handle type="target" position={Position.Left} className="!border-0 !bg-work" />
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="line-clamp-2 text-sm font-semibold leading-snug text-[var(--canvas-ink)]">{data.work.title}</div>
          <div className="mono mt-1 truncate text-2xs text-[var(--canvas-muted)]">{data.work.workId}</div>
        </div>
        <StateBadge state={data.work.runStatus || data.work.state} />
      </div>
      <div className="mt-2 flex flex-wrap gap-x-2 gap-y-1 text-2xs text-[var(--canvas-muted)]">
        <span title={data.work.harness ? `Recorded harness: ${data.work.harness}` : undefined}>{data.work.harness ? harnessDisplayName(data.work.harness) : data.laneLabel}</span>
        {data.work.actor ? <span>by {data.work.actor}</span> : null}
        {data.work.retryDepth > 0 ? <span className="text-seam">retry {data.work.retryDepth}</span> : null}
      </div>
    </div>
  );
});

const nodeTypes = { lane: LaneNode, work: WorkNode };

function graphFor(lanes: LiveLane[]) {
  const nodes: any[] = [];
  const edges: any[] = [];
  let cursorY = 28;
  lanes.forEach((lane, laneIndex) => {
    const laneId = `lane-${lane.id || laneIndex}`;
    const rows = Math.max(1, lane.work.length);
    nodes.push({ id: laneId, type: "lane", position: { x: 28, y: cursorY + Math.max(0, (rows - 1) * 38) }, data: { kind: "lane", lane } });
    lane.work.forEach((work, workIndex) => {
      const id = `work-${work.workId}-${laneIndex}-${workIndex}`;
      const active = ACTIVE_RUN.has(work.runStatus) || work.state === "running";
      nodes.push({ id, type: "work", position: { x: 390, y: cursorY + workIndex * 82 }, data: { kind: "work", work, laneLabel: lane.label, active } });
      edges.push({ id: `${laneId}-${id}`, source: laneId, target: id, animated: active, style: { stroke: active ? "var(--work-fg)" : "var(--canvas-border)", strokeWidth: active ? 1.8 : 1.2 } });
    });
    cursorY += Math.max(118, rows * 82 + 24);
  });
  return { nodes, edges };
}

function Canvas({ lanes }: { lanes: LiveLane[] }) {
  const graph = useMemo(() => graphFor(lanes), [lanes]);
  const [selected, setSelected] = useState<LiveData | null>(null);
  if (!lanes.length) {
    return <div className="live-canvas flex h-[520px] items-center justify-center rounded-xl border text-center text-sm text-[var(--canvas-muted)]">No harness projection is available. Nothing is animated without heartbeat or transition evidence.</div>;
  }
  const work = selected?.kind === "work" ? selected.work : null;
  const lane = selected?.kind === "lane" ? selected.lane : null;
  return (
    <>
      <div className="live-canvas h-[560px] overflow-hidden rounded-xl border">
        <ReactFlow
          nodes={graph.nodes}
          edges={graph.edges}
          nodeTypes={nodeTypes}
          onNodeClick={(_, node) => setSelected(node.data as LiveData)}
          onPaneClick={() => setSelected(null)}
          defaultViewport={{ x: 36, y: 22, zoom: 0.78 }} minZoom={0.35} maxZoom={1.6} nodesDraggable={false} nodesConnectable={false}
          proOptions={{ hideAttribution: true }}
        >
          <Background color="var(--canvas-border)" gap={24} size={1} />
          <Controls showInteractive={false} />
          <MiniMap pannable zoomable nodeColor={(node) => node.type === "lane" ? "var(--route-fg)" : "var(--work-fg)"} maskColor="rgba(3,7,13,.72)" style={{ background: "var(--canvas-panel)" }} />
        </ReactFlow>
      </div>
      <Drawer
        open={selected !== null}
        onOpenChange={(open) => { if (!open) setSelected(null); }}
        title={work?.title || lane?.label || "Live object"}
        meta={work?.workId || lane?.id}
        footer={work ? <Link href={`/work/${work.workId}`} className="text-xs font-semibold text-work hover:underline">Open full work trace</Link> : undefined}
      >
        {work ? (
          <div className="flex flex-col gap-4">
            <div className="grid grid-cols-2 gap-3">
              <div className="rounded-lg border border-hair bg-soft px-3 py-2"><div className="section-label">State</div><div className="mt-1"><StateBadge state={work.state} /></div></div>
              <div className="rounded-lg border border-hair bg-soft px-3 py-2"><div className="section-label">Run</div><div className="mt-1"><StateBadge state={work.runStatus || "unknown"} /></div></div>
            </div>
            <div><div className="section-label">Harness</div><p className="mt-1 text-sm text-ink" title={work.harness ? `Recorded harness: ${work.harness}` : undefined}>{harnessDisplayName(work.harness)}</p></div>
            <div><div className="section-label">Actor</div><p className="mono mt-1 text-xs text-muted">{work.actor || "not projected"}</p></div>
            <div><div className="section-label">What moves next</div><p className="mt-1 text-sm leading-relaxed text-muted">{work.nextAction || "No next action projected."}</p></div>
          </div>
        ) : lane ? (
          <div className="flex flex-col gap-4">
            <div className="grid grid-cols-3 gap-3">
              {[['Running', lane.running], ['Work', lane.totalWork], ['Paused', lane.needsMe]].map(([label, value]) => <div key={label} className="rounded-lg border border-hair bg-soft px-3 py-2"><div className="section-label">{label}</div><div className="mono mt-1 text-lg text-ink">{value}</div></div>)}
            </div>
            <p className="text-sm leading-relaxed text-muted">This lane is live only when its projection reports active work. Configured harnesses are not treated as activity.</p>
          </div>
        ) : null}
      </Drawer>
    </>
  );
}

export function LiveHarnessCanvas({ lanes }: { lanes: LiveLane[] }) {
  return <ReactFlowProvider><Canvas lanes={lanes} /></ReactFlowProvider>;
}

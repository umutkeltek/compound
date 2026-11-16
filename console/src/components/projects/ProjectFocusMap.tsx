"use client";
import { useState } from "react";
import { ReactFlowProvider } from "@xyflow/react";
import { SystemMapCanvas } from "@/components/map/SystemMapCanvas";
import type { LensGraph } from "@/lib/map/lens";

export default function ProjectFocusMap({ graph }: { graph: LensGraph }) {
  const [selected, setSelected] = useState<string | null>(graph.nodes[0]?.id || null);
  const [table, setTable] = useState(false);
  const node = graph.nodes.find((item) => item.id === selected);
  const label = (id: string) => graph.nodes.find((item) => item.id === id)?.label || id;
  return (
    <section aria-label="Focused work relationships" className="flex min-w-0 flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-3"><div><h2 className="text-lg font-semibold text-ink">Goal, work and evidence</h2><p className="mt-1 text-sm text-muted">One work item and its recorded references.</p></div><div className="flex rounded-lg border border-hair p-0.5" aria-label="Relationship presentation"><button aria-pressed={!table} onClick={() => setTable(false)} className={"rounded-md px-3 py-1.5 text-xs font-semibold " + (!table ? "bg-strong text-panel" : "text-muted")}>Map</button><button aria-pressed={table} onClick={() => setTable(true)} className={"rounded-md px-3 py-1.5 text-xs font-semibold " + (table ? "bg-strong text-panel" : "text-muted")}>Table</button></div></div>
      {table ? <div className="overflow-x-auto rounded-xl border border-hair p-4"><table className="w-full text-left text-sm"><caption className="sr-only">All relationships in the focused map</caption><thead><tr className="text-xs text-muted"><th scope="col" className="pb-3 pr-3">From</th><th scope="col" className="pb-3 pr-3">Relationship</th><th scope="col" className="pb-3">To</th></tr></thead><tbody>{graph.edges.map((edge) => <tr key={edge.id} className="border-t border-hair"><td className="py-3 pr-3">{label(edge.source)}</td><td className="py-3 pr-3 text-xs text-muted">{edge.label}</td><td className="py-3">{label(edge.target)}</td></tr>)}</tbody></table></div> : <ReactFlowProvider><SystemMapCanvas graph={graph} selectedId={selected} onSelect={setSelected} /></ReactFlowProvider>}
      <p className="text-xs leading-relaxed text-muted">{graph.note}</p>
      {node ? <details className="rounded-xl border border-hair p-4"><summary className="cursor-pointer text-sm font-medium text-ink">Selected reference: {node.label}</summary><p className="mt-2 break-all font-mono text-xs text-muted">{node.sublabel || node.id}</p><p className="mt-2 text-xs text-muted">{node.sources.join(" · ")}</p></details> : null}
      <p className="text-xs text-muted">Task prerequisites are not projected by these records. No task dependency is inferred from titles, owners or timing.</p>
    </section>
  );
}

import { MemoryGraphCanvas } from "@/components/map/MemoryGraphCanvas";
import { MemorySeedSearch } from "@/components/map/MemorySeedSearch";
import { Card, Chip, FreshnessChip } from "@/components/ui";
import { loadMemoryEgoGraph } from "@/lib/map/memory-graph";

export const dynamic = "force-dynamic";

const EDGE_LEGEND: { kind: string; tone: "green" | "red" | "gray" | "blue" | "violet" }[] = [
  { kind: "supports", tone: "green" },
  { kind: "contradicts", tone: "red" },
  { kind: "supersedes", tone: "gray" },
  { kind: "depends_on", tone: "blue" },
  { kind: "part_of", tone: "violet" },
];

export default async function MemoryGraphPage({
  searchParams,
}: {
  searchParams: Promise<{ seed?: string }>;
}) {
  const { seed: raw } = await searchParams;
  const seed = (raw ?? "umut-os").trim();
  const graph = await loadMemoryEgoGraph(seed, { maxNodes: 60 });

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-end justify-between gap-4">
        <div>
          <h1 className="text-[22px] font-semibold tracking-tight">Memory graph</h1>
          <p className="mt-0.5 text-[12.5px] text-ink-3">
            The dense web — claims (colored by entity) linked by supports / contradicts / supersedes.
            Seed at the center; edge-neighbors in the outer ring.
          </p>
        </div>
        <FreshnessChip source="~/.compound/ledger" />
      </div>

      <MemorySeedSearch initial={seed} />

      <div className="flex flex-wrap items-center gap-2">
        <span className="section-label">edges:</span>
        {EDGE_LEGEND.map((e) => (
          <Chip key={e.kind} tone={e.tone}>
            {e.kind}
          </Chip>
        ))}
        {graph.truncated ? (
          <Chip tone="orange">truncated — showing {graph.nodes.length}</Chip>
        ) : null}
      </div>

      <Card>
        <div className="mb-3 flex items-center justify-between">
          <span className="text-[12.5px] font-semibold text-ink">{graph.resolvedAs}</span>
          <span className="text-[11px] text-ink-3">
            {graph.nodes.length} claims · {graph.links.length} links
          </span>
        </div>
        {graph.found ? (
          <MemoryGraphCanvas graph={graph} />
        ) : (
          <div className="rounded-xl border border-dashed border-line bg-canvas/40 px-4 py-10 text-center text-[12.5px] text-ink-3">
            No entity or claim matches <code className="mono">{seed}</code>. Try a slug like{" "}
            <code className="mono">umut-os</code> or a claim id.
          </div>
        )}
      </Card>
    </div>
  );
}

import { PageHeader } from "@/components/kit";
import { LineageSearch } from "@/components/map/LineageSearch";
import { TimelineStep } from "@/components/map/TimelineStep";
import { Card, EmptyState, FreshnessChip } from "@/components/ui";
import { traceLineage } from "@/lib/map/lineage";

export const dynamic = "force-dynamic";

export default async function LineagePage({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const queryId = decodeURIComponent(id);
  const lineage = await traceLineage(queryId);

  return (
    <div className="flex flex-col gap-6">
      <PageHeader name="Map" title="Evidence history" lead="Trace where a record came from and which decisions use it."
        action={{ label: "Find a record", href: "#lineage-search" }} right={<FreshnessChip source="compound system lineage --json" />} />
      <div id="lineage-search" tabIndex={-1} className="scroll-mt-6" />

      <LineageSearch initial={queryId} />

      <Card>
        <div className="mb-4">
          <span className="section-label">Resolved</span>
          <div className="text-[13px] font-semibold text-ink">{lineage.resolvedAs}</div>
        </div>
        {lineage.found && lineage.steps.length ? (
          <ol className="flex flex-col">
            {lineage.steps.map((step, i) => (
              <TimelineStep key={i} step={step} last={i === lineage.steps.length - 1} />
            ))}
          </ol>
        ) : (
          <EmptyState>
            Not found — no claim, candidate, evidence, work, or entity matched{" "}
            <code className="mono">{queryId}</code>.
          </EmptyState>
        )}
      </Card>
    </div>
  );
}

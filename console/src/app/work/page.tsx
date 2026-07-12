import { FreshnessChip, PageHeader } from "@/components/kit";
import { WorkLenses } from "@/components/work/WorkLenses";
import { arr, getSourceConnections, getWorkbench, runJson, str } from "@/lib/compound";
import { buildLaneTraffic } from "@/lib/lane-traffic";
import { buildBoard, buildOffered, buildPipeline } from "@/lib/work-pipeline";

export const dynamic = "force-dynamic";

/**
 * Work — "Where is every unit of work in its lifecycle, and what can I move?"
 * Default Pipeline lens (the seven work stages), plus a by-state Board and
 * the Offered-packets menu; any card opens its history and available actions.
 */
export default async function WorkPage({
  searchParams,
}: {
  searchParams: Promise<{ trace?: string }>;
}) {
  const { trace } = await searchParams;

  const [workbench, sources, suggestions, packets, laneTraffic] = await Promise.all([
    getWorkbench(),
    getSourceConnections(),
    // Read-only: `director propose --dry-run` creates nothing (verified); the
    // suggestions feed the Propose stage. Fail-soft to null.
    runJson(["director", "propose", "--dry-run", "--json"]),
    runJson(["packets", "list", "--json"]),
    buildLaneTraffic(),
  ]);

  const open = arr(workbench?.open);
  const recent = arr(workbench?.recent);
  const proposals = arr(suggestions?.proposals ?? suggestions?.proposed ?? suggestions?.items);

  const stages = buildPipeline(workbench, sources, proposals);
  const columns = buildBoard(open, recent);
  const offered = buildOffered(arr(packets?.items));

  const initialTrace = str(trace).match(/^work_[a-z0-9]+$/) ? str(trace) : null;

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        breadcrumb="operate / work"
        title="Work"
        right={
          <FreshnessChip
            generatedAt={str(workbench?.generated_at)}
            source="compound workbench / sources / director dry-run"
          />
        }
      />
      <p className="text-sm text-muted" style={{ maxWidth: "var(--read-max)" }}>
        See where every job stands and what can move next. Scan Add to work for what waits on you; open any card
        for its history and available actions.
      </p>

      <WorkLenses
        stages={stages}
        columns={columns}
        offered={offered}
        openCount={open.length}
        initialTrace={initialTrace}
        laneTraffic={laneTraffic}
      />
    </div>
  );
}

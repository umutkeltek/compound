import { FreshnessChip, PageHeader } from "@/components/kit";
import { TrafficBoard } from "@/components/traffic/TrafficBoard";
import { getTraffic } from "@/lib/traffic";

export const dynamic = "force-dynamic";

/**
 * Traffic — "the multi-agent fleet in real time: who is executing what, how."
 * Server-seeded from the executor fleet + run-attempt join (lib/traffic), then
 * made live client-side by overlaying `compound stream --follow` moves
 * (lib/stream/useLiveFleet) — push, not the 30s reconcile poll. Read-only.
 */
export default async function TrafficPage() {
  const report = await getTraffic();
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        breadcrumb="operate / traffic"
        title="Traffic"
        right={
          <FreshnessChip
            generatedAt={report.generatedAt}
            source="compound executors / run-attempt / stream"
          />
        }
      />
      <p className="text-sm text-muted" style={{ maxWidth: "var(--read-max)" }}>
        The multi-agent fleet, live: each lane is an agent; each row a work it is executing, with the harness and
        run-attempt that drive it. State and actor stream in over the live envelope stream; the fleet reconciles on a
        short poll whether or not you are at the wheel.
      </p>
      <TrafficBoard report={report} />
    </div>
  );
}

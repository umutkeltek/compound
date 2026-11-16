import { ForAgents } from "@/components/settings/primitives";
import { BenchPanel } from "@/components/routing/BenchPanel";
import { LanesTable } from "@/components/routing/LanesTable";
import { PromotionBand } from "@/components/routing/PromotionBand";
import { RouteTraffic } from "@/components/routing/RouteTraffic";
import { RoutingTabs } from "@/components/routing/RoutingTabs";
import { FreshnessChip, hasCopy, PageHeader, ReadChip, ReadNotice, readsPending } from "@/components/kit";
import { PendingRefresh } from "@/components/PendingRefresh";
import { readAttention } from "@/lib/attention";
import { HEAVY_PROJECTION_TIMEOUT_MS, readProjection } from "@/lib/compound";
import { consoleMutationsEnabled } from "@/lib/console-mutations";
import { computeConsoleMutationReadiness } from "@/lib/instance-pin";
import { decideHref } from "@/lib/links";
import { buildRoutePromotionModel, getModelLanes, getRouteTraffic } from "@/lib/routing";

export const dynamic = "force-dynamic";

/**
 * Models and cost: which model handles work, what it costs, and which model
 * changes wait for a call. Each waiting change links to its decision on
 * Decide, which owns judging it; the header button opens the first one that
 * needs you, or there is none. The two heavy reads (the waiting list and the
 * review queue) are non-blocking: a cold page says it is reading. Commands sit
 * behind "For agents"; one with a blank to fill in is never shown.
 */

export default async function RoutingPage() {
  const [attentionLive, reviews, lanes, traffic] = await Promise.all([
    readAttention(),
    readProjection(["reviews", "--json"], { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS }),
    getModelLanes(),
    getRouteTraffic(),
  ]);
  const { read: attentionRead, report: attention } = attentionLive;
  const known = hasCopy(attentionRead) && hasCopy(reviews);
  const promotion = known ? buildRoutePromotionModel(reviews.value, attention, attentionRead.status === "loading" ? "unreachable" : attentionRead.status) : null;
  const first = promotion?.needsYou[0];
  const exampleClass = lanes?.capabilityClasses?.[0] ?? "";
  const mutationsEnabled = computeConsoleMutationReadiness(consoleMutationsEnabled()).ready;

  return (
    <div className="flex flex-col gap-8">
      <PendingRefresh pending={readsPending(attentionRead, reviews)} stamp={Date.now()} />
      <PageHeader
        name="Models and cost"
        action={first ? { label: "Decide the model change waiting for you", href: decideHref({ decision: first.key }) } : null}
        right={
          <>
            <span className="flex items-center gap-1 text-xs text-muted">Waiting changes <ReadChip read={attentionRead} /></span>
            {lanes?.generatedAt ? (
              <span className="flex items-center gap-1 text-xs text-muted">Models <FreshnessChip generatedAt={lanes.generatedAt} source="compound route model-capabilities --json" /></span>
            ) : null}
          </>
        }
      />
      {promotion ? (
        <PromotionBand needsYou={promotion.needsYou} holdRecord={promotion.watching} classA={promotion.classA} mutationsEnabled={mutationsEnabled} />
      ) : (
        <ReadNotice read={hasCopy(attentionRead) ? reviews : attentionRead} label="the model changes waiting for a call" />
      )}

      <RoutingTabs
        lanes={<LanesTable report={lanes} />}
        traffic={<RouteTraffic report={traffic} />}
        bench={<BenchPanel />}
      />

      <ForAgents
        label="For agents: plan a route"
        cmds={exampleClass ? [`compound route plan --capability-class ${exampleClass} --json`] : []}
      />
    </div>
  );
}

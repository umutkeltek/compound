import { ProductVisuals } from "@/components/products/VisualCampaigns";
import { notFound } from "next/navigation";
import { Suspense } from "react";
import { FreshnessChip, HeaderStat, PageHeader } from "@/components/kit";
import { AppNumbers } from "@/components/growth/AppNumbers";
import { GroupAudit, NextMoves, ProfileRow, makeLookup } from "@/components/growth/AppGrowth";
import { AppGlyph, DegradedGrowth, FocusChip, StatusLegend, coverageCounts } from "@/components/growth/GrowthKit";
import { OpenOnHash } from "@/components/growth/OpenOnHash";
import { arr, readProjection, readWorkProjectIndex, WORK_PROJECT_INDEX_READS } from "@/lib/compound";
import { AppWork } from "@/components/growth/AppWork";
import { appWorkModel } from "@/components/growth/app-work";
import { findApp, getGrowth, valueLabel } from "@/lib/growth";

export const dynamic = "force-dynamic";

/**
 * One app: what to do next and why (the hero), what the product is, then
 * the 12 groups of areas, then the numbers and the work already moving.
 * Every status, count and order is from `compound product growth --json`.
 */
export default async function AppPage({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  const growth = await getGrowth();
  const model = growth.model;
  if (!model) {
    return (
      <div className="flex flex-col gap-6">
        <PageHeader name="Portfolio" title="App not found" action={{ label: "Choose an app", href: "/apps" }} />
        <DegradedGrowth status={growth.status} error={growth.error} />
      </div>
    );
  }
  const app = findApp(model, slug);
  if (!app) notFound();

  const look = makeLookup(app, model.areas);
  const allocation = model.portfolio.allocation.find((row) => row.productId === app.productId);
  const judged = app.coverage.measured + app.coverage.partial + app.coverage.stale + app.coverage.blind;
  const kinds = app.profile.kinds.values.map(valueLabel);

  return (
    <div className="flex flex-col gap-8">
      <OpenOnHash />
      <PageHeader
        name="Portfolio"
        action={{ label: "Review next steps", href: "#app-next-steps" }}
        title={
          <span className="inline-flex items-center gap-3">
            <AppGlyph productId={app.productId} name={app.name} size="lg" />
            {app.name}
          </span>
        }
        stats={
          <>
            <span className="text-muted">{kinds.length ? kinds.join(" and ") : "Kind not on file"}</span>
            {allocation ? (
              <span className="inline-flex items-center gap-1.5">
                Rank <span className="font-semibold text-ink tabular-nums">#{allocation.rank}</span> <FocusChip focus={allocation.focus} />
              </span>
            ) : null}
            <HeaderStat value={`${app.coverage.measured} of ${judged}`} label="areas measured" />
            {app.hypotheses.open ? <HeaderStat value={app.hypotheses.open} label={app.hypotheses.open === 1 ? "open hypothesis" : "open hypotheses"} /> : null}
          </>
        }
        right={<FreshnessChip generatedAt={model.generatedAt} source={`compound product growth --product ${app.productId} --json`} />}
      />
      <div id="app-next-steps" tabIndex={-1} className="scroll-mt-6" />

      <section className="flex flex-col gap-3" aria-labelledby="next-question">
        <div>
          <h2 id="next-question" className="text-lg font-semibold tracking-tight text-ink">
            Recommended next steps
          </h2>
          <p className="mt-1 max-w-3xl text-sm text-muted">
            {app.nextMoves.length
              ? "In this order. Out-of-date data comes first, then the gaps that unblock the most other areas."
              : "Nothing is open right now."}
          </p>
        </div>
        <NextMoves app={app} look={look} />
      </section>

      <Suspense fallback={<p className="text-sm text-muted">Loading visual work…</p>}><ProductVisuals productIds={[app.productId, ...app.aliases]} /></Suspense>

      <ProfileRow app={app} areas={model.areas} groups={model.groups} />

      <section className="flex flex-col gap-3" aria-labelledby="groups-title">
        <div className="flex flex-wrap items-end justify-between gap-3">
          <div>
            <h2 id="groups-title" className="text-md font-semibold text-ink">
              All {model.groups.length} groups, {model.areas.length} areas
            </h2>
            <p className="text-sm text-muted">Groups with a next move are open. Open any other to see what we know there.</p>
          </div>
          <StatusLegend counts={coverageCounts(app.coverage)} />
        </div>
        <GroupAudit app={app} groups={model.groups} areas={model.areas} look={look} />
      </section>

      <AppNumbers app={app} />

      <AppWorkSection productId={app.productId} name={app.name} />

      <p className="text-xs text-faint">
        {model.boundary}. Catalog: {model.catalog.areas} areas in {model.catalog.groups} groups.
      </p>
    </div>
  );
}

/**
 * Every open work item the shared resolver places in this app. The reads are
 * non-blocking (they share the Work page's cache entries), so this renders at
 * once from the last good copies or says what it is still reading.
 */
async function AppWorkSection({ productId, name }: { productId: string; name: string }) {
  const nowMs = Date.now();
  const [projectIndex, workbench] = await Promise.all([
    readWorkProjectIndex({ nowMs }),
    readProjection([...WORK_PROJECT_INDEX_READS.work.args], { timeoutMs: WORK_PROJECT_INDEX_READS.work.timeoutMs }),
  ]);
  const model = appWorkModel(arr(workbench.value?.open), projectIndex.index, productId, nowMs);
  const labels = { work: "open work", apps: "your app list", inventory: "where each app's code lives" } as const;
  const keys = Object.keys(labels) as Array<keyof typeof labels>;
  return (
    <AppWork
      name={name}
      model={model}
      missing={projectIndex.missing.map((key) => ({ label: labels[key], read: projectIndex.reads[key] }))}
      reads={keys.map((key) => ({ label: labels[key], read: projectIndex.reads[key] }))}
    />
  );
}

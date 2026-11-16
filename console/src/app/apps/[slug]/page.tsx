import { ProductVisuals } from "@/components/products/VisualCampaigns";
import { notFound } from "next/navigation";
import { Suspense } from "react";
import { BreadcrumbTitle, FreshnessChip, HeaderStat, PageHeader } from "@/components/kit";
import { AppAds, AppNumbers } from "@/components/growth/AppNumbers";
import { AppNeedsYou } from "@/components/growth/AppNeedsYou";
import { GroupAudit, NextMoves, ProfileRow, makeLookup } from "@/components/growth/AppGrowth";
import { AppGlyph, DegradedGrowth, FocusChip, StatusLegend, coverageCounts } from "@/components/growth/GrowthKit";
import { OpenOnHash } from "@/components/growth/OpenOnHash";
import { arr, readProjection, readWorkProjectIndex, WORK_PROJECT_INDEX_READS } from "@/lib/compound";
import { AppWork } from "@/components/growth/AppWork";
import { appWorkModel } from "@/components/growth/app-work";
import { findApp, getGrowth, valueLabel } from "@/lib/growth";

export const dynamic = "force-dynamic";

/**
 * One app's page, a short briefing in the order the owner reads it: what
 * needs him, how the app is doing (every number dated), all of its open
 * work, the next steps, its ads, then what the product is and the 12 groups
 * of areas. Numbers, statuses and moves come from `compound product growth
 * --json`; the work comes from the shared work-to-app index.
 */
export default async function AppPage({ params }: { params: Promise<{ slug: string }> }) {
  const { slug } = await params;
  const growth = await getGrowth();
  const model = growth.model;
  if (!model) {
    return (
      <div className="flex flex-col gap-6">
        <PageHeader name="All apps" title="App not found" action={{ label: "Choose an app", href: "/apps" }} />
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

  // The app's work: non-blocking reads that share the Work page's cache entries.
  const nowMs = Date.now();
  const [projectIndex, workbench] = await Promise.all([
    readWorkProjectIndex({ nowMs }),
    readProjection([...WORK_PROJECT_INDEX_READS.work.args], { timeoutMs: WORK_PROJECT_INDEX_READS.work.timeoutMs }),
  ]);
  const work = appWorkModel(arr(workbench.value?.open), projectIndex.index, app.productId, nowMs);
  const labels = { work: "open work", apps: "your app list", inventory: "where each app's code lives" } as const;
  const keys = Object.keys(labels) as Array<keyof typeof labels>;

  return (
    <div className="flex flex-col gap-8">
      <OpenOnHash />
      <BreadcrumbTitle title={app.name} />
      <PageHeader
        name="All apps"
        action={null}
        title={
          <span className="inline-flex items-center gap-3">
            <AppGlyph productId={app.productId} name={app.name} size="lg" />
            {app.name}
          </span>
        }
        lead={`How ${app.name} is doing, what is being done for it, and what waits for you.`}
        stats={
          <>
            <span className="text-muted">{kinds.length ? kinds.join(" and ") : "Kind not on file"}</span>
            {allocation ? (
              <span className="inline-flex items-center gap-1.5">
                Rank <span className="font-semibold text-ink tabular-nums">#{allocation.rank}</span> of {model.apps.length} by proceeds <FocusChip focus={allocation.focus} />
              </span>
            ) : null}
            <HeaderStat value={`${app.coverage.measured} of ${judged}`} label="areas measured" />
          </>
        }
        right={<FreshnessChip generatedAt={model.generatedAt} source={`compound product growth --product ${app.productId} --json`} />}
      />

      <AppNeedsYou name={app.name} work={work} verdicts={[]} />

      <AppNumbers app={app} />

      <AppWork
        name={app.name}
        model={work}
        missing={projectIndex.missing.map((key) => ({ label: labels[key], read: projectIndex.reads[key] }))}
        reads={keys.map((key) => ({ label: labels[key], read: projectIndex.reads[key] }))}
      />

      <section id="app-next-steps" className="flex scroll-mt-24 flex-col gap-3" aria-labelledby="next-question">
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

      <AppAds app={app} />

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

      <p className="text-xs text-faint">
        {model.boundary}. Catalog: {model.catalog.areas} areas in {model.catalog.groups} groups.
      </p>
    </div>
  );
}

import Link from "next/link";
import { notFound } from "next/navigation";
import { Suspense } from "react";
import { FreshnessChip, HeaderStat, PageHeader } from "@/components/kit";
import { DivergingBars, Legend, LineChart } from "@/components/charts/Charts";
import { GroupAudit, NextMoves, ProfileRow, makeLookup } from "@/components/growth/AppGrowth";
import { AppGlyph, DegradedGrowth, FocusChip, KpiTile, Panel, StatusLegend, coverageCounts } from "@/components/growth/GrowthKit";
import { OpenOnHash } from "@/components/growth/OpenOnHash";
import { arr, getWorkbench, str } from "@/lib/compound";
import { findApp, formatValue, getGrowth, shortDate, STATUS_META, valueLabel, type FunnelStep } from "@/lib/growth";
import { ago } from "@/lib/time";

export const dynamic = "force-dynamic";

const SOURCE_LABEL: Record<string, string> = {
  app_store_connect: "App Store Connect",
  search_console: "Search Console",
  apple_search_ads: "Apple Search Ads",
  posthog: "PostHog",
  ga4: "Google Analytics",
};

function FunnelStrip({ steps }: { steps: FunnelStep[] }) {
  const firstBlind = steps.findIndex((s) => s.status === "blind");
  return (
    <div className="board-scroll overflow-x-auto pb-1">
      <ol className="flex min-w-max items-stretch gap-2">
        {steps.map((step, i) => {
          const meta = STATUS_META[step.status] ?? STATUS_META.blind;
          const gap = step.status === "blind" || step.status === "not_applicable" || step.status === "unknown";
          return (
            <li key={step.id} className="flex items-stretch gap-2">
              {i === firstBlind && firstBlind > 0 ? (
                <div className="flex w-6 flex-col items-center justify-center" title="Measurement stops here">
                  <span className="h-full w-px border-l border-dashed border-faint" />
                </div>
              ) : null}
              <div className={`flex w-44 flex-col gap-1.5 rounded-xl border p-3 ${gap ? "border-dashed border-hair bg-soft" : "border-hair bg-panel"}`}>
                <div className="flex items-center justify-between gap-2">
                  <span className="text-xs font-medium text-muted">{i + 1}. {step.label}</span>
                  <span className={`h-2 w-2 shrink-0 rounded-full ${meta.dot}`} title={meta.label} aria-label={meta.label} />
                </div>
                <div className={`text-xl font-semibold tabular-nums ${gap ? "text-faint" : "text-ink"}`}>
                  {step.value == null ? "—" : formatValue(step.value, step.id.includes("conversion") ? "ratio" : "count")}
                </div>
                <div className="text-2xs leading-snug text-muted">{step.note}</div>
              </div>
              {i < steps.length - 1 ? <span className="self-center text-faint" aria-hidden>→</span> : null}
            </li>
          );
        })}
      </ol>
    </div>
  );
}

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
        <PageHeader title="App" />
        <DegradedGrowth status={growth.status} error={growth.error} />
      </div>
    );
  }
  const app = findApp(model, slug);
  if (!app) notFound();

  const look = makeLookup(app, model.areas);
  const allocation = model.portfolio.allocation.find((row) => row.productId === app.productId);
  const hasAds = (app.series.ad_spend_usd ?? []).some((p) => p.value != null);
  const hasWeb = (app.series.web_clicks ?? []).some((p) => p.value != null);
  const hasSales = (app.series.proceeds_usd ?? []).some((p) => p.value != null);
  const hasSources = app.freshness.sources.length > 0;
  const judged = app.coverage.measured + app.coverage.partial + app.coverage.stale + app.coverage.blind;
  const kinds = app.profile.kinds.values.map(valueLabel);

  return (
    <div className="flex flex-col gap-8">
      <OpenOnHash />
      <PageHeader
        breadcrumb={`growth / apps / ${app.name}`}
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
              <span className="inline-flex items-center gap-1.5" title={allocation.reason}>
                Rank <span className="font-semibold text-ink tabular-nums">#{allocation.rank}</span> <FocusChip focus={allocation.focus} />
              </span>
            ) : null}
            <HeaderStat value={`${app.coverage.measured} of ${judged}`} label="areas measured" />
            {app.hypotheses.open ? <HeaderStat value={app.hypotheses.open} label={app.hypotheses.open === 1 ? "open hypothesis" : "open hypotheses"} /> : null}
          </>
        }
        right={<FreshnessChip generatedAt={model.generatedAt} source={`compound product growth --product ${app.productId} --json`} />}
      />

      <section className="flex flex-col gap-3" aria-labelledby="next-question">
        <div>
          <h2 id="next-question" className="text-hero font-semibold tracking-tight text-ink">
            What should I do next for {app.name}, and why?
          </h2>
          <p className="mt-1 max-w-3xl text-sm text-muted">
            {app.nextMoves.length
              ? "In this order. Out-of-date data comes first, then the gaps that unblock the most other areas."
              : "Nothing is open right now."}
          </p>
        </div>
        <NextMoves app={app} look={look} />
      </section>

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

      <section className="flex flex-col gap-4" aria-labelledby="numbers-title">
        <div className="flex flex-wrap items-baseline justify-between gap-3">
          <h2 id="numbers-title" className="text-md font-semibold text-ink">Numbers</h2>
          {allocation && hasSources ? <p className="max-w-3xl text-sm text-muted">{allocation.reason}</p> : null}
        </div>
        {hasSources ? (
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-xs text-faint">Data sources</span>
          {app.freshness.sources.map((source) => (
              <span
                key={source.source}
                className={`inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1 text-xs ${source.stale ? "border-seam-line bg-seam-soft text-seam" : "border-hair bg-panel text-muted"}`}
                title={`${source.latestDate} · ${source.daysSince} days ago`}
              >
                <span className={`h-1.5 w-1.5 rounded-full ${source.stale ? "bg-seam" : "bg-good"}`} aria-hidden />
                <span className="font-medium text-ink">{SOURCE_LABEL[source.source] ?? source.source}</span>
                {source.stale ? `stopped ${shortDate(source.latestDate)}` : `current to ${shortDate(source.latestDate)}`}
              </span>
            ))}
          {app.aliases.length ? <span className="text-2xs text-faint">Also recorded as {app.aliases.join(", ")}</span> : null}
        </div>
        ) : null}

        {hasSources ? (
          <>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
              <KpiTile kpiKey="proceeds_usd" kpi={app.kpis.proceeds_usd} color="var(--viz-1)" />
              <KpiTile kpiKey="units" kpi={app.kpis.units} color="var(--viz-2)" />
              <KpiTile kpiKey="web_clicks" kpi={app.kpis.web_clicks} color="var(--cap-fg)" />
              {hasAds ? (
                <KpiTile kpiKey="ad_cpa_usd" kpi={app.kpis.ad_cpa_usd} color="var(--memory-fg)" />
              ) : (
                <KpiTile kpiKey="first_time_downloads" kpi={app.kpis.first_time_downloads} color="var(--memory-fg)" />
              )}
            </div>

            <Panel title="Funnel" subtitle="From being seen to paying again. A dashed card means nothing measures that step yet.">
              <FunnelStrip steps={app.funnel} />
            </Panel>

            <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
              {hasSales || hasAds ? (
                <Panel
                  title="Money in and out"
                  subtitle="Proceeds per day above the line, ad spend below · USD"
                  right={<Legend items={[{ label: "Proceeds", color: "var(--viz-1)" }, { label: "Ad spend", color: "var(--viz-2)" }]} />}
                >
                  <DivergingBars up={app.series.proceeds_usd ?? []} down={app.series.ad_spend_usd ?? []} upLabel="Proceeds" downLabel="Ad spend" unit="usd" />
                </Panel>
              ) : null}
              <Panel
                title="Downloads and purchases"
                subtitle={hasAds ? "App Store units per day next to Apple Search Ads conversions (which also count re-downloads)" : "App Store units per day"}
                right={hasAds ? <Legend items={[{ label: "App Store units", color: "var(--viz-2)" }, { label: "Ad conversions", color: "var(--viz-1)" }]} /> : undefined}
              >
                <LineChart
                  unit="count"
                  empty="No App Store units recorded for this app."
                  series={[
                    { key: "units", label: "App Store units", color: "var(--viz-2)", points: app.series.units ?? [] },
                    ...(hasAds ? [{ key: "ads", label: "Ad conversions", color: "var(--viz-1)", points: app.series.ad_conversions ?? [] }] : []),
                  ]}
                />
              </Panel>
              {hasWeb ? (
                <Panel title="Web search clicks" subtitle="Google Search Console clicks per day">
                  <LineChart unit="count" series={[{ key: "clicks", label: "Web search clicks", color: "var(--viz-2)", points: app.series.web_clicks ?? [] }]} />
                </Panel>
              ) : null}
            </div>
          </>
        ) : (
          <p className="rounded-xl border border-dashed border-hair bg-page px-4 py-3 text-sm text-muted">
            No numbers yet. Sales, downloads and traffic appear here once a data source reports for this app.
          </p>
        )}
      </section>

      <Suspense
        fallback={
          <Panel title="Work on this app" subtitle="Loading open work…">
            <div className="flex flex-col gap-2" aria-hidden>
              <div className="skeleton h-5" />
              <div className="skeleton h-5" />
            </div>
          </Panel>
        }
      >
        <AppWork ids={[app.productId, ...app.aliases]} />
      </Suspense>

      <p className="text-xs text-faint">
        {model.boundary}. Catalog: {model.catalog.areas} areas in {model.catalog.groups} groups.
      </p>
    </div>
  );
}

/** Open work whose canonical surface is this app. The workbench read is slow, so it streams in on its own. */
async function AppWork({ ids }: { ids: string[] }) {
  const workbench = await getWorkbench();
  const wanted = new Set(ids);
  const work = arr(workbench?.open).filter((w) => wanted.has(str(w.canonical_surface)));
  return (
    <Panel title="Work on this app" subtitle={work.length ? `${work.length} open work items name this app` : "No open work names this app."}>
      {work.length ? (
        <ul className="divide-y divide-hair-2">
          {work.slice(0, 8).map((w) => {
            const id = str(w.work_id);
            const when = str(w.claimed_at) || str(w.created_at);
            return (
              <li key={id} className="flex items-start justify-between gap-4 py-2.5">
                <Link href={`/work?trace=${encodeURIComponent(id)}`} className="min-w-0 text-sm font-medium text-ink hover:underline">
                  <span className="line-clamp-2">{str(w.success_predicate) || str(w.desired_outcome) || id}</span>
                </Link>
                <span className="shrink-0 text-xs text-muted">{str(w.state)}{when ? ` · ${ago(when)}` : ""}</span>
              </li>
            );
          })}
        </ul>
      ) : null}
    </Panel>
  );
}

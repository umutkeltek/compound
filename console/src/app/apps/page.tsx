import Link from "next/link";
import { FreshnessChip, HeaderStat, PageHeader } from "@/components/kit";
import { DivergingBars, Legend, LineChart } from "@/components/charts/Charts";
import {
  AppLink,
  CoverageBar,
  DegradedGrowth,
  FocusChip,
  KpiTile,
  Panel,
  ShareBars,
} from "@/components/growth/GrowthKit";
import { allocationWhy, appHue, appSlug, formatKpi, formatValue, getGrowth, kpiPeriod, seriesPeriod, shortDate, valueLabel, type GrowthApp, type SeriesPoint } from "@/lib/growth";
import { adsHref } from "@/lib/links";

export const dynamic = "force-dynamic";


function sumLast(points: SeriesPoint[] | undefined, days: number): number {
  return (points ?? []).slice(-days).reduce((sum, p) => sum + (p.value ?? 0), 0);
}

/** Newest date any app reported for one source, and how many days ago that was. */
function sourceLag(apps: GrowthApp[], source: string): { latest: string; daysSince: number } | null {
  let best: { latest: string; daysSince: number } | null = null;
  for (const app of apps) {
    for (const s of app.freshness.sources) {
      if (s.source !== source || !s.latestDate) continue;
      if (!best || s.latestDate > best.latest) best = { latest: s.latestDate, daysSince: s.daysSince };
    }
  }
  return best;
}

/**
 * Portfolio — every app's money, downloads, web traffic and ads on one page,
 * plus the allocation call: where the next hour should go. All numbers come
 * from `compound product growth --json`; days without data show as gaps.
 */
export default async function PortfolioPage() {
  const growth = await getGrowth();
  const model = growth.model;
  if (!model) {
    return (
      <div className="flex flex-col gap-6">
        <PageHeader name="Portfolio" action={{ label: "Review data connections", href: "/sources" }} />
        <DegradedGrowth status={growth.status} error={growth.error} />
      </div>
    );
  }

  const { totals, series, allocation } = model.portfolio;
  const asc = sourceLag(model.apps, "app_store_connect");
  const ascStale = asc != null && asc.daysSince > model.staleDays;

  const byProceeds = model.apps
    .map((app) => ({ app, value: sumLast(app.series.proceeds_usd, 90) }))
    .filter((row) => row.value > 0)
    .sort((a, b) => b.value - a.value);
  const top = byProceeds.slice(0, 4);
  const otherValue = byProceeds.slice(4).reduce((sum, row) => sum + row.value, 0);
  const proceeds90 = byProceeds.reduce((sum, row) => sum + row.value, 0);
  const allocationById = new Map(allocation.map((row) => [row.productId, row]));
  const appById = new Map(model.apps.map((app) => [app.productId, app]));
  const appsSorted = [...model.apps].sort(
    (a, b) => (allocationById.get(a.productId)?.rank ?? 999) - (allocationById.get(b.productId)?.rank ?? 999),
  );
  // An app with no source at all is registered, not measured: list it apart instead of calling it "fresh".
  const withData = appsSorted.filter((app) => app.freshness.sources.length > 0);
  const noData = appsSorted.filter((app) => app.freshness.sources.length === 0);
  // Web traffic is dominated by a few sites that report on different days; a
  // summed line dips whenever one site skips a day. Plot the top sites apart.
  const topWeb = model.apps
    .map((app) => ({ app, clicks: sumLast(app.series.web_clicks, 90) }))
    .filter((row) => row.clicks > 0)
    .sort((a, b) => b.clicks - a.clicks)
    .slice(0, 2);

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        name="Portfolio"
        stats={
          <>
            <HeaderStat value={model.apps.length} label="registered apps" />
            <HeaderStat value={withData.length} label="with data" />
            <HeaderStat value={formatKpi(totals.proceeds_usd)} label={`proceeds, ${kpiPeriod(totals.proceeds_usd, model.windowDays)}`} />
            <HeaderStat value={formatKpi(totals.ad_spend_usd)} label={`ad spend, ${kpiPeriod(totals.ad_spend_usd, model.windowDays)}`} />
          </>
        }
        right={
          <>
            <FreshnessChip generatedAt={model.generatedAt} source="compound product growth --json" />
            <Link href="/growth" className="inline-flex items-center gap-1.5 rounded-lg border border-strong bg-strong px-3.5 py-2 text-sm font-medium text-panel shadow-sm hover:bg-ink">
              Growth by group →
            </Link>
          </>
        }
      />

      {ascStale && asc ? (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 rounded-xl border border-seam-line bg-seam-soft px-4 py-3 text-sm">
          <span className="font-semibold text-seam">App Store sales data stopped on {shortDate(asc.latest)} ({asc.daysSince} days ago).</span>
          <span className="text-muted">Proceeds and units after that date are missing, not zero. Refresh the App Store Connect import before judging any app on money.</span>
        </div>
      ) : null}

      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <KpiTile kpiKey="proceeds_usd" kpi={totals.proceeds_usd} color="var(--viz-1)" />
        <KpiTile kpiKey="units" kpi={totals.units} color="var(--viz-2)" />
        <KpiTile kpiKey="web_clicks" kpi={totals.web_clicks} color="var(--cap-fg)" />
        <KpiTile kpiKey="ad_cpa_usd" kpi={totals.ad_cpa_usd} color="var(--memory-fg)" href={adsHref({ window: 30 })} />
      </div>

      <div className="grid grid-cols-1 gap-4 xl:grid-cols-3">
        <Panel
          className="xl:col-span-2"
          title="Downloads and ad installs"
          subtitle={`New downloads per day next to ad installs · ${seriesPeriod(series.units) || `last ${model.seriesDays} days`}. Apple counts re-downloads as ad installs, so they can exceed new downloads.`}
          right={<Legend items={[{ label: "New downloads", color: "var(--viz-2)" }, { label: "Ad installs", color: "var(--viz-1)" }]} />}
        >
          <LineChart
            unit="count"
            series={[
              { key: "units", label: "New downloads", color: "var(--viz-2)", points: series.units ?? [] },
              { key: "ads", label: "Ad installs", color: "var(--viz-1)", points: series.ad_conversions ?? [] },
            ]}
          />
        </Panel>
        <Panel title="Proceeds by app" subtitle={`90 days, ${seriesPeriod(series.proceeds_usd, 90) || "of reported data"}`}>
          <div className="mb-5 text-3xl font-semibold tracking-tight text-ink tabular-nums">{formatValue(proceeds90, "usd")}</div>
          {top.length ? (
            <ShareBars
              format={(v) => formatValue(v, "usd")}
              rows={[
                ...top.map(({ app, value }) => ({ key: app.productId, label: <AppLink app={app} />, value, color: appHue(app.productId) })),
                ...(otherValue > 0 ? [{ key: "other", label: `${byProceeds.length - top.length} other apps`, value: otherValue, color: "var(--faint)" }] : []),
              ]}
            />
          ) : (
            <p className="text-sm text-faint">No proceeds reported in the last 90 days.</p>
          )}
        </Panel>
      </div>

      <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
        <Panel
          title="Money in and out"
          subtitle={`Proceeds per day above the line, ad spend below · US dollars · ${seriesPeriod(series.proceeds_usd) || `last ${model.seriesDays} days`}`}
          right={<Legend items={[{ label: "Proceeds", color: "var(--viz-1)" }, { label: "Ad spend", color: "var(--viz-2)" }]} />}
        >
          <DivergingBars up={series.proceeds_usd ?? []} down={series.ad_spend_usd ?? []} upLabel="Proceeds" downLabel="Ad spend" unit="usd" />
        </Panel>
        <Panel
          title="Web search clicks"
          subtitle={`Google Search Console clicks per day for the ${topWeb.length === 1 ? "top site" : `top ${topWeb.length} sites`} · ${formatKpi(totals.web_clicks)} clicks across all sites, ${kpiPeriod(totals.web_clicks, model.windowDays)}`}
          right={topWeb.length > 1 ? <Legend items={topWeb.map((row, i) => ({ label: row.app.name, color: i === 0 ? "var(--viz-2)" : "var(--viz-1)" }))} /> : undefined}
        >
          <LineChart
            unit="count"
            empty="No web search data."
            series={topWeb.map((row, i) => ({
              key: row.app.productId,
              label: row.app.name,
              color: i === 0 ? "var(--viz-2)" : "var(--viz-1)",
              points: row.app.series.web_clicks ?? [],
            }))}
          />
        </Panel>
      </div>

      <Panel
        title="Where the next hour should go"
        subtitle="Ranked by 90-day proceeds, then web traffic. Each call names its numbers; the rule is shown under the table."
      >
        <div className="overflow-x-auto">
          <table className="w-full min-w-xl text-left text-sm">
            <thead>
              <tr className="border-b border-hair text-xs text-faint">
                <th className="py-2 pr-3 font-medium">#</th>
                <th className="py-2 pr-3 font-medium">App</th>
                <th className="py-2 pr-3 font-medium">Focus</th>
                <th className="py-2 font-medium">Why</th>
              </tr>
            </thead>
            <tbody>
              {allocation.slice(0, 10).map((row) => (
                <tr key={row.productId} className="border-b border-hair-2 align-top last:border-0">
                  <td className="py-2.5 pr-3 tabular-nums text-faint">{row.rank}</td>
                  <td className="py-2.5 pr-3"><AppLink app={{ productId: row.productId, name: row.name }} /></td>
                  <td className="py-2.5 pr-3"><FocusChip focus={row.focus} /></td>
                  <td className="py-2.5 text-muted">{allocationWhy(row, appById.get(row.productId))}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {allocation[0]?.rule ? <p className="mt-3 text-xs text-faint">Rule: {allocation[0].rule}</p> : null}
      </Panel>

      <div id="apps-list" tabIndex={-1} className="scroll-mt-6" />
      <Panel title="All apps" subtitle={`${withData.length} apps with data${noData.length ? ` · ${noData.length} registered with no data yet` : ""}`}>
        <div className="overflow-x-auto">
          <table className="w-full min-w-3xl text-left text-sm">
            <thead>
              <tr className="border-b border-hair text-xs text-faint">
                <th className="py-2 pr-3 font-medium">App</th>
                <th className="py-2 pr-3 text-right font-medium">Proceeds</th>
                <th className="py-2 pr-3 text-right font-medium">New downloads</th>
                <th className="py-2 pr-3 text-right font-medium">Web clicks</th>
                <th className="py-2 pr-3 font-medium" title="Growth areas by status: measured, partly measured, out of date, no data yet, not on file">What we know</th>
                <th className="py-2 font-medium">Data</th>
              </tr>
            </thead>
            <tbody>
              {withData.map((app) => (
                <tr key={app.productId} className="border-b border-hair-2 last:border-0">
                  <td className="py-2.5 pr-3">
                    <div className="flex items-center gap-2">
                      <AppLink app={app} />
                      <span className="text-2xs text-faint">{app.kind === "unknown" ? "" : valueLabel(app.kind)}</span>
                    </div>
                  </td>
                  <td className="py-2.5 pr-3 text-right tabular-nums">{formatKpi(app.kpis.proceeds_usd)}</td>
                  <td className="py-2.5 pr-3 text-right tabular-nums">{formatKpi(app.kpis.units)}</td>
                  <td className="py-2.5 pr-3 text-right tabular-nums">{formatKpi(app.kpis.web_clicks)}</td>
                  <td className="w-56 py-2.5 pr-3"><CoverageBar coverage={app.coverage} /></td>
                  <td className="py-2.5 text-xs">
                    <span className={`inline-flex items-center gap-1.5 ${app.freshness.stale ? "text-seam" : "text-good"}`}>
                      <span className={`h-1.5 w-1.5 rounded-full ${app.freshness.stale ? "bg-seam" : "bg-good"}`} aria-hidden />
                      {app.freshness.stale ? "Stale" : "Fresh"}
                    </span>
                    {app.freshness.latestDate ? <span className="ml-1.5 text-faint">last {shortDate(app.freshness.latestDate)}</span> : null}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        {noData.length ? (
          <p className="mt-3 text-sm text-muted">
            <span className="font-medium text-ink">No data yet:</span>{" "}
            {noData.map((app, i) => (
              <span key={app.productId}>
                {i ? ", " : ""}
                <Link href={`/apps/${appSlug(app.productId)}`} className="hover:underline">{app.name}</Link>
              </span>
            ))}
            . Registered in Compound, but no App Store, web or ads source reports for them.
          </p>
        ) : null}
        <p className="mt-3 text-xs text-faint">
          Proceeds and new downloads: {kpiPeriod(totals.proceeds_usd, model.windowDays)}. Web clicks: {kpiPeriod(totals.web_clicks, model.windowDays)}.
        </p>
      </Panel>
    </div>
  );
}

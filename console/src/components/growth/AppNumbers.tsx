import Link from "next/link";

import { DivergingBars, Legend, LineChart } from "@/components/charts/Charts";
import { KpiTile, Panel } from "@/components/growth/GrowthKit";
import { adsHref } from "@/lib/links";
import { STATUS_META, formatValue, funnelPeriods, seriesPeriod, shortDate, type FunnelStep, type GrowthApp } from "@/lib/growth";

const SOURCE_LABEL: Record<string, string> = {
  app_store_connect: "App Store Connect",
  search_console: "Search Console",
  apple_search_ads: "Apple Search Ads",
  posthog: "PostHog",
  ga4: "Google Analytics",
};

/** The funnel, each step with the days it covers. Steps that do not say their days are never drawn as one funnel. */
export function FunnelStrip({ steps }: { steps: FunnelStep[] }) {
  const firstBlind = steps.findIndex((s) => s.status === "blind");
  const periods = funnelPeriods(steps);
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
                  {step.status === "partial" && step.value != null ? <span className="ml-1 text-xs font-normal text-seam">at least</span> : null}
                </div>
                {periods.steps[i] ? <div className="text-2xs font-medium text-faint" data-funnel-period>{periods.steps[i]}</div> : null}
                <div className="text-2xs leading-snug text-muted">{step.note}</div>
              </div>
              {i < steps.length - 1 && !periods.undated ? <span className="self-center text-faint" aria-hidden>→</span> : null}
            </li>
          );
        })}
      </ol>
    </div>
  );
}

/** The funnel's subtitle: the one range it covers, or why its steps cannot be compared. */
export function funnelSubtitle(steps: FunnelStep[]): string {
  const { shared, undated } = funnelPeriods(steps);
  if (undated) {
    return "The data does not say which days each step covers: one step can be a single day while the next is a month. Read each number on its own, not as a funnel.";
  }
  return `From being seen to paying again${shared ? `, ${shared}` : ""}. A dashed card means nothing measures that step yet.`;
}

/**
 * "How it is doing": every number dated. One proceeds figure (the tile), the
 * ads that bring installs (spend, installs, cost per install, each opening
 * this app's Ads page), the funnel and the daily charts with their days.
 */
export function AppNumbers({ app }: { app: GrowthApp }) {
  const hasAds = (app.series.ad_spend_usd ?? []).some((p) => p.value != null) || app.kpis.ad_spend_usd?.hasData === true;
  const hasWeb = (app.series.web_clicks ?? []).some((p) => p.value != null);
  const hasSales = (app.series.proceeds_usd ?? []).some((p) => p.value != null);
  const hasSources = app.freshness.sources.length > 0;
  const ads = adsHref({ product: app.productId, window: 30 });
  const seriesDays = seriesPeriod(app.series.proceeds_usd ?? app.series.units);

  return (
    <section className="flex scroll-mt-24 flex-col gap-4" id="app-numbers" aria-labelledby="numbers-title">
      <div className="flex flex-wrap items-baseline justify-between gap-3">
        <h2 id="numbers-title" className="text-lg font-semibold tracking-tight text-ink">How it is doing</h2>
        {hasSources ? (
          <div className="flex flex-wrap items-center gap-2">
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
          </div>
        ) : null}
      </div>

      {hasSources ? (
        <>
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
            <KpiTile kpiKey="proceeds_usd" kpi={app.kpis.proceeds_usd} color="var(--viz-1)" />
            <KpiTile kpiKey="units" kpi={app.kpis.units} color="var(--viz-2)" />
            <KpiTile kpiKey="web_clicks" kpi={app.kpis.web_clicks} color="var(--cap-fg)" />
          </div>

          {hasAds ? (
            <div className="flex flex-col gap-2" id="app-ads">
              <div className="flex flex-wrap items-baseline justify-between gap-2">
                <h3 className="text-md font-semibold text-ink">Ads</h3>
                <Link href={ads} className="text-sm font-medium text-work hover:underline">
                  See this app&apos;s campaigns →
                </Link>
              </div>
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
                <KpiTile kpiKey="ad_spend_usd" kpi={app.kpis.ad_spend_usd} color="var(--viz-2)" href={ads} />
                <KpiTile kpiKey="ad_conversions" kpi={app.kpis.ad_conversions} color="var(--viz-1)" href={ads} />
                <KpiTile kpiKey="ad_cpa_usd" kpi={app.kpis.ad_cpa_usd} color="var(--memory-fg)" href={ads} />
              </div>
            </div>
          ) : (
            <p className="text-sm text-muted">No ads run for this app: no ad report names it.</p>
          )}

          <Panel title="Funnel" subtitle={funnelSubtitle(app.funnel)}>
            <FunnelStrip steps={app.funnel} />
          </Panel>

          <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
            {hasSales || hasAds ? (
              <Panel
                title="Money in and out"
                subtitle={`Proceeds per day above the line, ad spend below · US dollars${seriesDays ? ` · ${seriesDays}` : ""}`}
                right={<Legend items={[{ label: "Proceeds", color: "var(--viz-1)" }, { label: "Ad spend", color: "var(--viz-2)" }]} />}
              >
                <DivergingBars up={app.series.proceeds_usd ?? []} down={app.series.ad_spend_usd ?? []} upLabel="Proceeds" downLabel="Ad spend" unit="usd" />
              </Panel>
            ) : null}
            <Panel
              title="Downloads and ad installs"
              subtitle={`${hasAds ? "New downloads per day next to ad installs (Apple counts re-downloads too)" : "New downloads per day"}${seriesPeriod(app.series.units) ? ` · ${seriesPeriod(app.series.units)}` : ""}`}
              right={hasAds ? <Legend items={[{ label: "New downloads", color: "var(--viz-2)" }, { label: "Ad installs", color: "var(--viz-1)" }]} /> : undefined}
            >
              <LineChart
                unit="count"
                empty="No App Store downloads recorded for this app."
                series={[
                  { key: "units", label: "New downloads", color: "var(--viz-2)", points: app.series.units ?? [] },
                  ...(hasAds ? [{ key: "ads", label: "Ad installs", color: "var(--viz-1)", points: app.series.ad_conversions ?? [] }] : []),
                ]}
              />
            </Panel>
            {hasWeb ? (
              <Panel title="Web search clicks" subtitle={`Google Search Console clicks per day · ${seriesPeriod(app.series.web_clicks)}`}>
                <LineChart unit="count" series={[{ key: "clicks", label: "Web search clicks", color: "var(--viz-2)", points: app.series.web_clicks ?? [] }]} />
              </Panel>
            ) : null}
          </div>
          {app.aliases.length ? <p className="text-2xs text-faint">Also recorded as {app.aliases.join(", ")}</p> : null}
        </>
      ) : (
        <p className="rounded-xl border border-dashed border-hair bg-page px-4 py-3 text-sm text-muted">
          No numbers yet: no App Store, web search or ads report names this app. Sales, downloads and traffic appear here once one does.
        </p>
      )}
    </section>
  );
}

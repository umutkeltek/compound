import Link from "next/link";
import { notFound } from "next/navigation";
import { Suspense } from "react";
import { CopyCmd } from "@/components/CopyCmd";
import { FreshnessChip, HeaderStat, PageHeader } from "@/components/kit";
import { DivergingBars, Legend, LineChart } from "@/components/charts/Charts";
import {
  AppGlyph,
  CoverageBar,
  DegradedGrowth,
  FocusChip,
  GrowthStatusChip,
  KpiTile,
  Panel,
} from "@/components/growth/GrowthKit";
import { arr, getWorkbench, str } from "@/lib/compound";
import { findApp, formatValue, getGrowth, shortDate, STATUS_META, type FunnelStep } from "@/lib/growth";
import { ago } from "@/lib/time";

export const dynamic = "force-dynamic";

const SOURCE_LABEL: Record<string, string> = {
  app_store_connect: "App Store Connect",
  search_console: "Search Console",
  apple_search_ads: "Apple Search Ads",
  posthog: "PostHog",
  ga4: "Google Analytics",
};

const KIND_LABEL: Record<string, string> = { ios_app: "iOS app", web: "Website", unknown: "Kind unknown" };

function FunnelStrip({ steps }: { steps: FunnelStep[] }) {
  const firstBlind = steps.findIndex((s) => s.status === "blind");
  return (
    <div className="board-scroll overflow-x-auto pb-1">
      <ol className="flex min-w-max items-stretch gap-2">
        {steps.map((step, i) => {
          const meta = STATUS_META[step.status] ?? STATUS_META.blind;
          const blind = step.status === "blind" || step.status === "not_applicable";
          return (
            <li key={step.id} className="flex items-stretch gap-2">
              {i === firstBlind && firstBlind > 0 ? (
                <div className="flex w-6 flex-col items-center justify-center" title="Measurement stops here">
                  <span className="h-full w-px border-l border-dashed border-crit" />
                </div>
              ) : null}
              <div className={`flex w-44 flex-col gap-1.5 rounded-xl border p-3 ${blind ? "border-dashed border-hair bg-soft" : "border-hair bg-panel"}`}>
                <div className="flex items-center justify-between gap-2">
                  <span className="text-xs font-medium text-muted">{i + 1}. {step.label}</span>
                  <span className={`h-2 w-2 shrink-0 rounded-full ${meta.dot}`} title={meta.label} aria-label={meta.label} />
                </div>
                <div className={`text-xl font-semibold tabular-nums ${blind ? "text-faint" : "text-ink"}`}>
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
 * One app's growth picture: who it is for, what is measured, where the funnel
 * goes dark, the 25-area audit and the work already moving on it. Every
 * number is from `compound product growth --json --product <id>`.
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

  const allocation = model.portfolio.allocation.find((row) => row.productId === app.productId);
  const hasAds = (app.series.ad_spend_usd ?? []).some((p) => p.value != null);
  const hasWeb = (app.series.web_clicks ?? []).some((p) => p.value != null);
  const hasSales = (app.series.proceeds_usd ?? []).some((p) => p.value != null);
  const applicable = app.coverage.measured + app.coverage.partial + app.coverage.stale + app.coverage.blind;

  return (
    <div className="flex flex-col gap-6">
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
            <span className="text-muted">{KIND_LABEL[app.kind] ?? app.kind}</span>
            {allocation ? <span className="inline-flex items-center gap-1.5">Rank <span className="font-semibold text-ink">#{allocation.rank}</span> <FocusChip focus={allocation.focus} /></span> : null}
            <HeaderStat value={`${app.coverage.measured} of ${applicable}`} label="areas measured" />
            <HeaderStat value={app.coverage.blind} label="blind" tone={app.coverage.blind ? "crit" : undefined} />
            <HeaderStat value={app.hypotheses.open} label={app.hypotheses.open === 1 ? "open hypothesis" : "open hypotheses"} />
          </>
        }
        right={
          <>
            <FreshnessChip generatedAt={model.generatedAt} source={`compound product growth --product ${app.productId} --json`} />
            <CopyCmd cmd={`compound product growth --product ${app.productId}`} compact />
          </>
        }
      />

      {allocation ? (
        <p className="max-w-4xl text-md text-ink">
          {allocation.reason}
        </p>
      ) : null}

      <div className="flex flex-wrap items-center gap-2">
        <span className="text-xs text-faint">Data sources</span>
        {app.freshness.sources.length ? (
          app.freshness.sources.map((source) => (
            <span
              key={source.source}
              className={`inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1 text-xs ${source.stale ? "border-seam-line bg-seam-soft text-seam" : "border-hair bg-panel text-muted"}`}
              title={`${source.latestDate} · ${source.daysSince} days ago`}
            >
              <span className={`h-1.5 w-1.5 rounded-full ${source.stale ? "bg-seam" : "bg-good"}`} aria-hidden />
              <span className="font-medium text-ink">{SOURCE_LABEL[source.source] ?? source.source}</span>
              {source.stale ? `stopped ${shortDate(source.latestDate)}` : `current to ${shortDate(source.latestDate)}`}
            </span>
          ))
        ) : (
          <span className="text-xs text-muted">None connected.</span>
        )}
        {app.aliases.length ? <span className="text-2xs text-faint">Also recorded as {app.aliases.join(", ")}</span> : null}
      </div>

      <div className="grid grid-cols-1 gap-4 xl:grid-cols-3">
        <Panel title="Next moves" subtitle="Earliest broken stage first" className="xl:col-span-2">
          {app.nextMoves.length ? (
            <ol className="flex flex-col gap-3">
              {app.nextMoves.map((move, i) => (
                <li key={`${move.areaId}-${i}`} className="flex gap-3">
                  <span className="flex h-6 w-6 shrink-0 items-center justify-center rounded-full bg-strong text-xs font-semibold text-panel">{i + 1}</span>
                  <div className="min-w-0">
                    <a href={`#area-${move.areaId}`} className="font-medium text-ink hover:underline">{move.title}</a>
                    <p className="text-sm text-muted">{move.why}</p>
                  </div>
                </li>
              ))}
            </ol>
          ) : (
            <p className="text-sm text-faint">No open move. Every applicable area is measured.</p>
          )}
        </Panel>
        <Panel title="Who it is for">
          {app.essence ? (
            <dl className="flex flex-col gap-3 text-sm">
              {[
                ["Audience", app.essence.audience],
                ["Problem", app.essence.problem],
                ["Value", app.essence.value],
              ].filter(([, v]) => v).map(([k, v]) => (
                <div key={k}>
                  <dt className="text-xs text-faint">{k}</dt>
                  <dd className="line-clamp-4 text-ink" title={v}>{v}</dd>
                </div>
              ))}
            </dl>
          ) : (
            <p className="text-sm text-muted">
              No positioning recorded. Without a named audience and problem, growth work has no target.{" "}
              <span className="text-faint">Record one with compound product essence.</span>
            </p>
          )}
        </Panel>
      </div>

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

      <Panel title="Funnel" subtitle="From being seen to paying again. A dashed card means nothing measures that step; the red line marks where measurement stops.">
        <FunnelStrip steps={app.funnel} />
      </Panel>

      <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
        {hasSales || hasAds ? (
          <Panel
            title="Money in and out"
            subtitle="Proceeds per day above the line, ad spend below · USD"
            right={<Legend items={[{ label: "Proceeds", color: "var(--viz-1)" }, { label: "Ad spend", color: "var(--viz-2)" }]} />}
          >
            <DivergingBars
              up={app.series.proceeds_usd ?? []}
              down={app.series.ad_spend_usd ?? []}
              upLabel="Proceeds"
              downLabel="Ad spend"
              unit="usd"
            />
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

      <Panel
        title="Growth audit"
        subtitle="25 areas. Each says what the evidence shows today and the one move that would improve what we know."
        right={<div className="w-64"><CoverageBar coverage={app.coverage} showLegend /></div>}
      >
        <div className="flex flex-col gap-6">
          {model.stages.map((stage) => {
            const areas = model.areas.filter((a) => a.stage === stage.id);
            return (
              <section key={stage.id}>
                <div className="mb-2 flex items-baseline gap-2">
                  <h3 className="text-md font-semibold text-ink">{stage.title}</h3>
                  <span className="text-sm text-muted">{stage.question}</span>
                </div>
                <div className="divide-y divide-hair-2 rounded-xl border border-hair">
                  {areas.map((area) => {
                    const row = app.audit.find((r) => r.areaId === area.id);
                    const status = row?.status ?? "blind";
                    return (
                      <div key={area.id} id={`area-${area.id}`} className="grid scroll-mt-24 grid-cols-1 gap-2 p-3 target:bg-work-soft md:grid-cols-[minmax(0,2fr)_minmax(0,3fr)] md:gap-6">
                        <div className="min-w-0">
                          <div className="flex items-center gap-2">
                            <span className="tabular-nums text-faint">{area.number}</span>
                            <span className="font-medium text-ink">{area.title}</span>
                            <GrowthStatusChip status={status} />
                          </div>
                          <p className="mt-0.5 text-xs text-muted">{area.question}</p>
                        </div>
                        <div className="min-w-0 text-sm">
                          <p className="text-ink">{row?.summary || "No evidence recorded."}</p>
                          {row?.nextMove && status !== "not_applicable" ? <p className="mt-0.5 text-muted">→ {row.nextMove}</p> : null}
                        </div>
                      </div>
                    );
                  })}
                </div>
              </section>
            );
          })}
        </div>
      </Panel>

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

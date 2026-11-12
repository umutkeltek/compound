import Link from "next/link";
import { FreshnessChip, HeaderStat, PageHeader } from "@/components/kit";
import { AppGlyph, DegradedGrowth, GrowthStatusChip, Panel } from "@/components/growth/GrowthKit";
import { STATUS_META, appSlug, barClass, getGrowth, type GrowthStatus } from "@/lib/growth";

export const dynamic = "force-dynamic";

const ORDER: GrowthStatus[] = ["measured", "partial", "stale", "blind", "not_applicable"];

/**
 * Growth audit — the 25 areas that decide whether an app becomes a business,
 * checked for every app against the evidence Compound actually holds. A cell
 * says what is known (measured / partial / stale) or that nothing measures it
 * (blind). It never scores quality it cannot see.
 */
export default async function GrowthAuditPage() {
  const growth = await getGrowth();
  const model = growth.model;
  if (!model) {
    return (
      <div className="flex flex-col gap-6">
        <PageHeader title="Growth audit" />
        <DegradedGrowth status={growth.status} error={growth.error} />
      </div>
    );
  }

  const rank = new Map(model.portfolio.allocation.map((row) => [row.productId, row.rank]));
  // Registered apps with no source at all would only add columns of "blind"; count them, don't chart them.
  const apps = [...model.apps]
    .filter((app) => app.freshness.sources.length > 0)
    .sort((a, b) => (rank.get(a.productId) ?? 999) - (rank.get(b.productId) ?? 999));
  const noDataCount = model.apps.length - apps.length;
  const cell = new Map<string, { status: GrowthStatus; summary: string; nextMove: string }>();
  for (const app of apps) for (const row of app.audit) cell.set(`${app.productId}|${row.areaId}`, row);

  const totals: Record<GrowthStatus, number> = { measured: 0, partial: 0, stale: 0, blind: 0, not_applicable: 0 };
  for (const app of apps) for (const row of app.audit) totals[row.status] = (totals[row.status] ?? 0) + 1;
  const applicable = totals.measured + totals.partial + totals.stale + totals.blind;

  const blindEverywhere = model.areas.filter((area) => {
    const rows = apps.map((app) => cell.get(`${app.productId}|${area.id}`)?.status).filter((s) => s && s !== "not_applicable");
    return rows.length > 0 && rows.every((s) => s === "blind");
  });

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        breadcrumb="growth / audit"
        title="Growth audit"
        stats={
          <>
            <HeaderStat value={model.areas.length} label="areas" />
            <HeaderStat value={apps.length} label={noDataCount ? `apps with data (${noDataCount} more have none)` : "apps"} />
            <HeaderStat value={applicable ? `${Math.round((totals.measured / applicable) * 100)}%` : "—"} label="of checks measured" />
            <HeaderStat value={blindEverywhere.length} label="areas blind in every app" tone={blindEverywhere.length ? "crit" : undefined} />
          </>
        }
        right={<FreshnessChip generatedAt={model.generatedAt} source="compound product growth --json" />}
      />

      {blindEverywhere.length ? (
        <div className="rounded-2xl border border-crit-line bg-crit-soft p-5">
          <div className="text-md font-semibold text-crit">
            {blindEverywhere.length} of {model.areas.length} areas are blind in every app.
          </div>
          <p className="mt-1 max-w-3xl text-sm text-muted">
            Nothing measures what happens after install, so Compound cannot tell a weak product page from a weak onboarding or a weak paywall. Measuring
            inside one app, in a way that keeps its privacy promise (on-device counts, opt-in feedback, or a handful of user interviews), would light up most
            of these at once.
          </p>
          <div className="mt-3 flex flex-wrap gap-1.5">
            {blindEverywhere.map((area) => (
              <span key={area.id} className="rounded-md border border-crit-line bg-panel px-2 py-0.5 text-xs text-ink">
                {area.number}. {area.title}
              </span>
            ))}
          </div>
        </div>
      ) : null}

      <div className="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-5">
        {model.stages.map((stage) => {
          const areaIds = new Set(model.areas.filter((a) => a.stage === stage.id).map((a) => a.id));
          const counts: Record<GrowthStatus, number> = { measured: 0, partial: 0, stale: 0, blind: 0, not_applicable: 0 };
          for (const app of apps) for (const row of app.audit) if (areaIds.has(row.areaId)) counts[row.status] += 1;
          const total = ORDER.reduce((sum, s) => sum + (s === "not_applicable" ? 0 : counts[s]), 0) || 1;
          return (
            <div key={stage.id} className="flex flex-col gap-3 rounded-2xl border border-hair bg-panel p-4">
              <div>
                <div className="text-md font-semibold text-ink">{stage.title}</div>
                <div className="text-xs text-muted">{stage.question}</div>
              </div>
              <div className="flex h-2 gap-0.5 overflow-hidden rounded-pill">
                {ORDER.filter((s) => s !== "not_applicable" && counts[s] > 0).map((s) => (
                  <span key={s} className={barClass(s)} style={{ width: `${(counts[s] / total) * 100}%` }} />
                ))}
              </div>
              <div className="text-xs text-muted">
                <span className="font-semibold text-ink tabular-nums">{Math.round((counts.measured / total) * 100)}%</span> measured ·{" "}
                <span className="tabular-nums">{counts.blind}</span> blind
              </div>
            </div>
          );
        })}
      </div>

      <Panel
        title="Every area, every app"
        subtitle="Hover a cell for what the evidence says; open an app for the full audit and its next moves."
        right={
          <div className="flex flex-wrap gap-1.5">
            {ORDER.map((s) => <GrowthStatusChip key={s} status={s} />)}
          </div>
        }
      >
        <div className="board-scroll overflow-x-auto">
          <table className="border-separate border-spacing-0 text-left text-sm">
            <thead>
              <tr>
                <th className="sticky left-0 z-10 min-w-64 bg-panel pb-2 pr-4 text-xs font-medium text-faint">Area</th>
                {apps.map((app) => (
                  <th key={app.productId} className="px-1 pb-2 align-bottom">
                    <Link href={`/apps/${appSlug(app.productId)}`} className="flex w-9 flex-col items-center gap-1.5 text-faint hover:text-ink" title={app.name}>
                      <span className="max-h-24 truncate text-xs font-medium [writing-mode:vertical-rl] rotate-180">{app.name}</span>
                      <AppGlyph productId={app.productId} name={app.name} size="sm" />
                    </Link>
                  </th>
                ))}
              </tr>
            </thead>
            {model.stages.map((stage) => (
              <tbody key={stage.id}>
                <tr>
                  <td colSpan={apps.length + 1} className="sticky left-0 bg-panel pb-1.5 pt-4 text-xs font-semibold uppercase tracking-wide text-faint">
                    {stage.title}
                  </td>
                </tr>
                {model.areas.filter((a) => a.stage === stage.id).map((area) => (
                  <tr key={area.id} className="group">
                    <td className="sticky left-0 z-10 border-t border-hair-2 bg-panel py-1.5 pr-4 group-hover:bg-soft" title={area.question}>
                      <span className="mr-2 tabular-nums text-faint">{area.number}</span>
                      <span className="text-ink">{area.title}</span>
                    </td>
                    {apps.map((app) => {
                      const row = cell.get(`${app.productId}|${area.id}`);
                      const status = row?.status ?? "blind";
                      return (
                        <td key={app.productId} className="border-t border-hair-2 px-1 py-1.5 text-center group-hover:bg-soft">
                          <Link
                            href={`/apps/${appSlug(app.productId)}#area-${area.id}`}
                            className={`mx-auto block h-5 w-7 rounded-md ${STATUS_META[status].fill}`}
                            title={`${app.name} · ${area.title}: ${STATUS_META[status].label}. ${row?.summary ?? ""}`}
                            aria-label={`${app.name}, ${area.title}: ${STATUS_META[status].label}`}
                          />
                        </td>
                      );
                    })}
                  </tr>
                ))}
              </tbody>
            ))}
          </table>
        </div>
      </Panel>

      <p className="text-xs text-faint">{model.boundary}</p>
    </div>
  );
}

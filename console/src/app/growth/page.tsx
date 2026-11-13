import Link from "next/link";
import { FreshnessChip, HeaderStat, PageHeader } from "@/components/kit";
import { AppGlyph, DegradedGrowth, StatusLegend, StatusMixBar } from "@/components/growth/GrowthKit";
import {
  STATUS_META,
  appSlug,
  countStatuses,
  getGrowth,
  hasEvidence,
  joinWords,
  type AuditRow,
  type GrowthApp,
  type GrowthArea,
  type GrowthStatus,
} from "@/lib/growth";

export const dynamic = "force-dynamic";

type Counts = Record<GrowthStatus, number>;

const judgedOf = (c: Counts) => c.measured + c.partial + c.stale + c.blind;
const knownOf = (c: Counts) => c.measured + c.partial + c.stale;

function sumCounts(list: Counts[]): Counts {
  const out = countStatuses([]);
  for (const c of list) for (const key of Object.keys(out) as GrowthStatus[]) out[key] += c[key];
  return out;
}

/** An app is in the grid when any source reports for it or any area has evidence (a record on file counts). */
function hasAnyEvidence(app: GrowthApp): boolean {
  return app.freshness.sources.length > 0 || app.audit.some((row) => hasEvidence(row.status));
}

function cellSummary(app: GrowthApp, group: string, c: Counts, move: boolean): string {
  const judged = judgedOf(c);
  const parts = [
    judged ? `${c.measured} of ${judged} measured` : "",
    knownOf(c) > c.measured ? `${knownOf(c) - c.measured} with some data` : "",
    c.blind ? `${c.blind} with no data yet` : "",
    c.unknown ? `${c.unknown} not on file` : "",
    c.deferred ? `${c.deferred} set aside` : "",
    !judged && !c.unknown && !c.deferred ? "nothing here applies" : "",
  ].filter(Boolean);
  return `${app.name} · ${group}: ${parts.join(", ")}.${move ? " Holds a next move." : ""}`;
}

function cellTitle(app: GrowthApp, title: string, row: AuditRow | undefined, move: boolean): string {
  const status = row?.status ?? "blind";
  const text = row?.summary || row?.reason || STATUS_META[status].meaning;
  return `${app.name} · ${row?.title || title}: ${STATUS_META[status].label}. ${text}${move ? " This is one of its next moves." : ""}`;
}

/**
 * Growth — where the portfolio has no data, group by group. Rows are the
 * catalog's groups and columns are apps; one group at a time opens into its
 * areas (`?group=<id>`), so the full areas × apps grid is never drawn at once.
 * Every status comes from `compound product growth --json`.
 */
export default async function GrowthPage({ searchParams }: { searchParams: Promise<Record<string, string | string[] | undefined>> }) {
  const params = await searchParams;
  const growth = await getGrowth();
  const model = growth.model;
  if (!model) {
    return (
      <div className="flex flex-col gap-6">
        <PageHeader title="Growth" />
        <DegradedGrowth status={growth.status} error={growth.error} />
      </div>
    );
  }

  const openGroup = typeof params.group === "string" && model.groups.some((g) => g.id === params.group) ? params.group : "";
  const rank = new Map(model.portfolio.allocation.map((row) => [row.productId, row.rank]));
  const byRank = (a: GrowthApp, b: GrowthApp) => (rank.get(a.productId) ?? 999) - (rank.get(b.productId) ?? 999) || a.name.localeCompare(b.name);
  const apps = model.apps.filter(hasAnyEvidence).sort(byRank);
  const noData = model.apps.filter((app) => !hasAnyEvidence(app)).sort(byRank);

  const rows = new Map<string, AuditRow>();
  for (const app of apps) for (const row of app.audit) rows.set(`${app.productId}|${row.areaId}`, row);
  const rowOf = (app: GrowthApp, areaId: string) => rows.get(`${app.productId}|${areaId}`);
  const moves = new Set<string>();
  for (const app of apps) for (const move of app.nextMoves) if (move.areaId) moves.add(`${app.productId}|${move.areaId}`);
  const isMove = (app: GrowthApp, areaId: string) => moves.has(`${app.productId}|${areaId}`);

  const areasOf = (groupId: string) => model.areas.filter((a) => a.group === groupId);
  const cellCounts = (app: GrowthApp, areas: GrowthArea[]) =>
    countStatuses(areas.map((a) => rowOf(app, a.id)).filter((r): r is AuditRow => Boolean(r)));
  /** An area no app has any evidence for, while at least one app could measure it. */
  const isDark = (area: GrowthArea) => {
    const statuses = apps.map((app) => rowOf(app, area.id)?.status ?? "blind");
    return !statuses.some(hasEvidence) && statuses.includes("blind");
  };

  const groupStats = model.groups.map((group) => {
    const areas = areasOf(group.id);
    const perApp = apps.map((app) => cellCounts(app, areas));
    const total = sumCounts(perApp);
    return { group, areas, perApp, total, dark: areas.filter(isDark) };
  });
  const total = sumCounts(groupStats.map((g) => g.total));
  const dark = groupStats.flatMap((g) => g.dark);
  const darkGroups = groupStats.filter((g) => g.areas.length > 0 && knownOf(g.total) === 0 && judgedOf(g.total) > 0).map((g) => g.group.title);

  const num = (v: number) => v.toLocaleString("en-US");
  const partly = knownOf(total) - total.measured;
  const answer = [
    dark.length
      ? `${num(dark.length)} of ${num(model.areas.length)} areas have no data in any app.`
      : `Every area has data in at least one app.`,
    darkGroups.length ? `${joinWords(darkGroups)} ${darkGroups.length === 1 ? "has" : "have"} none at all.` : "",
    `Across ${apps.length} ${apps.length === 1 ? "app" : "apps"}, ${num(total.measured)} app areas are fully measured${partly ? ` and ${num(partly)} more have partial or old data` : ""}.`,
    total.unknown ? `${num(total.unknown)} more cannot be judged until the product profiles say more.` : "",
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        breadcrumb="growth / audit"
        title="Growth"
        stats={
          <>
            <HeaderStat value={model.groups.length} label="groups" />
            <HeaderStat value={model.areas.length} label="areas" />
            <HeaderStat value={apps.length} label={noData.length ? `apps with data · ${noData.length} without` : "apps"} />
          </>
        }
        right={<FreshnessChip generatedAt={model.generatedAt} source="compound product growth --json" />}
      />

      <section className="flex flex-col gap-2" aria-labelledby="growth-question">
        <h2 id="growth-question" className="text-hero font-semibold tracking-tight text-ink">
          Where are we blind across the apps, group by group?
        </h2>
        <p className="max-w-4xl text-md text-ink">{answer}</p>
        <p className="max-w-4xl text-sm text-muted">
          Each cell is one app in one group. The bar shows what we know there. A number appears only where something is fully measured. Open a group to see its areas.
        </p>
      </section>

      {model.catalog.overlayError ? (
        <div className="rounded-2xl border border-seam-line bg-seam-soft px-5 py-4 text-sm">
          <div className="font-semibold text-seam">The private catalog file could not be read, so this page uses the built-in catalog.</div>
          <p className="mt-1 text-muted">
            {model.catalog.overlay ? <code className="mono text-xs">{model.catalog.overlay}</code> : null} {model.catalog.overlayError}
          </p>
        </div>
      ) : null}

      <section className="min-w-0 rounded-2xl border border-hair bg-panel p-5">
        <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
          <StatusLegend statuses={["measured", "partial", "stale", "blind", "unknown", "deferred", "not_applicable"]} />
          <span className="inline-flex items-center gap-1.5 text-2xs text-muted">
            <span className="h-1.5 w-1.5 rounded-full bg-ink" aria-hidden /> a next move for that app
          </span>
        </div>
        <div className="board-scroll overflow-x-auto">
          <table className="border-separate border-spacing-0 text-left text-sm">
            <thead>
              <tr>
                <th className="sticky left-0 z-10 min-w-56 bg-panel pb-2 pr-4 align-bottom text-xs font-medium text-faint">Group</th>
                {apps.map((app) => (
                  <th key={app.productId} className="px-0.5 pb-2 align-bottom">
                    <Link href={`/apps/${appSlug(app.productId)}`} className="mx-auto flex w-8 flex-col items-center gap-1.5 text-faint hover:text-ink" title={app.name}>
                      <span className="max-h-28 truncate text-xs font-medium [writing-mode:vertical-rl] rotate-180">{app.name}</span>
                      <AppGlyph productId={app.productId} name={app.name} size="sm" />
                    </Link>
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {groupStats.map(({ group, areas, perApp, dark: groupDark }) => {
                const open = group.id === openGroup;
                return [
                  <tr key={group.id} id={`g-${group.id}`} className={`group/row scroll-mt-24 ${open ? "bg-soft" : ""}`}>
                    <th scope="row" className={`sticky left-0 z-10 border-t border-hair-2 py-2 pr-4 text-left font-normal ${open ? "bg-soft" : "bg-panel group-hover/row:bg-soft"}`}>
                      <Link
                        href={open ? "/growth" : `/growth?group=${group.id}#g-${group.id}`}
                        scroll={false}
                        className="flex items-baseline gap-2 text-ink hover:underline"
                        aria-expanded={open}
                        title={group.question}
                      >
                        <span className={`inline-block w-3 shrink-0 text-faint transition-transform ${open ? "rotate-90" : ""}`} aria-hidden>
                          ›
                        </span>
                        <span className="font-semibold">{group.title}</span>
                        <span className="text-2xs text-faint tabular-nums">
                          {areas.length} {areas.length === 1 ? "area" : "areas"}
                        </span>
                      </Link>
                      <p className="max-w-52 truncate pl-5 text-2xs text-muted" title={group.question}>
                        {!areas.length
                          ? "No areas in the catalog yet"
                          : groupDark.length === areas.length
                            ? "No data in any app"
                            : groupDark.length
                              ? `${groupDark.length} with no data in any app`
                              : group.question}
                      </p>
                    </th>
                    {apps.map((app, i) => {
                      const counts = perApp[i];
                      const judged = judgedOf(counts);
                      const groupMove = areas.some((a) => isMove(app, a.id));
                      return (
                        <td key={app.productId} className={`border-t border-hair-2 px-0.5 py-2 align-middle ${open ? "" : "group-hover/row:bg-soft"}`}>
                          <Link
                            href={`/apps/${appSlug(app.productId)}#group-${group.id}`}
                            className="relative mx-auto flex w-8 flex-col items-center gap-1 rounded-md px-0.5 py-0.5 hover:bg-soft-2"
                            title={cellSummary(app, group.title, counts, groupMove)}
                            aria-label={cellSummary(app, group.title, counts, groupMove)}
                          >
                            {areas.length ? <StatusMixBar counts={counts} className="h-1.5" /> : <span className="h-1.5" />}
                            {/* Only a measured count is drawn: "0 of 5" would read as a zero, and a gap is not a zero. */}
                            <span className="h-4 text-2xs font-semibold tabular-nums text-ink">{counts.measured ? `${counts.measured}/${judged}` : ""}</span>
                            {groupMove ? <span className="absolute -right-0.5 -top-0.5 h-1.5 w-1.5 rounded-full bg-ink" aria-hidden /> : null}
                          </Link>
                        </td>
                      );
                    })}
                  </tr>,
                  ...(open
                    ? areas.map((area) => (
                        <tr key={area.id} className="bg-soft/60">
                          <th scope="row" className="sticky left-0 z-10 border-t border-hair-2 bg-soft py-1.5 pl-5 pr-4 text-left font-normal">
                            <span className="block max-w-52 truncate text-sm text-ink" title={area.question}>
                              {area.title}
                            </span>
                            <span className="block max-w-52 truncate text-2xs text-faint">
                              {groupDark.includes(area) ? "No data in any app" : <span className="mono">{area.id}</span>}
                            </span>
                          </th>
                          {apps.map((app) => {
                            const row = rowOf(app, area.id);
                            const status = row?.status ?? "blind";
                            const move = isMove(app, area.id);
                            return (
                              <td key={app.productId} className="border-t border-hair-2 px-0.5 py-1.5">
                                <Link
                                  href={`/apps/${appSlug(app.productId)}#area-${area.id}`}
                                  className={`mx-auto flex h-4 w-6 items-center justify-center rounded-xs ${STATUS_META[status].fill || "border border-hair-2"} ${move ? "ring-1 ring-ink" : ""}`}
                                  title={cellTitle(app, area.title, row, move)}
                                  aria-label={`${app.name}, ${row?.title || area.title}: ${STATUS_META[status].label}${move ? ", next move" : ""}`}
                                >
                                  {move ? <span className="h-1.5 w-1.5 rounded-full bg-ink" aria-hidden /> : null}
                                </Link>
                              </td>
                            );
                          })}
                        </tr>
                      ))
                    : []),
                ];
              })}
            </tbody>
          </table>
        </div>
      </section>

      {dark.length ? (
        <details className="group rounded-2xl border border-hair bg-panel p-5">
          <summary className="flex cursor-pointer list-none flex-wrap items-baseline gap-x-2 gap-y-1">
            <span className="inline-block w-3 text-faint transition-transform group-open:rotate-90" aria-hidden>
              ›
            </span>
            <span className="font-semibold text-ink">
              {dark.length} of {model.areas.length} areas have no data in any app.
            </span>
            <span className="text-sm text-muted">Open to see them by group.</span>
          </summary>
          <div className="mt-4 grid grid-cols-1 gap-x-6 gap-y-4 pl-5 md:grid-cols-2 xl:grid-cols-3">
            {groupStats
              .filter((g) => g.dark.length)
              .map((g) => (
                <div key={g.group.id}>
                  <div className="text-sm font-medium text-ink">
                    {g.group.title} <span className="text-xs font-normal text-faint tabular-nums">{g.dark.length} of {g.areas.length}</span>
                  </div>
                  <ul className="mt-1 flex flex-col gap-0.5 text-sm text-muted">
                    {g.dark.map((area) => (
                      <li key={area.id} title={area.question}>
                        {area.title}
                      </li>
                    ))}
                  </ul>
                </div>
              ))}
          </div>
        </details>
      ) : null}

      {noData.length ? (
        <p className="text-sm text-muted">
          <span className="font-medium text-ink">
            {noData.length} {noData.length === 1 ? "app has" : "apps have"} no data at all
          </span>{" "}
          and {noData.length === 1 ? "is" : "are"} left out of the grid:{" "}
          {noData.map((app, i) => (
            <span key={app.productId}>
              {i ? ", " : ""}
              <Link href={`/apps/${appSlug(app.productId)}`} className="hover:underline">
                {app.name}
              </Link>
            </span>
          ))}
          .
        </p>
      ) : null}

      <p className="text-xs text-faint">{model.boundary}</p>
    </div>
  );
}

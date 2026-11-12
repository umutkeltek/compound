import Link from "next/link";
import type { ReactNode } from "react";
import {
  FOCUS_META,
  KPI_META,
  STATUS_META,
  appHue,
  barClass,
  appSlug,
  formatKpi,
  kpiDelta,
  kpiLowerIsBetter,
  shortDate,
  type GrowthApp,
  type GrowthStatus,
  type Kpi,
  type KpiKey,
} from "@/lib/growth";

/* Server-safe building blocks shared by Portfolio, Growth audit and the app page. */

export function Panel({ title, subtitle, right, children, className = "" }: { title?: ReactNode; subtitle?: ReactNode; right?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <section className={`min-w-0 rounded-2xl border border-hair bg-panel p-5 ${className}`}>
      {title || right ? (
        <header className="mb-4 flex flex-wrap items-start justify-between gap-3">
          <div className="min-w-0">
            {title ? <h2 className="text-lg font-semibold tracking-tight text-ink">{title}</h2> : null}
            {subtitle ? <p className="mt-0.5 text-sm text-muted">{subtitle}</p> : null}
          </div>
          {right}
        </header>
      ) : null}
      {children}
    </section>
  );
}

/** Providers report with a lag of a day or two; more than 3 missing days means a feed stopped. */
export function windowHasGap(kpi: Kpi): boolean {
  return kpi.hasData && kpi.daysInWindow > 0 && kpi.daysReported < kpi.daysInWindow - 3;
}

/** The dotted meter from the reference KPI tiles — here it shows how many days of the window actually reported data. */
export function DayMeter({ reported, total, color }: { reported: number; total: number; color: string }) {
  const n = Math.max(1, Math.min(total || 30, 60));
  const filled = Math.round((Math.min(reported, total) / Math.max(1, total)) * n);
  return (
    <div className="flex h-2.5 items-stretch gap-0.5" role="img" aria-label={`${reported} of ${total} days reported`}>
      {Array.from({ length: n }, (_, i) => (
        <span key={i} className={`flex-1 rounded-pill ${i < filled ? "" : "bg-soft-2"}`} style={i < filled ? { backgroundColor: color } : undefined} />
      ))}
    </div>
  );
}

export function DeltaLine({ kpi, lowerIsBetter = false }: { kpi: Kpi | undefined; lowerIsBetter?: boolean }) {
  // A window with missing days is not comparable to a full one: a stopped
  // feed would read as a collapse. Say so instead of printing a fake drop.
  if (kpi && windowHasGap(kpi)) {
    return <span className="text-xs text-faint">Not comparable: window has missing days</span>;
  }
  const delta = kpiDelta(kpi);
  if (delta == null) return <span className="text-xs text-faint">No prior period to compare</span>;
  const up = delta >= 0;
  const good = lowerIsBetter ? !up : up;
  return (
    <span className="text-xs text-muted">
      <span className={`font-semibold ${good ? "text-good" : "text-crit"}`}>
        {up ? "↗" : "↘"} {up ? "+" : "−"}
        {Math.abs(delta * 100).toFixed(1)}%
      </span>{" "}
      vs. prior period
    </span>
  );
}

export function KpiTile({ kpiKey, kpi, color, label }: { kpiKey: KpiKey; kpi: Kpi | undefined; color: string; label?: string }) {
  const meta = KPI_META[kpiKey];
  const partial = kpi ? windowHasGap(kpi) : false;
  return (
    <div className="flex min-w-0 flex-col gap-3 rounded-2xl border border-hair bg-panel p-4" title={meta.hint}>
      <div className="text-sm text-muted">{label ?? meta.label}</div>
      <div className="text-2xl font-semibold tracking-tight text-ink tabular-nums">{formatKpi(kpi)}</div>
      <DayMeter reported={kpi?.daysReported ?? 0} total={kpi?.daysInWindow || 30} color={color} />
      <div className="flex flex-col gap-0.5">
        <DeltaLine kpi={kpi} lowerIsBetter={kpiLowerIsBetter(kpiKey)} />
        {!kpi?.hasData ? (
          <span className="text-xs text-faint">No data in the last {kpi?.daysInWindow || 30} days</span>
        ) : partial && kpi ? (
          <span className="text-xs text-seam">
            {kpi.daysReported} of {kpi.daysInWindow} days reported{kpi.latestDate ? ` · last ${shortDate(kpi.latestDate)}` : ""}
          </span>
        ) : null}
      </div>
    </div>
  );
}

export function GrowthStatusChip({ status, label }: { status: GrowthStatus; label?: string }) {
  const meta = STATUS_META[status] ?? STATUS_META.blind;
  return (
    <span className={`inline-flex shrink-0 items-center gap-1.5 rounded-md border px-1.5 py-0.5 text-2xs font-medium ${meta.chip}`} title={meta.meaning}>
      <span className={`h-1.5 w-1.5 rounded-full ${meta.dot}`} aria-hidden />
      {label ?? meta.short}
    </span>
  );
}

export function FocusChip({ focus }: { focus: string }) {
  const meta = FOCUS_META[focus];
  if (!meta) return null;
  return (
    <span className={`inline-flex shrink-0 items-center rounded-md border px-1.5 py-0.5 text-2xs font-medium ${meta.chip}`} title={meta.meaning}>
      {meta.label}
    </span>
  );
}

/** Stacked bar of the 25 areas by status for one app (measured · partial · stale · blind · n/a). */
export function CoverageBar({ coverage, showLegend = false }: { coverage: GrowthApp["coverage"]; showLegend?: boolean }) {
  const parts: { status: GrowthStatus; count: number }[] = [
    { status: "measured", count: coverage.measured },
    { status: "partial", count: coverage.partial },
    { status: "stale", count: coverage.stale },
    { status: "blind", count: coverage.blind },
    { status: "not_applicable", count: coverage.notApplicable },
  ];
  const total = parts.reduce((sum, p) => sum + p.count, 0) || 1;
  return (
    <div className="flex min-w-0 flex-col gap-1.5">
      <div className="flex h-2 w-full gap-0.5 overflow-hidden rounded-pill" role="img" aria-label={parts.map((p) => `${p.count} ${STATUS_META[p.status].label.toLowerCase()}`).join(", ")}>
        {parts.filter((p) => p.count > 0).map((p) => (
          <span key={p.status} className={barClass(p.status)} style={{ width: `${(p.count / total) * 100}%` }} />
        ))}
      </div>
      {showLegend ? (
        <div className="flex flex-wrap gap-x-3 gap-y-0.5 text-2xs text-muted">
          {parts.filter((p) => p.count > 0).map((p) => (
            <span key={p.status} className="inline-flex items-center gap-1">
              <span className={`h-1.5 w-1.5 rounded-full ${STATUS_META[p.status].dot}`} aria-hidden />
              {p.count} {STATUS_META[p.status].label.toLowerCase()}
            </span>
          ))}
        </div>
      ) : null}
    </div>
  );
}

export function AppGlyph({ productId, name, size = "md" }: { productId: string; name: string; size?: "sm" | "md" | "lg" }) {
  const cls = size === "lg" ? "h-10 w-10 text-lg" : size === "sm" ? "h-5 w-5 text-2xs" : "h-7 w-7 text-sm";
  return (
    <span className={`inline-flex shrink-0 items-center justify-center rounded-full font-bold text-white ${cls}`} style={{ backgroundColor: appHue(productId) }} aria-hidden>
      {name.charAt(0)}
    </span>
  );
}

export function AppLink({ app }: { app: Pick<GrowthApp, "productId" | "name"> }) {
  return (
    <Link href={`/apps/${appSlug(app.productId)}`} className="inline-flex min-w-0 items-center gap-2 font-medium text-ink hover:underline">
      <AppGlyph productId={app.productId} name={app.name} size="sm" />
      <span className="truncate">{app.name}</span>
    </Link>
  );
}

/** Horizontal share bars (the reference's "Gross volume" card). */
export function ShareBars({ rows, format }: { rows: { key: string; label: ReactNode; value: number; color: string }[]; format: (v: number) => string }) {
  const max = Math.max(1, ...rows.map((r) => r.value));
  return (
    <div className="flex flex-col gap-4">
      {rows.map((row) => (
        <div key={row.key} className="flex flex-col gap-1.5">
          <div className="flex items-center justify-between gap-3 text-sm">
            <span className="min-w-0 truncate text-muted">{row.label}</span>
            <span className="font-semibold tabular-nums text-ink">{format(row.value)}</span>
          </div>
          <div className="relative h-5 overflow-hidden rounded-md bg-soft-2">
            <span className="hatch absolute inset-0 text-faint opacity-60" aria-hidden />
            <span className="absolute inset-y-0 left-0 rounded-md" style={{ width: `${Math.max(2, (row.value / max) * 100)}%`, backgroundColor: row.color }} />
          </div>
        </div>
      ))}
    </div>
  );
}

export function DegradedGrowth({ status, error }: { status: string; error: string | null }) {
  return (
    <div className="rounded-2xl border border-seam-line bg-seam-soft p-5 text-sm text-seam">
      <div className="font-semibold">App growth data could not be loaded{status === "stale" ? " (showing nothing rather than a stale guess)" : ""}.</div>
      <p className="mt-1 text-muted">
        This page reads <code className="mono">compound product growth --json</code>. If the installed <code className="mono">compound</code> is older than this console, rebuild it onto your PATH.
        {error ? <span className="mt-1 block text-xs text-faint">{error.slice(0, 240)}</span> : null}
      </p>
    </div>
  );
}

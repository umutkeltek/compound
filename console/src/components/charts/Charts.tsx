"use client";

import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";

/*
 * Small, honest SVG charts for the growth pages (dataviz method):
 * one y-axis per chart, thin 2px lines, recessive grid, legend for ≥2 series,
 * crosshair + tooltip on hover, and gaps (never zeros) where a day has no data.
 * Series colours come from validated tokens --viz-1 / --viz-2.
 */

export type Point = { date: string; value: number | null };
export type ChartUnit = "usd" | "count";

/** Axis/tooltip formatter. Lives here (not passed from the server) because functions cannot cross the RSC boundary. */
function formatter(unit: ChartUnit): (v: number) => string {
  if (unit === "usd") {
    return (v) => (Math.abs(v) >= 1000 ? `$${(v / 1000).toFixed(1)}k` : `$${v.toLocaleString("en-US", { maximumFractionDigits: v < 10 ? 2 : 0 })}`);
  }
  return (v) => (Math.abs(v) >= 10_000 ? `${(v / 1000).toFixed(1)}k` : Math.round(v).toLocaleString("en-US"));
}
export type LineSeries = { key: string; label: string; color: string; points: Point[] };

function useWidth<T extends HTMLElement>(): [React.RefObject<T | null>, number] {
  const ref = useRef<T | null>(null);
  const [width, setWidth] = useState(0);
  useEffect(() => {
    if (!ref.current) return;
    const el = ref.current;
    const ro = new ResizeObserver(([entry]) => setWidth(Math.floor(entry.contentRect.width)));
    ro.observe(el);
    setWidth(Math.floor(el.getBoundingClientRect().width));
    return () => ro.disconnect();
  }, []);
  return [ref, width];
}

function niceMax(v: number): number {
  if (v <= 0) return 1;
  const exp = Math.pow(10, Math.floor(Math.log10(v)));
  const f = v / exp;
  const nice = f <= 1 ? 1 : f <= 2 ? 2 : f <= 2.5 ? 2.5 : f <= 5 ? 5 : 10;
  return nice * exp;
}

function shortDay(iso: string): string {
  const d = new Date(`${iso.slice(0, 10)}T00:00:00Z`);
  return Number.isNaN(d.getTime()) ? iso : d.toLocaleDateString("en-US", { month: "short", day: "numeric", timeZone: "UTC" });
}

export function Legend({ items }: { items: { label: string; color: string; dashed?: boolean }[] }) {
  return (
    <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted">
      {items.map((item) => (
        <span key={item.label} className="inline-flex items-center gap-1.5">
          <span className="inline-block h-2.5 w-1 rounded-pill" style={{ backgroundColor: item.color }} aria-hidden />
          {item.label}
        </span>
      ))}
    </div>
  );
}

function Tooltip({ x, width, children }: { x: number; width: number; children: ReactNode }) {
  const left = Math.min(Math.max(x + 12, 0), Math.max(0, width - 170));
  return (
    <div
      className="pointer-events-none absolute top-2 z-10 w-40 rounded-lg border border-hair bg-panel px-2.5 py-2 text-xs shadow-lg"
      style={{ left }}
    >
      {children}
    </div>
  );
}

const PAD = { top: 12, right: 12, bottom: 24, left: 44 };

/** Line chart over daily points. Nulls break the line, so a stopped feed reads as a gap, not a crash to zero. */
export function LineChart({
  series,
  unit,
  height = 240,
  area = true,
  empty = "No data in this window.",
}: {
  series: LineSeries[];
  unit: ChartUnit;
  height?: number;
  area?: boolean;
  empty?: string;
}) {
  const format = formatter(unit);
  const [ref, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const dates = series[0]?.points.map((p) => p.date) ?? [];
  const values = series.flatMap((s) => s.points.map((p) => p.value).filter((v): v is number => v != null));
  const hasData = values.length > 0;
  const max = niceMax(Math.max(0, ...values));
  const w = Math.max(0, width - PAD.left - PAD.right);
  const h = height - PAD.top - PAD.bottom;
  const x = (i: number) => PAD.left + (dates.length <= 1 ? 0 : (i / (dates.length - 1)) * w);
  const y = (v: number) => PAD.top + h - (v / max) * h;

  const paths = useMemo(
    () =>
      series.map((s) => {
        const segments: string[] = [];
        const areas: string[] = [];
        let seg: [number, number][] = [];
        const flush = () => {
          if (seg.length) {
            segments.push(seg.map(([px, py], j) => `${j ? "L" : "M"}${px.toFixed(1)},${py.toFixed(1)}`).join(""));
            const base = PAD.top + h;
            areas.push(
              `M${seg[0][0].toFixed(1)},${base}` + seg.map(([px, py]) => `L${px.toFixed(1)},${py.toFixed(1)}`).join("") + `L${seg[seg.length - 1][0].toFixed(1)},${base}Z`,
            );
          }
          seg = [];
        };
        s.points.forEach((p, i) => {
          if (p.value == null) flush();
          else seg.push([x(i), y(p.value)]);
        });
        flush();
        return { key: s.key, color: s.color, segments, areas };
      }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [series, width, height, max],
  );

  const ticks = [0, 0.25, 0.5, 0.75, 1].map((t) => t * max);
  const xTicks = dates.length ? [0, Math.floor((dates.length - 1) / 3), Math.floor(((dates.length - 1) * 2) / 3), dates.length - 1] : [];

  function onMove(event: React.MouseEvent<SVGRectElement>) {
    const rect = event.currentTarget.getBoundingClientRect();
    const rel = event.clientX - rect.left;
    const i = Math.round((rel / Math.max(1, rect.width)) * (dates.length - 1));
    setHover(Math.max(0, Math.min(dates.length - 1, i)));
  }

  return (
    <div ref={ref} className="relative w-full" style={{ height }}>
      {!hasData ? (
        <div className="flex h-full items-center justify-center rounded-xl border border-dashed border-hair text-sm text-faint">{empty}</div>
      ) : width > 0 ? (
        <>
          <svg width={width} height={height} role="img" aria-label={series.map((s) => s.label).join(" and ")}>
            {ticks.map((t) => (
              <g key={t}>
                <line x1={PAD.left} x2={PAD.left + w} y1={y(t)} y2={y(t)} stroke="var(--viz-grid)" strokeWidth={1} />
                <text x={PAD.left - 8} y={y(t) + 4} textAnchor="end" className="fill-faint text-2xs tabular-nums">
                  {format(t)}
                </text>
              </g>
            ))}
            {xTicks.map((i) => (
              <text key={i} x={x(i)} y={height - 6} textAnchor={i === 0 ? "start" : i === dates.length - 1 ? "end" : "middle"} className="fill-faint text-2xs">
                {shortDay(dates[i])}
              </text>
            ))}
            {paths.map((p) => (
              <g key={p.key}>
                {area ? p.areas.map((d, i) => <path key={i} d={d} fill={p.color} opacity={0.08} />) : null}
                {p.segments.map((d, i) => (
                  <path key={i} d={d} fill="none" stroke={p.color} strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" />
                ))}
              </g>
            ))}
            {hover != null ? (
              <g>
                <line x1={x(hover)} x2={x(hover)} y1={PAD.top} y2={PAD.top + h} stroke="var(--faint)" strokeDasharray="3 3" />
                {series.map((s) => {
                  const v = s.points[hover]?.value;
                  return v == null ? null : (
                    <circle key={s.key} cx={x(hover)} cy={y(v)} r={4} fill={s.color} stroke="var(--panel)" strokeWidth={2} />
                  );
                })}
              </g>
            ) : null}
            <rect
              x={PAD.left}
              y={PAD.top}
              width={w}
              height={h}
              fill="transparent"
              onMouseMove={onMove}
              onMouseLeave={() => setHover(null)}
            />
          </svg>
          {hover != null ? (
            <Tooltip x={x(hover)} width={width}>
              <div className="mb-1 font-medium text-ink">{shortDay(dates[hover])}</div>
              {series.map((s) => {
                const v = s.points[hover]?.value;
                return (
                  <div key={s.key} className="flex items-center justify-between gap-2 text-muted">
                    <span className="inline-flex items-center gap-1.5">
                      <span className="h-2 w-2 rounded-full" style={{ backgroundColor: s.color }} aria-hidden />
                      {s.label}
                    </span>
                    <span className="font-medium tabular-nums text-ink">{v == null ? "no data" : format(v)}</span>
                  </div>
                );
              })}
            </Tooltip>
          ) : null}
        </>
      ) : null}
    </div>
  );
}

/** Daily in/out bars on one axis: `up` above the baseline, `down` below it (both in the same unit). */
export function DivergingBars({
  up,
  down,
  upLabel,
  downLabel,
  unit,
  height = 240,
}: {
  up: Point[];
  down: Point[];
  upLabel: string;
  downLabel: string;
  unit: ChartUnit;
  height?: number;
}) {
  const format = formatter(unit);
  const [ref, width] = useWidth<HTMLDivElement>();
  const [hover, setHover] = useState<number | null>(null);
  const n = Math.max(up.length, down.length);
  const all = [...up, ...down].map((p) => p.value).filter((v): v is number => v != null);
  const hasData = all.some((v) => v !== 0);
  const max = niceMax(Math.max(0, ...all));
  const w = Math.max(0, width - PAD.left - PAD.right);
  const h = height - PAD.top - PAD.bottom;
  const mid = PAD.top + h / 2;
  const step = n ? w / n : 0;
  const barW = Math.max(1, Math.min(6, step - 2));
  const scale = (v: number) => (v / max) * (h / 2);
  const dates = (up.length >= down.length ? up : down).map((p) => p.date);
  const xTicks = n ? [0, Math.floor((n - 1) / 3), Math.floor(((n - 1) * 2) / 3), n - 1] : [];

  return (
    <div ref={ref} className="relative w-full" style={{ height }}>
      {!hasData ? (
        <div className="flex h-full items-center justify-center rounded-xl border border-dashed border-hair text-sm text-faint">No money in or out in this window.</div>
      ) : width > 0 ? (
        <>
          <svg width={width} height={height} role="img" aria-label={`${upLabel} above, ${downLabel} below`}>
            {[-1, -0.5, 0, 0.5, 1].map((t) => (
              <g key={t}>
                <line x1={PAD.left} x2={PAD.left + w} y1={mid - t * (h / 2)} y2={mid - t * (h / 2)} stroke={t === 0 ? "var(--hair)" : "var(--viz-grid)"} />
                <text x={PAD.left - 8} y={mid - t * (h / 2) + 4} textAnchor="end" className="fill-faint text-2xs tabular-nums">
                  {t < 0 ? `−${format(Math.abs(t) * max)}` : format(t * max)}
                </text>
              </g>
            ))}
            {Array.from({ length: n }, (_, i) => {
              const cx = PAD.left + i * step + step / 2;
              const u = up[i]?.value;
              const d = down[i]?.value;
              return (
                <g key={i} opacity={hover == null || hover === i ? 1 : 0.45}>
                  {u != null && u > 0 ? <rect x={cx - barW / 2} y={mid - scale(u) - 1} width={barW} height={Math.max(1, scale(u))} rx={Math.min(2, barW / 2)} fill="var(--viz-1)" /> : null}
                  {d != null && d > 0 ? <rect x={cx - barW / 2} y={mid + 1} width={barW} height={Math.max(1, scale(d))} rx={Math.min(2, barW / 2)} fill="var(--viz-2)" /> : null}
                </g>
              );
            })}
            {xTicks.map((i) => (
              <text key={i} x={PAD.left + i * step + step / 2} y={height - 6} textAnchor={i === 0 ? "start" : i === n - 1 ? "end" : "middle"} className="fill-faint text-2xs">
                {shortDay(dates[i] ?? "")}
              </text>
            ))}
            <rect
              x={PAD.left}
              y={PAD.top}
              width={w}
              height={h}
              fill="transparent"
              onMouseMove={(event) => {
                const rect = event.currentTarget.getBoundingClientRect();
                const i = Math.floor(((event.clientX - rect.left) / Math.max(1, rect.width)) * n);
                setHover(Math.max(0, Math.min(n - 1, i)));
              }}
              onMouseLeave={() => setHover(null)}
            />
          </svg>
          {hover != null ? (
            <Tooltip x={PAD.left + hover * step} width={width}>
              <div className="mb-1 font-medium text-ink">{shortDay(dates[hover] ?? "")}</div>
              {[
                { label: upLabel, color: "var(--viz-1)", v: up[hover]?.value },
                { label: downLabel, color: "var(--viz-2)", v: down[hover]?.value },
              ].map((row) => (
                <div key={row.label} className="flex items-center justify-between gap-2 text-muted">
                  <span className="inline-flex items-center gap-1.5">
                    <span className="h-2 w-2 rounded-full" style={{ backgroundColor: row.color }} aria-hidden />
                    {row.label}
                  </span>
                  <span className="font-medium tabular-nums text-ink">{row.v == null ? "no data" : format(row.v)}</span>
                </div>
              ))}
            </Tooltip>
          ) : null}
        </>
      ) : null}
    </div>
  );
}

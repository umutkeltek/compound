/**
 * Hand-built horizontal bar rows (DESIGN.md §8). "compare → table"-adjacent,
 * but for a tiny fixed set (a move-type mix, a lane split) a labelled bar reads
 * faster than a table. Tokens only; the fill color is a semantic tone, never a
 * raw palette color. `tabular-nums` on every value so columns align.
 *
 * Bars are proportioned against the largest value in the set (or an explicit
 * `max`), with a small floor so a non-zero-but-tiny value still shows a sliver.
 */
export type BarTone = "work" | "memory" | "capability" | "route" | "product" | "good" | "seam" | "crit" | "muted";

const FILL: Record<BarTone, string> = {
  work: "bg-work",
  memory: "bg-memory",
  capability: "bg-capability",
  route: "bg-route",
  product: "bg-product",
  good: "bg-good",
  seam: "bg-seam",
  crit: "bg-crit",
  muted: "bg-faint",
};

export type BarItem = { label: string; value: number; tone?: BarTone };

export function Bars({ items, max, className = "" }: { items: BarItem[]; max?: number; className?: string }) {
  const ceiling = Math.max(1, max ?? Math.max(...items.map((i) => i.value), 0));
  return (
    <div className={`grid gap-2.5 ${className}`}>
      {items.map((item) => {
        const pct = item.value > 0 ? Math.max(5, Math.round((item.value / ceiling) * 100)) : 0;
        return (
          <div key={item.label} className="grid grid-cols-[68px_1fr_28px] items-center gap-3 text-xs">
            <span className="truncate text-muted">{item.label}</span>
            <span className="h-1.5 overflow-hidden rounded-pill bg-hair">
              <span
                className={`block h-full rounded-pill ${FILL[item.tone ?? "muted"]}`}
                style={{ width: `${pct}%` }}
              />
            </span>
            <span className="text-right tabular-nums text-muted">{item.value.toLocaleString("en-US")}</span>
          </div>
        );
      })}
    </div>
  );
}

import type { ReactNode } from "react";

import { Panel } from "@/components/briefing/primitives";
import { DeltaPill } from "@/components/kit";
import { memoryBriefingNotice, type AutonomyPosture, type MemoryBriefing } from "@/lib/briefing";

/**
 * The week in three numbers (DESIGN.md §3 KPI stat): changes that reached the
 * outside world, what memory learned, and the autonomy level. Each tile is
 * label (domain-tinted dot) → value (26px/800 tabular) → sub → optional
 * visual. The first two carry a thin stacked bar of their composition;
 * Autonomy reads real operator authority. There is no spend tile: no cheap
 * spend read exists, and a tile that can only say "not wired" is noise.
 */

/** "Nov 13, 07:52" for an ISO time; the raw value stays in `dateTime`. */
function plainClock(iso: string): string {
  const at = new Date(iso);
  if (Number.isNaN(at.getTime())) return iso;
  return at.toLocaleString("en-US", { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit", hour12: false });
}

type StackSeg = { value: number; className: string; label: string };

function StackBar({ segments }: { segments: StackSeg[] }) {
  const total = segments.reduce((sum, s) => sum + s.value, 0);
  if (total <= 0) return null;
  return (
    <span className="mt-3 flex h-1.5 w-full overflow-hidden rounded-pill bg-hair" role="img" aria-label={segments.map((s) => `${s.label} ${s.value}`).join(", ")}>
      {segments.map((seg) =>
        seg.value > 0 ? (
          <span key={seg.label} title={`${seg.label}: ${seg.value}`} className={`block h-full ${seg.className}`} style={{ width: `${(seg.value / total) * 100}%` }} />
        ) : null,
      )}
    </span>
  );
}

function KpiTile({
  label,
  dot,
  value,
  delta,
  sub,
  children,
}: {
  label: string;
  dot?: string;
  value: ReactNode;
  delta?: ReactNode;
  sub: ReactNode;
  children?: ReactNode;
}) {
  return (
    <Panel className="flex flex-col px-5 py-4">
      <span className="flex items-center gap-1.5 text-xs font-bold uppercase tracking-wide text-faint">
        {dot ? <span className={`h-1.5 w-1.5 rounded-full ${dot}`} aria-hidden /> : null}
        {label}
      </span>
      <span className="mt-2 flex items-baseline gap-2">
        <span className="text-2xl font-extrabold tracking-tight tabular-nums text-strong">{value}</span>
        {delta ?? null}
      </span>
      <span className="mt-1 text-xs leading-snug text-muted">{sub}</span>
      {children}
    </Panel>
  );
}

export function KpiStrip({
  world,
  mind,
  autonomy,
  worldDelta,
  worldNote = "Reading work history…",
  autonomyNote = "Reading the autonomy level…",
}: {
  /** null while the workbench read has no copy: shown as "…", never 0. */
  world: { outward: number; meaning: number; rules: number; upkeep: number; total: number; lowOutward: boolean } | null;
  mind: MemoryBriefing;
  /** null while the policy read has no copy (never "fail-closed" by default). */
  autonomy: AutonomyPosture | null;
  /** what to say for a null world / autonomy ("being read…" or "could not read"). */
  worldNote?: string;
  autonomyNote?: string;
  /** week-over-week change in outward moves — null when the window can't cover the prior week (§9.6). */
  worldDelta?: number | null;
}) {
  return (
    <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
      {!world ? (
        <KpiTile label="Outside changes · 7 days" dot="bg-work" value={<span className="text-faint">…</span>} sub={worldNote} />
      ) : (
      <KpiTile
        label="Outside changes · 7 days"
        dot="bg-work"
        value={world.outward.toLocaleString("en-US")}
        delta={
          worldDelta != null ? (
            <DeltaPill value={worldDelta} showZero title="changes that reached the world vs the previous 7 days" />
          ) : null
        }
        sub={
          world.lowOutward && world.total > 0 ? (
            <span className="text-seam">changes that reached a product or the world — a thin week</span>
          ) : (
            <>changes that reached a product or the world, of {world.total.toLocaleString("en-US")} finished</>
          )
        }
      >
        <StackBar
          segments={[
            { value: world.outward, className: "bg-good", label: "Reached the world" },
            { value: world.meaning, className: "bg-memory", label: "Knowledge" },
            { value: world.rules, className: "bg-route", label: "Rules" },
            { value: world.upkeep, className: "bg-faint", label: "Upkeep" },
          ]}
        />
      </KpiTile>
      )}

      <KpiTile
        label="Learned · 7 days"
        dot="bg-memory"
        value={mind.promotedThisWeek == null ? "—" : `${mind.promotedThisWeek > 0 ? "+" : ""}${mind.promotedThisWeek.toLocaleString("en-US")}`}
        delta={mind.delta != null ? <DeltaPill value={mind.delta} showZero title="promotions vs previous 7 days" /> : null}
        sub={mind.totals ? <>promoted · {mind.totals.awaiting.toLocaleString("en-US")} awaiting · {mind.totals.parked.toLocaleString("en-US")} parked</> : memoryBriefingNotice(mind)}
      >
        {mind.totals ? <StackBar segments={[
          { value: mind.totals.promoted, className: "bg-good", label: "Promoted" },
          { value: mind.totals.awaiting, className: "bg-memory", label: "Awaiting" },
          { value: mind.totals.parked, className: "bg-faint", label: "Parked" },
        ]} /> : null}
        {mind.observation === "stale" && mind.totals ? (
          <span className="mt-2 text-xs text-seam">
            {memoryBriefingNotice(mind)}
            {mind.lastGoodAt ? <span className="block">Last successful read: <time dateTime={mind.lastGoodAt}>{plainClock(mind.lastGoodAt)}</time></span> : null}
          </span>
        ) : null}
      </KpiTile>
      {!autonomy ? (
        <KpiTile label="Autonomy" dot="bg-faint" value={<span className="text-faint">…</span>} sub={autonomyNote} />
      ) : (
      <KpiTile
        label="Autonomy"
        dot={autonomy.tone === "good" ? "bg-good" : "bg-seam"}
        value={autonomy.level}
        sub={<span title={autonomy.detail}>{autonomy.posture}</span>}
      >
        <span className="mt-3 text-2xs leading-snug text-faint">{autonomy.detail}</span>
      </KpiTile>
      )}

    </div>
  );
}

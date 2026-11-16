import Link from "next/link";
import { memoryReviewHref, settingsHref, workListHref } from "@/lib/links";
import type { ReactNode } from "react";

import { Panel } from "@/components/briefing/primitives";
import { DeltaPill, plainAge } from "@/components/kit";
import { memoryBriefingNotice, type AutonomyPosture, type MemoryBriefing } from "@/lib/briefing";
import { plainAutonomy } from "@/lib/plain-words";

/**
 * The week in three numbers (DESIGN.md §3 KPI stat): changes that reached the
 * outside world, what memory learned, and the autonomy level. Each tile is
 * label (domain-tinted dot) → value (26px/800 tabular) → sub → optional
 * visual. The first two carry a thin stacked bar of their composition;
 * Autonomy reads real operator authority. There is no spend tile: no cheap
 * spend read exists, and a tile that can only say "not wired" is noise.
 */

/** "12 min ago" for an ISO time, in the age format every read chip uses; the raw value stays in `dateTime`. */
function readAge(iso: string): string {
  const at = new Date(iso).getTime();
  return Number.isNaN(at) ? "at an unknown time" : plainAge(Math.max(0, Date.now() - at));
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
  link,
}: {
  /** where the tile leads: the list behind its number */
  link?: { label: string; href: string };
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
      {link ? <Link href={link.href} className="mt-2 text-xs font-semibold text-work hover:underline">{link.label} →</Link> : null}
    </Panel>
  );
}

/**
 * What the autonomy level lets agents do without asking, in one plain
 * sentence (lib/plain-words.ts `plainAutonomy`); the level itself is small.
 * An unknown level shows the system's own posture and gloss as written.
 */
function AutonomyTile({ autonomy }: { autonomy: AutonomyPosture }) {
  // Without imported operator authority, policy fails closed whatever the level says.
  const sentence = autonomy.installed ? plainAutonomy(autonomy.level) : "";
  return (
    <Panel className="flex flex-col px-5 py-4">
      <span className="flex items-center gap-1.5 text-xs font-bold uppercase tracking-wide text-faint">
        <span className={`h-1.5 w-1.5 rounded-full ${autonomy.tone === "good" ? "bg-good" : "bg-seam"}`} aria-hidden />
        Autonomy
        <span className="mono rounded-sm border border-hair px-1 py-px text-2xs font-semibold normal-case tracking-normal text-muted" title={autonomy.posture}>
          {autonomy.level}
        </span>
      </span>
      <span className="mt-2 text-sm font-semibold leading-snug text-strong">{sentence || autonomy.posture}</span>
      {sentence ? null : <span className="mt-1 text-2xs leading-snug text-faint">{autonomy.detail}</span>}
      <Link href={settingsHref("safety-permissions")} className="mt-2 text-xs font-semibold text-work hover:underline">See what agents may do →</Link>
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
        link={{ label: "See all work", href: workListHref({ filter: "all" }) }}
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
        link={mind.totals && mind.totals.awaiting > 0 ? { label: `${mind.totals.awaiting.toLocaleString("en-US")} ${mind.totals.awaiting === 1 ? "memory waits" : "memories wait"} for review`, href: memoryReviewHref() } : { label: "See what it knows", href: "/memory" }}
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
        {mind.observation === "stale" && mind.failed && mind.totals ? (
          <span className="mt-2 text-xs text-seam">
            {memoryBriefingNotice(mind)}
            {mind.lastGoodAt ? <span className="block">Last read <time dateTime={mind.lastGoodAt}>{readAge(mind.lastGoodAt)}</time></span> : null}
          </span>
        ) : null}
      </KpiTile>
      {!autonomy ? (
        <KpiTile label="Autonomy" dot="bg-faint" value={<span className="text-faint">…</span>} sub={autonomyNote} />
      ) : (
      <AutonomyTile autonomy={autonomy} />
      )}

    </div>
  );
}

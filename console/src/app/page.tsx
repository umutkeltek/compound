import Link from "next/link";
import type { ReactNode } from "react";

import { summarizeMoveTypeMix, type MoveTypeGroup } from "@/components/ChangedProofStream";
import { ContinuePanel } from "@/components/ContinuePanel";
import { EmptyState, FreshnessChip } from "@/components/ui";
import { attentionNeedsYouGroups, getAttention, type AttentionGroup } from "@/lib/attention";
import { humanAge } from "@/lib/briefing";
import { arr, getExecutors, num, runJson, str, toDate, type Json } from "@/lib/compound";

export const dynamic = "force-dynamic";

const TZ = "Europe/Istanbul";
const WARN_HOURS = 48;

/* ---------- small deterministic helpers (no LLM) ---------- */

function plural(value: number, one: string, many = `${one}s`): string {
  return `${value.toLocaleString("en-US")} ${value === 1 ? one : many}`;
}

function hoursSince(value: unknown): number | null {
  const date = toDate(value);
  if (!date) return null;
  return (Date.now() - date.getTime()) / 3_600_000;
}

function isPastWarn(value: unknown): boolean {
  const h = hoursSince(value);
  return h != null && h > WARN_HOURS;
}

/** Trim to ~max chars at a word boundary; keep it a clean phrase. */
function trimWords(text: string, max: number): string {
  const clean = str(text).replace(/\s+/g, " ").trim();
  if (clean.length <= max) return clean;
  const cut = clean.slice(0, max);
  const lastSpace = cut.lastIndexOf(" ");
  const body = (lastSpace > 24 ? cut.slice(0, lastSpace) : cut).replace(/[\s.,;:—–-]+$/, "");
  return `${body}…`;
}

const VERB_MAP: Record<string, string> = {
  record: "Record",
  promote: "Promote",
  demote: "Demote",
  add: "Add",
  settle: "Settle",
  file: "File",
  repair: "Repair",
  tidy: "Tidy",
  start: "Start",
  pick: "Pick",
  review: "Review",
  read: "Open",
  open: "Open",
};

/** Short verb from the summary's ask ("…Record the loss?" -> "Record"). */
function askVerb(summary: string): string {
  const s = str(summary).trim();
  const question = s.match(/([^.?!]*\?)\s*$/);
  const clause = question ? question[1] : s;
  const first = (clause.trim().match(/^[A-Za-z]+/)?.[0] ?? "").toLowerCase();
  if (VERB_MAP[first]) return VERB_MAP[first];
  const lead = (s.match(/^[A-Za-z]+/)?.[0] ?? "").toLowerCase();
  return VERB_MAP[lead] ?? "Review";
}

function greeting(now: Date): string {
  const hour = Number(
    new Intl.DateTimeFormat("en-US", { timeZone: TZ, hour: "numeric", hour12: false }).format(now),
  );
  const part = hour < 12 ? "morning" : hour < 18 ? "afternoon" : "evening";
  const operatorName = str(process.env.COMPOUND_OPERATOR_DISPLAY_NAME || process.env.COMPOUND_OPERATOR_NAME);
  return operatorName ? `Good ${part}, ${operatorName}` : `Good ${part}`;
}

function dateLabel(now: Date): string {
  return new Intl.DateTimeFormat("en-US", {
    timeZone: TZ,
    weekday: "long",
    month: "long",
    day: "numeric",
  }).format(now);
}

/* ---------- decide groups ----------
 * Selection + ranking are gone from the client: the `compound attention --json`
 * projection already emits needs-you groups in leverage order and names the
 * single highest-leverage `top`. This just shapes an AttentionGroup for the
 * Decide list card — WHERE the data comes from changed, the layout did not.
 */

type DecideGroup = {
  key: string;
  headline: string;
  unlock: string;
  verb: string;
  count: number;
  created: unknown;
};

function toDecideGroup(group: AttentionGroup): DecideGroup {
  const summary = group.operator_summary;
  return {
    key: group.key || group.area,
    headline: trimWords(summary || group.area || "A decision is waiting on you.", 60),
    unlock: group.operator_unlock,
    verb: askVerb(summary),
    count: group.count,
    created: group.oldest_created_at,
  };
}

/* ---------- agents ---------- */

type AgentLine = { name: string; tone: "good" | "warn" | "idle"; doing: string; rank: number };

function statusDoing(status: string): string {
  switch (status) {
    case "available":
      return "available — no work claimed";
    case "not_checked":
      return "not checked this session";
    case "waiting":
      return "waiting for runnable work";
    case "route_evidence_only":
      return "on call for route evidence";
    case "manual":
      return "manual lane — on call";
    default:
      return status ? status.replace(/_/g, " ") : "idle";
  }
}

function agentLine(row: Json): AgentLine {
  const name = str(row.label) || str(row.id) || "executor";
  const running = num(row.running_count);
  const needsMe = num(row.needs_me_count);
  const ready = num(row.ready_count);
  const assigned = num(row.assigned_count);
  const stale = num(row.stale_lease_count);
  const status = str(row.status);

  if (running) return { name, tone: "good", doing: `is moving ${plural(running, "item")}`, rank: 100 + running };
  if (needsMe) return { name, tone: "warn", doing: `waits on ${plural(needsMe, "decision")} from you`, rank: 90 + needsMe };
  if (ready) return { name, tone: "good", doing: `can start ${plural(ready, "ready item")}`, rank: 70 + ready };
  if (assigned) return { name, tone: "warn", doing: `has ${plural(assigned, "item")} to prepare`, rank: 60 + assigned };
  if (stale) return { name, tone: "warn", doing: `holds ${plural(stale, "stale lease")}`, rank: 40 };
  return { name, tone: "idle", doing: statusDoing(status), rank: status === "not_checked" ? 5 : 10 };
}

function agentLines(executors: Json | null): AgentLine[] {
  return arr(executors?.executors)
    .map(agentLine)
    .sort((a, b) => b.rank - a.rank)
    .slice(0, 4);
}

/* ---------- section components ---------- */

function SectionLabel({ children }: { children: ReactNode }) {
  return <div className="section-label mb-3.5">{children}</div>;
}

function AttentionPill({ count, oldest }: { count: number; oldest: unknown }) {
  const warn = isPastWarn(oldest);
  const age = humanAge(oldest);
  return (
    <span
      className={`inline-flex w-fit items-center gap-2 whitespace-nowrap rounded-full px-3.5 py-[5px] text-[12.5px] font-semibold tabular-nums ${
        warn ? "bg-amber-50 text-amber-700" : "bg-slate-100 text-ink-2"
      }`}
    >
      <span className={`h-[7px] w-[7px] rounded-full ${warn ? "bg-amber-600" : "bg-slate-400"}`} />
      {count.toLocaleString("en-US")} waiting
      {age ? <span className="opacity-70">· oldest {age}</span> : null}
    </span>
  );
}

/** The headline. Plain-language: the projection's `top.operator_summary` is the
 * sentence; `top.operator_unlock` is the consequence line. No client ranking. */
function Insight({ top, runningAgents }: { top: AttentionGroup | null; runningAgents: number }) {
  const summary = str(top?.operator_summary);
  const unlock = str(top?.operator_unlock);
  return (
    <p className="my-[52px] max-w-[44ch] text-[24px] font-medium leading-[1.45] tracking-[-0.01em] text-balance text-ink">
      {top && summary ? (
        <>
          {summary}
          {unlock ? (
            <>
              {" "}
              <strong className="font-[650] text-emerald-700">→ {unlock}.</strong>
            </>
          ) : null}
        </>
      ) : (
        <>
          Nothing waits on you — {plural(runningAgents, "agent")} {runningAgents === 1 ? "is" : "are"} working.
        </>
      )}
    </p>
  );
}

function DecideCard({ groups, total }: { groups: DecideGroup[]; total: number }) {
  if (!groups.length) {
    return (
      <div className="rounded-[14px] border border-line bg-card px-6 py-8">
        <EmptyState>Nothing waits on you right now.</EmptyState>
      </div>
    );
  }
  const overflow = total - groups.length;
  return (
    <div className="overflow-hidden rounded-[14px] border border-line bg-card">
      {groups.map((group, index) => {
        const warn = isPastWarn(group.created);
        const age = humanAge(group.created) || "new";
        return (
          <div
            key={group.key}
            title={group.key}
            className={`grid grid-cols-1 gap-3 px-6 py-[19px] sm:grid-cols-[1fr_auto] sm:items-center sm:gap-5 ${
              index > 0 ? "border-t border-line" : ""
            }`}
          >
            <div className="min-w-0">
              <h3 className="text-[15.5px] font-semibold leading-snug tracking-[-0.005em] text-ink">
                {group.headline}
              </h3>
              {group.unlock ? (
                <div className="mt-[3px] text-[13px] text-emerald-700">→ {group.unlock}</div>
              ) : null}
            </div>
            <div className="flex items-center justify-between gap-4 sm:justify-end">
              {group.count > 1 ? (
                <span className="shrink-0 text-[11.5px] tabular-nums text-ink-3">×{group.count.toLocaleString("en-US")}</span>
              ) : null}
              <span className={`shrink-0 text-[11.5px] tabular-nums ${warn ? "text-amber-700" : "text-ink-3"}`}>
                {age}
              </span>
              <Link
                href="/reviews"
                className="inline-flex shrink-0 whitespace-nowrap rounded-[9px] border border-line bg-card px-[15px] py-[7px] text-[13px] font-semibold text-ink transition hover:border-ink-3 focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-emerald-600"
              >
                {group.verb}
              </Link>
            </div>
          </div>
        );
      })}
      {overflow > 0 ? (
        <Link
          href="/reviews"
          className="flex items-center justify-between border-t border-line px-6 py-3.5 text-[13px] font-medium text-ink-2 transition hover:bg-slate-50 focus-visible:outline focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-emerald-600"
        >
          <span>+{overflow.toLocaleString("en-US")} more decisions</span>
          <span className="text-emerald-700">→ /reviews</span>
        </Link>
      ) : null}
    </div>
  );
}

function AgentsRail({ lines }: { lines: AgentLine[] }) {
  const dot = { good: "bg-emerald-600", warn: "bg-amber-500", idle: "bg-ink-3" };
  if (!lines.length) {
    return (
      <div className="rounded-[14px] border border-line bg-card px-6 py-6">
        <EmptyState>No executors reporting.</EmptyState>
      </div>
    );
  }
  return (
    <div className="overflow-hidden rounded-[14px] border border-line bg-card">
      {lines.map((line, index) => (
        <div
          key={`${line.name}:${index}`}
          className={`flex items-baseline gap-3 px-6 py-4 ${index > 0 ? "border-t border-line" : ""}`}
        >
          <span className={`h-2 w-2 shrink-0 -translate-y-px rounded-full ${dot[line.tone]}`} />
          <span className="min-w-[64px] shrink-0 text-[14px] font-semibold text-ink">{line.name}</span>
          <span className="text-[13px] leading-relaxed text-ink-2">{line.doing}</span>
        </div>
      ))}
    </div>
  );
}

function WorldMindCard({
  recent,
  topSubject,
  memoryCounts,
  promotedThisWeek,
}: {
  recent: Json[];
  topSubject: string;
  memoryCounts: Record<string, number>;
  promotedThisWeek: number;
}) {
  const mix = summarizeMoveTypeMix(recent);
  const bars: { key: MoveTypeGroup; label: string; className: string }[] = [
    { key: "outward", label: "Outward", className: "bg-emerald-600" },
    { key: "meaning", label: "Meaning", className: "bg-teal-600" },
    { key: "rules", label: "Rules", className: "bg-teal-600/55" },
    { key: "upkeep", label: "Upkeep", className: "bg-slate-400/60" },
  ];
  const max = Math.max(1, ...bars.map((bar) => mix.groups[bar.key]));
  const proposed = memoryCounts.proposed ?? 0;
  const parked = memoryCounts.parked ?? 0;

  return (
    <div className="rounded-[14px] border border-line bg-card px-6 py-5">
      {mix.total ? (
        <div className="grid gap-2.5">
          {bars.map((bar) => {
            const count = mix.groups[bar.key];
            return (
              <div key={bar.key} className="grid grid-cols-[64px_1fr_24px] items-center gap-3 text-[12.5px]">
                <span className="text-ink-2">{bar.label}</span>
                <span className="h-[7px] overflow-hidden rounded-full bg-line">
                  <span
                    className={`block h-full rounded-full ${bar.className}`}
                    style={{ width: `${count ? Math.max(6, Math.round((count / max) * 100)) : 0}%` }}
                  />
                </span>
                <span className="text-right tabular-nums text-ink-2">{count}</span>
              </div>
            );
          })}
        </div>
      ) : (
        <EmptyState>No moves recorded in the last 7 days.</EmptyState>
      )}

      {mix.lowOutward ? (
        <div className="mt-3.5 text-[12.5px] leading-relaxed text-amber-700">
          {mix.groups.outward <= 1 ? `Only ${mix.groups.outward} outward move` : `${mix.groups.outward} outward moves`} this
          week{topSubject ? ` — clearing ${topSubject} changes that.` : "."}
        </div>
      ) : null}

      <div className="mt-4 border-t border-line pt-3.5">
        <div className="flex items-baseline justify-between gap-3 text-[12.5px]">
          <span className="section-label">Mind Δ</span>
          <span className="tabular-nums text-ink-2">
            {promotedThisWeek > 0 ? (
              <>
                <span className="font-semibold text-emerald-700">+{promotedThisWeek.toLocaleString("en-US")}</span> promoted ·{" "}
              </>
            ) : null}
            {proposed.toLocaleString("en-US")} awaiting · {parked.toLocaleString("en-US")} parked
          </span>
        </div>
      </div>
    </div>
  );
}

function KnowsStrip({
  counts,
  promotedThisWeek,
  reopened,
}: {
  counts: Record<string, number>;
  promotedThisWeek: number;
  reopened: number;
}) {
  const promoted = counts.promoted ?? 0;
  const parked = counts.parked ?? 0;
  return (
    <div className="grid grid-cols-2 gap-7 lg:grid-cols-4">
      <div>
        <div className="text-[30px] font-[650] leading-none tracking-[-0.02em] tabular-nums text-ink">
          {promoted.toLocaleString("en-US")}
          {promotedThisWeek > 0 ? (
            <small className="ml-1.5 text-[13px] font-semibold text-emerald-700">+{promotedThisWeek}</small>
          ) : null}
        </div>
        <div className="mt-[3px] max-w-[22ch] text-[12.5px] text-ink-2">beliefs it can act on</div>
      </div>
      <div>
        <div className="text-[30px] font-[650] leading-none tracking-[-0.02em] tabular-nums text-ink">
          {reopened.toLocaleString("en-US")}
        </div>
        <div className="mt-[3px] max-w-[22ch] text-[12.5px] text-ink-2">reopened by new evidence</div>
      </div>
      <div>
        <div className="text-[30px] font-[650] leading-none tracking-[-0.02em] tabular-nums text-ink">
          {parked.toLocaleString("en-US")}
        </div>
        <div className="mt-[3px] max-w-[22ch] text-[12.5px] text-ink-2">candidates parked for your call</div>
      </div>
      <div>
        <Link
          href="/memory"
          className="text-[14px] font-semibold text-emerald-700 hover:underline focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-emerald-600"
        >
          Ask what it knows →
        </Link>
        <div className="mt-[3px] max-w-[22ch] text-[12.5px] text-ink-2">promoted beliefs, with sources</div>
      </div>
    </div>
  );
}

/* ---------- page ---------- */

export default async function Dashboard() {
  // `--include-evidence` joins evidence for ~1100 candidates and takes ~50s (past
  // the exec timeout). The plain projection is instant and already carries
  // `counts.by_state`, per-candidate `created_at`, and `generated_at` — all the
  // briefing needs.
  const [attention, executors, workbench, memory] = await Promise.all([
    getAttention(),
    getExecutors(),
    runJson(["workbench", "--json", "--recent", "500"]),
    runJson(["memory", "candidates", "--json"]),
  ]);
  const memoryCounts = (memory?.counts?.by_state ?? {}) as Record<string, number>;
  const memoryItems = arr(memory?.items);

  const now = new Date();

  // Decide + headline come from the ONE attention projection — no client ranking.
  // Groups arrive already leverage-sorted; `top` is the single highest-leverage
  // gate; `counts.needs_you_groups` is the number every surface must agree on.
  const needsYouGroups = attentionNeedsYouGroups(attention);
  const top = attention?.top ?? needsYouGroups[0] ?? null;
  const needsYouGroupCount = attention?.counts?.needs_you_groups ?? needsYouGroups.length;
  const decideGroups = needsYouGroups.slice(0, 6).map(toDecideGroup);
  const oldestNeedsYou = needsYouGroups
    .map((group) => toDate(group.oldest_created_at))
    .filter((date): date is Date => Boolean(date))
    .sort((a, b) => a.getTime() - b.getTime())[0];

  // Agents.
  const agents = agentLines(executors);
  const runningAgents = arr(executors?.executors).filter((row) => num(row.running_count) > 0).length;

  // World Δ (7-day moves) + Mind Δ.
  const recent = arr(workbench?.recent);

  // Memory deltas (this week) for Mind Δ + Knows.
  const weekAgo = Date.now() - 7 * 24 * 3_600_000;
  const promotedThisWeek = memoryItems.filter((candidate) => {
    if (str(candidate.state) !== "promoted") return false;
    const date = toDate(candidate.created_at);
    return date != null && date.getTime() >= weekAgo;
  }).length;
  const reopened = memoryItems.filter(
    (candidate) => str(candidate.state) === "needs_review" || str(candidate.review_state) === "needs_review",
  ).length;

  const stamps: { label: string; generatedAt: string; source: string }[] = [
    { label: "Decide", generatedAt: str(attention?.generated_at), source: "compound attention --json" },
    { label: "Agents", generatedAt: str(executors?.generated_at), source: "compound executors --json" },
    { label: "World Δ", generatedAt: str(workbench?.generated_at), source: "compound workbench --json --recent 500" },
    { label: "Knows", generatedAt: str(memory?.generated_at), source: "compound memory candidates --json" },
  ];

  return (
    <div className="mx-auto max-w-[1060px] px-7 pt-14 pb-[72px]">
      <header className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h1 className="text-[30px] font-[650] tracking-[-0.015em] text-ink">{greeting(now)}</h1>
          <div className="mt-1 text-[13px] text-ink-3">{dateLabel(now)}</div>
        </div>
        <AttentionPill count={needsYouGroupCount} oldest={oldestNeedsYou} />
      </header>

      <section className="mt-8">
        <ContinuePanel workbench={workbench} open={arr(workbench?.open)} />
      </section>

      <Insight top={top} runningAgents={runningAgents} />

      <div className="grid gap-7 lg:grid-cols-[minmax(0,3fr)_minmax(300px,2fr)] lg:items-start">
        <section>
          <SectionLabel>Decide</SectionLabel>
          <DecideCard groups={decideGroups} total={needsYouGroupCount} />
        </section>

        <div className="flex flex-col gap-10">
          <section>
            <SectionLabel>Agents</SectionLabel>
            <AgentsRail lines={agents} />
          </section>

          <section>
            <SectionLabel>World Δ · 7 days</SectionLabel>
            <WorldMindCard
              recent={recent}
              topSubject={str(top?.area).replace(/-/g, " ")}
              memoryCounts={memoryCounts}
              promotedThisWeek={promotedThisWeek}
            />
          </section>
        </div>
      </div>

      <section className="mt-14">
        <div className="mb-4">
          <SectionLabel>Knows</SectionLabel>
        </div>
        <KnowsStrip counts={memoryCounts} promotedThisWeek={promotedThisWeek} reopened={reopened} />
      </section>

      <footer className="mt-16 flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-line pt-4 text-[11.5px] text-ink-3">
        <span>Every section reads a live projection and stamps its own freshness.</span>
        <span className="flex flex-wrap items-center gap-2">
          {stamps.map((stamp) => (
            <span key={stamp.label} className="inline-flex items-center gap-1.5">
              <span className="text-ink-3">{stamp.label}</span>
              <FreshnessChip generatedAt={stamp.generatedAt || undefined} source={stamp.source} />
            </span>
          ))}
        </span>
      </footer>
    </div>
  );
}

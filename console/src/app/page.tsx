import Link from "next/link";
import type { ReactNode } from "react";

import { summarizeMoveTypeMix, type MoveTypeGroup } from "@/components/ChangedProofStream";
import { ContinuePanel } from "@/components/ContinuePanel";
import { DecisionDetailsDialog } from "@/components/DecisionDetailsDialog";
import { EmptyState } from "@/components/ui";
import { humanAge, isOld, selectHighestLeverageGate, type BriefingGateCandidate } from "@/lib/briefing";
import { arr, getExecutors, getReviews, getSourceConnections, getWorkbench, num, str, toDate, type Json } from "@/lib/compound";
import { specFor } from "@/lib/decision-registry";
import { loadActionableMemoryCandidates, loadMemoryCandidates } from "@/lib/memory-data";
import { externalReviewRows } from "@/lib/review-data";
import { enrichWorkItems, shortText, type WorkCardData } from "@/lib/work-data";

export const dynamic = "force-dynamic";

type DecisionRow = {
  id: string;
  headline: string;
  unlock: string;
  createdAt: unknown;
  downstreamWorkCount: number;
  action: ReactNode;
  source: string;
};

type AgentLine = {
  actor: string;
  tone: "good" | "warn" | "idle";
  doing: string;
};

function sourceLine(source: string) {
  return <div className="mt-1 text-[10.5px] font-medium text-ink-3">source: {source}</div>;
}

function looksLikeRawID(value: string): boolean {
  return /^(work|cand|extrepo|route|ctx|rcpt|tr|wbrc|prun|kx|ev)_[A-Za-z0-9_-]+$/.test(value) ||
    /^[a-f0-9]{12,}$/i.test(value);
}

function cleanTitle(value: unknown, fallback = "Untitled decision"): string {
  const title = str(value).replace(/\s+/g, " ").trim();
  if (!title || looksLikeRawID(title)) return fallback;
  return title;
}

function packetSurfaceTitle(surface: string): string {
  if (!surface.startsWith("packet:")) return "";
  return cleanTitle(surface.slice("packet:".length).replace(/-/g, " "), "Untitled packet");
}

function workTitle(work: Json | WorkCardData): string {
  const packet = (work as WorkCardData).packet;
  return cleanTitle(
    packet?.title ||
      str(work.action_context?.title) ||
      str(work.action_context?.action_summary) ||
      packetSurfaceTitle(str(work.canonical_surface)) ||
      str(work.success_predicate) ||
      str(work.desired_outcome),
    "Untitled work",
  );
}

function memoryTitle(candidate: Json): string {
  return cleanTitle(str(candidate.object) || str(candidate.entity) || str(candidate.predicate), "Memory decision");
}

function firstDate(...values: unknown[]): unknown {
  for (const value of values) {
    if (toDate(value)) return value;
  }
  return "";
}

function oldestDate(values: unknown[]): unknown {
  let oldest: Date | null = null;
  let raw: unknown = "";
  for (const value of values) {
    const date = toDate(value);
    if (!date) continue;
    if (!oldest || date.getTime() < oldest.getTime()) {
      oldest = date;
      raw = value;
    }
  }
  return raw;
}

function dateWithinDays(value: unknown, days: number): boolean {
  const date = toDate(value);
  if (!date) return false;
  return Date.now() - date.getTime() <= days * 24 * 3600 * 1000;
}

function plural(value: number, one: string, many = `${one}s`): string {
  return `${value} ${value === 1 ? one : many}`;
}

function linkButton(href: string, label: string, rawId?: string) {
  return (
    <Link
      href={href}
      title={rawId}
      className="inline-flex whitespace-nowrap rounded-[9px] border border-line bg-white px-3.5 py-1.5 text-[12.5px] font-semibold text-ink transition hover:border-slate-300 hover:bg-slate-50 active:translate-y-px"
    >
      {label}
    </Link>
  );
}

function decisionDetailsAction(area: string, item: Json, label: string) {
  const spec = specFor(area);
  if (!spec) return null;
  const id = str(item.id) || str(item.review_id) || str(item.work_id);
  if (!id) return null;
  const pl = spec.plainLanguage(item);
  const options = spec.optionsFor ? spec.optionsFor(item) : spec.options;
  return (
    <DecisionDetailsDialog
      area={area}
      itemId={id}
      item={item}
      pl={pl}
      formSpec={{
        area: spec.area,
        options,
        fields: spec.fields,
        defaultOption: spec.defaultOption ?? options[0]?.value,
      }}
      buttonLabel={label}
    />
  );
}

function gateUnlock(card: Json, work: Json): string {
  const kind = str(card.kind);
  if (kind === "source_private_config_gate") return "read-only source checks can start";
  if (kind === "memory_candidate_adjudication") return "memory keeps one clear version";
  const validation = cleanTitle(arr(card.expected_validation)[0], "");
  if (validation) return shortText(validation, 88);
  const next = cleanTitle(str(work.next_action), "");
  if (next) return shortText(next, 88);
  return "the blocked work can continue";
}

function downstreamCount(workId: string, card: Json, runs: Json[]): number {
  const refs = [workId, str(card.id), str(card.gate_id), str(card.candidate_id)].filter(Boolean);
  const blockedRunIds = new Set<string>();
  for (const run of runs) {
    const blocked = arr(run.blocked_on).map(String);
    if (blocked.some((ref) => refs.some((candidate) => ref.includes(candidate)))) {
      blockedRunIds.add(str(run.id) || `${str(run.generated_at)}:${blocked.join(",")}`);
    }
  }
  return 1 + blockedRunIds.size;
}

function buildDecisionRows({
  open,
  proposedWork,
  reviewRows,
  memoryActionable,
  continueRuns,
  dispatchStepRuns,
}: {
  open: Json[];
  proposedWork: WorkCardData[];
  reviewRows: Json[];
  memoryActionable: Json[];
  continueRuns: Json[];
  dispatchStepRuns: Json[];
}): DecisionRow[] {
  const rows: DecisionRow[] = [];
  const runs = [...continueRuns, ...dispatchStepRuns];

  for (const work of open) {
    const workId = str(work.work_id) || str(work.id);
    for (const card of arr(work.review_cards)) {
      rows.push({
        id: str(card.id) || `${workId}:card`,
        headline: cleanTitle(str(card.title) || str(card.candidate_object) || workTitle(work), "Work decision"),
        unlock: gateUnlock(card, work),
        createdAt: firstDate(card.created_at, work.claimed_at, work.updated_at, work.created_at),
        downstreamWorkCount: downstreamCount(workId, card, runs),
        action: linkButton(`/work/${workId}`, "Inspect", workId),
        source: "compound workbench --json",
      });
    }
  }

  for (const work of proposedWork) {
    const workId = work.displayId;
    rows.push({
      id: workId,
      headline: workTitle(work),
      unlock: "suggested work can become runnable",
      createdAt: firstDate(work.created_at, work.claimed_at),
      downstreamWorkCount: 1,
      action: linkButton(`/work/${workId}`, "Open", workId),
      source: "compound workbench --json",
    });
  }

  for (const row of reviewRows) {
    const action = decisionDetailsAction("external-repo-intake", row, "Review");
    if (!action) continue;
    const spec = specFor("external-repo-intake");
    const pl = spec?.plainLanguage(row);
    rows.push({
      id: str(row.id) || str(row.review_id) || "review",
      headline: cleanTitle(pl?.headline || str(row.next_action), "Review decision"),
      unlock: "the review can become evidence or leave the queue",
      createdAt: firstDate(row.created_at, row.updated_at, row.generated_at),
      downstreamWorkCount: 1,
      action,
      source: "compound reviews --json",
    });
  }

  for (const candidate of memoryActionable) {
    const action = decisionDetailsAction("memory-adjudicate", candidate, "Rule");
    if (!action) continue;
    rows.push({
      id: str(candidate.id),
      headline: memoryTitle(candidate),
      unlock: "memory keeps one clear version",
      createdAt: firstDate(candidate.created_at, candidate.observation_date),
      downstreamWorkCount: 1,
      action,
      source: "compound memory candidates --json",
    });
  }

  return rows.sort((a, b) => {
    if (b.downstreamWorkCount !== a.downstreamWorkCount) return b.downstreamWorkCount - a.downstreamWorkCount;
    const ad = toDate(a.createdAt)?.getTime() ?? Number.MAX_SAFE_INTEGER;
    const bd = toDate(b.createdAt)?.getTime() ?? Number.MAX_SAFE_INTEGER;
    return ad - bd;
  });
}

function insightCandidates(rows: DecisionRow[]): BriefingGateCandidate[] {
  return rows.map((row) => ({
    id: row.id,
    title: row.headline,
    consequence: row.unlock,
    downstreamWorkCount: row.downstreamWorkCount,
    createdAt: row.createdAt as string,
    source: row.source,
  }));
}

function actorOf(work: Json): string {
  return str(work.claimed_by) || str(work.owner) || "unassigned";
}

function fallbackAgentLines(workbench: Json | null, open: Json[]): AgentLine[] {
  const runs = [...arr(workbench?.continue_runs), ...arr(workbench?.dispatch_step_runs)]
    .sort((a, b) => (toDate(b.generated_at)?.getTime() ?? 0) - (toDate(a.generated_at)?.getTime() ?? 0));
  const actors = new Set<string>();
  for (const work of open) actors.add(actorOf(work));
  for (const run of runs) if (str(run.actor)) actors.add(str(run.actor));
  if (str(workbench?.dispatch_next?.actor)) actors.add(str(workbench?.dispatch_next?.actor));
  if (!actors.size) actors.add("codex");

  return [...actors].sort().slice(0, 5).map((actor) => {
    const assigned = open.filter((work) => actorOf(work) === actor);
    const running = assigned.filter((work) => ["claimed", "in_progress", "running", "verifying"].includes(str(work.state))).length;
    const gates = assigned.filter((work) => arr(work.review_cards).length || ["held", "blocked", "needs_review"].includes(str(work.state))).length;
    const ready = assigned.filter((work) => str(work.state) === "ready").length;
    const offered = assigned.filter((work) => ["proposed", "admitted"].includes(str(work.state))).length;
    if (running) return { actor, tone: "good", doing: `is moving ${plural(running, "item")}` };
    if (gates) return { actor, tone: "warn", doing: `waits on ${plural(gates, "decision")}` };
    if (ready) return { actor, tone: "good", doing: `can start ${plural(ready, "ready item")}` };
    if (offered) return { actor, tone: "warn", doing: `has ${plural(offered, "suggested item")} to prepare` };
    return { actor, tone: "idle", doing: "idle - waiting for runnable work" };
  });
}

function executorLines(projection: Json | null): AgentLine[] {
  const rows = arr(projection?.executors);
  if (!rows.length) return [];
  return rows.slice(0, 5).map((row) => {
    const actor = str(row.label) || str(row.id) || "executor";
    const running = num(row.running_count);
    const needsMe = num(row.needs_me_count);
    const ready = num(row.ready_count);
    const assigned = num(row.assigned_count);
    const leases = num(row.lease_count);
    if (running) return { actor, tone: "good", doing: `is moving ${plural(running, "item")}` };
    if (needsMe) return { actor, tone: "warn", doing: `waits on ${plural(needsMe, "decision")}` };
    if (ready) return { actor, tone: "good", doing: `can start ${plural(ready, "ready item")}` };
    if (assigned) return { actor, tone: "warn", doing: `has ${plural(assigned, "assigned item")} to prepare` };
    if (leases) return { actor, tone: "idle", doing: `has ${plural(leases, "lease")} on record` };
    return { actor, tone: "idle", doing: `${str(row.status).replace(/_/g, " ") || "idle"} · ${str(row.status_source) || "unknown source"}` };
  });
}

function DecisionRows({ rows }: { rows: DecisionRow[] }) {
  if (!rows.length) return <EmptyState>No decisions are waiting on the briefing.</EmptyState>;
  return (
    <div className="overflow-hidden rounded-[14px] border border-line bg-white">
      {rows.map((row, index) => {
        const old = isOld(row.createdAt);
        const age = humanAge(row.createdAt) || "new";
        return (
          <div
            key={`${row.id}:${index}`}
            title={row.id}
            className={`grid grid-cols-1 gap-3 px-5 py-4 sm:grid-cols-[1fr_auto] sm:items-center sm:gap-5 ${
              index > 0 ? "border-t border-line/70" : ""
            }`}
          >
            <div className="min-w-0">
              <h3 className="truncate text-[15px] font-semibold tracking-tight text-ink">{row.headline}</h3>
              <div className="mt-0.5 text-[12.5px] font-medium text-emerald-700">-&gt; {row.unlock}</div>
            </div>
            <div className="flex items-center justify-between gap-4 sm:justify-end">
              <span className={`shrink-0 text-[11.5px] tabular-nums ${old ? "text-amber-700" : "text-ink-3"}`}>
                {age}
              </span>
              {row.action}
            </div>
          </div>
        );
      })}
    </div>
  );
}

function Agents({ lines }: { lines: AgentLine[] }) {
  const dot = {
    good: "bg-emerald-600",
    warn: "bg-amber-600",
    idle: "bg-slate-400",
  };
  return (
    <div className="overflow-hidden rounded-[14px] border border-line bg-white">
      {lines.map((line) => (
        <div key={line.actor} className="flex items-baseline gap-3 px-5 py-3.5">
          <span className={`h-2 w-2 shrink-0 rounded-full ${dot[line.tone]}`} />
          <span className="min-w-[86px] text-[13.5px] font-semibold text-ink">{line.actor}</span>
          <span className="text-[12.5px] leading-relaxed text-ink-2">{line.doing}</span>
        </div>
      ))}
    </div>
  );
}

function SourcesStrip({ projection }: { projection: Json | null }) {
  const counts = (projection?.counts ?? {}) as Json;
  const connections = arr(projection?.connections);
  const evidenceCount =
    num(counts.evidence_observed) +
    num(counts.evidence_imported) +
    num(counts.evidence_has_cards) +
    num(counts.evidence_stale);
  const seen = evidenceCount;
  const needsInput = num(counts.availability_needs_private_config);
  const cards = num(counts.action_required);
  const privateSetup = num(counts.private_setup);
  const top = connections
    .filter((connection) => {
      const availability = str(connection.availability);
      const evidenceState = str(connection.evidence_state);
      return (
        availability === "needs_private_config" ||
        availability === "configured" ||
        availability === "failing" ||
        evidenceState === "has_cards" ||
        evidenceState === "stale"
      );
    })
    .slice(0, 2);

  return (
    <div className="rounded-[14px] border border-line bg-white px-5 py-4">
      <div className="grid gap-4 sm:grid-cols-4">
        <div>
          <div className="text-[24px] font-semibold leading-none tabular-nums">{connections.length}</div>
          <div className="mt-1 text-[12px] text-ink-3">sources</div>
        </div>
        <div>
          <div className="text-[24px] font-semibold leading-none tabular-nums">{seen}</div>
          <div className="mt-1 text-[12px] text-ink-3">with evidence</div>
        </div>
        <div>
          <div className="text-[24px] font-semibold leading-none tabular-nums">{needsInput}</div>
          <div className="mt-1 text-[12px] text-ink-3">private setup</div>
        </div>
        <div>
          <div className="text-[24px] font-semibold leading-none tabular-nums">{cards}</div>
          <div className="mt-1 text-[12px] text-ink-3">cards</div>
        </div>
      </div>

      {top.length ? (
        <div className="mt-4 grid gap-2">
          {top.map((connection) => (
            <div key={str(connection.id)} className="flex items-center justify-between gap-3 rounded-lg bg-slate-50 px-3 py-2">
              <div className="min-w-0">
                <div className="truncate text-[12.5px] font-semibold text-ink">{str(connection.label) || str(connection.id)}</div>
                <div className="truncate text-[11.5px] text-ink-3">
                  {str(connection.next_safe_action) ||
                    (str(connection.evidence_state) || str(connection.availability)).replace(/_/g, " ")}
                </div>
              </div>
              <Link href="/sources" className="shrink-0 text-[12px] font-semibold text-teal-700 hover:underline">
                Sources
              </Link>
            </div>
          ))}
        </div>
      ) : (
        <div className="mt-4 flex items-center justify-between gap-3 text-[12.5px] text-ink-3">
          <span>{privateSetup ? `${privateSetup} source${privateSetup === 1 ? "" : "s"} still need private setup.` : "No source action surfaced."}</span>
          <Link href="/sources" className="shrink-0 font-semibold text-teal-700 hover:underline">
            Open sources
          </Link>
        </div>
      )}
    </div>
  );
}

function WorldBars({ recent, topDecision }: { recent: Json[]; topDecision?: string }) {
  const mix = summarizeMoveTypeMix(recent);
  const labels: { key: MoveTypeGroup; label: string; className: string }[] = [
    { key: "outward", label: "Outward", className: "bg-emerald-600" },
    { key: "meaning", label: "Meaning", className: "bg-teal-700" },
    { key: "rules", label: "Rules", className: "bg-teal-700/55" },
    { key: "upkeep", label: "Upkeep", className: "bg-slate-400/70" },
  ];
  const max = Math.max(1, ...labels.map((item) => mix.groups[item.key]));
  return (
    <div className="rounded-[14px] border border-line bg-white px-5 py-4">
      {mix.total ? (
        <div className="grid gap-2.5">
          {labels.map((item) => {
            const count = mix.groups[item.key];
            return (
              <div key={item.key} className="grid grid-cols-[64px_1fr_24px] items-center gap-3 text-[12.5px]">
                <span className="text-ink-2">{item.label}</span>
                <span className="h-2 overflow-hidden rounded-full bg-slate-100">
                  <span
                    className={`block h-full rounded-full ${item.className}`}
                    style={{ width: `${Math.max(4, Math.round((count / max) * 100))}%` }}
                  />
                </span>
                <span className="text-right tabular-nums text-ink-2">{count}</span>
              </div>
            );
          })}
        </div>
      ) : (
        <EmptyState>No recent moves in the workbench projection.</EmptyState>
      )}
      {mix.lowOutward ? (
        <div className="mt-3 text-[12px] leading-relaxed text-amber-700">
          Outward moves quiet this week; {topDecision ? `clearing ${topDecision} can change that.` : "the top decision can change that."}
        </div>
      ) : null}
    </div>
  );
}

function KnowsStrip({ candidates }: { candidates: Json[] }) {
  const byState = candidates.reduce<Record<string, number>>((acc, item) => {
    const state = str(item.state) || "unknown";
    acc[state] = (acc[state] ?? 0) + 1;
    return acc;
  }, {});
  const addedThisWeek = candidates.filter((candidate) => dateWithinDays(candidate.created_at, 7)).length;
  const reopened = candidates.filter((candidate) => str(candidate.state) === "needs_review" || str(candidate.review_state) === "needs_review").length;
  const parked = byState.parked ?? 0;
  return (
    <section className="mt-12">
      <div className="mb-4">
        <div className="section-label">Knows</div>
        {sourceLine("compound memory candidates --json")}
      </div>
      <div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-4">
        <div>
          <div className="text-[30px] font-semibold leading-none tracking-tight tabular-nums">{byState.promoted ?? 0}</div>
          <div className="mt-1 text-[12.5px] text-ink-2">promoted claims it can act on</div>
        </div>
        <div>
          <div className="text-[30px] font-semibold leading-none tracking-tight tabular-nums">{addedThisWeek}</div>
          <div className="mt-1 text-[12.5px] text-ink-2">added this week</div>
        </div>
        <div>
          <div className="text-[30px] font-semibold leading-none tracking-tight tabular-nums">{reopened}</div>
          <div className="mt-1 text-[12.5px] text-ink-2">reopened by new evidence</div>
        </div>
        <div>
          <div className="text-[30px] font-semibold leading-none tracking-tight tabular-nums">{parked}</div>
          <div className="mt-1 text-[12.5px] text-ink-2">candidates parked</div>
          <Link href="/memory" className="mt-2 inline-flex text-[13px] font-semibold text-teal-700 hover:underline">
            Open memory
          </Link>
        </div>
      </div>
    </section>
  );
}

export default async function Dashboard() {
  const [workbench, reviews, memoryActionable, memoryCandidates, sourceConnections, executors] = await Promise.all([
    getWorkbench(),
    getReviews(),
    loadActionableMemoryCandidates(),
    loadMemoryCandidates(),
    getSourceConnections(),
    getExecutors(),
  ]);
  const open = arr(workbench?.open);
  const proposedWork = await enrichWorkItems(open.filter((work) => str(work.state) === "proposed"));
  const recent = arr(workbench?.recent);
  const continueRuns = arr(workbench?.continue_runs);
  const dispatchStepRuns = arr(workbench?.dispatch_step_runs);
  const reviewRows = externalReviewRows(reviews, workbench);
  const decisionRows = buildDecisionRows({
    open,
    proposedWork,
    reviewRows,
    memoryActionable,
    continueRuns,
    dispatchStepRuns,
  });
  const topInsight = selectHighestLeverageGate(insightCandidates(decisionRows));
  const attentionOldest = oldestDate(decisionRows.map((row) => row.createdAt));
  const agents = executorLines(executors);
  const dateLabel = new Intl.DateTimeFormat("en-US", {
    weekday: "long",
    month: "long",
    day: "numeric",
  }).format(new Date());

  return (
    <div className="mx-auto max-w-[1060px] py-8">
      <header className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div>
          <h1 className="text-[30px] font-semibold tracking-[-0.015em] text-ink">Good morning</h1>
          <div className="mt-1 text-[13px] text-ink-3">{dateLabel}</div>
          {sourceLine("compound workbench --json + compound source connections --json + compound executors --json")}
        </div>
        <div className="inline-flex w-fit items-center gap-2 rounded-full bg-amber-50 px-3.5 py-1.5 text-[12.5px] font-semibold text-amber-700">
          <span className="h-1.5 w-1.5 rounded-full bg-amber-600" />
          {decisionRows.length} waiting
          {attentionOldest ? <span>oldest {humanAge(attentionOldest)}</span> : null}
        </div>
      </header>

      <section className="mt-8">
        <ContinuePanel workbench={workbench} open={open} />
      </section>

      <section className="my-10 max-w-[44ch]">
        <p className="text-[24px] font-medium leading-[1.45] text-ink">
          {topInsight ? (
            <>
              Clearing <strong className="font-semibold text-teal-700">{topInsight.title}</strong> {topInsight.consequence}.
            </>
          ) : (
            "No blocked decision is holding downstream work right now."
          )}
        </p>
      </section>

      <div className="grid gap-7 lg:grid-cols-[minmax(0,3fr)_minmax(300px,2fr)] lg:items-start">
        <section>
          <div className="mb-4">
            <div className="section-label">Decide</div>
            {sourceLine("compound workbench --json + compound reviews --json + compound memory candidates --json")}
          </div>
          <DecisionRows rows={decisionRows} />
        </section>

        <div className="flex flex-col gap-9">
          <section>
            <div className="mb-4">
              <div className="section-label">Agents</div>
              {sourceLine("compound executors --json")}
            </div>
            <Agents lines={agents.length ? agents : fallbackAgentLines(workbench, open)} />
          </section>

          <section>
            <div className="mb-4">
              <div className="section-label">Sources</div>
              {sourceLine("compound source connections --json")}
            </div>
            <SourcesStrip projection={sourceConnections} />
          </section>

          <section>
            <div className="mb-4">
              <div className="section-label">World Delta · 7 days</div>
              {sourceLine("compound workbench --json")}
            </div>
            <WorldBars recent={recent} topDecision={topInsight?.title} />
          </section>
        </div>
      </div>

      <KnowsStrip candidates={memoryCandidates} />

      <footer className="mt-14 border-t border-line pt-4 text-[11.5px] text-ink-3">
        Every section reads live CLI projections and names its source.
      </footer>
    </div>
  );
}

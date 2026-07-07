import Link from "next/link";

import { accentColor, Chip, EmptyState, FreshnessChip, StateBadge, type Accent } from "@/components/ui";
import {
  ago,
  arr,
  getExecutors,
  getSourceConnections,
  getWorkbench,
  runJson,
  str,
  type Json,
} from "@/lib/compound";

export const dynamic = "force-dynamic";

/**
 * Flow pipeline (EP-U1 slice 1, per the approved cockpit mock): the envelope
 * lifecycle as columns — Signal · Propose · Admit · Claim · Execute · Receipt ·
 * Learn — each holding REAL items from read-only projections. The flow itself
 * is the surface: where things are, what is waiting on the operator, what the
 * system just learned.
 */

type Stage = {
  key: string;
  label: string;
  plain: string; // "what this stage does" — plain-language law
  accent: Accent;
  items: StageItem[];
  liveness: "live" | "cached";
};

type StageItem = {
  title: string;
  sub?: string;
  href?: string;
  badge?: string;
  gated?: boolean; // waiting on the operator
  running?: boolean;
  mono?: string;
};

function workTitle(item: Json): string {
  const surface = str(item.canonical_surface);
  if (surface.startsWith("packet:")) return surface.slice("packet:".length).replace(/-/g, " ");
  return str(item.success_predicate) || str(item.desired_outcome) || str(item.work_id);
}

function workItem(item: Json, extra?: Partial<StageItem>): StageItem {
  const id = str(item.work_id) || str(item.id);
  return {
    title: workTitle(item),
    sub: str(item.claimed_by) || str(item.owner) || undefined,
    href: id ? `/work/${id}` : undefined,
    badge: str(item.state) || undefined,
    mono: id || undefined,
    ...extra,
  };
}

export default async function FlowPage() {
  const [workbench, executors, sources, director] = await Promise.all([
    getWorkbench(),
    getExecutors(),
    getSourceConnections(),
    // Read-only: --dry-run creates nothing (verified J1); fail-soft to null.
    runJson(["director", "propose", "--dry-run", "--json"]),
  ]);

  const open = arr(workbench?.open);
  const recent = arr(workbench?.recent);
  const byStates = (states: string[]) => open.filter((w) => states.includes(str(w.state)));

  const connections = arr(sources?.connections ?? sources?.sources ?? sources?.rows);
  const proposals = arr(director?.proposals ?? director?.proposed ?? director?.items);
  const runningExecutors = arr(executors?.executors ?? executors?.rows).filter(
    (e) => str(e.status) === "running" || arr(e.active_work).length > 0,
  );

  const terminalRecent = recent.filter((w) => str(w.receipt_id)).slice(0, 8);
  const learnRecent = recent
    .filter((w) => ["memory_delta", "rule_delta", "control_plane_repair"].includes(str(w.move_type)))
    .slice(0, 8);

  const stages: Stage[] = [
    {
      key: "signal",
      label: "Signal",
      plain: "Where new information enters: connected sources and their freshness.",
      accent: "blue",
      liveness: "live",
      items: connections.slice(0, 8).map((c) => ({
        title: str(c.name) || str(c.source) || str(c.id) || "connection",
        badge: str(c.state) || str(c.status) || undefined,
        sub: c.last_observed ? `seen ${ago(c.last_observed)}` : undefined,
      })),
    },
    {
      key: "propose",
      label: "Propose",
      plain: "The director suggests bounded jobs from live evidence. Nothing advances without you.",
      accent: "violet",
      liveness: "live",
      items: [
        ...proposals.slice(0, 6).map((p) => ({
          title: str(p.predicate) || str(p.summary) || str(p.class) || "proposal",
          sub: str(p.consequence) || str(p.class) || undefined,
          badge: "proposed",
          gated: true,
        })),
        ...byStates(["proposed"]).map((w) => workItem(w, { gated: true })),
      ],
    },
    {
      key: "admit",
      label: "Admit",
      plain: "Your seat. Approving makes a job claimable — it does not run anything yet.",
      accent: "orange",
      liveness: "live",
      items: byStates(["admitted", "held"]).map((w) =>
        workItem(w, { gated: str(w.state) === "held" }),
      ),
    },
    {
      key: "claim",
      label: "Claim",
      plain: "An executor takes the job under a lease, so two agents never collide.",
      accent: "blue",
      liveness: "live",
      items: byStates(["ready", "claimed"]).map((w) => workItem(w)),
    },
    {
      key: "execute",
      label: "Execute",
      plain: "Rented executors do the work inside policy. Every mutation needs a decision record.",
      accent: "green",
      liveness: "live",
      items: [
        ...byStates(["running", "verifying", "blocked"]).map((w) =>
          workItem(w, { running: str(w.state) === "running" }),
        ),
        ...runningExecutors.slice(0, 4).map((e) => ({
          title: str(e.name) || str(e.executor) || "executor",
          sub: str(e.lane) || str(e.kind) || undefined,
          badge: "executor",
          running: true,
        })),
      ],
    },
    {
      key: "receipt",
      label: "Receipt",
      plain: "Proof a job ran: what was touched, what it cost, what came out.",
      accent: "gray",
      liveness: "live",
      items: terminalRecent.map((w) =>
        workItem(w, { badge: str(w.move_type) || str(w.state) }),
      ),
    },
    {
      key: "learn",
      label: "Learn",
      plain: "The write-back: outcomes become memory, rules, and skill-fitness edges that change the next run.",
      accent: "violet",
      liveness: "live",
      items: learnRecent.map((w) => ({
        title: workTitle(w),
        sub: str(w.move_type).replace(/_/g, " "),
        href: str(w.work_id) ? `/work/${str(w.work_id)}` : undefined,
        mono: str(w.transition_id) || undefined,
      })),
    },
  ];

  const gatedCount = stages.reduce(
    (n, s) => n + s.items.filter((i) => i.gated).length,
    0,
  );

  return (
    <main className="mx-auto max-w-[1400px] px-5 py-6">
      <div className="mb-4 flex flex-wrap items-end justify-between gap-2">
        <div>
          <h1 className="text-[17px] font-semibold text-ink">Flow</h1>
          <p className="mt-0.5 text-[12.5px] text-ink-3">
            One job ticket travels left to right. Orange items are waiting on you
            {gatedCount > 0 ? ` (${gatedCount} now)` : ""}; everything else flows on its own.
          </p>
        </div>
        <FreshnessChip generatedAt={str(workbench?.generated_at)} source="compound workbench / executors / sources / director dry-run" />
      </div>

      <div className="overflow-x-auto pb-3">
        <div className="flex min-w-[1220px] gap-3">
          {stages.map((stage) => (
            <section key={stage.key} className="w-[172px] shrink-0 grow" title={stage.plain}>
              <div className="mb-2 flex items-center gap-1.5">
                <span
                  className="h-[7px] w-[7px] rounded-full"
                  style={{ background: accentColor(stage.accent) }}
                />
                <span className="section-label text-[10.5px]">{stage.label}</span>
                <span className="ml-auto text-[10.5px] text-ink-3">{stage.items.length}</span>
              </div>
              <p className="mb-2 text-[10.5px] leading-snug text-ink-3">{stage.plain}</p>
              <div className="flex flex-col gap-2">
                {stage.items.length === 0 ? (
                  <div className="rounded-lg border border-dashed border-line px-2.5 py-3 text-center text-[10.5px] text-ink-3">
                    nothing here now
                  </div>
                ) : (
                  stage.items.map((item, i) => {
                    const body = (
                      <div
                        key={i}
                        className={`rounded-xl border bg-white px-2.5 py-2 ${item.gated ? "border-amber-300" : "border-line"}`}
                      >
                        <div className="flex items-start justify-between gap-1.5">
                          <span className="min-w-0 break-words text-[11.5px] font-medium leading-snug text-ink">
                            {item.title}
                          </span>
                          {item.running ? (
                            <span
                              className="mt-1 h-[6px] w-[6px] shrink-0 animate-pulse rounded-full bg-emerald-500"
                              title="running now"
                            />
                          ) : null}
                        </div>
                        {item.sub ? (
                          <div className="mt-0.5 truncate text-[10.5px] text-ink-3">{item.sub}</div>
                        ) : null}
                        <div className="mt-1 flex flex-wrap items-center gap-1">
                          {item.badge ? <StateBadge state={item.badge} /> : null}
                          {item.gated ? <Chip tone="orange">needs you</Chip> : null}
                        </div>
                        {item.mono ? (
                          <code className="mono mt-1 block truncate text-[9.5px] text-ink-3">{item.mono}</code>
                        ) : null}
                      </div>
                    );
                    return item.href ? (
                      <Link key={i} href={item.href} className="block hover:opacity-90">
                        {body}
                      </Link>
                    ) : (
                      body
                    );
                  })
                )}
              </div>
            </section>
          ))}
        </div>
      </div>

      {open.length === 0 && recent.length === 0 ? (
        <EmptyState>No live work data — is the compound binary reachable?</EmptyState>
      ) : null}
    </main>
  );
}

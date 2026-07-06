import Link from "next/link";

import { Card, Chip, EmptyState, FreshnessChip, Stat, StateBadge, type Accent } from "@/components/ui";
import { ago, arr, getWorkbench, getWorkLeases, str, type Json } from "@/lib/compound";
import { shortText } from "@/lib/work-data";

export const dynamic = "force-dynamic";

type ExecutorProfile = {
  id: string;
  label: string;
  aliases: string[];
  status: string;
  tone: Accent;
  bestFor: string;
  canUseTools: string;
  costLimits: string;
};

const PROFILES: ExecutorProfile[] = [
  {
    id: "codex",
    label: "Codex",
    aliases: ["codex"],
    status: "available",
    tone: "green",
    bestFor: "code edits, tests, local proof, repo changes",
    canUseTools: "terminal, files, browser checks when needed",
    costLimits: "subscription lane; proof before claim",
  },
  {
    id: "claude_code",
    label: "Claude Code",
    aliases: ["claude", "claude_code", "claude-code"],
    status: "manual",
    tone: "blue",
    bestFor: "architecture review, larger refactors, prose-heavy synthesis",
    canUseTools: "native Claude Code tool lane",
    costLimits: "subscription lane; assign intentionally",
  },
  {
    id: "local_lm_studio",
    label: "Local LM Studio",
    aliases: ["lm-studio", "local", "local_model"],
    status: "not checked",
    tone: "gray",
    bestFor: "cheap private extraction only when a model is loaded",
    canUseTools: "model API only",
    costLimits: "local watts and context limits",
  },
  {
    id: "switchback_api",
    label: "Switchback/API",
    aliases: ["switchback", "api", "route_gateway"],
    status: "route evidence only",
    tone: "violet",
    bestFor: "provider routing, cheap extraction, route evidence",
    canUseTools: "OpenAI-compatible gateway",
    costLimits: "check registry before unattended use",
  },
  {
    id: "scripts",
    label: "Scripts",
    aliases: ["script", "scripts", "compound reviews", "compound"],
    status: "available",
    tone: "green",
    bestFor: "deterministic preflights, smoke tests, projections",
    canUseTools: "local commands only",
    costLimits: "no model spend",
  },
  {
    id: "unassigned",
    label: "Unassigned",
    aliases: ["", "unassigned"],
    status: "waiting",
    tone: "orange",
    bestFor: "ready work that still needs an executor choice",
    canUseTools: "none until claimed",
    costLimits: "assign manually or Continue when safe",
  },
];

function workActor(work: Json): string {
  return str(work.claimed_by) || str(work.owner) || "unassigned";
}

function profileFor(actor: string): ExecutorProfile {
  const normalized = actor.toLowerCase().replace(/\s+/g, "_");
  return (
    PROFILES.find((profile) => profile.aliases.some((alias) => alias && normalized.includes(alias.replace(/\s+/g, "_")))) ??
    PROFILES[5]
  );
}

function workTitle(work: Json): string {
  const surface = str(work.canonical_surface);
  if (surface.startsWith("packet:")) return surface.slice("packet:".length).replace(/-/g, " ");
  return str(work.action_context?.title) || str(work.success_predicate) || str(work.desired_outcome) || str(work.work_id);
}

function ExecutorCard({
  profile,
  assigned,
  running,
  leases,
  recent,
}: {
  profile: ExecutorProfile;
  assigned: Json[];
  running: Json[];
  leases: Json[];
  recent: Json[];
}) {
  const lastDone = recent.find((work) => profileFor(workActor(work)).id === profile.id && str(work.state) === "completed");
  const lastFailed = recent.find((work) => profileFor(workActor(work)).id === profile.id && str(work.state) === "failed");

  return (
    <Card
      title={profile.label}
      accent={profile.tone}
      glyph="E"
      right={<Chip tone={profile.tone}>{profile.status}</Chip>}
    >
      <div className="flex flex-col gap-3">
        <div className="grid gap-2 sm:grid-cols-3">
          <div>
            <div className="section-label mb-1">Assigned</div>
            <div className="text-[18px] font-semibold leading-none">{assigned.length}</div>
          </div>
          <div>
            <div className="section-label mb-1">Running</div>
            <div className="text-[18px] font-semibold leading-none">{running.length}</div>
          </div>
          <div>
            <div className="section-label mb-1">Leases</div>
            <div className="text-[18px] font-semibold leading-none">{leases.length}</div>
          </div>
        </div>

        <div>
          <div className="section-label mb-1">Best for</div>
          <p className="text-[12px] leading-relaxed text-ink-3">{profile.bestFor}</p>
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          <div>
            <div className="section-label mb-1">Tools</div>
            <p className="text-[12px] leading-relaxed text-ink-3">{profile.canUseTools}</p>
          </div>
          <div>
            <div className="section-label mb-1">Cost / limits</div>
            <p className="text-[12px] leading-relaxed text-ink-3">{profile.costLimits}</p>
          </div>
        </div>

        {assigned.length ? (
          <div>
            <div className="section-label mb-2">Current work</div>
            <ul className="flex flex-col gap-2">
              {assigned.slice(0, 5).map((work) => (
                <li key={str(work.work_id)} className="rounded-lg border border-line px-3 py-2">
                  <div className="flex items-center justify-between gap-2">
                    <Link href={`/work/${str(work.work_id)}`} className="mono text-[10.5px] text-ink-3 hover:underline">
                      {str(work.work_id)}
                    </Link>
                    <StateBadge state={str(work.state)} />
                  </div>
                  <p className="mt-1 text-[12px] leading-relaxed text-ink-2">{shortText(workTitle(work), 150)}</p>
                  {str(work.next_action) ? (
                    <p className="mt-1 text-[11.5px] leading-relaxed text-ink-3">
                      Next: {shortText(str(work.next_action), 160)}
                    </p>
                  ) : null}
                </li>
              ))}
            </ul>
          </div>
        ) : (
          <EmptyState>No assigned work.</EmptyState>
        )}

        {(lastDone || lastFailed) ? (
          <div className="grid gap-2 sm:grid-cols-2">
            {lastDone ? (
              <p className="text-[11.5px] leading-relaxed text-ink-3">
                Last success: {shortText(workTitle(lastDone), 100)}
              </p>
            ) : null}
            {lastFailed ? (
              <p className="text-[11.5px] leading-relaxed text-red-500">
                Last failure: {shortText(workTitle(lastFailed), 100)}
              </p>
            ) : null}
          </div>
        ) : null}
      </div>
    </Card>
  );
}

export default async function ExecutorsPage() {
  const [workbench, leasesProjection] = await Promise.all([getWorkbench(), getWorkLeases()]);
  const open = arr(workbench?.open);
  const recent = arr(workbench?.recent);
  const leases = arr(leasesProjection?.leases);
  const runningStates = new Set(["claimed", "in_progress", "running", "verifying"]);
  const running = open.filter((work) => runningStates.has(str(work.state)));
  const ready = open.filter((work) => str(work.state) === "ready");

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-end justify-between gap-4">
        <div>
          <h1 className="text-[22px] font-semibold tracking-tight">Executors</h1>
          <p className="mt-0.5 text-[12.5px] text-ink-3">
            Who can take work, what they are holding, and what proof is expected.
          </p>
        </div>
        <FreshnessChip generatedAt={str(workbench?.generated_at)} source="compound workbench --json" />
      </div>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Stat label="Running" value={running.length} accent="blue" glyph=">" sub="claimed / in flight" />
        <Stat label="Ready" value={ready.length} accent="green" glyph="R" sub="can be assigned" />
        <Stat label="Open" value={open.length} accent="orange" glyph="O" sub="non-terminal work" />
        <Stat label="Leases" value={leases.length} accent="violet" glyph="L" sub="live + stale" />
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        {PROFILES.map((profile) => {
          const assigned = open.filter((work) => profileFor(workActor(work)).id === profile.id);
          const profileRunning = assigned.filter((work) => runningStates.has(str(work.state)));
          const profileLeases = leases.filter((lease) => profileFor(str(lease.by)).id === profile.id);
          return (
            <ExecutorCard
              key={profile.id}
              profile={profile}
              assigned={assigned}
              running={profileRunning}
              leases={profileLeases}
              recent={recent}
            />
          );
        })}
      </div>
    </div>
  );
}

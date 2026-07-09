import { summarizeMoveTypeMix } from "@/components/ChangedProofStream";
import { ActivityFeed } from "@/components/briefing/ActivityFeed";
import { BriefingHeader } from "@/components/briefing/BriefingHeader";
import { ContinueCard } from "@/components/briefing/ContinueCard";
import { DecideList } from "@/components/briefing/DecideList";
import { Headline } from "@/components/briefing/Headline";
import { KnowsStrip } from "@/components/briefing/KnowsStrip";
import { KpiStrip } from "@/components/briefing/KpiStrip";
import { PipelineStrip } from "@/components/briefing/PipelineStrip";
import { SectionLabel } from "@/components/briefing/primitives";
import { SelfHealingPanel } from "@/components/briefing/SelfHealingPanel";
import { FreshnessChip } from "@/components/kit";
import { attentionNeedsYouGroups, getAttention } from "@/lib/attention";
import {
  autonomyPosture,
  deriveContinue,
  headlineAct,
  oldestCreatedAt,
  pipelineStages,
  selfHealing,
} from "@/lib/briefing";
import { arr, getExecutors, num, runJson, str } from "@/lib/compound";
import { consoleMutationsEnabled } from "@/lib/console-mutations";
import { toDate } from "@/lib/time";

export const dynamic = "force-dynamic";

export default async function Briefing() {
  // The plain memory projection is instant; `--include-evidence` joins ~1100
  // candidates and blows the exec timeout. attention/workbench/home-doctor/
  // operator-authority are all cheap reads through the shared runJson TTL memo.
  const [attention, executors, workbench, memory, policyAuthority, homeDoctor] = await Promise.all([
    getAttention(),
    getExecutors(),
    runJson(["workbench", "--json", "--recent", "500"]),
    runJson(["memory", "candidates", "--json"]),
    runJson(["policy", "operator-authority", "--json"]),
    runJson(["home", "doctor", "--json"]),
  ]);

  const now = new Date();
  const mutationsEnabled = consoleMutationsEnabled();

  // Attention — the ONE projection. Groups arrive leverage-sorted; `top` is the
  // single highest-leverage gate. Never re-ranked client-side.
  const needsYouGroups = attentionNeedsYouGroups(attention);
  const top = attention?.top ?? needsYouGroups[0] ?? null;
  const needsYouGroupCount = attention?.counts?.needs_you_groups ?? needsYouGroups.length;
  const decideGroups = needsYouGroups.slice(0, 6);
  const act = headlineAct(top);
  const oldest = oldestCreatedAt(needsYouGroups);
  const runningAgents = arr(executors?.executors).filter((row) => num(row.running_count) > 0).length;

  // Continue + pipeline off the workbench projection.
  const open = arr(workbench?.open);
  const continueModel = deriveContinue(workbench, open);
  const stages = pipelineStages(workbench);
  const recent = arr(workbench?.recent);

  // World Δ (7-day move mix).
  const mix = summarizeMoveTypeMix(recent);

  // Mind Δ / Knows off the memory candidates projection.
  const memoryCounts = (memory?.counts?.by_state ?? {}) as Record<string, number>;
  const memoryItems = arr(memory?.items);
  const weekAgo = Date.now() - 7 * 24 * 3_600_000;
  const promotedThisWeek = memoryItems.filter((candidate) => {
    if (str(candidate.state) !== "promoted") return false;
    const date = toDate(candidate.created_at);
    return date != null && date.getTime() >= weekAgo;
  }).length;
  const reopened = memoryItems.filter(
    (candidate) => str(candidate.state) === "needs_review" || str(candidate.review_state) === "needs_review",
  ).length;

  const autonomy = autonomyPosture(policyAuthority);
  const health = selfHealing(homeDoctor);

  const stamps: { label: string; generatedAt: string; source: string }[] = [
    { label: "Decide", generatedAt: str(attention?.generated_at), source: "compound attention --json" },
    { label: "World", generatedAt: str(workbench?.generated_at), source: "compound workbench --json --recent 500" },
    { label: "Mind", generatedAt: str(memory?.generated_at), source: "compound memory candidates --json" },
    { label: "Health", generatedAt: str(homeDoctor?.generated_at), source: "compound home doctor --json" },
  ];

  return (
    <div className="mx-auto flex flex-col gap-9 pb-16" style={{ maxWidth: "1080px" }}>
      <BriefingHeader now={now} needsYouCount={needsYouGroupCount} oldest={oldest} />

      <Headline top={top} act={act} mutationsEnabled={mutationsEnabled} runningAgents={runningAgents} />

      <ContinueCard model={continueModel} />

      <section>
        <SectionLabel>Decide</SectionLabel>
        <DecideList groups={decideGroups} total={needsYouGroupCount} />
      </section>

      <KpiStrip
        world={{
          outward: mix.groups.outward,
          meaning: mix.groups.meaning,
          rules: mix.groups.rules,
          upkeep: mix.groups.upkeep,
          total: mix.total,
          lowOutward: mix.lowOutward,
        }}
        mind={{
          promoted: memoryCounts.promoted ?? 0,
          parked: memoryCounts.parked ?? 0,
          awaiting: memoryCounts.proposed ?? 0,
          promotedThisWeek,
        }}
        autonomy={autonomy}
      />

      {/* ---------- below the fold ---------- */}

      <section>
        <SectionLabel>Pipeline</SectionLabel>
        <PipelineStrip stages={stages} />
      </section>

      <SelfHealingPanel health={health} />

      <section>
        <SectionLabel>Live activity</SectionLabel>
        <ActivityFeed recent={recent} />
      </section>

      <section>
        <SectionLabel>Knows</SectionLabel>
        <KnowsStrip
          promoted={memoryCounts.promoted ?? 0}
          parked={memoryCounts.parked ?? 0}
          reopened={reopened}
          promotedThisWeek={promotedThisWeek}
        />
      </section>

      <footer className="mt-4 flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-hair pt-4 text-2xs text-faint">
        <span>Every panel reads a live projection and stamps its own freshness.</span>
        <span className="flex flex-wrap items-center gap-2">
          {stamps.map((stamp) => (
            <span key={stamp.label} className="inline-flex items-center gap-1.5">
              <span className="text-faint">{stamp.label}</span>
              <FreshnessChip generatedAt={stamp.generatedAt || undefined} source={stamp.source} />
            </span>
          ))}
        </span>
      </footer>
    </div>
  );
}

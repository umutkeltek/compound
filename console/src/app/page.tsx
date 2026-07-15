import { ActivityFeed } from "@/components/briefing/ActivityFeed";
import { AttentionAgeMatrix } from "@/components/briefing/AttentionAgeMatrix";
import { BriefingHeader } from "@/components/briefing/BriefingHeader";
import { ContinueCard } from "@/components/briefing/ContinueCard";
import { DecideList } from "@/components/briefing/DecideList";
import { Headline } from "@/components/briefing/Headline";
import { KnowsStrip } from "@/components/briefing/KnowsStrip";
import { KpiStrip } from "@/components/briefing/KpiStrip";
import { LatestMoment } from "@/components/briefing/LatestMoment";
import { PipelineStrip } from "@/components/briefing/PipelineStrip";
import { SectionLabel } from "@/components/briefing/primitives";
import { SelfHealingPanel } from "@/components/briefing/SelfHealingPanel";
import { FreshnessChip } from "@/components/kit";
import { attentionNeedsYouGroups, getAttentionProjection, hasClassAGate } from "@/lib/attention";
import {
  autonomyPosture,
  deriveContinue,
  headlineAct,
  oldestCreatedAt,
  pipelineStages,
  rowTsMs,
  selfHealing,
  weeklyChange,
} from "@/lib/briefing";
import { arr, getExecutors, num, runJson, runProjection, str } from "@/lib/compound";
import { consoleMutationsEnabled } from "@/lib/console-mutations";
import { computeConsoleMutationReadiness } from "@/lib/instance-pin";
import { moveTypeGroup, summarizeMoveTypeMix } from "@/lib/move-mix";
import { toDate } from "@/lib/time";
import { latestCompoundingMoment } from "@/representation";

export const dynamic = "force-dynamic";

export default async function Briefing() {
  // The plain memory projection is instant; `--include-evidence` joins ~1100
  // candidates and blows the exec timeout. home-doctor/operator-authority are
  // cheap reads through the shared runJson TTL memo. attention + workbench are
  // NOT cheap (a real `compound attention --json` / `workbench --json
  // --recent 500` run ~20s today) — they read through `runProjection` with an
  // extended 45s per-door timeout so the door can genuinely SUCCEED, and the
  // briefing renders the honest degradation model (live/stale/unreachable)
  // instead of ever caching a swallowed timeout as null (packet:
  // console-honest-degradation).
  const [attentionProjection, executors, workbenchRead, memory, policyAuthority, homeDoctor] = await Promise.all([
    getAttentionProjection({ timeoutMs: 45_000 }),
    getExecutors(),
    runProjection(["workbench", "--json", "--recent", "500"], { timeoutMs: 45_000 }),
    runJson(["memory", "candidates", "--json"]),
    runJson(["policy", "operator-authority", "--json"]),
    runJson(["home", "doctor", "--json"]),
  ]);
  const { read: attentionRead, report: attention } = attentionProjection;
  const workbench = workbenchRead.value;

  const now = new Date();
  const mutationsEnabled = computeConsoleMutationReadiness(consoleMutationsEnabled()).ready;

  // Attention — the ONE projection. Groups arrive leverage-sorted; `top` is the
  // single highest-leverage gate. Never re-ranked client-side.
  const needsYouGroups = attentionNeedsYouGroups(attention);
  const top = attention?.top ?? needsYouGroups[0] ?? null;
  const needsYouGroupCount = attention?.counts?.needs_you_groups ?? needsYouGroups.length;
  const decideGroups = needsYouGroups.slice(0, 6);
  const act = headlineAct(top);
  const oldest = oldestCreatedAt(needsYouGroups);
  // Any Class-A gate waiting → the hero glows amber (§9.2/§9.4).
  const classAWaiting = hasClassAGate(needsYouGroups);
  const runningAgents = arr(executors?.executors).filter((row) => num(row.running_count) > 0).length;

  // Continue + pipeline off the workbench projection.
  const open = arr(workbench?.open);
  const continueModel = deriveContinue(workbench, open);
  const stages = pipelineStages(workbench);
  const recent = arr(workbench?.recent);
  const compounded = latestCompoundingMoment(recent);

  // World Δ (7-day move mix) + an honest week-over-week delta (only shown when
  // the recent window reaches back far enough to cover the prior week — §9.6).
  const mix = summarizeMoveTypeMix(recent);
  const worldChange = weeklyChange(
    recent,
    rowTsMs,
    (item) => moveTypeGroup(str(item.move_type) || str(item.move)) === "outward",
  );

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
  const mindChange = weeklyChange(memoryItems, rowTsMs, (candidate) => str(candidate.state) === "promoted");

  const autonomy = autonomyPosture(policyAuthority);
  const health = selfHealing(homeDoctor);

  // Decide/World read the ProjectionRead's own `generatedAt` (packet:
  // console-honest-degradation) so "unknown" appears only when a door has
  // genuinely never succeeded — never for a swallowed timeout that still has
  // a good last-good value to show.
  const stamps: { label: string; generatedAt: string; source: string }[] = [
    { label: "Decide", generatedAt: attentionRead.generatedAt ?? "", source: "compound attention --json" },
    { label: "World", generatedAt: workbenchRead.generatedAt ?? "", source: "compound workbench --json --recent 500" },
    { label: "Mind", generatedAt: str(memory?.generated_at), source: "compound memory candidates --json" },
    { label: "Health", generatedAt: str(homeDoctor?.generated_at), source: "compound home doctor --json" },
  ];

  return (
    <div className="mx-auto flex flex-col gap-9 pb-16" style={{ maxWidth: "1080px" }}>
      <BriefingHeader now={now} needsYouCount={needsYouGroupCount} oldest={oldest} />

      <div className="grid gap-4 lg:grid-cols-2">
        <Headline
          top={top}
          act={act}
          mutationsEnabled={mutationsEnabled}
          runningAgents={runningAgents}
          classA={classAWaiting}
          read={{ status: attentionRead.status, lastGoodAt: attentionRead.lastGoodAt }}
        />
        <LatestMoment
          moment={compounded}
          read={{ status: workbenchRead.status, lastGoodAt: workbenchRead.lastGoodAt }}
        />
      </div>

      <ContinueCard model={continueModel} mutationsReady={mutationsEnabled} />

      <section>
        <SectionLabel>Decide</SectionLabel>
        <DecideList
          groups={decideGroups}
          total={needsYouGroupCount}
          read={{ status: attentionRead.status, lastGoodAt: attentionRead.lastGoodAt }}
        />
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
        worldDelta={worldChange.covered ? worldChange.delta : null}
        mindDelta={mindChange.covered ? mindChange.delta : null}
      />

      {/* ---------- below the fold ---------- */}

      <AttentionAgeMatrix
        groups={needsYouGroups}
        read={{ status: attentionRead.status, lastGoodAt: attentionRead.lastGoodAt }}
      />

      <section>
        <SectionLabel>Pipeline</SectionLabel>
        <PipelineStrip stages={stages} read={{ status: workbenchRead.status, lastGoodAt: workbenchRead.lastGoodAt }} />
      </section>

      <SelfHealingPanel health={health} />

      <section>
        <SectionLabel>Live activity</SectionLabel>
        <ActivityFeed recent={recent} read={{ status: workbenchRead.status, lastGoodAt: workbenchRead.lastGoodAt }} />
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

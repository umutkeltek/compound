import { ActivityFeed } from "@/components/briefing/ActivityFeed";
import { AgentsNow } from "@/components/briefing/AgentsNow";
import { AttentionAgeMatrix } from "@/components/briefing/AttentionAgeMatrix";
import { BriefingHeader } from "@/components/briefing/BriefingHeader";
import { ContinueCard } from "@/components/briefing/ContinueCard";
import { KnowsStrip } from "@/components/briefing/KnowsStrip";
import { KpiStrip } from "@/components/briefing/KpiStrip";
import { LatestMoment } from "@/components/briefing/LatestMoment";
import { NeedsYouList } from "@/components/briefing/NeedsYouList";
import { PipelineStrip } from "@/components/briefing/PipelineStrip";
import { SectionLabel } from "@/components/briefing/primitives";
import { SelfHealingPanel } from "@/components/briefing/SelfHealingPanel";
import { normalizeExecutorProjection } from "@/components/agents/executor-normalize";
import { OperatorCoordinationList } from "@/components/coordination/OperatorCoordinationList";
import { hasCopy, ReadChip, ReadFailedBanner, ReadNotice, readsPending, type ReadView } from "@/components/kit";
import { PendingRefresh } from "@/components/PendingRefresh";
import { attentionNeedsYouGroups, readAttention } from "@/lib/attention";
import {
  autonomyPosture,
  deriveContinue,
  headlineAct,
  memoryBriefing,
  oldestCreatedAt,
  pipelineStages,
  rowTsMs,
  selfHealing,
  weeklyChange,
} from "@/lib/briefing";
import { arr, HEAVY_PROJECTION_TIMEOUT_MS, readExecutors, readProjection, str } from "@/lib/compound";
import { countAgents, countInProgress, waitingOnYou } from "@/lib/counts";
import { depthShows, requestDepth } from "@/lib/depth";
import { cookies } from "next/headers";
import { operatorSessionReadiness } from "@/lib/operator-session";
import { moveTypeGroup, summarizeMoveTypeMix } from "@/lib/move-mix";
import { agentNameFor } from "@/lib/plain-words";
import { latestProofBackedChange } from "@/representation";

export const dynamic = "force-dynamic";

/** A read that has a copy, narrowed to the projection status the feed
 * components take ("loading" never reaches them: it renders ReadNotice). */
function feed(read: ReadView): { status: "live" | "stale" | "unreachable"; lastGoodAt: string | null } {
  return { status: read.status === "loading" ? "unreachable" : read.status, lastGoodAt: read.lastGoodAt };
}

/** Render `children` when the read has a copy; else the honest placeholder. */
function Gate({ read, label, children }: { read: ReadView; label: string; children: React.ReactNode }) {
  if (!hasCopy(read)) return <ReadNotice read={read} label={label} />;
  return (
    <>
      <ReadFailedBanner read={read} label={label} />
      {children}
    </>
  );
}

/**
 * Today answers one question first: "What needs you today?" The answer is a
 * sentence and at most five decisions, most urgent first (the attention
 * projection's order). When nothing needs the operator, the page says so and
 * shows what the agents are doing instead. Everything else sits below, quieter,
 * and the deeper panels stay behind the depth control.
 */
export default async function Briefing() {
  // Every read is non-blocking (`readProjection`): the page renders at once
  // from the cached copy, or shows "being read for the first time" while the
  // read queue fetches it in the background. Nothing here waits for compound.
  //
  // The memory candidates call is window-bounded on purpose: the unbounded
  // projection returns the FULL inbox (150k+ candidates, >100MB). `--state
  // promoted --created-after` bounds returned items to exactly the promoted
  // rows the 14d weekly delta consumes, while `counts.by_state` stays global
  // over the full snapshot. The 15d window is QUANTIZED to 6h buckets so the
  // argv (and therefore the cache key) is stable within a bucket.
  const sixHours = 6 * 3_600_000;
  const windowBucket = Math.floor(Date.now() / sixHours) * sixHours;
  const memoryWindowStart = new Date(windowBucket - 15 * 24 * 3_600_000).toISOString();
  // The home doctor panel shows only at the Technical depth: skip its read
  // (one to two minutes of compound) when the panel is hidden.
  const depth = await requestDepth();
  const showHealth = depthShows(depth, "technical");
  const [{ read: attentionRead, report: attention }, executorsRead, workbenchRead, memoryRead, policyRead, homeDoctorRead] = await Promise.all([
    readAttention(),
    readExecutors(),
    readProjection(["workbench", "--json", "--recent", "500"], { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS }),
    readProjection(
      ["memory", "candidates", "--json", "--state", "promoted", "--created-after", memoryWindowStart, "--limit", "4000"],
      { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS },
    ),
    readProjection(["policy", "operator-authority", "--json"]),
    readProjection(["home", "doctor", "--json"], { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS, enabled: showHealth }),
  ]);
  const workbench = workbenchRead.value;

  const now = new Date();
  const mutationsEnabled = await operatorSessionReadiness((await cookies()).toString());

  // Attention: ONE projection. Groups arrive leverage-sorted; never re-ranked here.
  const needsYouGroups = attentionNeedsYouGroups(attention);
  const top = attention?.top ?? needsYouGroups[0] ?? null;
  const first = needsYouGroups[0] ?? null;
  const waiting = waitingOnYou(attention);
  // The one-click act belongs to the first row; offered only when it is the top group.
  const act = first && top && first.key === top.key ? headlineAct(top, feed(attentionRead).status) : null;
  const oldest = oldestCreatedAt(needsYouGroups);

  // Agents and in-progress work: one definition each (lib/counts.ts). Unknown is null, never 0.
  const executors = hasCopy(executorsRead) ? normalizeExecutorProjection(executorsRead.value) : null;
  const agents = executors ? countAgents(executors.executors) : null;
  // Agent id → name, as Live names agents (the executors projection's labels).
  const agentNames = new Map((executors?.executors ?? []).map((row) => [row.id, row.label] as const));
  const workbenchKnown = hasCopy(workbenchRead);
  const open = arr(workbench?.open);
  const inProgress = workbenchKnown ? countInProgress(open, now.getTime()) : null;

  // Continue + pipeline off the workbench projection.
  const continueModel = deriveContinue(workbench, open);
  const stages = pipelineStages(workbench);
  const recent = arr(workbench?.recent);
  const compounded = latestProofBackedChange(recent);
  const producer = compounded ? agentNameFor(compounded.producer, agentNames) : { name: "", lane: "" };

  // Changes that reached the world (7-day move mix) + an honest week-over-week delta.
  const mix = summarizeMoveTypeMix(recent);
  const worldChange = weeklyChange(
    recent,
    rowTsMs,
    (item) => moveTypeGroup(str(item.move_type) || str(item.move)) === "outward",
  );

  const memory = memoryBriefing(memoryRead);
  const autonomy = hasCopy(policyRead) ? autonomyPosture(policyRead.value) : null;
  const health = selfHealing({ ...homeDoctorRead, status: feed(homeDoctorRead).status });

  const pending = readsPending(
    attentionRead,
    executorsRead,
    workbenchRead,
    memoryRead,
    policyRead,
    showHealth ? homeDoctorRead : null,
  );

  const stamps: { label: string; read: ReadView }[] = [
    { label: "Decisions", read: attentionRead },
    { label: "Agents", read: executorsRead },
    { label: "Work", read: workbenchRead },
    { label: "Memory", read: memoryRead },
    { label: "Health", read: homeDoctorRead },
  ];

  const nothingWaits = waiting !== null && waiting.groups <= 0;

  return (
    <div className="console-stage mx-auto flex flex-col gap-7 pb-16" style={{ maxWidth: "1180px" }}>
      <PendingRefresh pending={pending} stamp={now.getTime()} />

      {/* ---------- the question and its answer ---------- */}
      <div className="flex flex-col gap-4">
        <BriefingHeader now={now} waiting={waiting} read={attentionRead} oldest={oldest} />
        {hasCopy(attentionRead) ? <ReadFailedBanner read={attentionRead} label="what waits for you" /> : null}
        {waiting && waiting.groups > 0 ? (
          <NeedsYouList groups={needsYouGroups} total={waiting.groups} act={act} mutationsEnabled={mutationsEnabled} />
        ) : null}
        {nothingWaits ? (
          <AgentsNow agents={agents} agentsRead={executorsRead} inProgress={inProgress} workRead={workbenchRead} prominent />
        ) : null}
      </div>

      {/* ---------- below: quieter ---------- */}
      {nothingWaits ? null : (
        <AgentsNow agents={agents} agentsRead={executorsRead} inProgress={inProgress} workRead={workbenchRead} />
      )}

      <section data-depth-min="understand" className="flex flex-col gap-3">
        <SectionLabel>This week</SectionLabel>
        <KpiStrip
          world={
            workbenchKnown
              ? {
                  outward: mix.groups.outward,
                  meaning: mix.groups.meaning,
                  rules: mix.groups.rules,
                  upkeep: mix.groups.upkeep,
                  total: mix.total,
                  lowOutward: mix.lowOutward,
                }
              : null
          }
          worldNote={workbenchRead.status === "unreachable" ? "Could not read work history." : "Reading work history…"}
          mind={memory}
          autonomy={autonomy}
          autonomyNote={policyRead.status === "unreachable" ? "Could not read the autonomy level." : "Reading the autonomy level…"}
          worldDelta={workbenchKnown && worldChange.covered ? worldChange.delta : null}
        />
        <Gate read={workbenchRead} label="the last finished change">
          <LatestMoment
            moment={compounded}
            read={feed(workbenchRead)}
            agentName={producer.name}
            agentLane={producer.lane}
          />
        </Gate>
      </section>

      {/* ---------- deeper: behind the depth control ---------- */}

      <section data-depth-min="investigate">
        <SectionLabel>Next work an agent can finish</SectionLabel>
        <Gate read={workbenchRead} label="open work">
          <ContinueCard model={continueModel} mutationsReady={false} />
        </Gate>
      </section>

      <div data-depth-min="investigate">
        <Gate read={attentionRead} label="how long decisions have waited">
          <AttentionAgeMatrix groups={needsYouGroups} read={feed(attentionRead)} />
        </Gate>
      </div>

      {attention ? (
        <section data-depth-min="investigate">
          <OperatorCoordinationList projection={attention.operatorAttention} label="Coordination changes recorded for you" />
        </section>
      ) : null}

      <section data-depth-min="investigate">
        <SectionLabel>Work by stage</SectionLabel>
        <Gate read={workbenchRead} label="work by stage">
          <PipelineStrip stages={stages} read={feed(workbenchRead)} />
        </Gate>
      </section>

      <section data-depth-min="investigate">
        <SectionLabel>Recent changes</SectionLabel>
        <Gate read={workbenchRead} label="recent changes">
          <ActivityFeed recent={recent} read={feed(workbenchRead)} />
        </Gate>
      </section>

      <div data-depth-min="technical">
        <Gate read={homeDoctorRead} label="home health">
          <SelfHealingPanel health={health} />
        </Gate>
      </div>

      <section data-depth-min="technical">
        <SectionLabel>What the system knows</SectionLabel>
        <KnowsStrip memory={memory} />
      </section>

      <footer data-depth-min="technical" className="mt-4 flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-hair pt-4 text-2xs text-faint">
        <span>Each part of this page is read on its own and shows its own age.</span>
        <span className="flex flex-wrap items-center gap-2">
          {stamps.map((stamp) => (
            <span key={stamp.label} className="inline-flex items-center gap-1.5">
              <span className="text-faint">{stamp.label}</span>
              <ReadChip read={stamp.read} />
            </span>
          ))}
        </span>
      </footer>
    </div>
  );
}

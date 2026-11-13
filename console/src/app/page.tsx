import { ActivityFeed } from "@/components/briefing/ActivityFeed";
import { AttentionAgeMatrix } from "@/components/briefing/AttentionAgeMatrix";
import { BriefingHeader } from "@/components/briefing/BriefingHeader";
import { ContinueCard } from "@/components/briefing/ContinueCard";
import { CausalFocus } from "@/components/briefing/CausalFocus";
import { DecideList } from "@/components/briefing/DecideList";
import { Headline } from "@/components/briefing/Headline";
import { KnowsStrip } from "@/components/briefing/KnowsStrip";
import { KpiStrip } from "@/components/briefing/KpiStrip";
import { LatestMoment } from "@/components/briefing/LatestMoment";
import { PipelineStrip } from "@/components/briefing/PipelineStrip";
import { SectionLabel } from "@/components/briefing/primitives";
import { SelfHealingPanel } from "@/components/briefing/SelfHealingPanel";
import { OperatorCoordinationList } from "@/components/coordination/OperatorCoordinationList";
import { hasCopy, ReadChip, ReadFailedBanner, ReadNotice, readsPending, type ReadView } from "@/components/kit";
import { PendingRefresh } from "@/components/PendingRefresh";
import { attentionNeedsYouGroups, hasClassAGate, readAttention, waitingOnYou } from "@/lib/attention";
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
import { arr, HEAVY_PROJECTION_TIMEOUT_MS, num, readExecutors, readProjection, str } from "@/lib/compound";
import { consoleMutationsEnabled } from "@/lib/console-mutations";
import { depthShows, requestDepth } from "@/lib/depth";
import { computeConsoleMutationReadiness } from "@/lib/instance-pin";
import { moveTypeGroup, summarizeMoveTypeMix } from "@/lib/move-mix";
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
  const mutationsEnabled = computeConsoleMutationReadiness(consoleMutationsEnabled()).ready;

  // Attention — ONE projection. Groups arrive leverage-sorted; `top` is the
  // single highest-leverage gate. Never re-ranked client-side.
  const needsYouGroups = attentionNeedsYouGroups(attention);
  const top = attention?.top ?? needsYouGroups[0] ?? null;
  const waiting = waitingOnYou(attention);
  const decideGroups = needsYouGroups.slice(0, 6);
  const act = headlineAct(top, feed(attentionRead).status);
  const oldest = oldestCreatedAt(needsYouGroups);
  // Any Class-A gate waiting → hero glows amber (§9.2/§9.4).
  const classAWaiting = hasClassAGate(needsYouGroups);
  // Unknown (no executors copy yet) is null, never 0.
  const runningAgents = hasCopy(executorsRead)
    ? arr(executorsRead.value?.executors).filter((row) => num(row.running_count) > 0).length
    : null;

  // Continue + pipeline off the workbench projection.
  const workbenchKnown = hasCopy(workbenchRead);
  const open = arr(workbench?.open);
  const continueModel = deriveContinue(workbench, open);
  const stages = pipelineStages(workbench);
  const recent = arr(workbench?.recent);
  const compounded = latestProofBackedChange(recent);

  // World Δ (7-day move mix) + an honest week-over-week delta.
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
    { label: "Decide", read: attentionRead },
    { label: "Lanes", read: executorsRead },
    { label: "World", read: workbenchRead },
    { label: "Mind", read: memoryRead },
    { label: "Health", read: homeDoctorRead },
  ];

  return (
    <div className="console-stage mx-auto flex flex-col gap-7 pb-16" style={{ maxWidth: "1180px" }}>
      <PendingRefresh pending={pending} stamp={now.getTime()} />
      <BriefingHeader now={now} waiting={waiting} read={attentionRead} oldest={oldest} />

      <div className="grid gap-4 lg:grid-cols-[1.3fr_.9fr]">
        <Gate read={attentionRead} label="what waits on you">
          <Headline
            top={top}
            act={act}
            mutationsEnabled={mutationsEnabled}
            runningAgents={runningAgents ?? 0}
            classA={classAWaiting}
            read={feed(attentionRead)}
          />
        </Gate>
        <Gate read={workbenchRead} label="recent proof-backed change">
          <LatestMoment moment={compounded} read={feed(workbenchRead)} />
        </Gate>
      </div>

      <div data-depth-min="understand">
        <CausalFocus top={top} openWork={workbenchKnown ? open.length : null} activeLanes={runningAgents} />
      </div>

      <div data-depth-min="understand">
        <Gate read={workbenchRead} label="open work">
          <ContinueCard model={continueModel} mutationsReady={mutationsEnabled} />
        </Gate>
      </div>

      <section data-depth-min="understand">
        <SectionLabel>Decide</SectionLabel>
        <Gate read={attentionRead} label="what waits on you">
          <DecideList groups={decideGroups} total={waiting?.groups ?? 0} read={feed(attentionRead)} />
          {attention ? (
            <OperatorCoordinationList projection={attention.operatorAttention} label="Authority references" />
          ) : null}
        </Gate>
      </section>

      <div data-depth-min="understand">
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
      </div>

      {/* ---------- below the fold ---------- */}

      <div data-depth-min="investigate">
        <Gate read={attentionRead} label="the age of what waits on you">
          <AttentionAgeMatrix groups={needsYouGroups} read={feed(attentionRead)} />
        </Gate>
      </div>

      <section data-depth-min="investigate">
        <SectionLabel>Pipeline</SectionLabel>
        <Gate read={workbenchRead} label="the pipeline">
          <PipelineStrip stages={stages} read={feed(workbenchRead)} />
        </Gate>
      </section>

      <div data-depth-min="technical">
        <Gate read={homeDoctorRead} label="home health">
          <SelfHealingPanel health={health} />
        </Gate>
      </div>

      <section data-depth-min="investigate">
        <SectionLabel>Live activity</SectionLabel>
        <Gate read={workbenchRead} label="live activity">
          <ActivityFeed recent={recent} read={feed(workbenchRead)} />
        </Gate>
      </section>

      <section data-depth-min="technical">
        <SectionLabel>Knows</SectionLabel>
        <KnowsStrip memory={memory} />
      </section>

      <footer data-depth-min="technical" className="mt-4 flex flex-wrap items-center gap-x-4 gap-y-2 border-t border-hair pt-4 text-2xs text-faint">
        <span>Every panel reads a cached projection and stamps its own age.</span>
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

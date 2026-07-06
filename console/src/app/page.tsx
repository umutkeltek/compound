import { DecisionCard } from "@/components/DecisionCard";
import { ChangedProofStream } from "@/components/ChangedProofStream";
import { ContinuePanel } from "@/components/ContinuePanel";
import { NeedsMeStream } from "@/components/NeedsMeStream";
import { WorkNeedCard } from "@/components/WorkNeedCard";
import { WorkCard } from "@/components/WorkCard";
import {
  Card,
  Chip,
  EmptyState,
  FreshnessChip,
  SectionLabel,
  Stat,
  StateBadge,
} from "@/components/ui";
import { ago, arr, getReviews, getSourceConnections, getWorkbench, str, type Json } from "@/lib/compound";
import { externalReviewRows } from "@/lib/review-data";
import { loadActionableMemoryCandidates } from "@/lib/memory-data";
import { enrichWorkItems } from "@/lib/work-data";

export const dynamic = "force-dynamic";

function sourceStatusLabel(status: string): string {
  switch (status) {
    case "blocked_private_config":
      return "needs private config";
    case "needs_private_input":
      return "needs private input";
    case "ready_preflight":
      return "ready for preflight";
    case "needs_review":
      return "needs review";
    case "needs_mapping":
      return "needs mapping";
    case "not_connected":
      return "not connected";
    default:
      return status.replace(/_/g, " ") || "unknown";
  }
}

export default async function Dashboard() {
  const [workbench, reviews, memoryActionable, sourceConnections] = await Promise.all([
    getWorkbench(),
    getReviews(),
    loadActionableMemoryCandidates(),
    getSourceConnections(),
  ]);

  const open = arr(workbench?.open);
  const proposedWork = await enrichWorkItems(
    open.filter((work) => str(work.state) === "proposed"),
  );
  const running = open.filter((work) =>
    ["in_progress", "running", "verifying", "claimed"].includes(str(work.state)),
  );
  const recent = arr(workbench?.recent);
  const continueRuns = arr(workbench?.continue_runs);
  const dispatchStepRuns = arr(workbench?.dispatch_step_runs);
  const heldWorkNeeds = open.flatMap((work) =>
    arr(work.review_cards).map((card) => ({ work, card })),
  );
  const memoryWorkNeeds = heldWorkNeeds.filter(
    ({ card }) => str(card.kind) === "memory_candidate_adjudication",
  );
  const operatorWorkNeeds = heldWorkNeeds.filter(
    ({ card }) => str(card.kind) !== "memory_candidate_adjudication",
  );

  const reviewRows = externalReviewRows(reviews);
  const sourceRows = arr(sourceConnections?.connections);
  const sourceCounts = (sourceConnections?.counts ?? {}) as Json;
  const sourceAttentionRows = sourceRows
    .filter((source) => !["connected", "observed"].includes(str(source.status)))
    .slice(0, 5);

  // Unified needs-me stream: external reviews + held work decisions + proposed work + memory.
  const counts = {
    reviews: reviewRows.length,
    work: operatorWorkNeeds.length + proposedWork.length,
    memory: memoryWorkNeeds.length + memoryActionable.length,
  };
  const needsMeTotal = counts.reviews + counts.work + counts.memory;

  const executors = new Map<string, { running: number; ready: number; offered: number; needsMe: number; assigned: number }>();
  for (const work of open) {
    const who = str(work.claimed_by) || str(work.owner) || "unassigned";
    const entry = executors.get(who) ?? { running: 0, ready: 0, offered: 0, needsMe: 0, assigned: 0 };
    const state = str(work.state);
    entry.assigned++;
    if (["claimed", "in_progress", "running", "verifying"].includes(state)) entry.running++;
    else if (state === "ready") entry.ready++;
    else if (["proposed", "admitted"].includes(state)) entry.offered++;
    else if (["held", "blocked"].includes(state)) entry.needsMe++;
    executors.set(who, entry);
  }

  // Trajectory signal: recent landings split by §8 yield (productive vs low-yield).
  const surfaceTitle = (item: Json) => {
    const s = str(item.canonical_surface);
    if (s.startsWith("packet:")) return s.slice("packet:".length).replace(/-/g, " ");
    return str(item.success_predicate) || str(item.work_id) || str(item.id);
  };
  const recentTypes = recent.map((r) => str(r.move_type) || str(r.move));
  const productiveRecent = recentTypes.filter((t) =>
    ["outward_delta", "memory_delta", "rule_delta"].includes(t),
  ).length;
  const lowYieldRecent = recentTypes.filter((t) =>
    ["control_plane_repair", "audit_only"].includes(t),
  ).length;
  const grinding = recent.length > 0 && lowYieldRecent > productiveRecent;

  return (
    <div className="flex flex-col gap-8">
      <div className="flex items-end justify-between gap-4">
        <div>
          <h1 className="text-[22px] font-semibold tracking-tight">Dashboard</h1>
          <p className="mt-0.5 text-[12.5px] text-ink-3">
            Continue what can move, handle what needs you, and see what changed.
          </p>
        </div>
        <FreshnessChip generatedAt={str(workbench?.generated_at)} source="compound workbench --json" />
      </div>

      <ContinuePanel workbench={workbench} open={open} />

      <div className="grid gap-4 md:grid-cols-4">
        <Stat label="Needs me" value={needsMeTotal} accent="orange" glyph="!" sub="reviews + work + memory" />
        <Stat label="Running" value={running.length} accent="blue" glyph="▶" sub="claimed / in flight" />
        <Stat
          label="Changed"
          value={recent.length + continueRuns.length + dispatchStepRuns.length}
          accent="green"
          glyph="Δ"
          sub="proof + movement"
        />
        <Stat
          label="Sources"
          value={(Number(sourceCounts.needs_input) || 0) + (Number(sourceCounts.action_required) || 0)}
          accent="violet"
          glyph="S"
          sub="input + cards"
        />
      </div>

      <section>
        <SectionLabel>Needs Me</SectionLabel>
        <NeedsMeStream
          counts={counts}
          slots={{
            reviews: reviewRows.map((row) => (
              <DecisionCard key={str(row.id)} area="external-repo-intake" item={row} />
            )),
            work: [
              ...operatorWorkNeeds.map(({ work, card }) => (
                <WorkNeedCard
                  key={`${str(work.work_id) || str(work.id)}:${str(card.id)}`}
                  work={work}
                  card={card}
                />
              )),
              ...proposedWork.map((work) => <WorkCard key={work.displayId} work={work} compact />),
            ],
            memory: [
              ...memoryWorkNeeds.map(({ work, card }) => (
                <WorkNeedCard
                  key={`${str(work.work_id) || str(work.id)}:${str(card.id)}`}
                  work={work}
                  card={card}
                />
              )),
              ...memoryActionable
                .slice(0, 12)
                .map((candidate) => (
                  <DecisionCard key={candidate.id} area="memory-adjudicate" item={candidate} />
                )),
            ],
          }}
        />
      </section>

      <section>
        <SectionLabel>Running</SectionLabel>
        <div className="grid gap-4 lg:grid-cols-3">
          <Card title="In flight" accent="blue" glyph="▶" className="lg:col-span-2">
            {running.length ? (
              <ul className="flex flex-col gap-2.5">
                {running.map((work) => (
                  <li
                    key={str(work.work_id)}
                    className="flex items-center justify-between gap-3 rounded-xl border border-line px-3 py-2.5"
                  >
                    <div className="flex min-w-0 flex-col gap-0.5">
                      <div className="flex items-center gap-2">
                        <span className="mono truncate text-[11px] text-ink-3">{str(work.work_id) || str(work.id)}</span>
                        <StateBadge state={str(work.state)} />
                      </div>
                      <span className="truncate text-[12px] text-ink-2">{surfaceTitle(work)}</span>
                    </div>
                    <div className="flex shrink-0 items-center gap-2 text-[11.5px] text-ink-3">
                      <Chip tone="violet">{str(work.claimed_by) || str(work.owner) || "unknown"}</Chip>
                      {str(work.claimed_at) ? <span>{ago(work.claimed_at)}</span> : null}
                    </div>
                  </li>
                ))}
              </ul>
            ) : (
              <EmptyState>No executor is running work right now.</EmptyState>
            )}
          </Card>

          <Card title="Executors" accent="violet" glyph="E">
            {executors.size ? (
              <ul className="flex flex-col gap-2.5">
	                {[...executors.entries()].map(([who, entry]) => (
	                  <li key={who} className="flex items-center justify-between gap-2">
	                    <span className="text-[13px] font-medium">{who}</span>
	                    <span className="text-[11.5px] text-ink-3">
	                      {[
	                        entry.running ? `${entry.running} running` : "",
	                        entry.ready ? `${entry.ready} ready` : "",
	                        entry.needsMe ? `${entry.needsMe} needs me` : "",
	                        entry.offered ? `${entry.offered} offered` : "",
	                      ]
	                        .filter(Boolean)
	                        .join(" / ") || `${entry.assigned} assigned`}
	                    </span>
	                  </li>
	                ))}
              </ul>
            ) : (
              <EmptyState>No active executors.</EmptyState>
            )}
          </Card>
        </div>
      </section>

      <section>
        <SectionLabel>Changed</SectionLabel>
        <Card
          title="Proof stream"
          accent="green"
          glyph="Δ"
          right={
            <span className="text-[11px]">
              <span className="font-semibold text-emerald-600">{productiveRecent} productive</span>
              <span className="text-ink-3"> · </span>
              <span className="text-ink-3">{continueRuns.length + dispatchStepRuns.length} moved</span>
              <span className="text-ink-3"> · </span>
              <span className={grinding ? "font-semibold text-red-500" : "text-ink-3"}>
                {lowYieldRecent} low-yield
              </span>
              {grinding ? (
                <>
                  <span className="text-ink-3"> · </span>
                  <span className="rounded-full bg-red-50 px-1.5 py-0.5 text-[10px] font-semibold text-red-600">
                    grinding
                  </span>
                </>
              ) : null}
            </span>
          }
        >
          <ChangedProofStream recent={recent} continueRuns={continueRuns} dispatchStepRuns={dispatchStepRuns} />
        </Card>
      </section>

      <section>
        <SectionLabel>Sources</SectionLabel>
        <Card title="Source readiness" accent="violet" glyph="S">
          {sourceAttentionRows.length ? (
            <ul className="flex flex-col gap-2.5">
              {sourceAttentionRows.map((source) => (
                <li
                  key={str(source.id)}
                  className="flex items-start justify-between gap-3 rounded-xl border border-line px-3 py-2.5"
                >
                  <div className="min-w-0">
                    <div className="text-[12.5px] font-medium text-ink">{str(source.label) || str(source.id)}</div>
                    <div className="mt-0.5 text-[11.5px] leading-relaxed text-ink-3">
                      {str(source.next_safe_action) || "No next action surfaced."}
                    </div>
                  </div>
                  <Chip tone="gray">{sourceStatusLabel(str(source.status))}</Chip>
                </li>
              ))}
            </ul>
        ) : sourceRows.length ? (
          <EmptyState>All observed sources are quiet.</EmptyState>
          ) : (
            <EmptyState>No source projection available.</EmptyState>
          )}
        </Card>
      </section>
    </div>
  );
}

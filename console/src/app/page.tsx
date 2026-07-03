import { DecisionCard } from "@/components/DecisionCard";
import { NeedsMeStream } from "@/components/NeedsMeStream";
import { WorkCard } from "@/components/WorkCard";
import {
  Card,
  Chip,
  EmptyState,
  FreshnessChip,
  MoveChip,
  SectionLabel,
  Stat,
  StateBadge,
} from "@/components/ui";
import { ago, arr, getReviews, getWorkbench, str, type Json } from "@/lib/compound";
import { externalReviewRows } from "@/lib/review-data";
import { loadActionableMemoryCandidates } from "@/lib/memory-data";
import { enrichWorkItems } from "@/lib/work-data";

export const dynamic = "force-dynamic";

export default async function Dashboard() {
  const [workbench, reviews, memoryActionable] = await Promise.all([
    getWorkbench(),
    getReviews(),
    loadActionableMemoryCandidates(),
  ]);

  const open = arr(workbench?.open);
  const proposedWork = await enrichWorkItems(
    open.filter((work) => str(work.state) === "proposed"),
  );
  const running = open.filter((work) =>
    ["in_progress", "running", "verifying", "claimed"].includes(str(work.state)),
  );
  const recent = arr(workbench?.recent);

  const reviewRows = externalReviewRows(reviews);

  // Unified needs-me stream: reviews + proposed work + proposed memory.
  const counts = {
    reviews: reviewRows.length,
    work: proposedWork.length,
    memory: memoryActionable.length,
  };
  const needsMeTotal = counts.reviews + counts.work + counts.memory;

  const executors = new Map<string, { running: number; proposed: number }>();
  for (const work of open) {
    const who = str(work.claimed_by) || str(work.owner) || "unassigned";
    const entry = executors.get(who) ?? { running: 0, proposed: 0 };
    if (str(work.state) === "proposed") entry.proposed++;
    else entry.running++;
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
    ["outward_delta", "memory_delta", "rule_delta", "landed_owner"].includes(t),
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
            Read it, understand it, decide it — in one place. Needs-me first, then execution, then change.
          </p>
        </div>
        <FreshnessChip generatedAt={str(workbench?.generated_at)} source="compound workbench --json" />
      </div>

      <div className="grid gap-4 md:grid-cols-3">
        <Stat label="Needs me" value={needsMeTotal} accent="orange" glyph="!" sub="reviews + work + memory" />
        <Stat label="Running" value={running.length} accent="blue" glyph="▶" sub="claimed / in flight" />
        <Stat label="Changed" value={recent.length} accent="green" glyph="Δ" sub="recent terminal receipts" />
      </div>

      <section>
        <SectionLabel>Needs Me</SectionLabel>
        <NeedsMeStream
          counts={counts}
          slots={{
            reviews: reviewRows.map((row) => (
              <DecisionCard key={str(row.id)} area="external-repo-intake" item={row} />
            )),
            work: proposedWork.map((work) => <WorkCard key={work.displayId} work={work} compact />),
            memory: memoryActionable
              .slice(0, 12)
              .map((candidate) => (
                <DecisionCard key={candidate.id} area="memory-adjudicate" item={candidate} />
              )),
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
                      {entry.running ? `${entry.running} running` : ""}
                      {entry.running && entry.proposed ? " / " : ""}
                      {entry.proposed ? `${entry.proposed} offered` : ""}
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
          title="Recent landings"
          accent="green"
          glyph="Δ"
          right={
            <span className="text-[11px]">
              <span className="font-semibold text-emerald-600">{productiveRecent} productive</span>
              <span className="text-ink-3"> · </span>
              <span className={grinding ? "font-semibold text-red-500" : "text-ink-3"}>
                {lowYieldRecent} low-yield
              </span>
              {grinding ? (
                <span className="ml-1.5 rounded-full bg-red-50 px-1.5 py-0.5 text-[10px] font-semibold text-red-600">
                  grinding
                </span>
              ) : null}
            </span>
          }
        >
          {recent.length ? (
            <ul className="flex flex-col gap-2">
              {recent.slice(0, 8).map((item, index) => (
                <li key={index} className="flex items-center justify-between gap-3 rounded-lg px-1 py-1.5">
                  <div className="flex min-w-0 items-center gap-3">
                    <span className="mono truncate text-[12px]">{str(item.work_id) || str(item.id)}</span>
                    <StateBadge state={str(item.state)} />
                    <MoveChip move={str(item.move) || str(item.move_type)} />
                  </div>
                  <span className="mono shrink-0 text-[11px] text-ink-3">
                    {str(item.receipt) || str(item.receipt_id)}
                  </span>
                </li>
              ))}
            </ul>
          ) : (
            <EmptyState>No recent terminal work in the report.</EmptyState>
          )}
        </Card>
      </section>
    </div>
  );
}

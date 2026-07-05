import { WorkCard } from "@/components/WorkCard";
import { Card, Chip, EmptyState, FreshnessChip, SectionLabel } from "@/components/ui";
import { arr, getWorkbench, str, type Json } from "@/lib/compound";
import { enrichWorkItems, WorkCardData } from "@/lib/work-data";

export const dynamic = "force-dynamic";

const LANES: {
  key: string;
  title: string;
  description: string;
  states: string[];
  tint: string;
  empty: string;
}[] = [
  {
    key: "intake",
    title: "Make Runnable",
    description: "Admission and ready-state moves. No execution yet.",
    states: ["proposed", "admitted"],
    tint: "#f59e0b",
    empty: "No work needs intake.",
  },
  {
    key: "ready",
    title: "Ready for Harness",
    description: "Claimable by Codex, Claude, or another executor.",
    states: ["ready"],
    tint: "#10b981",
    empty: "No claimable work.",
  },
  {
    key: "active",
    title: "In Flight",
    description: "Already claimed, running, or verifying.",
    states: ["claimed", "in_progress", "running", "verifying"],
    tint: "#3b82f6",
    empty: "No active executor work.",
  },
  {
    key: "operator",
    title: "Needs Operator",
    description: "Human gates parked outside the runnable path.",
    states: ["blocked", "held"],
    tint: "#8b5cf6",
    empty: "No operator gates.",
  },
];

function Column({
  title,
  description,
  tint,
  items,
  empty,
}: {
  title: string;
  description: string;
  tint: string;
  items: WorkCardData[];
  empty: string;
}) {
  return (
    <div className="flex w-[328px] shrink-0 flex-col gap-3">
      <div className="flex items-center justify-between gap-2 px-1">
        <div className="flex min-w-0 items-start gap-2">
          <span className="mt-1 h-2.5 w-2.5 shrink-0 rounded-[4px]" style={{ background: tint }} />
          <div className="min-w-0">
            <h2 className="text-[13px] font-semibold">{title}</h2>
            <p className="mt-0.5 text-[11.5px] leading-snug text-ink-3">{description}</p>
          </div>
        </div>
        <Chip tone={items.length ? "orange" : "gray"}>{items.length}</Chip>
      </div>
      <div className="flex flex-col gap-3">
        {items.length ? (
          items.map((work) => <WorkCard key={work.displayId} work={work} />)
        ) : (
          <EmptyState>{empty}</EmptyState>
        )}
      </div>
    </div>
  );
}

function DispatchStatus({ workbench, open }: { workbench: Json | null; open: WorkCardData[] }) {
  const next = (workbench?.dispatch_next ?? {}) as Json;
  const canRun = Boolean(next.can_run);
  const action = str(next.action);
  const waitReason = str(next.wait_reason);
  const blockedOn = arr(next.blocked_on).map(String);
  const intakeCount = open.filter((work) => ["proposed", "admitted"].includes(str(work.state))).length;
  const readyCount = open.filter((work) => str(work.state) === "ready").length;
  const operatorGateCount = open.filter((work) => ["blocked", "held"].includes(str(work.state))).length;
  const command = arr(next.command).map(String).join(" ");

  let title = "No runnable work";
  let body = "The board has no claimable lifecycle step right now.";
  let tone: "blue" | "green" | "orange" | "violet" | "gray" = "gray";

  if (canRun) {
    title = action ? `Next step: ${action}` : "Next step is ready";
    body = command || "A safe dispatch step is available.";
    tone = "green";
  } else if (readyCount > 0) {
    title = "Claimable work is waiting";
    body = `${readyCount} ready item${readyCount === 1 ? "" : "s"} can be claimed by a harness.`;
    tone = "green";
  } else if (intakeCount > 0) {
    title = "Make work runnable";
    body = `${intakeCount} item${intakeCount === 1 ? "" : "s"} need admit/ready state moves before a harness can claim them.`;
    tone = "orange";
  } else if (operatorGateCount > 0) {
    title = "Only operator gates remain";
    body = `${operatorGateCount} held/blocked gate${operatorGateCount === 1 ? "" : "s"} are parked for explicit review.`;
    tone = "violet";
  } else if (waitReason) {
    title = "Dispatch is waiting";
    body = waitReason.replace(/_/g, " ");
  }

  return (
    <Card title={title} accent={tone} glyph=">" className="p-4">
      <div className="flex flex-col gap-3">
        <p className="text-[12.5px] leading-relaxed text-ink-2">{body}</p>
        <div className="flex flex-wrap gap-2">
          <Chip tone={readyCount ? "green" : "gray"}>{readyCount} ready</Chip>
          <Chip tone={intakeCount ? "orange" : "gray"}>{intakeCount} intake</Chip>
          <Chip tone={operatorGateCount ? "violet" : "gray"}>{operatorGateCount} operator gates</Chip>
          {waitReason ? <Chip tone="gray">{waitReason.replace(/_/g, " ")}</Chip> : null}
        </div>
        {!canRun && blockedOn.length > 0 ? (
          <p className="text-[11.5px] leading-relaxed text-ink-3">
            Blocked refs are parked as evidence, not the main queue: {blockedOn.slice(0, 4).join(" / ")}
            {blockedOn.length > 4 ? " / ..." : ""}
          </p>
        ) : null}
      </div>
    </Card>
  );
}

export default async function WorkBoard() {
  const workbench = await getWorkbench();
  const open = await enrichWorkItems(arr(workbench?.open));
  const recent = await enrichWorkItems(arr(workbench?.recent).slice(0, 8));
  const byState = ((workbench?.counts ?? {}).by_state ?? {}) as Record<string, number>;

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-end justify-between gap-4">
        <div>
          <h1 className="text-[22px] font-semibold tracking-tight">Work</h1>
          <p className="mt-0.5 text-[12.5px] text-ink-3">
            Move work toward claimable execution without letting review gates become the whole job.
          </p>
        </div>
        <FreshnessChip generatedAt={str(workbench?.generated_at)} source="compound workbench --json" />
      </div>

      <DispatchStatus workbench={workbench} open={open} />

      <section>
        <SectionLabel>Execution Board</SectionLabel>
        <div className="overflow-x-auto pb-2">
          <div className="flex min-w-max gap-4">
            {LANES.map((column) => (
              <Column
                key={column.key}
                title={column.title}
                description={column.description}
                tint={column.tint}
                empty={column.empty}
                items={open.filter((work) => column.states.includes(str(work.state)))}
              />
            ))}
          </div>
        </div>
      </section>

      <section>
        <SectionLabel>Recently terminal</SectionLabel>
        {recent.length ? (
          <div className="grid gap-3 lg:grid-cols-2">
            {recent.map((work) => (
              <WorkCard key={work.displayId} work={work} compact />
            ))}
          </div>
        ) : (
          <EmptyState>No recent terminal work in the report.</EmptyState>
        )}
      </section>

      <p className="text-[11.5px] text-ink-3">
        by_state:{" "}
        {Object.entries(byState)
          .map(([key, value]) => `${key} ${value}`)
          .join(" / ") || "none"}
      </p>
    </div>
  );
}

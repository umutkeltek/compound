import type { ReactNode } from "react";

import { ContinuePanel } from "@/components/ContinuePanel";
import { WorkNeedCard } from "@/components/WorkNeedCard";
import { WorkCard } from "@/components/WorkCard";
import { Chip, EmptyState, FreshnessChip, SectionLabel } from "@/components/ui";
import { arr, getWorkbench, str } from "@/lib/compound";
import { enrichWorkItems, type WorkCardData } from "@/lib/work-data";

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
    title: "Inbox",
    description: "Suggested tasks can be prepared.",
    states: ["proposed", "admitted"],
    tint: "#f59e0b",
    empty: "No suggested tasks.",
  },
  {
    key: "ready",
    title: "Ready",
    description: "Waiting for Codex, Claude, or another executor.",
    states: ["ready"],
    tint: "#10b981",
    empty: "Nothing is ready to start.",
  },
  {
    key: "active",
    title: "Running",
    description: "Assigned, in progress, or being checked.",
    states: ["claimed", "in_progress", "running", "verifying"],
    tint: "#3b82f6",
    empty: "Nothing is running.",
  },
  {
    key: "operator",
    title: "Needs me",
    description: "Real decisions, missing inputs, or blocked work.",
    states: ["blocked", "held"],
    tint: "#8b5cf6",
    empty: "Nothing needs your decision.",
  },
];

function Column({
  title,
  description,
  tint,
  items,
  empty,
  renderItem = (work) => <WorkCard work={work} />,
}: {
  title: string;
  description: string;
  tint: string;
  items: WorkCardData[];
  empty: string;
  renderItem?: (work: WorkCardData) => ReactNode;
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
          items.map((work) => (
            <div key={work.displayId} className="contents">
              {renderItem(work)}
            </div>
          ))
        ) : (
          <EmptyState>{empty}</EmptyState>
        )}
      </div>
    </div>
  );
}

function renderOperatorWork(work: WorkCardData): ReactNode {
  const cards = arr(work.review_cards);
  if (!cards.length) return <WorkCard work={work} />;
  return cards.map((card) => <WorkNeedCard key={`${work.displayId}:${str(card.id)}`} work={work} card={card} />);
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
            Continue what can move, and keep real decisions separate.
          </p>
        </div>
        <FreshnessChip generatedAt={str(workbench?.generated_at)} source="compound workbench --json" />
      </div>

      <ContinuePanel workbench={workbench} open={open} />

      <section>
        <SectionLabel>Work Board</SectionLabel>
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
                renderItem={column.key === "operator" ? renderOperatorWork : undefined}
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

import { WorkCard } from "@/components/WorkCard";
import { Chip, EmptyState, FreshnessChip, SectionLabel } from "@/components/ui";
import { arr, getWorkbench, str } from "@/lib/compound";
import { enrichWorkItems, WorkCardData } from "@/lib/work-data";

export const dynamic = "force-dynamic";

const COLUMNS: { key: string; title: string; states: string[]; tint: string }[] = [
  { key: "proposed", title: "Proposed", states: ["proposed"], tint: "#f59e0b" },
  { key: "admitted", title: "Admitted / ready", states: ["admitted", "ready"], tint: "#9aa4b2" },
  { key: "active", title: "Claimed / running", states: ["claimed", "in_progress", "running", "verifying"], tint: "#3b82f6" },
  { key: "blocked", title: "Blocked / held", states: ["blocked", "held"], tint: "#8b5cf6" },
];

function Column({
  title,
  tint,
  items,
}: {
  title: string;
  tint: string;
  items: WorkCardData[];
}) {
  return (
    <div className="flex w-[310px] shrink-0 flex-col gap-3">
      <div className="flex items-center justify-between gap-2 px-1">
        <div className="flex items-center gap-2">
          <span className="h-2.5 w-2.5 rounded-[4px]" style={{ background: tint }} />
          <h2 className="text-[13px] font-semibold">{title}</h2>
        </div>
        <Chip tone={items.length ? "orange" : "gray"}>{items.length}</Chip>
      </div>
      <div className="flex flex-col gap-3">
        {items.length ? (
          items.map((work) => <WorkCard key={work.displayId} work={work} />)
        ) : (
          <EmptyState>Empty.</EmptyState>
        )}
      </div>
    </div>
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
            Proposed work can be admitted or cancelled here. Each card links to the full work view.
          </p>
        </div>
        <FreshnessChip generatedAt={str(workbench?.generated_at)} source="compound workbench --json" />
      </div>

      <section>
        <SectionLabel>Open Board</SectionLabel>
        <div className="overflow-x-auto pb-2">
          <div className="flex min-w-max gap-4">
            {COLUMNS.map((column) => (
              <Column
                key={column.key}
                title={column.title}
                tint={column.tint}
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

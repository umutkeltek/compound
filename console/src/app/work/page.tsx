import type { ReactNode } from "react";

import { ContinueButton, type ContinueRequest } from "@/components/ContinueButton";
import { WorkNeedCard } from "@/components/WorkNeedCard";
import { WorkCard } from "@/components/WorkCard";
import { Card, Chip, EmptyState, FreshnessChip, SectionLabel } from "@/components/ui";
import { arr, getWorkbench, str, type Json } from "@/lib/compound";
import { formatBlockedRef } from "@/lib/work-labels";
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
    title: "Inbox",
    description: "Suggested tasks that can be prepared.",
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
  return cards.map((card) => (
    <WorkNeedCard key={`${work.displayId}:${str(card.id)}`} work={work} card={card} />
  ));
}

function DispatchStatus({ workbench, open }: { workbench: Json | null; open: WorkCardData[] }) {
  const next = (workbench?.dispatch_next ?? {}) as Json;
  const canRun = Boolean(next.can_run);
  const action = str(next.action);
  const actor = str(next.actor) || "codex";
  const workId = str(next.work_id);
  const waitReason = str(next.wait_reason);
  const blockedOn = arr(next.blocked_on).map(String);
  const admissionPreviews = arr(workbench?.admission_previews);
  const continuableAdmissionPreviews = admissionPreviews.filter(
    (preview) =>
      Boolean(str(preview?.work_id)) &&
      Boolean(preview?.policy_allowed) &&
      !Boolean(preview?.requires_operator) &&
      !str(preview?.admission_blocked_reason) &&
      str(preview?.staleness) === "fresh",
  );
  const blockedAdmissionPreviewCount = admissionPreviews.length - continuableAdmissionPreviews.length;
  const admissionPreview = continuableAdmissionPreviews[0] ?? null;
  const admissionPreviewWorkId = str(admissionPreview?.work_id);
  const inboxItems = open.filter((work) => ["proposed", "admitted"].includes(str(work.state)));
  const inboxItem = inboxItems[0] ?? null;
  const intakeCount = inboxItems.length + continuableAdmissionPreviews.length;
  const readyCount = open.filter((work) => str(work.state) === "ready").length;
  const operatorGateCount =
    open.filter((work) => ["blocked", "held"].includes(str(work.state))).length + blockedAdmissionPreviewCount;
  let continueRequest: ContinueRequest | null = null;

  let title = "Nothing to continue";
  let body = "Compound is not seeing a safe next move right now.";
  let tone: "blue" | "green" | "orange" | "violet" | "gray" = "gray";

  if (canRun) {
    const nextAction = {
      claim: "start",
      advance: "continue",
      close: "finish",
    }[action] ?? action.replace(/_/g, " ");
    title = "Continue";
    body = nextAction ? `Next safe move: ${nextAction}.` : "A safe next move is ready.";
    tone = "green";
    continueRequest = workId
      ? {
          area: "work-continue",
          itemId: workId,
          option: "continue",
          fields: { actor },
          command: ["compound", "work", "continue", "--actor", actor, "--expect-work", workId, "--until-wait", "--json"],
          note: "Moves safe lifecycle steps until the next real wait. Does not run task commands.",
        }
      : null;
  } else if (readyCount > 0) {
    title = "Ready to start";
    body = `${readyCount} item${readyCount === 1 ? "" : "s"} can be started by an executor.`;
    tone = "green";
  } else if (intakeCount > 0) {
    title = "Prepare suggested work";
    body = `${intakeCount} suggested task${intakeCount === 1 ? "" : "s"} can be prepared before an executor starts.`;
    tone = "orange";
    continueRequest = inboxItem
      ? {
          area: "work-continue",
          itemId: inboxItem.displayId,
          option: "continue",
          fields: { actor },
          command: ["compound", "work", "continue", "--actor", actor, "--expect-work", inboxItem.displayId, "--until-wait", "--json"],
          note: "Prepares and starts one suggested task until the next real wait. Does not run task commands.",
        }
      : admissionPreviewWorkId
        ? {
            area: "work-continue",
            itemId: admissionPreviewWorkId,
            option: "continue",
            fields: { actor },
            command: [
              "compound",
              "work",
              "continue",
              "--actor",
              actor,
              "--expect-work",
              admissionPreviewWorkId,
              "--until-wait",
              "--json",
            ],
            note: "Adds and starts one suggested task until the next real wait. Does not run task commands.",
          }
      : null;
  } else if (operatorGateCount > 0) {
    title = "Needs your decision";
    body = `${operatorGateCount} item${operatorGateCount === 1 ? "" : "s"} need judgment, private input, or an unblock step.`;
    tone = "violet";
  } else if (waitReason) {
    title = "Waiting";
    body = waitReason.replace(/_/g, " ");
  }

  return (
    <Card title={title} accent={tone} glyph=">" className="p-4">
      <div className="flex flex-col gap-3">
        <p className="text-[12.5px] leading-relaxed text-ink-2">{body}</p>
        <div className="flex flex-wrap gap-2">
          <Chip tone={readyCount ? "green" : "gray"}>{readyCount} ready</Chip>
          <Chip tone={intakeCount ? "orange" : "gray"}>{intakeCount} inbox</Chip>
          <Chip tone={operatorGateCount ? "violet" : "gray"}>{operatorGateCount} needs me</Chip>
          {waitReason ? <Chip tone="gray">{waitReason.replace(/_/g, " ")}</Chip> : null}
        </div>
        <ContinueButton request={continueRequest} />
        {!canRun && blockedOn.length > 0 ? (
          <p className="text-[11.5px] leading-relaxed text-ink-3">
            Waiting on {blockedOn.slice(0, 4).map(formatBlockedRef).join(" / ")}
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
            Continue what can move, and separate real decisions from lifecycle plumbing.
          </p>
        </div>
        <FreshnessChip generatedAt={str(workbench?.generated_at)} source="compound workbench --json" />
      </div>

      <DispatchStatus workbench={workbench} open={open} />

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

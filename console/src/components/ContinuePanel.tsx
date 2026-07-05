import { ContinueButton, type ContinueRequest } from "@/components/ContinueButton";
import { Card, Chip, type Accent } from "@/components/ui";
import { arr, str, type Json } from "@/lib/compound";
import { formatBlockedRef } from "@/lib/work-labels";

type ContinuePanelProps = {
  workbench: Json | null;
  open: Json[];
  className?: string;
};

function itemWorkId(item: Json): string {
  return str(item.work_id) || str(item.id) || str(item.displayId);
}

function plainReason(value: string): string {
  return value.replace(/_/g, " ");
}

function actionLabel(action: string): string {
  switch (action) {
    case "admit":
      return "add suggested work";
    case "prepare":
      return "prepare work";
    case "claim":
      return "start work";
    case "advance":
      return "continue work";
    case "close":
      return "finish with proof";
    default:
      return action ? plainReason(action) : "continue work";
  }
}

function continueRequest(workId: string, actor: string, note: string): ContinueRequest {
  return {
    area: "work-continue",
    itemId: workId,
    option: "continue",
    fields: { actor },
    command: ["compound", "work", "continue", "--actor", actor, "--expect-work", workId, "--until-wait", "--json"],
    note,
  };
}

export function ContinuePanel({ workbench, open, className = "" }: ContinuePanelProps) {
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
  const reviewCardIds = new Set(
    open.flatMap((work) => arr(work.review_cards).map((card) => str(card.id)).filter(Boolean)),
  );
  const blockedAdmissionPreviewCount = admissionPreviews.filter(
    (preview) =>
      !continuableAdmissionPreviews.includes(preview) &&
      !reviewCardIds.has(str(preview?.action_context?.source_id)),
  ).length;
  const admissionPreviewWorkId = str(continuableAdmissionPreviews[0]?.work_id);

  const inboxItems = open.filter((work) => ["proposed", "admitted"].includes(str(work.state)));
  const inboxWorkId = itemWorkId(inboxItems[0] ?? {});
  const intakeCount = inboxItems.length + continuableAdmissionPreviews.length;
  const readyCount = open.filter((work) => str(work.state) === "ready").length;
  const operatorGateCount =
    open.reduce((total, work) => total + arr(work.review_cards).length, 0) + blockedAdmissionPreviewCount;

  let headline = "Nothing to continue";
  let body = "Compound is not seeing a safe next move right now.";
  let tone: Accent = "blue";
  let request: ContinueRequest | null = null;

  if (canRun && workId) {
    headline = `Next: ${actionLabel(action)}`;
    body = `Compound can move ${workId} until the next real wait.`;
    tone = "green";
    request = continueRequest(workId, actor, "Moves safe prep/start/finish steps until the next real wait. Does not run task commands.");
  } else if (readyCount > 0) {
    headline = "Ready to start";
    body = `${readyCount} item${readyCount === 1 ? "" : "s"} can be started by an executor.`;
    tone = "green";
  } else if (intakeCount > 0) {
    headline = "Prepare suggested work";
    body = `${intakeCount} suggested task${intakeCount === 1 ? "" : "s"} can be prepared before an executor starts.`;
    tone = "orange";
    const target = inboxWorkId || admissionPreviewWorkId;
    request = target
      ? continueRequest(target, actor, "Adds, prepares, and starts one suggested task until the next real wait. Does not run task commands.")
      : null;
  } else if (operatorGateCount > 0) {
    headline = "Needs your decision";
    body =
      operatorGateCount === 1
        ? "1 item needs judgment, private input, or an unblock step."
        : `${operatorGateCount} items need judgment, private input, or an unblock step.`;
    tone = "violet";
  } else if (waitReason) {
    headline = "Waiting";
    body = plainReason(waitReason);
    tone = "gray";
  }

  return (
    <Card title="Continue" accent={tone} glyph=">" className={`p-4 ${className}`}>
      <div className="flex flex-col gap-3">
        <div>
          <h2 className="text-[15px] font-semibold leading-snug text-ink">{headline}</h2>
          <p className="mt-1 text-[12.5px] leading-relaxed text-ink-2">{body}</p>
        </div>

        <div className="flex flex-wrap gap-2">
          <Chip tone={readyCount ? "green" : "gray"}>{readyCount} ready</Chip>
          <Chip tone={intakeCount ? "orange" : "gray"}>{intakeCount} inbox</Chip>
          <Chip tone={operatorGateCount ? "violet" : "gray"}>{operatorGateCount} needs me</Chip>
          {waitReason ? <Chip tone="gray">{plainReason(waitReason)}</Chip> : null}
          <Chip tone="gray">{actor}</Chip>
        </div>

        <ContinueButton request={request} />

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

import type { HeadlineActDescriptor } from "./briefing";
import type { DecideAct } from "./decide-model";

export type ClientDecideAct = Extract<DecideAct, { mode: "bespoke" | "batch-atomic" | "generic" }>;
export type GovernedSubmitKind = "preview" | "direct" | "confirm";

function confirmedFor(kind: GovernedSubmitKind): boolean {
  return kind === "confirm";
}

export function decideActIdentity(act: ClientDecideAct): string {
  if (act.mode === "bespoke") {
    return JSON.stringify([act.mode, act.area, act.itemId, act.expectedRevision, act.options.map((option) => option.value)]);
  }
  if (act.mode === "generic") {
    return JSON.stringify([
      act.mode,
      act.area,
      act.itemId,
      act.actionId,
      act.decisionOptions?.map((option) => option.value) ?? [],
      act.expectedRevision,
      act.argv,
      act.requiresConfirmation,
      act.effectKind,
    ]);
  }
  return JSON.stringify([act.mode, act.area, act.itemId, act.option, act.argv, act.requiresConfirmation]);
}

export function headlineActIdentity(act: HeadlineActDescriptor): string {
  return JSON.stringify([
    act.pathway,
    act.area,
    act.itemId,
    act.option ?? "",
    act.actionId ?? "",
    act.expectedRevision ?? "",
    act.argv,
    act.requiresConfirmation,
    act.rationaleRequired,
  ]);
}

export function buildDecideActionRequest(
  act: ClientDecideAct,
  selectedOption: string,
  fields: Record<string, string>,
  kind: GovernedSubmitKind,
): Record<string, unknown> {
  const confirmed = confirmedFor(kind);
  if (act.mode === "generic") {
    const selectedAction = act.decisionOptions?.some((option) => option.value === selectedOption)
      ? selectedOption
      : act.actionId;
    const request: Record<string, unknown> = { area: act.area, itemId: act.itemId, actionId: selectedAction, expectedRevision: act.expectedRevision, confirmed };
    // The fixed review-resolution adapter is the only generic consumer of a
    // reason; it validates presence server-side, so send it only when asked.
    if (act.reasonRequired) request.reason = String(fields.reason ?? "");
    return request;
  }
  return {
    area: act.area,
    itemId: act.itemId,
    option: act.mode === "bespoke" ? selectedOption : act.option,
    fields,
    expectedRevision: act.mode === "bespoke" ? act.expectedRevision : "",
    preview: kind === "preview",
    confirmed,
  };
}

export function buildHeadlineActionRequest(
  act: HeadlineActDescriptor,
  rationale: string,
  kind: Exclude<GovernedSubmitKind, "preview">,
): Record<string, unknown> {
  const confirmed = confirmedFor(kind);
  if (act.pathway === "bespoke") {
    return { area: act.area, itemId: act.itemId, option: act.option, fields: act.fields ?? {}, confirmed };
  }
  return { area: act.area, itemId: act.itemId, actionId: act.actionId, expectedRevision: act.expectedRevision ?? "", confirmed, rationale };
}

import { agentDisplayName } from "@/lib/harness-label";
import { plainState, plainWorkStep } from "@/lib/plain-words";

/**
 * Where a work item stands and what happens next, from its own record: the
 * item page and the preview drawer both show this. The recorded reason leads;
 * a line is added only when it is true for this item, never a generic one
 * that contradicts the record. Cached board text cannot override it.
 */
export type WorkNextStep = {
  /** the state in the board's words (lib/plain-words.ts) */
  title: string;
  /** the recorded reason or step, in plain words; says so when none was recorded */
  reason: string;
  /** what happens next and who handles it; "" when the record says nothing more */
  next: string;
  /** who holds it, by the agent's one name ("No agent" when none) */
  owner: string;
  /** true when `reason` is what the record says, false when nothing was recorded */
  recorded: boolean;
};

export function workNextStep(work: Record<string, unknown>): WorkNextStep {
  const text = (value: unknown) => typeof value === "string" ? value.trim() : "";
  const state = text(work.state);
  const lifecycle = Array.isArray(work.lifecycle) ? work.lifecycle : [];
  const latestState = [...lifecycle].reverse().find((step) =>
    step && typeof step === "object" && step.to === state && text(step.note) && !/^action recorded:|^created$|^claimed(?: |$)/i.test(text(step.note)),
  );
  const recordedReason = plainWorkStep(text(work.blocking_reason) || text(work.next_action) ||
    text(latestState?.note).replace(/\s*\(policy [^)]*\)\s*$/, ""));
  const holder = text(work.claimed_by) || text(work.owner);
  const owner = agentDisplayName(holder);
  const title = plainState(state || "unknown");
  const step = (reason: string, missing: string, next: string): WorkNextStep => ({
    title, reason: reason || missing, next, owner, recorded: !!reason,
  });

  if (state === "held" || state === "blocked") {
    return step(recordedReason, "No reason was recorded for the hold.", "Nothing moves until the hold is lifted.");
  }
  if (state === "verifying") {
    const check = text(work.success_predicate);
    return step(check ? `The check must show: ${check}` : "", "What the check must show was not recorded.", "It closes when its proof is accepted.");
  }
  if (["completed", "failed", "cancelled"].includes(state)) {
    return step(recordedReason, "No closing note was recorded.", "");
  }
  if (state === "running" || state === "in_progress") {
    return step(recordedReason, "No progress note was recorded since it started.", "When the run ends, it waits for a proof check.");
  }
  if (state === "claimed") {
    return step(recordedReason, "No first step was recorded.", holder ? `${owner} picked it up and starts it next.` : "");
  }
  if (["proposed", "suggested", "candidate", "admitted"].includes(state)) {
    return step(recordedReason, "No plan was recorded yet.", "It needs someone to accept it or cancel it.");
  }
  if (state === "ready") {
    return step(recordedReason, "No first step was recorded.", "An agent picks it up next; none has yet.");
  }
  return step(recordedReason, "Nothing was recorded about where it stands.", "");
}

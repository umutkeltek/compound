/** A work record explains its own wait. Cached board text cannot override it. */
export function workNextStep(work: Record<string, unknown>) {
  const text = (value: unknown) => typeof value === "string" ? value.trim() : "";
  const state = text(work.state);
  const lifecycle = Array.isArray(work.lifecycle) ? work.lifecycle : [];
  const latestState = [...lifecycle].reverse().find((step) =>
    step && typeof step === "object" && step.to === state && text(step.note) && !/^action recorded:|^created$|^claimed(?: |$)/i.test(text(step.note)),
  );
  const recordedReason = text(work.blocking_reason) || text(work.next_action) ||
    text(latestState?.note).replace(/\s*\(policy [^)]*\)\s*$/, "");
  const owner = text(work.owner) || "No agent assigned";
  if (state === "held" || state === "blocked") return {
    title: "Resolve the blocker",
    reason: recordedReason || "The work is on hold, but no reason was recorded.",
    next: "The assigned agent needs to check this dependency before resuming. No automatic retry is confirmed here.",
    owner,
  };
  if (state === "verifying") return {
    title: "Check the result",
    reason: text(work.success_predicate) || "The result still needs verification.",
    next: "The assigned agent needs to verify the result and record the evidence before closing this task.",
    owner,
  };
  if (["completed", "failed", "cancelled"].includes(state)) return {
    title: state === "completed" ? "Review the result" : "Review how this ended",
    reason: recordedReason || "This task is closed.",
    next: "Open the activity and evidence to see the recorded outcome.",
    owner,
  };
  if (state === "running") return {
    title: "Follow progress",
    reason: "This task is marked as running. Open its activity to check the latest recorded progress.",
    next: "The next step is verification when the agent finishes its run.",
    owner,
  };
  return {
    title: state === "claimed" ? "Start the assigned work" : "Prepare the work",
    reason: text(work.next_action) || "This task has not started running yet.",
    next: "An agent needs to check the requirements and current permissions before starting.",
    owner,
  };
}

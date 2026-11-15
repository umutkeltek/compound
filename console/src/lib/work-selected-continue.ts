import { arr, type Json, str } from "@/lib/compound";

export type SelectedContinueView = {
  state: string;
  status: string;
  detail: string;
  actor: string;
  goal: string;
  successPredicate: string;
  contextPackId: string;
  selection: string;
  runAttemptIds: string[];
  canStart: boolean;
  canResume: boolean;
};

export function selectedWorkLease(leases: Json | null, workId: string): Json | null {
  return (arr(leases?.leases).find((entry) => str(entry.work_id) === workId) ?? null) as Json | null;
}

export function selectedContinueView(work: Json, lease: Json | null): SelectedContinueView {
  const state = str(work.state);
  const actor = str(lease?.by);
  const selection = (work.execution_selection ?? {}) as Json;
  const delivery = (work.git_delivery ?? {}) as Json;
  const ids = (work.ids ?? {}) as Json;
  const runAttemptIds = arr(work.run_attempt_ids).map(String);
  const hasSelection = Boolean(str(selection.launch_profile_id) && str(selection.harness));
  const hasWorkspace = Boolean(str(delivery.target_worktree_root));
  const ready = Boolean(actor && hasSelection && hasWorkspace);
  const canStart = state === "claimed" && ready;
  const canResume = state === "verifying" && ready;
  let status = "Waiting";
  let detail = "This task has not started a new run. Check its next step for what is needed.";
  if (state === "claimed") {
    status = ready ? "Ready to run" : "Needs setup";
    detail = ready
      ? "An agent, model and workspace are selected. An authorized agent can check the task and start a run."
      : "An agent, model or workspace still needs to be assigned before work can start.";
  } else if (state === "running") {
    status = "Running";
    detail = "The task is marked as running. Check its activity for the latest progress; completion still needs verification.";
  } else if (state === "verifying") {
    status = "Result needs checking";
    detail = "The run has finished. The agent needs to check its result before this task can be completed. Starting another run is never automatic.";
  } else if (state === "completed") {
    status = "Completed";
    detail = "Closed as completed. Inspect its receipt and proof trail for the recorded evidence.";
  } else if (state === "failed") {
    status = "Failed";
    detail = "The task ended without a verified result. Review the activity to understand what failed.";
  }
  const selectedRoute = [str(selection.launch_profile_id), str(selection.harness), str(selection.requested_model_id)]
    .filter(Boolean).join(" · ");
  return {
    state, status, detail, actor,
    goal: str(work.desired_outcome),
    successPredicate: str(work.success_predicate),
    contextPackId: str(ids.context_pack_id),
    selection: selectedRoute,
    runAttemptIds, canStart, canResume,
  };
}

export function selectedContinueRevision(work: Json, lease: Json | null): string {
  const selection = (work.execution_selection ?? {}) as Json;
  return JSON.stringify([
    str(work.state), str(work.policy_decision_id), str(lease?.by), str(lease?.work_sha256),
    selection.revision ?? null, arr(work.run_attempt_ids).map(String),
  ]);
}

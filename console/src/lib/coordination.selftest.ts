/*
 * Depth tests for the coordination data plane's pure builder (the Me / Others
 * / Waiting-on view's read model over `compound coordination --json`). Would
 * FAIL if the builder invented a fleet when the door is unreachable, dropped a
 * wait edge, let a refused lane read as can-continue-ready data, hardcoded an
 * actor, or re-targeted an explicit me. Run via scripts/coordination-smoke.mjs.
 */

import { buildCoordinationView, buildWait, laneGated, type CoordinationActor } from "./coordination";

type Check = { name: string; ok: boolean; detail: string };

function finalize(checks: Check[]): { ok: boolean; details: string[] } {
  return {
    ok: checks.every((c) => c.ok),
    details: checks.map((c) => `${c.ok ? "ok" : "FAIL"}: ${c.name} — ${c.detail}`),
  };
}

function mkActor(over: Record<string, unknown> = {}): Record<string, unknown> {
  return {
    actor: "lane-x",
    doing: { work_id: "work_a", title: "ship the parser", state: "running", next_action: "run it", claimed_by: "lane-x", owner: "lane-x" },
    next_action: "",
    can_continue: false,
    wait_reason: "run_evidence",
    blocked_on: ["work.run_evidence:work_a"],
    open: 2,
    in_progress: 2,
    needs_review: 0,
    ...over,
  };
}

function mkReport(over: Record<string, unknown> = {}): Record<string, any> {
  return {
    schema_version: "compound.coordination.v0.1",
    generated_at: "2026-07-21T08:00:00Z",
    actor: "lane-x",
    actor_explicit: true,
    me: mkActor(),
    others: [mkActor({ actor: "lane-y", doing: { work_id: "work_b", title: "write the docs", state: "claimed", next_action: "", claimed_by: "lane-y", owner: "lane-y" }, can_continue: true, wait_reason: "", blocked_on: [] })],
    waits: [
      { work: "work_a", actor: "lane-x", reason: "run_evidence", blocked_on: ["work.run_evidence:work_a"] },
      { work: "work_b", actor: "lane-x", reason: "claimed_by_other_actor", blocked_on: ["work.claimed_by_other_actor:work_b"] },
    ],
    integrity: [],
    ...over,
  };
}

export function runCoordinationSelfTest(): { ok: boolean; details: string[] } {
  const checks: Check[] = [];

  // F1: door unreachable => present=false, NOTHING invented (the honesty law).
  {
    const view = buildCoordinationView(null);
    checks.push({
      name: "unreachable door invents nothing",
      ok: view.present === false && view.others.length === 0 && view.waits.length === 0 && view.me.actor === "" && view.actorOptions.length === 0,
      detail: `present=${view.present} others=${view.others.length} waits=${view.waits.length} me.actor=${JSON.stringify(view.me.actor)}`,
    });
    const wrongSchema = buildCoordinationView({ schema_version: "something.else.v9" } as any);
    checks.push({
      name: "foreign schema is not the coordination door",
      ok: wrongSchema.present === false,
      detail: `present=${wrongSchema.present}`,
    });
  }

  // F2: actors flow through generically — lane-x/lane-y, no hardcoded names.
  {
    const view = buildCoordinationView(mkReport() as any);
    const names = [view.me.actor, ...view.others.map((o) => o.actor)];
    checks.push({
      name: "generic actors pass through unmodified",
      ok: view.present && names.includes("lane-x") && names.includes("lane-y"),
      detail: `actors=${names.join(",")}`,
    });
    checks.push({
      name: "actorOptions offers every actor, me first",
      ok: view.actorOptions[0] === "lane-x" && view.actorOptions.includes("lane-y"),
      detail: `options=${view.actorOptions.join(",")}`,
    });
  }

  // F3: wait edges survive the mapping — work, actor, reason, blocked_on.
  {
    const view = buildCoordinationView(mkReport() as any);
    const runEvidence = view.waits.find((w) => w.work === "work_a");
    const claimed = view.waits.find((w) => w.reason === "claimed_by_other_actor");
    checks.push({
      name: "work-scoped wait edge maps with blocked_on",
      ok: !!runEvidence && runEvidence.actor === "lane-x" && runEvidence.reason === "run_evidence" && runEvidence.blockedOn.includes("work.run_evidence:work_a"),
      detail: JSON.stringify(runEvidence),
    });
    checks.push({
      name: "handoff edge (claimed by other actor) maps",
      ok: !!claimed && claimed.work === "work_b" && claimed.blockedOn.includes("work.claimed_by_other_actor:work_b"),
      detail: JSON.stringify(claimed),
    });
    checks.push({
      name: "me doing + wait truth maps",
      ok: view.me.doing?.workId === "work_a" && view.me.canContinue === false && view.me.waitReason === "run_evidence" && view.me.blockedOn.includes("work.run_evidence:work_a"),
      detail: JSON.stringify({ doing: view.me.doing, wait: view.me.waitReason }),
    });
  }

  // F4: a refused / confirmation-held lane is legible as a gate.
  {
    const refused = buildCoordinationView(
      mkReport({
        me: mkActor({ can_continue: false, wait_reason: "capacity_refused", blocked_on: ["lane.capacity_refused:lane-x"], lane: { state: "walled", remaining: 0, refused: true, message: "budget exhausted" } }),
        waits: [{ actor: "lane-x", reason: "capacity_refused", blocked_on: ["lane.capacity_refused:lane-x"], lane: { state: "walled", remaining: 0, refused: true } }],
      }) as any,
    );
    checks.push({
      name: "refused lane maps to a gated lane",
      ok: refused.me.lane !== null && refused.me.lane.refused === true && laneGated(refused.me.lane) === true && refused.me.canContinue === false,
      detail: JSON.stringify(refused.me.lane),
    });
    const held = buildWait({ actor: "lane-y", reason: "capacity_confirmation_required", lane: { state: "open", remaining: 1, confirmation_required: true } });
    checks.push({
      name: "confirmation hold reads as gated",
      ok: held.lane !== null && held.lane.confirmationRequired === true && laneGated(held.lane) === true,
      detail: JSON.stringify(held.lane),
    });
    const open = buildWait({ actor: "lane-z", reason: "run_evidence", lane: { state: "open", remaining: 5 } });
    checks.push({
      name: "open lane is not a gate",
      ok: open.lane !== null && laneGated(open.lane) === false,
      detail: JSON.stringify(open.lane),
    });
  }

  // F5: defensive mapping — ragged rows never throw, never fabricate fields.
  {
    let threw = false;
    let view: ReturnType<typeof buildCoordinationView> | null = null;
    try {
      view = buildCoordinationView({
        schema_version: "compound.coordination.v0.1",
        actor: "lane-q",
        me: { actor: "lane-q" },
        others: [null, 42, { actor: "lane-r", blocked_on: "not-an-array" }],
        waits: [{ reason: "run_evidence" }],
      } as any);
    } catch {
      threw = true;
    }
    const other = (view?.others ?? [])[0] as CoordinationActor | undefined;
    checks.push({
      name: "ragged payload maps defensively",
      ok: !threw && view !== null && view.present === true && view.others.length === 3 && (other?.blockedOn.length ?? 1) === 0 && view.waits[0]?.actor === "",
      detail: `threw=${threw} others=${view?.others.length} waits=${JSON.stringify(view?.waits)}`,
    });
  }

  return finalize(checks);
}

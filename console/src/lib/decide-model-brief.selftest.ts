/*
 * Falsifiers for the decision-brief sidecar's console-side seam-audit fact:
 * `group.sample` is only the FIRST member internal/reviewartifact/queue.go's
 * buildQueueGroups saw while building the group — a brief prepared for any
 * OTHER member must still surface on the card. These checks operate purely on
 * the DecideGroup data model (no React renderer is wired into this repo) —
 * DecideQueue.tsx's brief block is gated on `group.brief` alone, so proving
 * the model's `brief` field correct/absent here is equivalent to proving the
 * rendered card is correct/frozen. decide-model resolves through `@/lib` path
 * aliases, so this runs under bun via scripts/decide-model-brief-smoke.mjs
 * (same reason as generic-actions.selftest.ts).
 */

import { buildDecideGroups } from "./decide-model";
import type { Json } from "./compound";

type Check = { name: string; ok: boolean; detail: string };

function baseGroup(overrides: Partial<Json> = {}): Json {
  return {
    key: "capability-lifecycle-proposals|fixture",
    area: "capability-lifecycle-proposals",
    count: 2,
    status: "review_required",
    band: "needs_you",
    operator_summary: "Two members need a call.",
    context_label: "capability-lifecycle-proposals",
    item_ids: ["member_a", "member_b"],
    evidence_refs: ["compound://evidence/a", "compound://evidence/b"],
    sample: { id: "member_a", area: "capability-lifecycle-proposals", created_at: "2026-07-10T00:00:00Z" },
    ...overrides,
  };
}

function validBriefPayload(preparedBy = "switchback_api"): Json {
  return {
    schema_version: "compound.decision_brief.v0.1",
    headline: "Choose the bounded next move",
    what_this_is: "A prepared translation of the underlying review artifact.",
    findings: [{ finding: "No wired action exists yet.", evidence_ref: "compound://evidence/finding-1" }],
    options: [
      {
        value: "approve",
        label: "Approve the direction",
        consequence_approve: "A separate governed action may move the work forward.",
        consequence_hold: "The review remains open without mutation.",
        command_argv: null,
      },
    ],
    recommendation: { option: "approve", confidence: 0.8, rationale: "Evidence supports it." },
    prepared_by: preparedBy,
    prepared_at: "2026-07-10T04:00:00Z",
    propose_only: true,
  };
}

function build(group: Json, itemByID: Map<string, Json>) {
  const [decided] = buildDecideGroups([group], itemByID, null, () => itemByID.get("member_a")!);
  return decided;
}

export function runDecideModelBriefSelfTest(): { ok: boolean; details: string[] } {
  const checks: Check[] = [];
  const push = (name: string, ok: boolean, detail: string) => checks.push({ name, ok, detail });

  // 1. The seam-audit falsifier: group.sample points at member_a (no brief);
  //    the brief lives on member_b, the SECOND member — exactly the shape the
  //    live proof target (a multi-member capability-lifecycle-proposals
  //    group) hits when the briefed item isn't group-order-first.
  {
    const itemByID = new Map<string, Json>([
      ["member_a", { id: "member_a", area: "capability-lifecycle-proposals" }],
      ["member_b", { id: "member_b", area: "capability-lifecycle-proposals", brief: validBriefPayload() }],
    ]);
    const decided = build(baseGroup(), itemByID);
    push(
      "scans-every-group-member",
      decided.brief != null &&
        decided.brief.headline === "Choose the bounded next move" &&
        decided.brief.preparedBy === "switchback_api" &&
        decided.brief.findings.length === 1 &&
        decided.brief.findings[0].evidenceRef === "compound://evidence/finding-1" &&
        decided.brief.options[0].value === "approve" &&
        decided.brief.recommendation.option === "approve",
      `brief: ${decided.brief ? "present" : "MISSING"} preparedBy=${decided.brief?.preparedBy ?? "-"}`,
    );
  }

  // 2. A briefed group.sample (first member) still surfaces the brief.
  {
    const itemByID = new Map<string, Json>([
      ["member_a", { id: "member_a", area: "capability-lifecycle-proposals", brief: validBriefPayload("codex") }],
      ["member_b", { id: "member_b", area: "capability-lifecycle-proposals" }],
    ]);
    const group = baseGroup({
      sample: { id: "member_a", area: "capability-lifecycle-proposals", brief: validBriefPayload("codex") },
    });
    const decided = build(group, itemByID);
    push(
      "briefed-sample-surfaces",
      decided.brief?.preparedBy === "codex",
      `preparedBy=${decided.brief?.preparedBy ?? "MISSING"}`,
    );
  }

  // 3. No member briefed: brief absent and every other card field unchanged
  //    (frozen-markup proxy — DecideQueue's brief block gates on group.brief
  //    alone, so an absent brief means the card renders exactly as before
  //    this packet).
  {
    const itemByID = new Map<string, Json>([
      ["member_a", { id: "member_a", area: "capability-lifecycle-proposals" }],
      ["member_b", { id: "member_b", area: "capability-lifecycle-proposals" }],
    ]);
    const decided = build(baseGroup(), itemByID);
    push(
      "unbriefed-card-frozen",
      (decided.brief ?? null) === null &&
        decided.title === "Two members need a call." &&
        decided.members.map((m) => m.id).join(",") === "member_a,member_b" &&
        decided.evidence.length > 0 &&
        decided.act.mode === "copy",
      `brief=${decided.brief ? "PRESENT" : "absent"} title=${decided.title} act=${decided.act.mode}`,
    );
  }

  // 4. An invalid brief shape (missing evidence_ref) never renders client-side.
  {
    const invalid = validBriefPayload();
    invalid.findings = [{ finding: "no evidence", evidence_ref: "" }];
    const itemByID = new Map<string, Json>([
      ["member_a", { id: "member_a", area: "capability-lifecycle-proposals", brief: invalid }],
    ]);
    const group = baseGroup({
      item_ids: ["member_a"],
      sample: { id: "member_a", area: "capability-lifecycle-proposals" },
    });
    const decided = build(group, itemByID);
    push(
      "invalid-brief-missing-evidence-rejected",
      (decided.brief ?? null) === null,
      `brief=${decided.brief ? "PRESENT (BUG)" : "absent"}`,
    );
  }

  // 5. A brief missing prepared_by never renders client-side either
  //    (provenance is not optional).
  {
    const invalid = validBriefPayload();
    invalid.prepared_by = "";
    const itemByID = new Map<string, Json>([
      ["member_a", { id: "member_a", area: "capability-lifecycle-proposals", brief: invalid }],
    ]);
    const group = baseGroup({
      item_ids: ["member_a"],
      sample: { id: "member_a", area: "capability-lifecycle-proposals" },
    });
    const decided = build(group, itemByID);
    push(
      "brief-without-provenance-rejected",
      (decided.brief ?? null) === null,
      `brief=${decided.brief ? "PRESENT (BUG)" : "absent"}`,
    );
  }

  // 6. command_argv passes through as copy/display data only — the group's
  //    OWN act stays whatever the real projection supports (here: honest
  //    copy, no registry spec exists) and the brief's argv is never consulted
  //    by resolveAct.
  {
    const brief = validBriefPayload();
    brief.options = [
      {
        value: "approve",
        label: "Approve",
        consequence_approve: "",
        consequence_hold: "",
        command_argv: ["compound", "capability", "lifecycle-promote", "--id", "clp_1"],
      },
    ];
    const itemByID = new Map<string, Json>([
      ["member_a", { id: "member_a", area: "capability-lifecycle-proposals", brief } as Json],
    ]);
    const group = baseGroup({
      item_ids: ["member_a"],
      sample: { id: "member_a", area: "capability-lifecycle-proposals" },
    });
    const decided = build(group, itemByID);
    push(
      "brief-argv-copy-only",
      decided.brief?.options[0].commandArgv?.join(" ") ===
        "compound capability lifecycle-promote --id clp_1" && decided.act.mode === "copy",
      `argv=${decided.brief?.options[0].commandArgv?.join(" ") ?? "MISSING"} act=${decided.act.mode}`,
    );
  }

  const failed = checks.filter((c) => !c.ok);
  return {
    ok: failed.length === 0,
    details: checks.map((c) => `${c.ok ? "ok" : "FAIL"} ${c.name} — ${c.detail}`),
  };
}

import { describe, expect, test } from "bun:test";

import { buildDecideGroups } from "./decide-model";
import type { Json } from "./compound";

/**
 * Falsifiers for the decision-brief sidecar's console-side seam-audit fact:
 * `group.sample` is only the FIRST member internal/reviewartifact/queue.go's
 * buildQueueGroups saw while building the group — a brief prepared for any
 * OTHER member must still surface on the card. These tests operate purely on
 * the DecideGroup data model (no React renderer is wired into this repo, and
 * this packet's boundaries forbid installing one) — DecideQueue.tsx's brief
 * block is gated on `group.brief` alone (see the component), so proving the
 * model's `brief` field is correct/absent here is equivalent to proving the
 * rendered card is correct/frozen.
 */

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

describe("buildDecideGroups: decision-brief translation", () => {
  test("scans every group member, not just group.sample (the seam-audit falsifier)", () => {
    // group.sample points at member_a (no brief); the brief lives on
    // member_b, the SECOND member — exactly the shape the live proof target
    // clp_326cfa93213e (4-member capability-lifecycle-proposals group) can
    // hit when the briefed item isn't group-order-first.
    const itemByID = new Map<string, Json>([
      ["member_a", { id: "member_a", area: "capability-lifecycle-proposals" }],
      ["member_b", { id: "member_b", area: "capability-lifecycle-proposals", brief: validBriefPayload() }],
    ]);
    const group = baseGroup();
    const [decided] = buildDecideGroups([group], itemByID, null, () => itemByID.get("member_a")!);

    expect(decided.brief).not.toBeNull();
    expect(decided.brief).not.toBeUndefined();
    expect(decided.brief?.headline).toBe("Choose the bounded next move");
    expect(decided.brief?.preparedBy).toBe("switchback_api");
    expect(decided.brief?.findings).toHaveLength(1);
    expect(decided.brief?.findings[0].evidenceRef).toBe("compound://evidence/finding-1");
    expect(decided.brief?.options[0].value).toBe("approve");
    expect(decided.brief?.recommendation.option).toBe("approve");
  });

  test("a briefed group.sample (first member) still surfaces the brief", () => {
    const itemByID = new Map<string, Json>([
      ["member_a", { id: "member_a", area: "capability-lifecycle-proposals", brief: validBriefPayload("codex") }],
      ["member_b", { id: "member_b", area: "capability-lifecycle-proposals" }],
    ]);
    const group = baseGroup({ sample: { id: "member_a", area: "capability-lifecycle-proposals", brief: validBriefPayload("codex") } });
    const [decided] = buildDecideGroups([group], itemByID, null, () => itemByID.get("member_a")!);
    expect(decided.brief?.preparedBy).toBe("codex");
  });

  test("no member briefed: brief is absent and every other card field is unchanged (frozen-markup proxy)", () => {
    const itemByID = new Map<string, Json>([
      ["member_a", { id: "member_a", area: "capability-lifecycle-proposals" }],
      ["member_b", { id: "member_b", area: "capability-lifecycle-proposals" }],
    ]);
    const group = baseGroup();
    const [decided] = buildDecideGroups([group], itemByID, null, () => itemByID.get("member_a")!);

    expect(decided.brief ?? null).toBeNull();
    // Frozen-markup proxy: DecideQueue.tsx's brief block is gated solely on
    // `group.brief` (see BriefPanel usage) — every OTHER field the card
    // renders from is exactly what it was before this packet.
    expect(decided.title).toBe("Two members need a call.");
    expect(decided.members.map((m) => m.id)).toEqual(["member_a", "member_b"]);
    expect(decided.evidence.length).toBeGreaterThan(0);
    expect(decided.act.mode).toBe("copy");
  });

  test("an invalid brief shape (missing evidence_ref) never renders client-side either", () => {
    const invalid = validBriefPayload();
    invalid.findings = [{ finding: "no evidence", evidence_ref: "" }];
    const itemByID = new Map<string, Json>([
      ["member_a", { id: "member_a", area: "capability-lifecycle-proposals", brief: invalid }],
    ]);
    const group = baseGroup({ item_ids: ["member_a"], sample: { id: "member_a", area: "capability-lifecycle-proposals" } });
    const [decided] = buildDecideGroups([group], itemByID, null, () => itemByID.get("member_a")!);
    expect(decided.brief ?? null).toBeNull();
  });

  test("a brief missing prepared_by never renders client-side either", () => {
    const invalid = validBriefPayload();
    invalid.prepared_by = "";
    const itemByID = new Map<string, Json>([
      ["member_a", { id: "member_a", area: "capability-lifecycle-proposals", brief: invalid }],
    ]);
    const group = baseGroup({ item_ids: ["member_a"], sample: { id: "member_a", area: "capability-lifecycle-proposals" } });
    const [decided] = buildDecideGroups([group], itemByID, null, () => itemByID.get("member_a")!);
    expect(decided.brief ?? null).toBeNull();
  });

  test("command_argv passes through as copy/display data only (never becomes an executable act)", () => {
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
    const group = baseGroup({ item_ids: ["member_a"], sample: { id: "member_a", area: "capability-lifecycle-proposals" } });
    const [decided] = buildDecideGroups([group], itemByID, null, () => itemByID.get("member_a")!);
    expect(decided.brief?.options[0].commandArgv).toEqual(["compound", "capability", "lifecycle-promote", "--id", "clp_1"]);
    // The group's OWN act (independent of the brief) is still whatever the
    // real projection supports — here, no registry spec/action exists, so it
    // stays an honest copy. The brief's argv is never consulted by resolveAct.
    expect(decided.act.mode).toBe("copy");
  });
});

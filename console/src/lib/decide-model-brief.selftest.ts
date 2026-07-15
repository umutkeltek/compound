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

import { remainingDecideMembers } from "./decide-members";
import { buildDecideGroups, genericReviewResolutionActs } from "./decide-model";
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
    const decisionCard = {
      claim: "Review member B.",
      choices: [{ id: "reject", label: "Reject proposal", effect: "File member B without work." }],
      meta: { proposal_hash: "b".repeat(64) },
    };
    const itemByID = new Map<string, Json>([
      ["member_a", { id: "member_a", area: "capability-lifecycle-proposals" }],
      ["member_b", { id: "member_b", area: "capability-lifecycle-proposals", brief: validBriefPayload(), decision_card: decisionCard }],
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
        decided.brief.recommendation.option === "approve" &&
        decided.act.mode === "bespoke" &&
        decided.act.itemId === "member_b",
      `brief: ${decided.brief ? "present" : "MISSING"} preparedBy=${decided.brief?.preparedBy ?? "-"} target=${decided.act.mode === "bespoke" ? decided.act.itemId : decided.act.mode}`,
    );
  }

  // 13. A panel apply SBA and per-member verdict-record actions are different
  //     effects. The clean member action must not replace the atomic apply.
  {
    const item = {
      id: "panel_apply_member01",
      area: "panel-verdicts",
      actions: [
        { id: "decision:promote", argv: ["/bin/sh", "-lc", "compound panel verdict-record && apply"] },
        {
          id: "panel_verdict_record_member",
          argv: ["compound", "panel", "verdict-record", "--review", "/tmp/panel_apply_member01.json"],
        },
      ],
    } as Json;
    const group = {
      key: "panel-verdicts|prun_fixture_apply|promote",
      area: item.area,
      count: 1,
      item_ids: [item.id],
      sample: item,
      safe_batch_action: {
        id: "panel_run_apply",
        argv: ["compound", "panel", "run", "--plan", "prun_fixture_apply", "--apply"],
        requires_review: true,
      },
    } as Json;
    const decided = buildDecideGroups([group], new Map([[item.id, item]]), null, () => item)[0];
    push(
      "panel-apply-is-not-replaced-by-member-record",
      decided.act.mode === "batch-atomic" && decided.act.area === "panel-apply" && decided.capability.kind === "apply",
      `act=${decided.act.mode}${decided.act.mode === "batch-atomic" ? ` area=${decided.act.area}` : ""} capability=${decided.capability.kind}`,
    );
  }

  // 14. A future clean panel review action must not become panel-apply merely
  //     because the group key happens to carry a recoverable panel run id.
  //     Only the owner's exact panel_run_apply action and argv shape may cross
  //     the apply boundary.
  {
    const item = {
      id: "future_panel_member01",
      area: "panel-verdicts",
    } as Json;
    const group = {
      key: "panel-verdicts|prun_fixture_future|needs_human",
      area: item.area,
      count: 1,
      item_ids: [item.id],
      sample: item,
      safe_batch_action: {
        id: "future_panel_record",
        argv: ["compound", "panel", "verdict-record", "--group", "panel-verdicts|prun_fixture_future|needs_human"],
        requires_review: true,
      },
    } as Json;
    const decided = buildDecideGroups([group], new Map([[item.id, item]]), null, () => item)[0];
    push(
      "panel-apply-requires-exact-owner-action",
      !(decided.act.mode === "batch-atomic" && decided.act.area === "panel-apply"),
      `act=${decided.act.mode}${decided.act.mode === "batch-atomic" ? ` area=${decided.act.area}` : ""}`,
    );
  }

  // 15. The grouped record id cannot relabel an apply argv. Atomic handlers
  //     bind both the owner action id and the exact represented command.
  {
    const item = { id: "mismatched_panel_member01", area: "panel-verdicts" } as Json;
    const group = {
      key: "panel-verdicts|prun_fixture_mismatch|needs_human",
      area: item.area,
      count: 1,
      item_ids: [item.id],
      sample: item,
      safe_batch_action: {
        id: "panel_verdict_record_group",
        argv: ["compound", "panel", "run", "--plan", "prun_fixture_mismatch", "--apply"],
        requires_review: true,
      },
    } as Json;
    const decided = buildDecideGroups([group], new Map([[item.id, item]]), null, () => item)[0];
    push(
      "panel-record-group-requires-exact-owner-argv",
      !(decided.act.mode === "batch-atomic" && decided.act.area === "panel-verdict-record-group"),
      `act=${decided.act.mode}${decided.act.mode === "batch-atomic" ? ` area=${decided.act.area}` : ""}`,
    );
  }

  // 12. Certification is area + verb, not area alone. A job-health action
  //     carrying another area's clean verb stays unavailable before any click.
  {
    const item = { id: "job_health_mismatch01", area: "job-health" } as Json;
    const group = {
      key: "job-health|mismatch",
      area: item.area,
      count: 1,
      item_ids: [item.id],
      sample: item,
      operator_summary: "Inspect a mismatched action.",
    } as Json;
    const attention = {
      groups: [{
        key: group.key,
        area: item.area,
        action: {
          id: "wrong_verb",
          kind: "one_click",
          argv: ["compound", "authority", "dirty-review", "/tmp/other.review.yaml"],
        },
      }],
    } as Parameters<typeof buildDecideGroups>[2];
    const decided = buildDecideGroups([group], new Map([[item.id, item]]), attention, () => item)[0];
    push(
      "certified-area-mismatched-verb-stays-unavailable",
      decided.act.mode === "copy" && decided.capability.kind === "unavailable",
      `act=${decided.act.mode} capability=${decided.capability.kind}`,
    );
  }

  // An Attention-owned external-repo group may contain several repositories,
  // while the legacy bespoke registry action resolves only one review YAML.
  // Keep the grouped card read-only so one click can never look like it ruled
  // on every member while mutating only the first sample.
  {
    const area = "external-repo-intake";
    const items = ["extrepo_alpha01", "extrepo_bravo02"].map((id) => ({
      id,
      area,
      context_label: id,
      actions: [{
        id: `review_${id}`,
        argv: ["compound", "capability", "external-intake-review", `/tmp/${id}.review.yaml`],
      }],
    })) as Json[];
    const group = {
      key: "external-repo-intake|needs-you",
      area,
      count: items.length,
      item_ids: items.map((item) => String(item.id)),
      operator_summary: "Two external repositories need separate intake decisions.",
      sample: items[0],
    } as Json;
    const attention = {
      groups: [{
        ...group,
        action: {
          id: "review_external_repo",
          kind: "one_click",
          argv: ["compound", "capability", "external-intake-review", "/tmp/extrepo_alpha01.review.yaml"],
        },
      }],
    } as Parameters<typeof buildDecideGroups>[2];
    const itemByID = new Map(items.map((item) => [String(item.id), item]));
    const decided = buildDecideGroups([group], itemByID, attention, () => items[0])[0];
    const resolutionActs = genericReviewResolutionActs(decided);
    push(
      "external-attention-group-stays-copy-only",
      decided.key === group.key &&
        decided.count === items.length &&
        decided.members.map((member) => member.id).join(",") === items.map((item) => item.id).join(",") &&
        decided.act.mode === "copy" &&
        decided.capability.kind === "unavailable" &&
        resolutionActs.length === 0,
      `key=${decided.key} count=${decided.count} members=${decided.members.map((member) => member.id).join(",")} act=${decided.act.mode} capability=${decided.capability.kind} resolutions=${resolutionActs.length}`,
    );
  }

  // Attention-only groups are real operator gates, but they are not review
  // items. A copy fallback must not invent reviews-resolve Park/Dismiss actions
  // against the synthetic group key.
  {
    const group = {
      key: "workbench:blocked-on-you",
      area: "workbench-blocked-on-you",
      count: 1,
      item_ids: ["work_blocked_fixture"],
      operator_summary: "One work item needs a ruling.",
    } as Json;
    const decided = buildDecideGroups([group], new Map(), null, () => ({}))[0];
    const resolutionActs = genericReviewResolutionActs(decided);
    push(
      "attention-only-copy-card-has-no-review-disposition",
      decided.act.mode === "copy" && resolutionActs.length === 0,
      `act=${decided.act.mode} sample=${decided.sampleId} resolutions=${resolutionActs.length}`,
    );
  }

  // Attention-only identities still carry their oldest observed timestamp.
  // Do not erase it merely because Reviews has no sample to enrich the group.
  {
    const group = {
      key: "workbench:blocked-on-you",
      area: "workbench-blocked-on-you",
      count: 1,
      item_ids: ["work_blocked_fixture"],
      oldest_created_at: "2024-01-02T03:04:05Z",
      operator_summary: "One work item needs a ruling.",
    } as Json;
    const decided = buildDecideGroups([group], new Map(), null, () => ({}))[0];
    push(
      "attention-oldest-created-at-survives-without-review-sample",
      decided.createdAt === group.oldest_created_at && decided.ageLabel !== "age unknown" && decided.old,
      `created_at=${decided.createdAt} age=${decided.ageLabel} old=${decided.old}`,
    );
  }

  // 11. After a partial enumerable batch, a retry excludes both deliberately
  //     skipped members and members already recorded before the failure.
  {
    const members = ["a", "b", "c"].map((id) => ({ id, label: id, argv: ["panel", "verdict-record"], actionId: `record_${id}` }));
    const remaining = remainingDecideMembers(members, new Set(["c"]), new Set(["a"]));
    push(
      "partial-enumerable-retry-skips-completed-members",
      remaining.map((member) => member.id).join(",") === "b",
      `remaining=${remaining.map((member) => member.id).join(",")}`,
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

  // 7. Owner-projected lifecycle choices become an explicit, unselected
  //    disposition target. The rendered proposal hash is carried separately
  //    for optimistic concurrency; brief options never feed this list.
  {
    const card = {
      claim: "Teach the existing skill one verified failure pattern.",
      why: [{ label: "evidence", detail: "Two source refs support the proposal." }],
      choices: [
        { id: "open_work", label: "Turn into tracked work", effect: "Create proposed work only." },
        { id: "defer", label: "Defer with a trigger", effect: "Watch until the trigger." },
        { id: "reject", label: "Reject proposal", effect: "File it without work." },
      ],
      meta: { proposal_hash: "a".repeat(64) },
      boundary: ["no capability mutation"],
    };
    const item = {
      id: "clp_fixture",
      area: "capability-lifecycle-proposals",
      decision_card: card,
      evidence_refs: ["session:fixture"],
    } as Json;
    const group = baseGroup({
      key: "capability-lifecycle-proposals|clp_fixture",
      count: 1,
      item_ids: ["clp_fixture"],
      sample: item,
      evidence_summaries: [{
        ref: "session:fixture",
        label: "Source session",
        source_kind: "session",
        summary: "The same failure repeated twice.",
      }],
    });
    const decided = buildDecideGroups([group], new Map([["clp_fixture", item]]), null, () => item)[0];
    const act = decided.act;
    const actionableHere = [decided].filter((candidate) => candidate.capability.kind !== "unavailable").length;
    push(
      "owner-choices-explicit-and-version-bound",
      act.mode === "bespoke" &&
        act.options.map((option) => option.value).join(",") === "open_work,defer,reject" &&
        act.option === "" &&
        act.expectedRevision === "a".repeat(64) &&
        act.supportsPreview &&
        decided.capability.kind === "track" &&
        actionableHere === 1 &&
        decided.evidence[0]?.summary === "The same failure repeated twice.",
      `act=${act.mode} option=${act.mode === "bespoke" ? act.option : "-"} capability=${decided.capability.kind} actionable=${actionableHere}`,
    );
  }

  // 8. A bespoke owner with zero effect-compatible choices stays inspection
  //    only even when the projection carries a different record action.
  {
    const item = {
      id: "route_promotion_fixture01",
      area: "route-promotions",
      group_key: "route-promotions|demote|classify",
      json_path: "/tmp/route_promotion_fixture01.json",
      actions: [{
        id: "route_promotion_record",
        argv: ["compound", "route", "promotion-record", "--review", "/tmp/route_promotion_fixture01.json"],
        requires_review: true,
      }],
    } as Json;
    const group = {
      key: item.group_key,
      area: item.area,
      count: 1,
      item_ids: [item.id],
      sample: item,
      safe_batch_action: item.actions[0],
      operator_summary: "Demote this lane?",
    } as Json;
    const decided = buildDecideGroups([group], new Map([[item.id, item]]), null, () => item)[0];
    push(
      "zero-option-bespoke-owner-stays-unavailable",
      decided.act.mode === "copy" &&
        decided.capability.kind === "unavailable" &&
        decided.act.command === "compound reviews --area route-promotions" &&
        decided.consequences?.approve.includes("this review card cannot promote or demote") === true,
      `act=${decided.act.mode} capability=${decided.capability.kind}`,
    );
  }

  // 9. Truly generic enumerable actions carry confirmation and rationale
  //    requirements into the client instead of self-confirming on first click.
  {
    const item = {
      id: "panel_verdict_fixture01",
      area: "panel-verdicts",
      context_label: "fixture verdict",
      actions: [
        ...["promote", "park", "reject", "needs_human"].map((choice) => ({
          id: `decision:${choice}`,
          argv: ["/bin/sh", "-lc", `compound panel verdict-record --review fixture && ${choice}`],
          requires_review: true,
        })),
        {
          id: "panel_verdict_record_member",
          argv: ["compound", "panel", "verdict-record", "--review", "/tmp/panel_verdict_fixture01.json"],
          requires_review: true,
        },
      ],
    } as Json;
    const group = {
      key: "panel-verdicts|fixture",
      area: item.area,
      count: 1,
      item_ids: [item.id],
      sample: item,
      safe_batch_action: {
        id: "panel_verdict_record_group",
        label: "Record selected verdicts",
        argv: ["compound", "panel", "verdict-record", "--review", "/tmp/panel_verdict_fixture01.json"],
        requires_review: true,
        policy_note: "Records reviewed provenance only.",
      },
      operator_summary: "Record the reviewed verdict.",
    } as Json;
    const decided = buildDecideGroups([group], new Map([[item.id, item]]), null, () => item)[0];
    const act = decided.act;
    push(
      "generic-enumerable-carries-confirmation-contract",
      act.mode === "batch-enumerable" && act.requiresConfirmation && act.requiresRationale &&
        act.effectKind === "record" && decided.capability.kind === "record",
      `act=${act.mode}${act.mode === "batch-enumerable" ? ` confirm=${act.requiresConfirmation} rationale=${act.requiresRationale} effect=${act.effectKind}` : ""}`,
    );
  }

  // 10. A clean projection action in an uncertified area remains inspection
  //     only. The UI must not promise "Apply now" before the server 403s it.
  {
    const item = {
      id: "uncertified_fixture01",
      area: "uncertified-area",
      actions: [{ id: "looks_clean", argv: ["compound", "work", "close", "work_fixture"] }],
    } as Json;
    const group = {
      key: "uncertified-area|fixture",
      area: item.area,
      count: 1,
      item_ids: [item.id],
      sample: item,
      safe_batch_action: item.actions[0],
      operator_summary: "Inspect an uncertified action.",
    } as Json;
    const decided = buildDecideGroups([group], new Map([[item.id, item]]), null, () => item)[0];
    push(
      "uncertified-clean-action-stays-unavailable",
      decided.act.mode === "copy" && decided.capability.kind === "unavailable",
      `act=${decided.act.mode} capability=${decided.capability.kind}`,
    );
  }

  // Generic review resolution derives at render time. A zero-wired
  // unknown-status card keeps its serialized model untouched while one
  // governed control shares a reason across Park and Dismiss.
  {
    const item: Json = { id: "parity_unknown_fixture", area: "parity", context_label: "Parity unknown" };
    const group: Json = {
      key: "parity|unknown",
      area: "parity",
      count: 1,
      status: "unknown",
      operator_summary: "Review parity unknown.",
      context_label: "parity",
      item_ids: [item.id],
      evidence_refs: ["evidence:parity-unknown"],
      sample: item,
    };
    const [decided] = buildDecideGroups([group], new Map([[String(item.id), item]]), null, () => item);
    const before = JSON.stringify(decided);
    const acts = genericReviewResolutionActs(decided);
    const resolutionAct = acts[0];
    const after = JSON.stringify(decided);
    push(
      "copy-card-gains-one-park-dismiss-control",
      decided.act.mode === "copy" &&
        acts.length === 1 &&
        resolutionAct?.reasonRequired === true &&
        resolutionAct.effectKind === "record" &&
        resolutionAct.decisionOptions?.map((option) => option.value).join(",") === "review_resolution_park,review_resolution_dismiss" &&
        before === after,
      `model_unchanged=${before === after} controls=${acts.length} options=${JSON.stringify(resolutionAct?.decisionOptions?.map((option) => option.value))}`,
    );
    push(
      "single-review-copy-card-targets-its-sole-member",
      resolutionAct?.itemId === "parity_unknown_fixture",
      `control_item=${resolutionAct?.itemId ?? ""}`,
    );
  }

  // Deriving resolution acts against a briefed card never mutates the
  // serialized model (frozen-rendering law), and a card whose act is already
  // executable gets no derived resolution acts at all.
  {
    const brief = validBriefPayload();
    const item: Json = { id: "unknown_freeze", area: "parity", context_label: "Parity unknown", brief };
    const group: Json = {
      key: "parity|unknown",
      area: "parity",
      count: 1,
      status: "unknown",
      operator_summary: "Review parity unknown.",
      context_label: "parity",
      item_ids: [item.id],
      evidence_refs: ["evidence:parity-freeze"],
      sample: item,
    };
    const [decided] = buildDecideGroups([group], new Map([[String(item.id), item]]), null, () => item);
    const before = JSON.stringify(decided);
    genericReviewResolutionActs(decided);
    push(
      "briefed-card-derivation-never-mutates-model",
      JSON.stringify(decided) === before,
      `after_match=${JSON.stringify(decided) === before}`,
    );
    const actionable = { ...decided, act: { ...decided.act, mode: "generic" } } as typeof decided;
    push(
      "actionable-card-gets-no-derived-acts",
      genericReviewResolutionActs(actionable).length === 0,
      `acts=${genericReviewResolutionActs(actionable).length}`,
    );
  }

  const failed = checks.filter((c) => !c.ok);
  return {
    ok: failed.length === 0,
    details: checks.map((c) => `${c.ok ? "ok" : "FAIL"} ${c.name} — ${c.detail}`),
  };
}

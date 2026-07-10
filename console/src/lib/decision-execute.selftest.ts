/*
 * Falsifiers for the execute-route ↔ decide-model agreement on WHO may act.
 * A bespoke owner with zero options means the current item has no compatible
 * workflow. A projected evidence/record command must not be substituted for a
 * different represented effect (lived: route demote cards exposed the
 * hold-record command as if it could change routing authority).
 *
 * These checks pin the agreement contract at the pure layer (no spawned CLI):
 * Generic execution remains available only when no bespoke owner exists.
 * Runs under bun via scripts/decision-execute-smoke.mjs.
 */

import {
  decisionCommandFailureResponse,
  decisionFailureStatus,
  decisionStaleRevisionResponse,
  executeDecision,
  extractDecisionRefs,
  genericActionRevisionMatches,
  genericActionAllowedForOwner,
  missingBespokeConfirmation,
  renderedRevisionMatches,
} from "./decision-execute";
import { headlineAct, type HeadlineActDescriptor } from "./briefing";
import type { AttentionGroup } from "./attention";
import {
  buildDecideActionRequest,
  buildHeadlineActionRequest,
  type ClientDecideAct,
  decideActIdentity,
  headlineActIdentity,
} from "./governed-action-client";
import { specFor } from "./decision-registry";
import { genericActionRevision } from "./generic-actions";
import type { Json } from "./compound";

type Check = { name: string; ok: boolean; detail: string };

function routePromotionItem(recommendation: string): Json {
  return {
    id: "route_promotion_selftest01",
    area: "route-promotions",
    group_key: `route-promotions|${recommendation}|classify`,
    json_path: "/tmp/route_promotion_selftest01.json",
  };
}

export async function runDecisionExecuteSelfTest(): Promise<{ ok: boolean; details: string[] }> {
  const checks: Check[] = [];
  const push = (name: string, ok: boolean, detail: string) => checks.push({ name, ok, detail });

  const spec = specFor("route-promotions");
  if (!spec || !spec.optionsFor) {
    return { ok: false, details: ["FAIL route-promotions spec or optionsFor missing"] };
  }

  // 1. Demote has no effect-compatible bespoke option. The projected
  //    promotion-record action records holds only, so it must stay unavailable.
  {
    const options = spec.optionsFor(routePromotionItem("demote"));
    push(
      "demote-zero-options-does-not-use-generic-record-action",
      options.length === 0 && !genericActionAllowedForOwner(true, "route_promotion_record"),
      `options=${options.length} generic=${genericActionAllowedForOwner(true, "route_promotion_record")}`,
    );
  }

  // 8. A stale revision caught by the server preflight has the same stable
  //    conflict code as a stale revision reported by the CLI race path.
  {
    const response = decisionStaleRevisionResponse();
    const payload = await response.json() as { ok?: boolean; error_code?: string };
    push(
      "lifecycle-preflight-stale-carries-stable-code",
      response.status === 409 && payload.ok === false && payload.error_code === "stale_revision",
      `response=${response.status} code=${payload.error_code ?? "missing"}`,
    );
  }

  // 7. A lifecycle conflict discovered by the CLI after the server's fresh
  //    projection check is still an optimistic-concurrency conflict, never a
  //    misleading HTTP 200. Other command failures remain server errors.
  {
    const area = "capability-lifecycle-proposals";
    const conflict = JSON.stringify({
      schema_version: "compound.capability.lifecycle_review_error.v0.1",
      status: "error",
      error_code: "disposition_conflict",
      error: "refresh and review the current proposal",
    });
    const response = decisionCommandFailureResponse(area, { message: "command failed", stdout: conflict, stderr: "" }, false);
    const payload = await response.json() as { ok?: boolean; error_code?: string };
    push(
      "lifecycle-cli-conflicts-map-to-http-409",
      response.status === 409 && payload.ok === false && payload.error_code === "disposition_conflict" &&
        decisionFailureStatus(area, JSON.stringify({ error_code: "stale_revision" })) === 409 &&
        decisionFailureStatus(area, JSON.stringify({ error_code: "command_failed" })) === 500,
      `response=${response.status} code=${payload.error_code ?? "missing"}; unknown failures are 500`,
    );
  }

  // 2. hold_failing is another mutating recommendation, so the same owner
  //    boundary applies.
  {
    const options = spec.optionsFor(routePromotionItem("hold_failing"));
    push(
      "hold-failing-zero-options-does-not-use-generic-record-action",
      options.length === 0 && !genericActionAllowedForOwner(true, "route_promotion_record"),
      `options=${options.length}`,
    );
  }

  // 3. hold_insufficient_evidence: the bespoke spec CAN act (record_hold) —
  //    the bespoke path must keep winning; fallback must refuse even with an
  //    actionId present ("a bespoke spec always wins when it can act").
  {
    const options = spec.optionsFor(routePromotionItem("hold_insufficient_evidence"));
    push(
      "bespoke-wins-when-it-can-act",
      options.length === 1 && !genericActionAllowedForOwner(true, "route_promotion_record"),
      `options=${options.length} generic=${genericActionAllowedForOwner(true, "route_promotion_record")}`,
    );
  }

  // 4. Generic actions remain available for ownerless areas only, and still
  //    require a projected action id.
  {
    push(
      "generic-actions-require-ownerless-area-and-action-id",
      genericActionAllowedForOwner(false, "record_job_health_review") &&
        !genericActionAllowedForOwner(false, "") &&
        !genericActionAllowedForOwner(true, "record_job_health_review"),
      "ownerless+id accepted; missing id or bespoke owner refused",
    );
  }

  // 5. Structured JSON output must retain non-ID-shaped proof refs and the
  //    real `pol_` prefix. The old text-only regex silently dropped both.
  {
    const refs = extractDecisionRefs(JSON.stringify({
      schema_version: "compound.work_create_plan.v0.2",
      proof_refs: ["capability_lifecycle_proposal:clp_fixture", "sha256:abc123"],
      disposition: {
        id: "cld_fixture",
        work_id: "work_fixture",
        action_intent_id: "act_fixture",
        policy_decision_id: "pol_fixture",
      },
    }));
    push(
      "structured-proof-refs-retained",
      refs.proof_refs.includes("capability_lifecycle_proposal:clp_fixture") &&
        refs.proof_refs.includes("sha256:abc123") &&
        refs.work_ids.includes("work_fixture") &&
        !refs.work_ids.includes("work_create_plan") &&
        refs.action_intent_ids.includes("act_fixture") &&
        refs.policy_decision_ids.includes("pol_fixture") &&
        refs.decision_ids.includes("cld_fixture"),
      JSON.stringify(refs),
    );
  }

  // 6. Optimistic concurrency compares the revision rendered to the operator
  //    with the freshly resolved owner revision. Missing or changed is stale.
  {
    push(
      "rendered-revision-must-match-fresh-owner",
      renderedRevisionMatches("hash_a", "hash_a", true) &&
        !renderedRevisionMatches("hash_b", "hash_a", true) &&
        !renderedRevisionMatches("hash_a", "", true) &&
        renderedRevisionMatches("", "", false),
      "matching accepted; changed/missing rejected; non-versioned actions unaffected",
    );
  }

  // 9. A reserved lifecycle proposal resumes only through the freshly
  //    projected named action. Editable client fields never rebuild this argv.
  {
    const lifecycle = specFor("capability-lifecycle-proposals");
    const hash = "b".repeat(64);
    const projected = [
      "compound", "capability", "lifecycle-review",
      "--proposal", "clp_reserved",
      "--expected-hash", hash,
      "--decision", "open_work",
      "--rationale", "resume the owner-reserved admission",
      "--actor", "operator-console",
      "--json",
    ];
    const item = {
      id: "clp_reserved",
      actions: [{ id: "resume_open_work", argv: projected }],
      decision_card: {
        choices: [{ id: "resume_open_work", label: "Resume tracked work", effect: "Resume the reserved admission." }],
        meta: { proposal_hash: hash },
      },
    } as Json;
    const options = lifecycle?.optionsFor?.(item) ?? [];
    const argv = lifecycle?.buildArgv?.({
      itemId: "clp_reserved",
      item,
      option: "resume_open_work",
      fields: { rationale: "client text must not replace owner argv" },
      expectedRevision: hash,
    });
    const rationaleField = lifecycle?.fields.find((field) => field.name === "rationale");
    push(
      "reserved-lifecycle-resolves-fresh-named-action",
      options.map((option) => option.value).join(",") === "resume_open_work" &&
        argv?.join(" ") === projected.slice(1).join(" ") &&
        Boolean(rationaleField?.showForOptions) &&
        !rationaleField?.showForOptions?.includes("resume_open_work"),
      `options=${options.map((option) => option.value).join(",")} argv=${argv?.join(" ") ?? "missing"}`,
    );
  }

  // 10. The new server confirmation marker belongs to owners that declare it.
  //     Existing bespoke dashboard actions keep their established one-click
  //     contract; lifecycle preview remains write-free and confirmation-free.
  {
    const lifecycle = specFor("capability-lifecycle-proposals");
    const existing = specFor("work-advance");
    push(
      "bespoke-confirmation-is-owner-scoped",
      lifecycle?.requiresConfirmation === true &&
        existing?.requiresConfirmation !== true &&
        missingBespokeConfirmation(Boolean(lifecycle?.requiresConfirmation), false, false) &&
        !missingBespokeConfirmation(Boolean(lifecycle?.requiresConfirmation), true, false) &&
        !missingBespokeConfirmation(Boolean(lifecycle?.requiresConfirmation), false, true) &&
        !missingBespokeConfirmation(Boolean(existing?.requiresConfirmation), false, false),
      `lifecycle=${Boolean(lifecycle?.requiresConfirmation)} existing=${Boolean(existing?.requiresConfirmation)}`,
    );
  }

  // 11. Preview is a bespoke owner contract. A generic-shaped preview request
  //     must be rejected before action resolution can reach a mutation.
  {
    const response = await executeDecision({
      area: "generic-preview-fixture",
      itemId: "fixture_item",
      actionId: "future_action",
      preview: true,
    });
    const payload = await response.json() as { ok?: boolean; error_code?: string };
    push(
      "generic-preview-rejected-before-resolution",
      response.status === 400 && payload.ok === false && payload.error_code === "preview_unsupported",
      `response=${response.status} code=${payload.error_code ?? "missing"}`,
    );
  }

  // 12. Panel mutations are server-confirmed owners, and Briefing recognizes
  //     panel apply only from the exact action id plus exact argv shape.
  {
    const base: Omit<AttentionGroup, "action"> = {
      key: "panel-verdicts|prun_fixture|needs_human",
      area: "panel-verdicts",
      band: "needs_you",
      source: "reviews",
      kind: "review",
      count: 1,
      operator_summary: "Review the panel run.",
      operator_unlock: "",
      blast_radius: "local_reversible",
      reversibility: "reversible",
      leverage: 1,
      oldest_created_at: "",
      terminal_reason: "",
      item_ids: ["fixture_item"],
      freshness: { generated_at: "", source: "fixture" },
    };
    const future = headlineAct({
      ...base,
      action: {
        id: "future_panel_record",
        label: "Record panel review",
        argv: ["compound", "panel", "run", "--plan", "prun_fixture", "--apply"],
        kind: "batch",
        requires_confirmation: true,
      },
    });
    const exact = headlineAct({
      ...base,
      action: {
        id: "panel_run_apply",
        label: "Apply panel run",
        argv: ["compound", "panel", "run", "--plan", "prun_fixture", "--apply"],
        kind: "batch",
        requires_confirmation: false,
      },
    });
    const grouped = headlineAct({
      ...base,
      action: {
        id: "panel_verdict_record_group",
        label: "Record panel reviews",
        argv: [
          "compound", "panel", "verdict-record", "--group", base.key,
          "--rationale", "reviewed current panel evidence", "--reviewed-by", "operator-fixture",
        ],
        kind: "batch",
        requires_confirmation: true,
      },
    });
    const groupedBody = grouped ? buildHeadlineActionRequest(grouped, "", "confirm") : {};
    const groupedFields = groupedBody.fields as Record<string, string> | undefined;
    push(
      "panel-owner-requires-confirmation-and-exact-action-shape",
      specFor("panel-apply")?.requiresConfirmation === true &&
        specFor("panel-verdict-record-group")?.requiresConfirmation === true &&
        future === null &&
        exact?.area === "panel-apply" &&
        exact.requiresConfirmation &&
        groupedFields?.rationale === "reviewed current panel evidence" &&
        groupedFields.reviewed_by === "operator-fixture",
      `panel=${Boolean(specFor("panel-apply")?.requiresConfirmation)} group=${Boolean(specFor("panel-verdict-record-group")?.requiresConfirmation)} future=${future?.area ?? "null"} exactConfirm=${Boolean(exact?.requiresConfirmation)} groupedFields=${JSON.stringify(groupedFields)}`,
    );
  }

  // 13. First-click requests never claim operator confirmation. Only the
  //     explicit modal confirmation phase may set confirmed=true, in both
  //     Decide and Briefing.
  {
    const decideAct: ClientDecideAct = {
      mode: "generic",
      area: "job-health",
      itemId: "job_fixture",
      actionId: "record_job_health_review",
      expectedRevision: "revision_fixture",
      label: "Record review",
      argv: ["authority", "job-health-review", "/tmp/review.yaml"],
      requiresConfirmation: false,
      copyCommand: "compound reviews --area job-health",
      effectKind: "record",
    };
    const headlineDescriptor: HeadlineActDescriptor = {
      pathway: "generic",
      area: "job-health",
      itemId: "job_fixture",
      actionId: "record_job_health_review",
      expectedRevision: "revision_fixture",
      label: "Record review",
      argv: ["compound", "authority", "job-health-review", "/tmp/review.yaml"],
      requiresConfirmation: false,
      rationaleRequired: false,
    };
    const decideDirect = buildDecideActionRequest(decideAct, "", {}, "direct");
    const decideConfirm = buildDecideActionRequest(decideAct, "", {}, "confirm");
    const headlineDirect = buildHeadlineActionRequest(headlineDescriptor, "", "direct");
    const headlineConfirm = buildHeadlineActionRequest(headlineDescriptor, "", "confirm");
    push(
      "only-modal-submit-claims-confirmation",
      decideDirect.confirmed === false &&
        decideConfirm.confirmed === true &&
        decideDirect.expectedRevision === "revision_fixture" &&
        headlineDirect.confirmed === false &&
        headlineConfirm.confirmed === true &&
        headlineDirect.expectedRevision === "revision_fixture",
      `decide=${String(decideDirect.confirmed)}/${String(decideConfirm.confirmed)}:${String(decideDirect.expectedRevision)} headline=${String(headlineDirect.confirmed)}/${String(headlineConfirm.confirmed)}:${String(headlineDirect.expectedRevision)}`,
    );
  }

  // 14. Action identity includes the actual target. React keys based on this
  //     value remount draft controls when one stable group advances A -> B.
  {
    const decideA: ClientDecideAct = {
      mode: "generic",
      area: "job-health",
      itemId: "job_a",
      actionId: "record_job_health_review",
      expectedRevision: "revision_a",
      label: "Record review",
      argv: ["authority", "job-health-review", "/tmp/a.yaml"],
      requiresConfirmation: true,
      copyCommand: "compound reviews --area job-health",
      effectKind: "record",
    };
    const decideB: ClientDecideAct = { ...decideA, itemId: "job_b", expectedRevision: "revision_b", argv: ["authority", "job-health-review", "/tmp/b.yaml"] };
    const headlineA: HeadlineActDescriptor = {
      pathway: "generic",
      area: "job-health",
      itemId: "job_a",
      actionId: "record_job_health_review",
      label: "Record review",
      argv: ["compound", "authority", "job-health-review", "/tmp/a.yaml"],
      requiresConfirmation: true,
      rationaleRequired: false,
    };
    const headlineB: HeadlineActDescriptor = { ...headlineA, itemId: "job_b", argv: ["compound", "authority", "job-health-review", "/tmp/b.yaml"] };
    push(
      "action-identity-changes-with-target",
      decideActIdentity(decideA) !== decideActIdentity(decideB) &&
        headlineActIdentity(headlineA) !== headlineActIdentity(headlineB),
      `decide=${decideActIdentity(decideA)} -> ${decideActIdentity(decideB)} headline=${headlineActIdentity(headlineA)} -> ${headlineActIdentity(headlineB)}`,
    );
  }

  // 15. Briefing cannot route around a bespoke owner. A clean projected route
  //     record action for a demote group remains unavailable because that
  //     owner's current item has no effect-compatible option.
  {
    const routeHeadline = headlineAct({
      key: "route-promotions|demote|classify",
      area: "route-promotions",
      band: "needs_you",
      source: "reviews",
      kind: "review",
      count: 1,
      operator_summary: "Demote this route?",
      operator_unlock: "",
      blast_radius: "local_reversible",
      reversibility: "reversible",
      leverage: 1,
      oldest_created_at: "",
      action: {
        id: "route_promotion_record",
        label: "Record route review",
        argv: ["compound", "route", "promotion-record", "--review", "/tmp/route.json"],
        kind: "one_click",
        requires_confirmation: true,
      },
      terminal_reason: "",
      item_ids: ["route_promotion_fixture01"],
      freshness: { generated_at: "", source: "fixture" },
    });
    push(
      "briefing-honors-bespoke-owner-precedence",
      routeHeadline === null,
      `headline=${routeHeadline?.pathway ?? "null"}`,
    );
  }

  // 16. Generic confirmation is optimistic-concurrency bound to the rendered
  //     owner action. Field-name normalization keeps Attention and Reviews
  //     projections equivalent; any argv drift or missing revision is stale.
  {
    const rendered = genericActionRevision({
      id: "record_job_health_review",
      label: "Record job-health review",
      argv: ["compound", "authority", "job-health-review", "/tmp/a.yaml"],
      requires_confirmation: true,
    });
    const sameFresh = genericActionRevision({
      id: "record_job_health_review",
      label: "Record job-health review",
      argv: ["compound", "authority", "job-health-review", "/tmp/a.yaml"],
      requires_review: true,
    });
    const changedFresh = genericActionRevision({
      id: "record_job_health_review",
      label: "Record job-health review",
      argv: ["compound", "authority", "job-health-review", "/tmp/b.yaml"],
      requires_review: true,
    });
    push(
      "generic-confirmation-binds-rendered-action-revision",
      rendered === sameFresh &&
        rendered !== changedFresh &&
        genericActionRevisionMatches(sameFresh, rendered) &&
        !genericActionRevisionMatches(changedFresh, rendered) &&
        !genericActionRevisionMatches(sameFresh, ""),
      `rendered=${rendered} same=${sameFresh} changed=${changedFresh}`,
    );
  }

  // 17. A one-click Attention action resolves by its sole item id; only a
  //     genuinely batch-scoped action uses the group key.
  {
    const base: Omit<AttentionGroup, "action"> = {
      key: "job-health|fixture",
      area: "job-health",
      band: "needs_you",
      source: "reviews",
      kind: "review",
      count: 1,
      operator_summary: "Record this job review.",
      operator_unlock: "",
      blast_radius: "local_reversible",
      reversibility: "reversible",
      leverage: 1,
      oldest_created_at: "",
      terminal_reason: "",
      item_ids: ["jhp_fixture"],
      freshness: { generated_at: "", source: "fixture" },
    };
    const action = {
      id: "record_job_health_review",
      label: "Record job-health review",
      argv: ["compound", "authority", "job-health-review", "/tmp/review.yaml"],
      requires_confirmation: true,
    };
    const oneClick = headlineAct({ ...base, action: { ...action, kind: "one_click" } });
    const batch = headlineAct({ ...base, action: { ...action, kind: "batch" } });
    push(
      "briefing-generic-action-preserves-owner-scope",
      oneClick?.itemId === "jhp_fixture" && batch?.itemId === base.key,
      `oneClick=${oneClick?.itemId ?? "null"} batch=${batch?.itemId ?? "null"}`,
    );
  }

  const failed = checks.filter((c) => !c.ok);
  return {
    ok: failed.length === 0,
    details: checks.map((c) => `${c.ok ? "ok" : "FAIL"} ${c.name} — ${c.detail}`),
  };
}

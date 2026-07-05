import {
  argvFromActions,
  argvMatchesAllowlist,
  clamp01,
  type DecisionAreaSpec,
  specFor,
} from "@/lib/decision-registry";
import {
  arr,
  compoundFailure,
  type Json,
  reviewItems,
  runCompound,
  runJson,
  str,
} from "@/lib/compound";

type DecisionExecutionRefs = {
  action_refs: string[];
  action_intent_ids: string[];
  policy_decision_ids: string[];
  receipt_ids: string[];
  transition_ids: string[];
  proof_refs: string[];
  work_ids: string[];
  review_ids: string[];
  candidate_ids: string[];
};

const REF_KEYS: Record<string, keyof DecisionExecutionRefs> = {
  action_ref: "action_refs",
  action_refs: "action_refs",
  action_intent: "action_intent_ids",
  action_intent_id: "action_intent_ids",
  action_intent_ids: "action_intent_ids",
  policy_decision: "policy_decision_ids",
  policy_decision_id: "policy_decision_ids",
  policy_decision_ids: "policy_decision_ids",
  receipt: "receipt_ids",
  receipt_id: "receipt_ids",
  receipt_ids: "receipt_ids",
  transition: "transition_ids",
  transition_id: "transition_ids",
  transition_ids: "transition_ids",
  proof_ref: "proof_refs",
  proof_refs: "proof_refs",
  work: "work_ids",
  work_id: "work_ids",
  work_ids: "work_ids",
  review: "review_ids",
  review_id: "review_ids",
  review_ids: "review_ids",
  candidate: "candidate_ids",
  candidate_id: "candidate_ids",
  candidate_ids: "candidate_ids",
};

const ID_PATTERNS: Array<[keyof DecisionExecutionRefs, RegExp]> = [
  ["action_intent_ids", /\bact_[a-z0-9_]+\b/g],
  ["policy_decision_ids", /\bpd_[a-z0-9_]+\b/g],
  ["receipt_ids", /\b(?:receipt|rec)_[a-z0-9_]+\b/g],
  ["transition_ids", /\b(?:transition|tr)_[a-z0-9_]+\b/g],
  ["work_ids", /\bwork_[a-z0-9_]+\b/g],
  ["review_ids", /\brev_[a-z0-9_]+\b/g],
  ["candidate_ids", /\bcand_[a-z0-9_]+\b/g],
];

function emptyRefs(): DecisionExecutionRefs {
  return {
    action_refs: [],
    action_intent_ids: [],
    policy_decision_ids: [],
    receipt_ids: [],
    transition_ids: [],
    proof_refs: [],
    work_ids: [],
    review_ids: [],
    candidate_ids: [],
  };
}

function addRef(refs: DecisionExecutionRefs, key: keyof DecisionExecutionRefs, value: string) {
  const clean = value.trim().replace(/^["']|["']$/g, "").replace(/[,.]$/g, "");
  if (!clean || refs[key].includes(clean)) return;
  refs[key].push(clean);
}

function addDelimitedRefs(refs: DecisionExecutionRefs, key: keyof DecisionExecutionRefs, raw: string) {
  for (const part of raw.split(/[,\s]+/)) addRef(refs, key, part);
}

function extractDecisionRefs(...texts: string[]): DecisionExecutionRefs {
  const refs = emptyRefs();
  const text = texts.join("\n");

  for (const line of text.split(/\r?\n/)) {
    const trimmed = line.trim().replace(/^-\s*/, "");
    const match = trimmed.match(/^([a-zA-Z_]+):\s*(.+)$/);
    if (!match) continue;
    const key = REF_KEYS[match[1].toLowerCase()];
    if (!key) continue;
    addDelimitedRefs(refs, key, match[2]);
  }

  for (const [key, pattern] of ID_PATTERNS) {
    const matches = text.match(pattern) ?? [];
    for (const match of matches) addRef(refs, key, match);
  }
  return refs;
}

async function resolveItem(spec: DecisionAreaSpec, itemId: string): Promise<Json | null> {
  if (spec.source === "reviews") {
    const data = await runJson(["reviews", "--json", "--area", spec.area]);
    return reviewItems(data).find((it) => str(it.id) === itemId) ?? null;
  }
  if (spec.source === "workbench") {
    const data = await runJson(["workbench", "--json"]);
    const open = arr(data?.open);
    const admissionPreviews = arr(data?.admission_previews);
    return (
      open.find((it) => str(it.work_id) === itemId || str(it.id) === itemId) ??
      admissionPreviews.find((it) => str(it.work_id) === itemId || str(it.id) === itemId) ??
      null
    ) as Json | null;
  }
  if (spec.area === "memory-adjudicate") {
    const data = await runJson(["memory", "candidates", "--json", "--include-evidence"]);
    return (arr(data?.items).find((it) => str(it.id) === itemId) ?? null) as Json | null;
  }
  return null;
}

async function currentDispatchTarget(): Promise<Json | null> {
  const data = await runJson(["workbench", "--json"]);
  const next = (data?.dispatch_next ?? {}) as Json;
  if (!next.can_run) return null;
  return next;
}

export async function executeDecision(body: Record<string, unknown>): Promise<Response> {
  const area = typeof body.area === "string" ? body.area : "";
  const itemId = typeof body.itemId === "string" ? body.itemId.trim() : "";
  const option = typeof body.option === "string" ? body.option : "";

  const fields: Record<string, string> = {};
  if (body.fields && typeof body.fields === "object") {
    for (const [k, v] of Object.entries(body.fields as Record<string, unknown>)) {
      fields[k] = typeof v === "string" ? v : String(v ?? "");
    }
  }
  if ("confidence" in fields) fields.confidence = String(clamp01(fields.confidence));

  const spec = specFor(area);
  if (!spec) {
    return Response.json({ ok: false, stderr: `unknown decision area: ${area}` }, { status: 400 });
  }

  if (!spec.idPattern.test(itemId)) {
    return Response.json({ ok: false, stderr: `invalid id for ${area}` }, { status: 400 });
  }

  const item = await resolveItem(spec, itemId);
  if (!item && (spec.source !== "orient" || spec.requiresResolvedItem)) {
    return Response.json({ ok: false, stderr: `item ${itemId} not found in ${area}` }, { status: 404 });
  }

  if (area === "work-dispatch") {
    const next = await currentDispatchTarget();
    const nextWorkID = str(next?.work_id);
    if (!next || nextWorkID !== itemId) {
      return Response.json(
        {
          ok: false,
          stderr: nextWorkID
            ? `continue target changed; refresh the board before continuing ${nextWorkID}`
            : "nothing is currently safe to continue; refresh the board",
        },
        { status: 409 },
      );
    }
    const requestedActor = str(fields.actor) || "codex";
    const nextActor = str(next.actor) || "codex";
    if (requestedActor !== nextActor) {
      return Response.json(
        { ok: false, stderr: `continue target actor changed; refresh the board before continuing as ${nextActor}` },
        { status: 409 },
      );
    }
  }

  const allowedOptions = spec.optionsFor ? spec.optionsFor(item ?? {}) : spec.options;
  if (!allowedOptions.some((o) => o.value === option)) {
    return Response.json(
      { ok: false, stderr: `decision "${option}" is not valid for this item's current state` },
      { status: 400 },
    );
  }

  for (const field of spec.fields) {
    const relevant = !field.showForOptions || field.showForOptions.includes(option);
    if (field.required && relevant && !str(fields[field.name]).trim()) {
      return Response.json({ ok: false, stderr: `${field.label} is required` }, { status: 400 });
    }
  }

  const ctx = { itemId, item: item ?? {}, option, fields };

  const argv = spec.buildArgv ? spec.buildArgv(ctx) : argvFromActions(item ?? {});
  if (!argv || argv.length === 0) {
    return Response.json({ ok: false, stderr: "could not resolve the command to run" }, { status: 400 });
  }

  if (!argvMatchesAllowlist(argv, spec.argvAllowlist)) {
    return Response.json({ ok: false, stderr: `command not allowed for ${area}` }, { status: 403 });
  }

  try {
    const result = await runCompound(argv);
    return Response.json({
      ok: true,
      stdout: result.stdout,
      stderr: result.stderr,
      refs: extractDecisionRefs(result.stdout, result.stderr),
    });
  } catch (error) {
    const failure = compoundFailure(error);
    return Response.json({
      ok: false,
      stdout: failure.stdout,
      stderr: failure.stderr || failure.message,
      refs: extractDecisionRefs(failure.stdout, failure.stderr || failure.message),
    });
  }
}

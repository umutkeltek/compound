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

async function resolveItem(spec: DecisionAreaSpec, itemId: string): Promise<Json | null> {
  if (spec.source === "reviews") {
    const data = await runJson(["reviews", "--json", "--area", spec.area]);
    return reviewItems(data).find((it) => str(it.id) === itemId) ?? null;
  }
  if (spec.source === "workbench") {
    const data = await runJson(["workbench", "--json"]);
    const open = arr(data?.open);
    return (open.find((it) => str(it.work_id) === itemId || str(it.id) === itemId) ?? null) as Json | null;
  }
  if (spec.area === "memory-adjudicate") {
    const data = await runJson(["memory", "candidates", "--json", "--include-evidence"]);
    return (arr(data?.items).find((it) => str(it.id) === itemId) ?? null) as Json | null;
  }
  return null;
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
  if (!item && spec.source !== "orient") {
    return Response.json({ ok: false, stderr: `item ${itemId} not found in ${area}` }, { status: 404 });
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

  if (spec.preExec) {
    try {
      await spec.preExec(ctx);
    } catch (error) {
      const message = error instanceof Error ? error.message : "pre-execution step failed";
      return Response.json({ ok: false, stderr: message }, { status: 400 });
    }
  }

  const argv = spec.buildArgv ? spec.buildArgv(ctx) : argvFromActions(item ?? {});
  if (!argv || argv.length === 0) {
    return Response.json({ ok: false, stderr: "could not resolve the command to run" }, { status: 400 });
  }

  if (!argvMatchesAllowlist(argv, spec.argvAllowlist)) {
    return Response.json({ ok: false, stderr: `command not allowed for ${area}` }, { status: 403 });
  }

  try {
    const result = await runCompound(argv);
    return Response.json({ ok: true, stdout: result.stdout, stderr: result.stderr });
  } catch (error) {
    const failure = compoundFailure(error);
    return Response.json({
      ok: false,
      stdout: failure.stdout,
      stderr: failure.stderr || failure.message,
    });
  }
}

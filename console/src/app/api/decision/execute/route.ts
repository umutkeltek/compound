import { readFile } from "node:fs/promises";
import path from "node:path";

import {
  argvFromActions,
  argvMatchesAllowlist,
  clamp01,
  type DecisionAreaSpec,
  specFor,
} from "@/lib/decision-registry";
import {
  arr,
  COMPOUND_HOME,
  compoundFailure,
  type Json,
  reviewItems,
  runCompound,
  runJson,
  str,
} from "@/lib/compound";

/*
 * The single generic mutation endpoint. Replaces /api/reviews/record and
 * /api/work/act. Flow: look up the registry entry → validate id → RE-RESOLVE
 * the item from the projection server-side (never trust a client-sent argv) →
 * compute the allowed options for THIS item (dynamic for work state machines)
 * → validate the chosen option + only the fields relevant to it → preExec
 * (e.g. YAML rewrite, repairs the confidence:2.00 draft bug) → buildArgv →
 * verb-prefix allowlist (defense in depth) → clamp confidence → runCompound.
 * DEC-0021: every action is a human click.
 */

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
    const candidatePath = path.resolve(
      COMPOUND_HOME,
      "inbox",
      "memory-candidates",
      `${itemId}.json`,
    );
    const inboxRoot = path.resolve(COMPOUND_HOME, "inbox");
    if (!candidatePath.startsWith(inboxRoot + path.sep)) return null;
    try {
      return JSON.parse(await readFile(candidatePath, "utf8")) as Json;
    } catch {
      return null;
    }
  }
  return null;
}

export async function POST(request: Request) {
  let body: Record<string, unknown>;
  try {
    body = (await request.json()) as Record<string, unknown>;
  } catch {
    return Response.json({ ok: false, stderr: "invalid JSON body" }, { status: 400 });
  }

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

  // Re-resolve from the projection so a tampered client payload can't inject argv
  // or pick a transition the item's current state doesn't allow.
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

  // Validate only the fields relevant to the chosen option (showForOptions).
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

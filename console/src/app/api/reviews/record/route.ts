import { readFile, writeFile } from "node:fs/promises";

import { compoundFailure, runCompound } from "@/lib/compound";
import { externalReviewYamlPath } from "@/lib/review-data";
import { isReviewDecision, replaceReviewDecisionFields } from "@/lib/review-yaml";

const REVIEW_ID_RE = /^extrepo_[a-z0-9]{6,}$/;

export async function POST(request: Request) {
  let body: Record<string, unknown>;
  try {
    body = (await request.json()) as Record<string, unknown>;
  } catch {
    return Response.json({ ok: false, stderr: "invalid JSON body" }, { status: 400 });
  }

  const id = typeof body.id === "string" ? body.id.trim() : "";
  const decision = body.decision;
  const rationale = typeof body.rationale === "string" ? body.rationale.trim() : "";
  const confidence = Number(body.confidence);

  if (!REVIEW_ID_RE.test(id)) {
    return Response.json({ ok: false, stderr: "invalid external repo review id" }, { status: 400 });
  }
  if (!isReviewDecision(decision)) {
    return Response.json({ ok: false, stderr: "invalid external repo review decision" }, { status: 400 });
  }
  if (!rationale) {
    return Response.json({ ok: false, stderr: "external repo review rationale required" }, { status: 400 });
  }
  if (!Number.isFinite(confidence)) {
    return Response.json({ ok: false, stderr: "external repo review confidence must be numeric" }, { status: 400 });
  }

  const yamlPath = externalReviewYamlPath(id);
  try {
    const source = await readFile(yamlPath, "utf8");
    const updated = replaceReviewDecisionFields(source, { decision, rationale, confidence });
    await writeFile(yamlPath, updated, "utf8");
  } catch (error) {
    const message = error instanceof Error ? error.message : "failed to update review YAML";
    return Response.json({ ok: false, stderr: message }, { status: 400 });
  }

  try {
    const result = await runCompound(["capability", "external-intake-review", yamlPath]);
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

import { consoleMutationBlockedResponse } from "@/lib/console-mutations";
import { executeDecision } from "@/lib/decision-execute";
import { isReviewDecision } from "@/lib/review-yaml";

const REVIEW_ID_RE = /^extrepo_[a-z0-9]{6,}$/;

export async function POST(request: Request) {
  const blocked = consoleMutationBlockedResponse(request);
  if (blocked) return blocked;

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

  return executeDecision({
    area: "external-repo-intake",
    itemId: id,
    option: decision,
    fields: { rationale, confidence: String(confidence) },
  });
}

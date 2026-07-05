import { consoleMutationBlockedResponse } from "@/lib/console-mutations";
import { executeDecision } from "@/lib/decision-execute";

const WORK_ID_RE = /^work_[a-z0-9]{6,}$/;

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
  const action = body.action;

  if (!WORK_ID_RE.test(id)) {
    return Response.json({ ok: false, stderr: "invalid work id" }, { status: 400 });
  }
  if (action !== "admit" && action !== "cancel") {
    return Response.json({ ok: false, stderr: "invalid work action" }, { status: 400 });
  }

  return executeDecision({
    area: "work-advance",
    itemId: id,
    option: action === "admit" ? "admitted" : "cancelled",
    fields:
      action === "cancel"
        ? {
            summary: "operator cancelled proposed work",
            move_type: "audit_only",
          }
        : {},
  });
}

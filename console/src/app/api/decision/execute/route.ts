import { consoleMutationBlockedResponse } from "@/lib/console-mutations";
import { executeDecision } from "@/lib/decision-execute";

/*
 * The single generic mutation endpoint. Replaces /api/reviews/record and
 * /api/work/act. Flow: look up the registry entry, validate id, re-resolve
 * item projection server-side, compute allowed options for this item, validate
 * the chosen option and fields, build argv, enforce the verb-prefix allowlist,
 * clamp confidence, then run compound. DEC-0021: every action is a human click.
 */
export async function POST(request: Request) {
  const blocked = consoleMutationBlockedResponse(request);
  if (blocked) return blocked;

  let body: Record<string, unknown>;
  try {
    body = (await request.json()) as Record<string, unknown>;
  } catch {
    return Response.json({ ok: false, stderr: "invalid JSON body" }, { status: 400 });
  }

  return executeDecision(body);
}

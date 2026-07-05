import { consoleMutationBlockedResponse } from "@/lib/console-mutations";
import { executeDecision } from "@/lib/decision-execute";

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

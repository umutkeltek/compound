import { consoleMutationBlockedResponse } from "@/lib/console-mutations";
import { executeDecision } from "@/lib/decision-execute";
import { instanceUnpinnedBlockedResponse } from "@/lib/instance-pin";
import { authorizeOperatorSession, operatorSessionActionBlockedResponse } from "@/lib/operator-session";

/*
 * The single generic mutation endpoint. Replaces /api/reviews/record and
 * /api/work/act. Flow: look up the registry entry, validate id, re-resolve
 * item projection server-side, compute allowed options for this item, validate
 * the chosen option and fields, build argv, enforce the verb-prefix allowlist,
 * clamp confidence, then run compound. DEC-0021: every action is a human click.
 */
export async function POST(request: Request) {
  const blocked = consoleMutationBlockedResponse(request);
  // The established capability caller and the explicitly paired browser have
  // separate credentials. A cookie can never stand in for the server capability.
  const browserSession = blocked !== null;
  if (browserSession) {
    const sessionBlocked = await authorizeOperatorSession(request);
    if (sessionBlocked) return sessionBlocked;
  }

  // Origin-pin: an instance started without an explicit home pin would silently
  // write the canonical home (the 2026-07-11 cross-port breach). Refuse the
  // mutation unless this server is pinned to a home the operator chose. See
  // instance-pin.ts.
  const unpinned = instanceUnpinnedBlockedResponse();
  if (unpinned) return unpinned;

  let body: Record<string, unknown>;
  try {
    body = (await request.json()) as Record<string, unknown>;
  } catch {
    return Response.json({ ok: false, stderr: "invalid JSON body" }, { status: 400 });
  }

  if (!body || Array.isArray(body) || typeof body !== "object") {
    return Response.json({ ok: false, stderr: "invalid action body" }, { status: 400 });
  }
  if (browserSession) {
    const unsupported = operatorSessionActionBlockedResponse(body);
    if (unsupported) return unsupported;
  }

  return executeDecision(body);
}

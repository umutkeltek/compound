import { request as httpRequest } from "node:http";
import { INSTANCE_PIN } from "./instance-pin";
import { isOperatorActionSupported } from "./operator-action";

// Server-only transport. These values must never be re-exported by a client
// module or passed as React props. The native supervisor owns every session.
const CONFIG = {
  enabled: process.env.COMPOUND_CONSOLE_OPERATOR_SESSION === "1" && process.env.COMPOUND_CONSOLE_MUTATIONS === "1" && INSTANCE_PIN.explicit,
  socket: process.env.COMPOUND_CONSOLE_OPERATOR_SOCKET ?? "",
  key: process.env.COMPOUND_CONSOLE_OPERATOR_BROKER_KEY ?? "",
  instance: process.env.COMPOUND_CONSOLE_OPERATOR_INSTANCE ?? "",
  home: INSTANCE_PIN.home,
};

export const OPERATOR_SESSION_COOKIE = "compound_operator_session";
export const OPERATOR_CSRF_HEADER = "x-compound-operator-csrf";
export const OPERATOR_ORIGIN = "http://127.0.0.1:3777";
const TOKEN = /^[a-f0-9]{64}$/;

type BrokerReply = { ok: boolean; session?: string; csrf?: string; expiresAt?: string; instance?: string };
export type OperatorSessionStatus = { enabled: boolean; connected: boolean; expiresAt?: string; csrf?: string };

export function operatorSessionConfigured(): boolean {
  return CONFIG.enabled && CONFIG.socket.startsWith("/") && TOKEN.test(CONFIG.key) && TOKEN.test(CONFIG.instance);
}

export function operatorRequestOriginAllowed(request: Request, write = request.method !== "GET"): boolean {
  const url = new URL(request.url);
  if (url.origin !== OPERATOR_ORIGIN || request.headers.get("host") !== "127.0.0.1:3777") return false;
  const origin = request.headers.get("origin");
  if ((write && origin !== OPERATOR_ORIGIN) || (origin !== null && origin !== OPERATOR_ORIGIN)) return false;
  if (request.headers.get("sec-fetch-site") !== "same-origin") return false;
  if (request.headers.has("forwarded")) return false;
  // Next may add canonical forwarding headers itself. They cannot retarget the
  // request and an upstream proxy cannot introduce a different host or scheme.
  for (const [name, expected] of [["x-forwarded-host", "127.0.0.1:3777"], ["x-forwarded-proto", "http"], ["x-forwarded-port", "3777"]]) {
    const value = request.headers.get(name);
    if (value !== null && value !== expected) return false;
  }
  return true;
}

export function operatorSessionCookie(cookieHeader: string): string {
  const values = cookieHeader.split(";").map((part) => part.trim()).filter((part) => part.startsWith(`${OPERATOR_SESSION_COOKIE}=`));
  if (values.length !== 1) return "";
  const value = values[0].slice(OPERATOR_SESSION_COOKIE.length + 1);
  return TOKEN.test(value) ? value : "";
}

function refused(message = "Operator connection unavailable. Connect this browser from the local console.", status = 403): Response {
  return Response.json({ ok: false, stderr: message }, { status, headers: { "cache-control": "no-store" } });
}

async function boundedPairBody(request: Request): Promise<string | null> {
  if (!request.body) return null;
  const reader = request.body.getReader();
  let bytes = 0;
  const chunks: Uint8Array[] = [];
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      bytes += value.byteLength;
      if (bytes > 1024) { await reader.cancel(); return null; }
      chunks.push(value);
    }
    const body = new Uint8Array(bytes);
    let offset = 0;
    for (const chunk of chunks) { body.set(chunk, offset); offset += chunk.byteLength; }
    return new TextDecoder("utf-8", { fatal: true }).decode(body);
  } catch { return null; }
  finally { reader.releaseLock(); }
}

function brokerCall(operation: string, fields: Record<string, string>): Promise<BrokerReply | null> {
  if (!operatorSessionConfigured()) return Promise.resolve(null);
  const body = JSON.stringify({ operation, home: CONFIG.home, origin: OPERATOR_ORIGIN, instance: CONFIG.instance, ...fields });
  return new Promise((resolve) => {
    let settled = false;
    const finish = (value: BrokerReply | null) => { if (!settled) { settled = true; resolve(value); } };
    const req = httpRequest({ socketPath: CONFIG.socket, path: "/operator-session", method: "POST", headers: {
      "content-type": "application/json", "content-length": Buffer.byteLength(body), "x-compound-operator-broker": CONFIG.key,
    } }, (res) => {
      let data = "";
      res.setEncoding("utf8");
      res.on("data", (chunk: string) => { data += chunk; if (data.length > 8192) { req.destroy(); finish(null); } });
      res.on("error", () => finish(null));
      res.on("end", () => {
        if (res.statusCode !== 200) { finish(null); return; }
        try { const parsed = JSON.parse(data) as BrokerReply; finish(parsed.ok && parsed.instance === CONFIG.instance ? parsed : null); } catch { finish(null); }
      });
    });
    req.setTimeout(2500, () => { req.destroy(); finish(null); });
    req.on("error", () => finish(null));
    req.end(body);
  });
}

/** Used only by server renderers; browser readiness is display evidence, never
 * authorization for POST. No CSRF or transport material is returned here. */
export async function operatorSessionReadiness(cookieHeader: string): Promise<boolean> {
  const session = operatorSessionCookie(cookieHeader);
  if (!session) return false;
  const reply = await brokerCall("status", { session });
  return Boolean(reply?.ok);
}

export async function operatorSessionStatus(request: Request): Promise<Response> {
  if (!operatorSessionConfigured()) return Response.json({ enabled: false, connected: false }, { headers: { "cache-control": "no-store" } });
  if (!operatorRequestOriginAllowed(request, false)) return refused();
  const session = operatorSessionCookie(request.headers.get("cookie") ?? "");
  const reply = session ? await brokerCall("status", { session }) : null;
  const status: OperatorSessionStatus = { enabled: true, connected: Boolean(reply?.ok) };
  if (reply?.ok) { status.expiresAt = reply.expiresAt; status.csrf = reply.csrf; }
  return Response.json(status, { headers: { "cache-control": "no-store" } });
}

export async function pairOperatorSession(request: Request): Promise<Response> {
  if (!operatorSessionConfigured() || !operatorRequestOriginAllowed(request)) return refused();
  if (request.headers.get("content-type")?.split(";")[0] !== "application/json" || Number(request.headers.get("content-length") ?? 0) > 1024) return refused("Invalid pairing request.", 400);
  const text = await boundedPairBody(request);
  if (text === null) return refused("Invalid pairing request.", 400);
  let code: unknown;
  try { const body = JSON.parse(text) as Record<string, unknown>; code = body.code; if (Object.keys(body).some((key) => key !== "code")) return refused("Invalid pairing request.", 400); } catch { return refused("Invalid pairing request.", 400); }
  if (typeof code !== "string" || !TOKEN.test(code)) return refused("The pairing code is invalid or expired.");
  const reply = await brokerCall("pair", { code });
  if (!reply?.session || !TOKEN.test(reply.session) || !reply.csrf || !reply.expiresAt) return refused("The pairing code is invalid, expired or already used. Run compound console pair for a new code.");
  const response = Response.json({ ok: true, connected: true, expiresAt: reply.expiresAt, csrf: reply.csrf }, { headers: { "cache-control": "no-store" } });
  response.headers.set("set-cookie", `${OPERATOR_SESSION_COOKIE}=${reply.session}; HttpOnly; SameSite=Strict; Path=/; Max-Age=3600`);
  return response;
}

/** Every mutation independently rechecks native liveness, expiry, home,
 * instance, exact origin and CSRF, even after a successful page render. */
export async function authorizeOperatorSession(request: Request): Promise<Response | null> {
  if (!operatorSessionConfigured() || !operatorRequestOriginAllowed(request)) return refused();
  const session = operatorSessionCookie(request.headers.get("cookie") ?? "");
  const csrf = request.headers.get(OPERATOR_CSRF_HEADER) ?? "";
  if (!session || !TOKEN.test(csrf)) return refused();
  const reply = await brokerCall("authorize", { session, csrf });
  return reply?.ok ? null : refused("Your operator connection expired or was disconnected. Connect again to continue.");
}

export async function revokeOperatorSession(request: Request): Promise<Response> {
  const blocked = await authorizeOperatorSession(request);
  if (blocked) return blocked;
  const reply = await brokerCall("revoke", { session: operatorSessionCookie(request.headers.get("cookie") ?? ""), csrf: request.headers.get(OPERATOR_CSRF_HEADER) ?? "" });
  if (!reply?.ok) return refused();
  const response = Response.json({ ok: true, connected: false }, { headers: { "cache-control": "no-store" } });
  response.headers.set("set-cookie", `${OPERATOR_SESSION_COOKIE}=; HttpOnly; SameSite=Strict; Path=/; Max-Age=0`);
  return response;
}

/** New browser transport is deliberately narrower than the legacy extension.
 * Existing executeDecision must still re-resolve these exact tokens fresh. */
export function operatorSessionActionBlockedResponse(body: Record<string, unknown>): Response | null {
  if (!isOperatorActionSupported(body)) {
    return refused("This action needs a current owner-issued action and policy revision before it can run from a connected browser.", 409);
  }
  return null;
}

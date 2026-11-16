import assert from "node:assert/strict";
import { createServer } from "node:http";
import { mkdtempSync, rmSync } from "node:fs";
import { join } from "node:path";
import { NextRequest } from "next/server";

// Native replay/expiry/concurrency are tested by internal/console. This fixture
// falsifies the HTTP adapter's independent boundaries and credential handling.
const dir = mkdtempSync("/tmp/cc-operator-adapter-");
const socket = join(dir, "broker.sock");
const HOME = "/synthetic/operator-home";
const KEY = "a".repeat(64);
const INSTANCE = "b".repeat(64);
const SESSION = "c".repeat(64);
const CSRF = "d".repeat(64);
const PAIR = "e".repeat(64);
const CAPABILITY = "f".repeat(64);
const ORIGIN = "http://127.0.0.1:3777";
let calls = 0;
let live = true;
const server = createServer(async (request, response) => {
  calls++;
  let raw = "";
  for await (const chunk of request) raw += chunk;
  const body = JSON.parse(raw);
  const correct = request.headers["x-compound-operator-broker"] === KEY && body.home === HOME && body.origin === ORIGIN && body.instance === INSTANCE;
  const authorized = body.operation === "pair" ? body.code === PAIR : body.session === SESSION && live && (body.operation === "status" || body.csrf === CSRF);
  if (!correct || !authorized) { response.writeHead(403); response.end("refused"); return; }
  if (body.operation === "revoke") live = false;
  response.setHeader("content-type", "application/json");
  response.end(JSON.stringify({ ok: true, instance: INSTANCE, session: body.operation === "pair" ? SESSION : undefined, csrf: CSRF, expiresAt: new Date(Date.now() + 3600_000).toISOString() }));
});
await new Promise((resolve) => server.listen(socket, resolve));
Object.assign(process.env, {
  COMPOUND_HOME: HOME,
  COMPOUND_CONSOLE_MUTATIONS: "1",
  COMPOUND_CONSOLE_CAPABILITY: CAPABILITY,
  COMPOUND_CONSOLE_OPERATOR_SESSION: "1",
  COMPOUND_CONSOLE_OPERATOR_SOCKET: socket,
  COMPOUND_CONSOLE_OPERATOR_BROKER_KEY: KEY,
  COMPOUND_CONSOLE_OPERATOR_INSTANCE: INSTANCE,
});

try {
  const session = await import("../src/lib/operator-session.ts");
  const { consoleChildEnv } = await import("../src/lib/console-child-env.ts");
  const { consoleMutationBlockedResponse } = await import("../src/lib/console-mutations.ts");
  function request(patch = {}) {
    const { headers = {}, url = ORIGIN + "/api/decision/execute", method = "POST", body, ...rest } = patch;
    return new Request(url, { method, headers: { host: "127.0.0.1:3777", origin: ORIGIN, "sec-fetch-site": "same-origin", cookie: `compound_operator_session=${SESSION}`, "x-compound-operator-csrf": CSRF, ...headers }, body, ...rest });
  }
  assert.equal(session.operatorSessionConfigured(), true);
  assert.equal(await session.authorizeOperatorSession(request()), null);
  const nativeRequest = new NextRequest(request());
  assert.equal(new URL(nativeRequest.url).hostname, "localhost", "exercise Next's actual loopback normalization");
  assert.equal(await session.authorizeOperatorSession(nativeRequest), null);
  assert.equal((await session.operatorSessionStatus(new NextRequest(request({ method: "GET" })))).status, 200);
  for (const headers of [
    { host: "localhost:3777" },
    { origin: ORIGIN.replace("127.0.0.1", "localhost") },
    { host: "127.0.0.2:3777" },
    { origin: ORIGIN.replace("127.0.0.1", "127.0.0.2") },
    { "x-compound-operator-csrf": "" },
  ]) assert.equal((await session.authorizeOperatorSession(new NextRequest(request({ headers }))))?.status, 403);
  const before = calls;
  for (const headers of [
    { origin: "http://console.invalid" },
    { host: "console.invalid" },
    { "x-forwarded-host": "console.invalid" },
    { "x-forwarded-proto": "https" },
    { "x-forwarded-port": "9000" },
    { forwarded: "host=console.invalid" },
    { "sec-fetch-site": "cross-site" },
    { "sec-fetch-site": "none" },
    { origin: "" },
    { "x-compound-operator-csrf": "" },
    { cookie: `compound_operator_session=${SESSION}; compound_operator_session=${SESSION}` },
  ]) assert.equal((await session.authorizeOperatorSession(request({ headers })))?.status, 403);
  assert.equal(calls, before, "untrusted browser requests must not reach native broker");
  const wrongPort = new URL("/api/decision/execute", ORIGIN);
  wrongPort.port = "9000";
  assert.equal((await session.authorizeOperatorSession(request({ url: wrongPort.href })))?.status, 403);
  assert.equal((await session.authorizeOperatorSession(request({ headers: { cookie: `compound_operator_session=${CAPABILITY}` } })))?.status, 403);
  assert.equal(consoleMutationBlockedResponse(request())?.status, 403, "new opaque cookie must never authenticate legacy capability extension");
  const paired = await session.pairOperatorSession(new NextRequest(request({ headers: { "content-type": "application/json" }, body: JSON.stringify({ code: PAIR }) })));
  assert.equal(paired.status, 200);
  const cookie = paired.headers.get("set-cookie");
  assert.match(cookie, /HttpOnly; SameSite=Strict; Path=\/; Max-Age=3600/);
  const pairedBody = await paired.text();
  for (const secret of [KEY, INSTANCE, SESSION, CAPABILITY, PAIR]) assert.equal(pairedBody.includes(secret), false, "pair JSON must not expose transport/session secrets");
  assert.equal(cookie.includes(CAPABILITY), false);
  assert.equal(cookie.includes(KEY), false);
  assert.equal((await session.pairOperatorSession(request({ headers: { "content-type": "application/json" }, body: JSON.stringify({ code: PAIR, origin: "http://console.invalid" }) }))).status, 400);
  assert.equal((await session.pairOperatorSession(request({ headers: { "content-type": "application/json" }, body: "x".repeat(1025) }))).status, 400);
  const status = await session.operatorSessionStatus(request({ method: "GET" }));
  const statusBody = await status.json();
  assert.equal(statusBody.connected, true);
  assert.equal(statusBody.csrf, CSRF);
  assert.equal(status.headers.get("cache-control"), "no-store");
  assert.equal(await session.operatorSessionReadiness(`compound_operator_session=${SESSION}`), true);
  const action = { actionId: "record_review", expectedRevision: "generic-action-v2:current", actionIntentId: "act_synthetic", expectedAuthorityRevision: "policy-authority-v1:sha256:" + "0".repeat(64) };
  assert.equal(session.operatorSessionActionBlockedResponse(action), null);
  for (const field of Object.keys(action)) assert.equal(session.operatorSessionActionBlockedResponse({ ...action, [field]: "" })?.status, 409);
  assert.equal(session.operatorSessionActionBlockedResponse({ area: "memory-adjudicate", itemId: "cand_synthetic", option: "promote" })?.status, 409);
  assert.equal(session.operatorSessionActionBlockedResponse({ ...action, preview: true })?.status, 409);
  const child = consoleChildEnv();
  for (const key of ["COMPOUND_CONSOLE_CAPABILITY", "COMPOUND_CONSOLE_OPERATOR_SESSION", "COMPOUND_CONSOLE_OPERATOR_SOCKET", "COMPOUND_CONSOLE_OPERATOR_BROKER_KEY", "COMPOUND_CONSOLE_OPERATOR_INSTANCE"]) assert.equal(key in child, false, "console subprocess inherited transport authority");
  // Import the real endpoint. Only the final executable effect is replaced;
  // stale identity uses the existing owner's actual authority comparison.
  const { mock } = await import("bun:test");
  const { actionAuthorityRequestError } = await import("../src/lib/decision-execute.ts");
  let executions = 0;
  mock.module("../src/lib/decision-execute.ts", () => ({ executeDecision: async (body) => {
    executions++;
    return actionAuthorityRequestError({ revision: action.expectedRevision, actionIntentId: action.actionIntentId, authorityRevision: action.expectedAuthorityRevision }, body) ?? Response.json({ ok: true });
  } }));
  const { POST } = await import("../src/app/api/decision/execute/route.ts");
  const unsupported = await POST(request({ headers: { "content-type": "application/json" }, body: JSON.stringify({ area: "memory-adjudicate", itemId: "cand_synthetic", option: "promote" }) }));
  assert.equal(unsupported.status, 409);
  assert.equal(executions, 0, "valid session must not admit unstamped mutation to execution owner");
  const wrongOrigin = await POST(request({ headers: { origin: "http://console.invalid", "content-type": "application/json" }, body: JSON.stringify(action) }));
  assert.equal(wrongOrigin.status, 403);
  assert.equal(executions, 0, "session fallback must not authorize wrong origin");
  const stale = await POST(request({ headers: { "content-type": "application/json" }, body: JSON.stringify({ ...action, expectedRevision: "generic-action-v2:stale" }) }));
  assert.equal(stale.status, 409, "existing owner must reject stale revision after session admission");
  assert.equal((await stale.json()).error_code, "authority_drift");
  const exact = await POST(new NextRequest(request({ headers: { "content-type": "application/json" }, body: JSON.stringify(action) })));
  assert.equal(exact.status, 200);
  assert.equal(executions, 2);
  const revoked = await session.revokeOperatorSession(request());
  assert.equal(revoked.status, 200);
  assert.match(revoked.headers.get("set-cookie"), /Max-Age=0/);
  assert.equal((await session.authorizeOperatorSession(request()))?.status, 403);
  assert.equal(await session.operatorSessionReadiness(`compound_operator_session=${SESSION}`), false);
  await new Promise((resolve) => server.close(resolve));
  assert.equal((await session.authorizeOperatorSession(request()))?.status, 403, "stopped native broker must fail closed");
  console.log("operator-session adapter smoke passed: origin, CSRF, opaque cookie, action tuple, revocation and subprocess isolation");
} finally {
  server.close();
  rmSync(dir, { recursive: true, force: true });
}

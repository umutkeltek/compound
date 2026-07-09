import assert from "node:assert/strict";

import {
  CONSOLE_MUTATIONS_ENV,
  CONSOLE_SESSION_TOKEN_COOKIE,
  CONSOLE_SESSION_TOKEN_ENV,
  CONSOLE_SESSION_TOKEN_HEADER,
  consoleMutationBlockedResponse,
  consolePrivateReadBlockedResponse,
} from "../src/lib/console-mutations.ts";

const LOCAL_ORIGIN = "http://localhost:3000";

function request(origin, host, token, options = {}) {
  const headers = new Headers();
  if (origin) headers.set("origin", origin);
  if (host) headers.set("host", host);
  if (token) headers.set(CONSOLE_SESSION_TOKEN_HEADER, token);
  if (options.cookie) {
    headers.set("cookie", `${CONSOLE_SESSION_TOKEN_COOKIE}=${encodeURIComponent(options.cookie)}`);
  }
  if (options.fetchSite) headers.set("sec-fetch-site", options.fetchSite);
  if (options.body) headers.set("content-type", "application/json");
  return new Request(options.url ?? new URL("/api/decision/execute", LOCAL_ORIGIN), {
    method: options.method ?? "POST",
    headers,
    body: options.body,
  });
}

/**
 * Same POST /api/decision/execute request the bespoke path uses, except the
 * body is generic-pathway-shaped ({area, itemId, actionId, confirmed} — see
 * generic-actions.ts / decision-execute.ts's executeGenericAction). The
 * route handler (src/app/api/decision/execute/route.ts) calls
 * consoleMutationBlockedResponse(request) BEFORE it ever parses the request
 * body, so the gate is body-shape-agnostic by construction: this proves that
 * invariant holds for the generic shape too, rather than assuming it because
 * it happens to hold for the bespoke shape above. If a future refactor moves
 * the gate check after body-parsing (and only re-adds it for one shape),
 * this goes red.
 */
function genericActionRequest(origin, host, token, options = {}) {
  return request(origin, host, token, {
    ...options,
    body: JSON.stringify({
      area: "job-health",
      itemId: "jhp_smoke",
      actionId: "record_job_health_review",
      confirmed: true,
    }),
  });
}

function readRequest(origin, host, token, options = {}) {
  return request(origin, host, token, {
    url: options.url ?? new URL("/api/document?path=/tmp/source.md", LOCAL_ORIGIN),
    method: "GET",
    fetchSite: options.fetchSite,
    cookie: options.cookie,
  });
}

const oldEnabled = process.env[CONSOLE_MUTATIONS_ENV];
const oldToken = process.env[CONSOLE_SESSION_TOKEN_ENV];

try {
  delete process.env[CONSOLE_MUTATIONS_ENV];
  delete process.env[CONSOLE_SESSION_TOKEN_ENV];

  let blocked = consoleMutationBlockedResponse(request("http://localhost:3000", "localhost:3000"));
  assert.equal(blocked?.status, 403);

  process.env[CONSOLE_MUTATIONS_ENV] = "1";
  blocked = consoleMutationBlockedResponse(request("https://example.com", "localhost:3000"));
  assert.equal(blocked?.status, 403);

  blocked = consoleMutationBlockedResponse(request("http://localhost:3000", "localhost:3000"));
  assert.equal(blocked, null);

  process.env[CONSOLE_SESSION_TOKEN_ENV] = "secret";
  blocked = consoleMutationBlockedResponse(request("http://localhost:3000", "localhost:3000"));
  assert.equal(blocked?.status, 403);

  blocked = consoleMutationBlockedResponse(request("http://localhost:3000", "localhost:3000", "secret"));
  assert.equal(blocked, null);

  blocked = consoleMutationBlockedResponse(
    request("http://localhost:3000", "localhost:3000", null, { cookie: "secret" }),
  );
  assert.equal(blocked, null);

  // The generic governed-action pathway (Phase 3a) is dispatched from the
  // SAME POST /api/decision/execute handler, so it must be gated identically
  // -- guard-off blocks it regardless of GENERIC_ACTION_ALLOWLIST, exactly
  // like the bespoke path above.
  delete process.env[CONSOLE_MUTATIONS_ENV];
  delete process.env[CONSOLE_SESSION_TOKEN_ENV];
  blocked = consoleMutationBlockedResponse(genericActionRequest("http://localhost:3000", "localhost:3000"));
  assert.equal(blocked?.status, 403, "generic-shaped request must 403 when COMPOUND_CONSOLE_MUTATIONS is unset");

  process.env[CONSOLE_MUTATIONS_ENV] = "1";
  blocked = consoleMutationBlockedResponse(genericActionRequest("https://example.com", "localhost:3000"));
  assert.equal(blocked?.status, 403, "generic-shaped cross-origin request must still 403 with guard on");

  blocked = consoleMutationBlockedResponse(genericActionRequest("http://localhost:3000", "localhost:3000"));
  assert.equal(blocked, null, "generic-shaped same-origin request must pass the gate once guard-on, exactly like the bespoke shape");

  process.env[CONSOLE_SESSION_TOKEN_ENV] = "secret";
  blocked = consoleMutationBlockedResponse(genericActionRequest("http://localhost:3000", "localhost:3000"));
  assert.equal(blocked?.status, 403, "generic-shaped request without the session token must still 403 once one is required");

  blocked = consoleMutationBlockedResponse(
    genericActionRequest("http://localhost:3000", "localhost:3000", "secret"),
  );
  assert.equal(blocked, null, "generic-shaped request with the right session token passes, identical to the bespoke shape");

  delete process.env[CONSOLE_MUTATIONS_ENV];
  delete process.env[CONSOLE_SESSION_TOKEN_ENV];

  blocked = consolePrivateReadBlockedResponse(readRequest("https://example.com", "localhost:3000"));
  assert.equal(blocked?.status, 403);

  blocked = consolePrivateReadBlockedResponse(readRequest(null, "localhost:3000", null, { fetchSite: "cross-site" }));
  assert.equal(blocked?.status, 403);

  blocked = consolePrivateReadBlockedResponse(readRequest(null, "localhost:3000"));
  assert.equal(blocked, null);

  process.env[CONSOLE_SESSION_TOKEN_ENV] = "secret";
  blocked = consolePrivateReadBlockedResponse(readRequest(null, "localhost:3000"));
  assert.equal(blocked?.status, 403);

  blocked = consolePrivateReadBlockedResponse(readRequest(null, "localhost:3000", "secret"));
  assert.equal(blocked, null);
  blocked = consolePrivateReadBlockedResponse(readRequest(null, "localhost:3000", null, { cookie: "secret" }));
  assert.equal(blocked, null);
} finally {
  if (oldEnabled === undefined) delete process.env[CONSOLE_MUTATIONS_ENV];
  else process.env[CONSOLE_MUTATIONS_ENV] = oldEnabled;

  if (oldToken === undefined) delete process.env[CONSOLE_SESSION_TOKEN_ENV];
  else process.env[CONSOLE_SESSION_TOKEN_ENV] = oldToken;
}

console.log("smoke opt-in, same-origin, and token gates ok (bespoke and generic-action request shapes alike)");

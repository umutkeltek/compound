import assert from "node:assert/strict";

import {
  CONSOLE_MUTATIONS_ENV,
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
  if (options.fetchSite) headers.set("sec-fetch-site", options.fetchSite);
  return new Request(options.url ?? new URL("/api/decision/execute", LOCAL_ORIGIN), {
    method: options.method ?? "POST",
    headers,
  });
}

function readRequest(origin, host, token, options = {}) {
  return request(origin, host, token, {
    url: options.url ?? new URL("/api/document?path=/tmp/source.md", LOCAL_ORIGIN),
    method: "GET",
    fetchSite: options.fetchSite,
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
} finally {
  if (oldEnabled === undefined) delete process.env[CONSOLE_MUTATIONS_ENV];
  else process.env[CONSOLE_MUTATIONS_ENV] = oldEnabled;

  if (oldToken === undefined) delete process.env[CONSOLE_SESSION_TOKEN_ENV];
  else process.env[CONSOLE_SESSION_TOKEN_ENV] = oldToken;
}

console.log("smoke opt-in, same-origin, and token gates ok");

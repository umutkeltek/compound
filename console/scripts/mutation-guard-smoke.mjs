import assert from "node:assert/strict";

import {
  CONSOLE_MUTATIONS_ENV,
  CONSOLE_SESSION_TOKEN_ENV,
  CONSOLE_SESSION_TOKEN_HEADER,
  consoleMutationBlockedResponse,
} from "../src/lib/console-mutations.ts";

function request(origin, host, token) {
  const headers = new Headers();
  if (origin) headers.set("origin", origin);
  if (host) headers.set("host", host);
  if (token) headers.set(CONSOLE_SESSION_TOKEN_HEADER, token);
  return new Request("http://localhost:3000/api/decision/execute", {
    method: "POST",
    headers,
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
} finally {
  if (oldEnabled === undefined) delete process.env[CONSOLE_MUTATIONS_ENV];
  else process.env[CONSOLE_MUTATIONS_ENV] = oldEnabled;

  if (oldToken === undefined) delete process.env[CONSOLE_SESSION_TOKEN_ENV];
  else process.env[CONSOLE_SESSION_TOKEN_ENV] = oldToken;
}

console.log("smoke opt-in, same-origin, and token gates ok");

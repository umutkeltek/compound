const LOCAL_HOSTS = new Set(["localhost", "127.0.0.1", "::1"]);

export const CONSOLE_MUTATIONS_ENV = "COMPOUND_CONSOLE_MUTATIONS";
export const CONSOLE_SESSION_TOKEN_ENV = "COMPOUND_CONSOLE_SESSION_TOKEN";
export const CONSOLE_SESSION_TOKEN_HEADER = "x-compound-console-token";
export const CONSOLE_SESSION_TOKEN_COOKIE = "compound_console_session_token";

function cleanHostname(hostname: string): string {
  return hostname.trim().toLowerCase().replace(/^\[/, "").replace(/\]$/, "");
}

function isLocalHostname(hostname: string): boolean {
  return LOCAL_HOSTS.has(cleanHostname(hostname));
}

function sameLocalOrigin(request: Request): boolean {
  const origin = request.headers.get("origin");
  const host = request.headers.get("host");
  if (!origin || !host) return false;

  try {
    const parsed = new URL(origin);
    return parsed.host === host && isLocalHostname(parsed.hostname);
  } catch {
    return false;
  }
}

function localOperatorRequest(request: Request): boolean {
  const origin = request.headers.get("origin");
  if (origin) return sameLocalOrigin(request);

  const host = request.headers.get("host");
  if (!host) return false;

  try {
    const parsed = new URL(request.url);
    return parsed.host === host && isLocalHostname(parsed.hostname);
  } catch {
    return false;
  }
}

function allowedFetchSite(request: Request): boolean {
  const site = request.headers.get("sec-fetch-site");
  return !site || site === "same-origin" || site === "none";
}

function cookieValue(request: Request, name: string): string | null {
  const raw = request.headers.get("cookie");
  if (!raw) return null;
  for (const part of raw.split(";")) {
    const index = part.indexOf("=");
    if (index < 0) continue;
    const key = part.slice(0, index).trim();
    if (key !== name) continue;
    return decodeURIComponent(part.slice(index + 1).trim());
  }
  return null;
}

function sessionTokenBlockedResponse(request: Request, label: string): Response | null {
  const expectedToken = process.env[CONSOLE_SESSION_TOKEN_ENV];
  const headerToken = request.headers.get(CONSOLE_SESSION_TOKEN_HEADER);
  const cookieToken = cookieValue(request, CONSOLE_SESSION_TOKEN_COOKIE);
  if (expectedToken && headerToken !== expectedToken && cookieToken !== expectedToken) {
    return Response.json(
      { ok: false, stderr: `${label} session token missing or invalid` },
      { status: 403 },
    );
  }
  return null;
}

export function consoleMutationBlockedResponse(request: Request): Response | null {
  if (process.env[CONSOLE_MUTATIONS_ENV] !== "1") {
    return Response.json(
      {
        ok: false,
        stderr:
          "console mutations disabled; set COMPOUND_CONSOLE_MUTATIONS=1 and use the local operator console",
      },
      { status: 403 },
    );
  }

  if (!sameLocalOrigin(request)) {
    return Response.json(
      { ok: false, stderr: "console mutations require same-origin localhost requests" },
      { status: 403 },
    );
  }

  const tokenBlocked = sessionTokenBlockedResponse(request, "console mutation");
  if (tokenBlocked) return tokenBlocked;

  return null;
}

export function consolePrivateReadBlockedResponse(request: Request): Response | null {
  if (!localOperatorRequest(request) || !allowedFetchSite(request)) {
    return Response.json(
      { ok: false, stderr: "console private reads require local operator console requests" },
      { status: 403 },
    );
  }

  return sessionTokenBlockedResponse(request, "console private read");
}

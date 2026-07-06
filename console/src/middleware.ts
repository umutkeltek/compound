import { NextResponse, type NextRequest } from "next/server";

import { CONSOLE_SESSION_TOKEN_COOKIE, CONSOLE_SESSION_TOKEN_ENV } from "@/lib/console-mutations";

const LOCAL_HOSTS = new Set(["localhost", "127.0.0.1", "::1"]);

function cleanHostname(hostname: string): string {
  return hostname.trim().toLowerCase().replace(/^\[/, "").replace(/\]$/, "");
}

function isLocalConsoleRequest(request: NextRequest): boolean {
  return LOCAL_HOSTS.has(cleanHostname(request.nextUrl.hostname));
}

export function middleware(request: NextRequest) {
  const response = NextResponse.next();
  const token = process.env[CONSOLE_SESSION_TOKEN_ENV]?.trim();
  if (!token || !isLocalConsoleRequest(request)) return response;

  if (request.cookies.get(CONSOLE_SESSION_TOKEN_COOKIE)?.value !== token) {
    response.cookies.set(CONSOLE_SESSION_TOKEN_COOKIE, token, {
      httpOnly: true,
      sameSite: "strict",
      secure: request.nextUrl.protocol === "https:",
      path: "/",
    });
  }
  return response;
}

export const config = {
  matcher: ["/((?!_next/static|_next/image|favicon.ico).*)"],
};

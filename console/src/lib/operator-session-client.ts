"use client";

export type BrowserOperatorSession = { enabled: boolean; connected: boolean; csrf?: string; expiresAt?: string };

export async function readBrowserOperatorSession(): Promise<BrowserOperatorSession> {
  const response = await fetch("/api/operator-session/status", { credentials: "same-origin", cache: "no-store" });
  if (!response.ok) throw new Error("The local operator connection is unavailable.");
  return response.json() as Promise<BrowserOperatorSession>;
}

/** Shared submit transport; no browser receives the server capability. The
 * native broker independently authorizes every POST after these display reads. */
export async function fetchWithOperatorSession(input: string, init: RequestInit): Promise<Response> {
  const status = await readBrowserOperatorSession();
  const headers = new Headers(init.headers);
  if (status.connected && status.csrf) headers.set("x-compound-operator-csrf", status.csrf);
  return fetch(input, { ...init, credentials: "same-origin", headers });
}

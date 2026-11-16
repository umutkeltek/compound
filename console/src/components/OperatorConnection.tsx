"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { fetchWithOperatorSession, readBrowserOperatorSession, type BrowserOperatorSession } from "@/lib/operator-session-client";

/** The code is deliberately requested in a local terminal, never issued by a
 * web page. Pairing changes transport identity; owner policy still decides. */
export function OperatorConnection() {
  const router = useRouter();
  const [session, setSession] = useState<BrowserOperatorSession | null>(null);
  const [open, setOpen] = useState(false);
  const [code, setCode] = useState("");
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState("");
  useEffect(() => {
    let current = true;
    const refresh = () => { void readBrowserOperatorSession().then((value) => { if (current) setSession(value); }).catch(() => { if (current) setSession(null); }); };
    refresh();
    const timer = window.setInterval(refresh, 60_000);
    window.addEventListener("focus", refresh);
    return () => { current = false; window.clearInterval(timer); window.removeEventListener("focus", refresh); };
  }, []);
  if (!session?.enabled) return null;
  async function submit(disconnect = false) {
    setPending(true); setMessage("");
    try {
      const response = disconnect
        ? await fetchWithOperatorSession("/api/operator-session/revoke", { method: "POST" })
        : await fetch("/api/operator-session/pair", { method: "POST", credentials: "same-origin", headers: { "content-type": "application/json" }, body: JSON.stringify({ code: code.trim() }) });
      const result = await response.json() as { ok?: boolean; stderr?: string };
      setCode("");
      if (!response.ok || !result.ok) throw new Error(result.stderr || "The operator connection could not be updated.");
      setSession(await readBrowserOperatorSession()); setOpen(false); router.refresh();
    } catch (error) { setMessage(error instanceof Error ? error.message : "The operator connection could not be updated."); }
    finally { setPending(false); }
  }
  return (
    <div className="relative text-xs">
      <button type="button" className="rounded-md border border-hair px-2 py-1.5 font-medium" onClick={() => setOpen(!open)} aria-expanded={open}>
        {session.connected ? "Operator connected" : "Connect operator"}
      </button>
      {open ? (
        <section className="absolute right-0 z-50 mt-2 w-80 rounded-lg border border-hair bg-panel p-4 shadow-lg" aria-label="Local operator connection">
          <p className="font-semibold">{session.connected ? "This browser is connected" : "Connect this local browser"}</p>
          <p className="mt-2 text-muted">{session.connected ? `Connection expires ${session.expiresAt ? new Date(session.expiresAt).toLocaleTimeString() : "within an hour"}. Each action still checks its current policy and your confirmation.` : "In a local terminal with the same Compound home, run compound console pair. Enter its one-use code here."}</p>
          {!session.connected ? (
            <form className="mt-3 flex flex-col gap-2" onSubmit={(event) => { event.preventDefault(); void submit(); }}>
              <label htmlFor="operator-pair-code" className="font-medium">Pairing code</label>
              <input id="operator-pair-code" type="password" autoComplete="off" value={code} onChange={(event) => setCode(event.target.value)} className="w-full rounded border border-hair bg-panel px-2 py-2" required />
              <button disabled={pending} type="submit" className="rounded border border-hair px-3 py-2 font-semibold">{pending ? "Connecting…" : "Connect"}</button>
            </form>
          ) : <button disabled={pending} type="button" onClick={() => { void submit(true); }} className="mt-3 rounded border border-hair px-3 py-2 font-semibold">Disconnect</button>}
          {message ? <p className="mt-2 text-crit" role="alert">{message}</p> : null}
        </section>
      ) : null}
    </div>
  );
}

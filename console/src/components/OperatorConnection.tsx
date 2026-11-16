"use client";

import { useEffect, useState, type ReactNode } from "react";
import { usePathname, useRouter } from "next/navigation";
import { fetchWithOperatorSession, readBrowserOperatorSession, type BrowserOperatorSession } from "@/lib/operator-session-client";

/**
 * The one sentence every page uses for what the console can do from the
 * browser. Today, Settings and this card used to give three different
 * instructions; they should all say this.
 */
export const CONSOLE_ACTS_SENTENCE =
  "The console shows and explains your apps, work and agents. Acting from it is being built; for now nothing you click here changes them.";

/**
 * Settings' last card: which Compound home this console reads (the instance
 * badge, moved here from the sidebar) and the browser connection, which left
 * the top bar. A
 * one-use code from a terminal on this computer connects this browser to the
 * local Compound. The connection is not proof that a click is the
 * operator's, so it unlocks nothing yet, and the card says so. The code is
 * requested in a local terminal, never issued by a web page; pairing changes
 * transport identity only, owner policy still decides.
 */
export function OperatorConnection({ instance }: { instance?: ReactNode } = {}) {
  const router = useRouter();
  const pathname = usePathname();
  const onSettings = pathname === "/settings";
  const [session, setSession] = useState<BrowserOperatorSession | null>(null);
  const [code, setCode] = useState("");
  const [pending, setPending] = useState(false);
  const [message, setMessage] = useState("");
  useEffect(() => {
    if (!onSettings) return;
    let current = true;
    const refresh = () => { void readBrowserOperatorSession().then((value) => { if (current) setSession(value); }).catch(() => { if (current) setSession(null); }); };
    refresh();
    const timer = window.setInterval(refresh, 60_000);
    window.addEventListener("focus", refresh);
    return () => { current = false; window.clearInterval(timer); window.removeEventListener("focus", refresh); };
  }, [onSettings]);
  if (!onSettings) return null;
  if (!session?.enabled) {
    return instance ? (
      <section className="mt-6 rounded-xl border border-hair bg-panel p-5 text-sm" aria-label="Where this console reads from" data-console-instance>
        <h2 className="text-base font-semibold text-ink">Where this console reads from</h2>
        <p className="mt-1 flex flex-wrap items-center gap-2 text-muted">Reads and shows the Compound home {instance}</p>
        <p className="mt-2 max-w-read text-muted">{CONSOLE_ACTS_SENTENCE}</p>
      </section>
    ) : null;
  }
  async function submit(disconnect = false) {
    setPending(true); setMessage("");
    try {
      const response = disconnect
        ? await fetchWithOperatorSession("/api/operator-session/revoke", { method: "POST" })
        : await fetch("/api/operator-session/pair", { method: "POST", credentials: "same-origin", headers: { "content-type": "application/json" }, body: JSON.stringify({ code: code.trim() }) });
      const result = await response.json() as { ok?: boolean; stderr?: string };
      setCode("");
      if (!response.ok || !result.ok) throw new Error(result.stderr || "The browser connection could not be changed.");
      setSession(await readBrowserOperatorSession()); router.refresh();
    } catch (error) { setMessage(error instanceof Error ? error.message : "The browser connection could not be changed."); }
    finally { setPending(false); }
  }
  const until = session.expiresAt ? new Date(session.expiresAt).toLocaleTimeString() : "";
  return (
    <section className="mt-6 rounded-xl border border-hair bg-panel p-5 text-sm" aria-label="Browser connection" data-browser-connection>
      <h2 className="text-base font-semibold text-ink">Browser connection</h2>
      {instance ? <p className="mt-1 flex flex-wrap items-center gap-2 text-muted">This console reads and shows the Compound home {instance}</p> : null}
      <p className="mt-1 max-w-read text-muted">
        {session.connected
          ? `This browser is connected to Compound on this computer${until ? ` until ${until}` : ""}. The connection is not used for anything yet.`
          : "This browser can be connected to Compound on this computer with a one-use code. The connection is not used for anything yet."}{" "}
        {CONSOLE_ACTS_SENTENCE}
      </p>
      {session.connected ? (
        <button disabled={pending} type="button" onClick={() => { void submit(true); }} className="mt-3 rounded-md border border-hair px-3 py-2 text-xs font-semibold hover:bg-soft">
          {pending ? "Disconnecting…" : "Disconnect this browser"}
        </button>
      ) : (
        <details className="mt-3 text-xs text-muted">
          <summary className="cursor-pointer">For agents: how to connect</summary>
          <p className="mt-2">Run <span className="mono">compound console pair</span> in a terminal on this computer with the same Compound home, then enter its one-use code here.</p>
          <form className="mt-3 flex max-w-sm flex-col gap-2" onSubmit={(event) => { event.preventDefault(); void submit(); }}>
            <label htmlFor="operator-pair-code" className="font-medium text-ink">One-use code</label>
            <input id="operator-pair-code" type="password" autoComplete="off" value={code} onChange={(event) => setCode(event.target.value)} className="w-full rounded border border-hair bg-panel px-2 py-2" required />
            <button disabled={pending} type="submit" className="rounded border border-hair px-3 py-2 font-semibold">{pending ? "Connecting…" : "Connect"}</button>
          </form>
        </details>
      )}
      {message ? <p className="mt-2 text-crit" role="alert">{message}</p> : null}
    </section>
  );
}

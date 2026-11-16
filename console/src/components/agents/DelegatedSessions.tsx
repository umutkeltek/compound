import Link from "next/link";

import { workItemHref } from "@/lib/links";
import { ago } from "@/lib/time";

import {
  delegatedSessionViews,
  type DelegatedSessionView,
  type DelegatedSessionCounts,
  type DelegatedSessionRow,
  type DelegatedTone,
} from "./executor-model";

/**
 * The delegated supervised-session board (Agents page). One safe row per work,
 * folded server-side from work.delegate.{spawned,steered,refused,exited} onto
 * `compound executors --json`, retaining the latest session per work+lane.
 * Compare → table doctrine: homogeneous columns
 * scanned top-to-bottom. Privacy law: this component only ever draws the
 * reduced fields the projection exposes — lane, session name, status, last
 * steer TIME, and the exact RunAttempt id/status. It NEVER shows prompt text,
 * pane content, argv, credentials, or provider env (none reach the row).
 */

const TONE_TEXT: Record<DelegatedTone, string> = {
  live: "text-work",
  done: "text-good",
  seam: "text-seam",
  faint: "text-faint",
};

const TONE_DOT: Record<DelegatedTone, string> = {
  live: "bg-work",
  done: "bg-good",
  seam: "bg-seam",
  faint: "bg-faint",
};

function SessionTable({ views }: { views: DelegatedSessionView[] }) {
  return (
    <div className="overflow-x-auto rounded-lg border border-hair bg-panel">
      <table className="w-full border-collapse text-sm">
        <thead>
          <tr className="border-b border-hair text-left text-2xs uppercase tracking-wide text-faint">
            <th className="px-3 py-2 font-bold">Status</th>
            <th className="px-3 py-2 font-bold">Work</th>
            <th className="px-3 py-2 font-bold">Lane</th>
            <th className="px-3 py-2 font-bold">Progress</th>
            <th className="px-3 py-2 font-bold">Detail</th>
            <th className="px-3 py-2 font-bold">Last steer</th>
            <th className="px-3 py-2 font-bold">Run</th>
          </tr>
        </thead>
        <tbody>
          {views.map((v) => (
            <tr key={`${v.workId}:${v.lane}`} className="border-b border-hair/60 last:border-0">
              <td className="whitespace-nowrap px-3 py-2">
                <span className={`inline-flex items-center gap-1.5 font-medium ${TONE_TEXT[v.tone]}`}>
                  <span className={`h-2 w-2 shrink-0 rounded-full ${TONE_DOT[v.tone]}`} aria-hidden />
                  {v.statusLabel}
                </span>
              </td>
              <td className="px-3 py-2"><Link href={workItemHref(v.workId)} className="mono text-2xs text-work hover:underline">{v.workId}</Link></td>
              <td className="px-3 py-2 text-2xs text-muted">{v.lane || "—"}</td>
              <td className="mono px-3 py-2 text-2xs text-muted">{v.progressLabel || "—"}</td>
              <td className="px-3 py-2 text-2xs text-muted">{v.detail || "—"}</td>
              <td className="whitespace-nowrap px-3 py-2 text-2xs text-faint">{v.lastSteerAt ? ago(v.lastSteerAt) : "—"}</td>
              <td className="mono px-3 py-2 text-2xs text-faint">{v.runAttemptLabel || "—"}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

/**
 * Sessions started by agents: the running ones and the latest few are shown,
 * every older one is folded behind its count, so hundreds of finished
 * sessions never push the page down.
 */
export function DelegatedSessions({
  rows,
  counts,
  latest = 10,
}: {
  rows: DelegatedSessionRow[];
  counts: DelegatedSessionCounts;
  /** how many finished sessions to show before folding the rest */
  latest?: number;
}) {
  const views = delegatedSessionViews(rows);
  const running = views.filter((v) => v.status === "running");
  const others = views.filter((v) => v.status !== "running");
  const shown = [...running, ...others.slice(0, latest)];
  const folded = others.slice(latest);
  return (
    <section className="flex flex-col gap-3">
      <div className="flex flex-wrap items-baseline justify-between gap-2">
        <h2 className="text-sm font-bold uppercase tracking-wide text-muted">Sessions started by agents</h2>
        <span className="text-2xs text-faint">
          {counts.running.toLocaleString("en-US")} running · {counts.exited.toLocaleString("en-US")} ended · {counts.refused.toLocaleString("en-US")} refused
        </span>
      </div>
      {views.length === 0 ? (
        <p className="text-sm text-muted">No agent has started a session yet.</p>
      ) : (
        <>
          <SessionTable views={shown} />
          {folded.length ? (
            <details className="text-sm">
              <summary className="cursor-pointer text-xs font-semibold text-muted">{folded.length.toLocaleString("en-US")} earlier sessions</summary>
              <div className="mt-2"><SessionTable views={folded} /></div>
            </details>
          ) : null}
        </>
      )}
    </section>
  );
}

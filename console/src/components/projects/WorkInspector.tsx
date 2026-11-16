"use client";
import Link from "next/link";
import { StateBadge, ReadNotice, ReadChip } from "@/components/kit";
import type { ReadView } from "@/components/kit/ReadState";
import type { ProjectWork } from "@/lib/project-workspace";

function References({ label, refs }: { label: string; refs: string[] }) {
  return <details className="border-t border-hair pt-3"><summary className="cursor-pointer text-sm font-medium text-ink">{label} <span className="text-muted">({refs.length})</span></summary>
    {refs.length ? <ul className="mt-2 max-h-64 space-y-2 overflow-auto">{refs.map((ref) => <li key={ref} className="break-all font-mono text-xs leading-relaxed text-muted">{ref}</li>)}</ul> : <p className="mt-2 text-xs text-muted">No references recorded in this work.</p>}
  </details>;
}
export function WorkInspector({ work, benchRead }: { work: ProjectWork | null; benchRead: ReadView }) {
  if (!work) return <aside className="rounded-xl border border-hair bg-soft p-5"><h2 className="font-semibold text-ink">Selected work</h2><p className="mt-2 text-sm text-muted">Choose a work item to follow its goal, next step and evidence.</p></aside>;
  return (
    <aside aria-label="Selected work" className="min-w-0 rounded-xl border border-hair bg-panel p-5 xl:sticky xl:top-5">
      <div className="flex flex-wrap items-center justify-between gap-2"><h2 className="text-xs font-semibold uppercase tracking-wide text-muted">Selected work</h2><StateBadge state={work.state} /></div>
      <h3 className="mt-3 line-clamp-3 text-lg font-semibold leading-snug text-ink">{work.title}</h3>
      <p className="mt-2 text-xs text-muted">Accountable owner: {work.owner || "not recorded"}</p>
      <div className="mt-5 space-y-4">
        <section><h4 className="text-xs font-semibold text-muted">Recorded goal</h4><p className="mt-1 text-sm leading-relaxed text-ink">{work.outcome || "No desired outcome recorded."}</p></section>
        <section><div className="flex flex-wrap items-center justify-between gap-2"><h4 className="text-xs font-semibold text-muted">Next step</h4><ReadChip read={benchRead} /></div>{work.next ? <p className="mt-1 text-sm leading-relaxed text-ink">{work.next}</p> : <><p className="mt-1 text-sm text-muted">No next-step summary is available in this read.</p>{benchRead.status !== "live" ? <div className="mt-2"><ReadNotice read={benchRead} label="next steps" /></div> : null}</>}</section>
        <section><h4 className="text-xs font-semibold text-muted">What proves success</h4><p className="mt-1 text-sm leading-relaxed text-ink">{work.success || "Success criteria were not recorded."}</p></section>
        <section className="rounded-lg bg-soft p-3"><h4 className="text-xs font-semibold text-muted">Result and proof</h4><p className="mt-1 text-sm text-ink">{work.receipt ? "A result record is attached." : "No result record is surfaced in these reads."}{work.transition ? " A recorded change is linked." : ""}</p><p className="mt-2 text-xs leading-relaxed text-muted">Attached references do not establish that checks passed. Open the work record to inspect its proof.</p></section>
        <p className="text-xs leading-relaxed text-muted">{work.attempts.length} execution attempt references. Live process activity has not been checked in this view.</p>
        <Link href={"/work/" + encodeURIComponent(work.id)} className="inline-flex rounded-lg bg-strong px-3 py-2 text-sm font-semibold text-panel">Open work record →</Link>
        <details className="border-t border-hair pt-3"><summary className="cursor-pointer text-sm font-medium text-ink">Context and permissions</summary><dl className="mt-3 space-y-2 text-xs text-muted"><dt className="font-semibold">Agent context reference</dt><dd className="break-all font-mono">{work.context || "No context reference recorded."}</dd><dt className="font-semibold">Permission decision reference</dt><dd className="break-all font-mono">{work.policy || "No decision reference recorded."}</dd></dl><p className="mt-3 text-xs leading-relaxed text-muted">These are recorded identities. Context contents and the current permission evaluation are not projected here.</p><Link className="mt-2 inline-block text-xs font-medium text-work" href="/settings?section=safety-permissions#settings-safety-permissions">Review permission settings →</Link></details>
        <References label="Supporting evidence" refs={work.evidence} />
        <References label="Verification references" refs={work.checks} />
        <References label="Execution attempts" refs={work.attempts} />
        <References label="Result references" refs={[work.receipt, work.transition].filter(Boolean)} />
        {work.boundaries.length ? <References label="Boundaries" refs={work.boundaries} /> : null}
        <details className="border-t border-hair pt-3"><summary className="cursor-pointer text-sm font-medium text-ink">Record details</summary><p className="mt-2 break-all font-mono text-xs text-muted">{work.id}</p><p className="mt-2 text-xs text-muted">Source: compound work list --json. Next step and result links may also come from the workbench read.</p></details>
      </div>
    </aside>
  );
}

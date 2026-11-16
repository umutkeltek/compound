import Link from "next/link";

import { PageHeader, EmptyState, hasCopy, ReadChip, ReadFailedBanner, ReadNotice, readsPending, StatusChip } from "@/components/kit";
import { PendingRefresh } from "@/components/PendingRefresh";
import { ForAgents } from "@/components/settings/primitives";
import { SourceIntakeButton } from "@/components/SourceIntakeButton";
import { readProjection } from "@/lib/compound";
import { countSourcesByStatus, unit } from "@/lib/counts";
import { consoleMutationsEnabled } from "@/lib/console-mutations";
import { computeConsoleMutationReadiness } from "@/lib/instance-pin";
import { appHref } from "@/lib/links";
import {
  compareSources,
  isSourceStatusKey,
  SOURCE_STALE_AFTER_HOURS,
  SOURCE_STATUS_ORDER,
  SOURCE_STATUS_WORDS,
  sourceView,
  type SourceView,
} from "@/lib/sources";

export const dynamic = "force-dynamic";

/*
 * Sources — where the agents' information comes from, and whether it is
 * current. One plain status per source, derived from the date the card shows
 * (lib/sources.ts), so a label never contradicts its date. The tiles count
 * those statuses, add up to the total, and filter the list (`?status=`, so
 * reload, Back and a pasted link keep it). The read is non-blocking: no copy
 * yet reads as "reading", a failed read as "could not read", never as zeros.
 *
 * Nothing on this page refreshes a source. Each card says in one sentence
 * what happens next and who handles it; the read-only commands that look at
 * a source sit behind "For agents". The intake-planning button renders only
 * when console readiness lets the browser act.
 */

const SOURCES_ARGS = ["source", "connections", "--json"];

function sourcesHref(status: string): string {
  return status ? `/sources?status=${encodeURIComponent(status)}#source-connections` : "/sources#source-connections";
}

function SourceCard({ view, mutationsEnabled }: { view: SourceView; mutationsEnabled: boolean }) {
  return (
    <article id={`source-${view.id}`} className="scroll-mt-6 rounded-lg border border-hair bg-panel p-4">
      <div className="flex flex-wrap items-start justify-between gap-2">
        <h3 className="text-sm font-semibold text-ink">{view.label}</h3>
        <StatusChip tone={view.status.tone} label={view.status.label} />
      </div>
      <p className="mt-1 text-xs text-muted">
        {view.status.detail}
        {view.rows !== null && view.rows > 0 ? ` ${unit(view.rows, "row")} on record.` : ""}
        {view.availability ? ` ${view.availability}.` : ""}
        {view.needsPrivateSetting && view.status.key !== "needs_setup" ? " Needs a private setting on this computer to refresh." : ""}
      </p>
      {view.produces.length ? (
        <p className="mt-1 text-xs text-muted">Brings in: {view.produces.slice(0, 4).join(", ")}.</p>
      ) : null}
      <p className="mt-2 text-xs leading-relaxed text-ink">{view.next}</p>
      {view.link ? (
        <Link href={view.link.href} className="mt-1 inline-flex text-xs font-semibold text-work hover:underline">{view.link.label} →</Link>
      ) : null}
      {mutationsEnabled && view.intakeCards.length ? (
        <div className="mt-2 flex flex-wrap gap-2">
          {view.intakeCards.map((card) => <SourceIntakeButton key={card.id} cardId={card.id} mutationsEnabled={mutationsEnabled} />)}
        </div>
      ) : null}
      <ForAgents label="For agents: look at this source (these commands do not refresh it)" cmds={view.inspectCommands}>
        <ul className="flex flex-col gap-0.5 text-2xs text-faint">
          {view.recordedNextStep ? <li>Recorded next step: {view.recordedNextStep}</li> : null}
          {view.intakeCards.map((card) => <li key={card.id}>Card: {card.title || "untitled"} <span className="mono">({card.id})</span></li>)}
          {view.sourceRef ? <li className="mono break-all">ref: {view.sourceRef}</li> : null}
          {view.sourceKinds.length ? <li className="mono break-all">kinds: {view.sourceKinds.join(", ")}</li> : null}
          <li className="mono">id: {view.id}</li>
        </ul>
      </ForAgents>
    </article>
  );
}

export default async function SourcesPage({ searchParams }: { searchParams?: Promise<{ status?: string }> }) {
  const requested = (await searchParams)?.status ?? "";
  const status = isSourceStatusKey(requested) ? requested : "";
  const read = await readProjection(SOURCES_ARGS);
  const mutationsEnabled = computeConsoleMutationReadiness(consoleMutationsEnabled()).ready;
  const nowMs = Date.now();
  const known = hasCopy(read);
  const connections = known && Array.isArray(read.value?.connections) ? (read.value!.connections as Record<string, unknown>[]) : [];
  const views = connections.map((connection) => sourceView(connection, nowMs, appHref)).sort(compareSources);
  const counts = known ? countSourcesByStatus(views) : null;
  const shown = status ? views.filter((view) => view.status.key === status) : views;

  const tile = (key: string, label: string, n: number | null) => {
    const active = status === key;
    return (
      <Link
        key={key || "all"}
        href={active && key ? sourcesHref("") : sourcesHref(key)}
        aria-current={active ? "true" : undefined}
        scroll={false}
        className={`flex flex-col gap-0.5 rounded-lg border px-3 py-2 transition-colors ${active ? "border-strong bg-soft-2" : "border-hair bg-panel hover:bg-soft"}`}
      >
        <span className="text-xl font-extrabold tabular-nums text-ink">{n === null ? "—" : n.toLocaleString("en-US")}</span>
        <span className="text-xs text-muted">{label}</span>
      </Link>
    );
  };

  return (
    <div className="flex flex-col gap-6">
      <PendingRefresh pending={readsPending(read)} stamp={nowMs} />
      <PageHeader
        name="Sources"
        lead="Where your agents' information comes from, and whether it is current. Nothing on this page refreshes a source."
        right={<ReadChip read={read} />}
      />

      <nav aria-label="Sources by status" className="grid grid-cols-2 gap-2 sm:grid-cols-4 lg:grid-cols-7">
        {tile("", "sources in all", counts ? counts.total : null)}
        {SOURCE_STATUS_ORDER.map((key) => tile(key, SOURCE_STATUS_WORDS[key].plural, counts ? (counts.byStatus[key] ?? 0) : null))}
      </nav>
      <p className="-mt-3 text-2xs text-faint">
        A source that brings in data regularly is out of date after {SOURCE_STALE_AFTER_HOURS} hours without new data. Imports happen once and are not judged by age.
      </p>

      <section id="source-connections" tabIndex={-1} className="scroll-mt-6 flex flex-col gap-3">
        {!known ? (
          <ReadNotice read={read} label="the source connections" />
        ) : (
          <>
            <ReadFailedBanner read={read} label="the source connections" />
            {shown.length ? (
              <div className="grid gap-3 lg:grid-cols-2">
                {shown.map((view) => <SourceCard key={view.id} view={view} mutationsEnabled={mutationsEnabled} />)}
              </div>
            ) : views.length ? (
              <EmptyState>
                No source is {SOURCE_STATUS_WORDS[status as keyof typeof SOURCE_STATUS_WORDS]?.plural ?? "in this group"}. <Link href={sourcesHref("")} className="font-semibold text-work hover:underline">Show every source →</Link>
              </EmptyState>
            ) : (
              <EmptyState>No sources are listed yet.</EmptyState>
            )}
          </>
        )}
      </section>
    </div>
  );
}

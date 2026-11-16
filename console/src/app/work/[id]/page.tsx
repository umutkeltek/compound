import { Suspense } from "react";
import Link from "next/link";
import { notFound } from "next/navigation";

import { WorkVisuals } from "@/components/products/VisualCampaigns";
import { hasCopy, PageHeader, ReadChip, ReadFailedBanner, ReadNotice, readsPending, StateBadge } from "@/components/kit";
import { PendingRefresh } from "@/components/PendingRefresh";
import { AgentHandoff } from "@/components/AgentHandoff";
import { EmptyState } from "@/components/ui";
import { WorkActionRow } from "@/components/work/WorkActionRow";
import { WorkBackLink } from "@/components/work/WorkBackLink";
import { readAttention } from "@/lib/attention";
import { arr, getWork, getWorkLeases, readProjection, readWorkProjectIndex, str, WORK_PROJECT_INDEX_READS } from "@/lib/compound";
import { consoleMutationsEnabled } from "@/lib/console-mutations";
import { agentDisplayName } from "@/lib/harness-label";
import { computeConsoleMutationReadiness } from "@/lib/instance-pin";
import { appHref, decideHref, evidenceHistoryHref, workGroupHref, workListHref } from "@/lib/links";
import { plainArea, plainState, plainWorkStep } from "@/lib/plain-words";
import { enrichWorkItem, readWorkbenchItem, packetForSurface, shortText } from "@/lib/work-data";
import { daysAgo, plainDay, workDates, workHistory } from "@/lib/work-history";
import { workNextStep } from "@/lib/work-next-step";
import { buildActionRow } from "@/lib/work-pipeline";
import { catalogFromGrowth, inventoryFromJson, resolveProject, type WorkProject } from "@/lib/work-project";
import { selectedContinueView, selectedWorkLease } from "@/lib/work-selected-continue";
import { isOnHold, STALE_AFTER_DAYS } from "@/lib/work-state";
import { workTitleFull } from "@/lib/work-title";

export const dynamic = "force-dynamic";

const HISTORY_SHOWN = 5;

function TextBlock({ title, body }: { title: string; body: string }) {
  return (
    <div>
      <div className="section-label mb-1">{title}</div>
      <p className="text-base leading-relaxed text-ink-2">{body || "Not recorded."}</p>
    </div>
  );
}

function BulletList({ items, empty }: { items: string[]; empty: string }) {
  if (!items.length) return <EmptyState>{empty}</EmptyState>;
  return (
    <ul className="flex flex-col gap-2">
      {items.map((item) => (
        <li key={item} className="rounded-lg bg-soft px-3 py-2 text-sm leading-relaxed text-ink-2">
          {item}
        </li>
      ))}
    </ul>
  );
}

/** Record ids, each a link to its evidence history page. */
function RecordLinks({ items, empty }: { items: string[]; empty: string }) {
  if (!items.length) return <EmptyState>{empty}</EmptyState>;
  return (
    <ul className="flex flex-col gap-1.5">
      {items.map((ref) => (
        <li key={ref}>
          <Link href={evidenceHistoryHref(ref)} className="mono break-all text-xs leading-relaxed text-work hover:underline">{ref}</Link>
        </li>
      ))}
    </ul>
  );
}

function RelatedItem({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <dt className="text-2xs font-semibold uppercase tracking-wide text-faint">{label}</dt>
      <dd className="min-w-0 text-sm text-ink">{children}</dd>
    </div>
  );
}

const PROJECT_KIND_LABEL: Record<string, string> = { app: "App", codebase: "Codebase", unlisted: "App", none: "App or codebase" };

/** A plain sentence for "why this work exists": a bare system word becomes everyday words. */
function plainWhy(raw: string): string {
  if (!raw) return "";
  return /^[a-z][a-z0-9_-]*$/.test(raw) ? plainState(raw) : plainWorkStep(raw);
}

export default async function WorkDetail({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const work = await getWork(id);
  if (!work) notFound();
  const nowMs = Date.now();
  const { apps: appArgs, inventory: inventoryArgs } = WORK_PROJECT_INDEX_READS;
  const [leases, workbench, projectIndex, appsRead, inventoryRead, attention] = await Promise.all([
    getWorkLeases(),
    readWorkbenchItem(id),
    readWorkProjectIndex({ nowMs }),
    // The same reads the index makes (shared cache entries): to place a
    // closed item, which the index of open work does not hold.
    readProjection([...appArgs.args], { timeoutMs: appArgs.timeoutMs }),
    readProjection([...inventoryArgs.args], { timeoutMs: inventoryArgs.timeoutMs }),
    readAttention(),
  ]);
  const { read: workbenchRead, item: workbenchItem } = workbench;
  const execution = selectedContinueView(work, selectedWorkLease(leases, id));
  const merged = await enrichWorkItem({ ...(workbenchItem ?? {}), ...work, work_id: id });
  const packet = merged.packet ?? (await packetForSurface(str(work.canonical_surface)));
  const state = str(work.state);
  const holder = str(workbenchItem?.claimed_by) || str(work.claimed_by) || str(work.owner);
  const evidence = arr(work.evidence_refs).map(String);
  const nonGoals = arr(work.non_goals).map(String);
  const ids = (work.ids ?? {}) as Record<string, unknown>;
  const receiptId = str(workbenchItem?.receipt_id) || str(work.receipt_id);
  const transitionId = str(workbenchItem?.transition_id) || str(ids.transition_id) || str(work.transition_id);
  const actionContext = ((merged.action_context ?? work.action_context ?? {}) || {}) as Record<string, unknown>;
  // The record's own reason (its lifecycle note) leads; the workbench's tool
  // instruction is not a reason and never overrides it.
  const nextStep = workNextStep({ ...work, claimed_by: holder });
  const dates = workDates(work, { nowMs, claimedAt: workbenchItem?.claimed_at });
  const history = workHistory(work);
  const whyExists = plainWhy(
    str(actionContext.title) || str(actionContext.action_summary) || str(work.review_trigger) || str(merged.open_reason) || str(work.open_reason),
  );
  const expectedProof = str(actionContext.proof) || str(work.success_predicate) || str(merged.success_predicate);

  // Which app or codebase: the shared index for open work (the same answer as
  // the List and "By app"); the resolver over the same catalog for the rest.
  const indexed = projectIndex.index?.byWork[id];
  const catalog = appsRead.value ? catalogFromGrowth(appsRead.value) : null;
  const inventory = inventoryRead.value ? inventoryFromJson(inventoryRead.value) : null;
  const project: WorkProject | null = indexed
    ? (() => {
        const group = projectIndex.index!.projects.find((p) => p.id === indexed);
        return group ? { id: group.id, name: group.name, kind: group.kind, via: "none" as const } : null;
      })()
    : catalog && inventory ? resolveProject({ ...(workbenchItem ?? {}), ...work }, catalog, inventory) : null;
  const holdingDecision = isOnHold(state)
    ? attention.report?.groups.find((group) => group.item_ids.includes(id)) ?? null
    : null;
  const records = [...evidence, receiptId, transitionId, ...execution.runAttemptIds, execution.contextPackId].filter(Boolean);

  const actionRow = buildActionRow(id, state);
  const mutationsReady = computeConsoleMutationReadiness(consoleMutationsEnabled()).ready;
  const pickedUp = dates.pickedUpMs !== null
    ? `Picked up ${plainDay(dates.pickedUpMs)} (${daysAgo(dates.pickedUpMs, nowMs)})`
    : ["proposed", "suggested", "admitted", "ready"].includes(state) ? "Not picked up yet" : "Pick-up time not recorded";

  return (
    <div className="flex flex-col gap-6 pb-16">
      <PendingRefresh pending={readsPending(workbenchRead, projectIndex.reads.apps, projectIndex.reads.inventory)} stamp={nowMs} />
      <WorkBackLink />
      {/* No header button: nothing on this page changes the work, and the Work
          list's own lead and action do not describe one item. */}
      <PageHeader name="Work" title={workTitleFull(work)} lead="" action={null}
        right={<><StateBadge state={state} label={plainState(state || "unknown")} /><span>{nextStep.owner === "No agent" ? "No agent has it" : `Held by ${nextStep.owner}`}</span></>} />

      <section className="flex flex-col gap-4 rounded-xl border border-hair bg-panel p-5" aria-label="When and what it is connected to">
        <p className="flex flex-wrap gap-x-2 gap-y-1 text-sm text-muted">
          <span>{dates.createdMs !== null ? `Created ${plainDay(dates.createdMs)}` : "Creation date not recorded"}</span>
          <span className="text-faint">·</span>
          <span className={dates.stale ? "font-semibold text-crit" : undefined}>{pickedUp}</span>
          <span className="text-faint">·</span>
          <span>{dates.changedMs !== null ? `Last changed ${plainDay(dates.changedMs)} (${daysAgo(dates.changedMs, nowMs)})` : "No change recorded"}</span>
        </p>
        {dates.stale ? (
          <p className="w-fit rounded-md border border-crit-line bg-crit-soft px-2.5 py-1 text-xs font-semibold text-crit">
            In progress for more than {STALE_AFTER_DAYS} days without finishing.
          </p>
        ) : null}
        <dl className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          <RelatedItem label={project ? PROJECT_KIND_LABEL[project.kind] ?? "App" : "App"}>
            {project ? (
              project.kind === "app" ? (
                <span className="flex flex-col"><Link href={appHref(project.id)} className="font-medium text-work hover:underline">{project.name}</Link>
                  <Link href={workGroupHref(project)} className="text-xs text-muted hover:underline">All its open work</Link></span>
              ) : project.kind === "none" ? (
                <span className="flex flex-col"><span>Names no app or codebase</span>
                  <Link href={workGroupHref(project)} className="text-xs text-muted hover:underline">Other work like this</Link></span>
              ) : (
                <span className="flex flex-col"><Link href={workGroupHref(project)} className="font-medium text-work hover:underline">{project.name}</Link>
                  {project.kind === "unlisted" ? <span className="text-xs text-muted">Not in your app list</span> : null}</span>
              )
            ) : (
              <span className="text-muted">{projectIndex.status === "unreachable" ? "Could not read your app list" : "Still reading which app this is for"}</span>
            )}
          </RelatedItem>
          <RelatedItem label="Agent">
            {holder ? (
              <span className="flex flex-col"><Link href={workListHref({ filter: "all", owner: holder })} className="font-medium text-work hover:underline">{agentDisplayName(holder)}</Link>
                <span className="mono truncate text-2xs text-faint" title={holder}>{holder}</span></span>
            ) : <span className="text-muted">No agent has it</span>}
          </RelatedItem>
          {isOnHold(state) ? (
            <RelatedItem label="Decision">
              {holdingDecision ? (
                <Link href={decideHref({ decision: holdingDecision.key })} className="font-medium text-work hover:underline">
                  Listed in Decide: {plainArea(holdingDecision.area)}
                </Link>
              ) : (
                <span className="text-muted">{attention.report ? "Not listed in Decide" : "Still reading Decide"}</span>
              )}
            </RelatedItem>
          ) : null}
          <RelatedItem label="Evidence">
            {records.length ? (
              <span className="flex flex-col">
                {records.slice(0, 2).map((ref) => (
                  <Link key={ref} href={evidenceHistoryHref(ref)} className="mono truncate text-xs text-work hover:underline" title={ref}>{ref}</Link>
                ))}
                {records.length > 2 ? <a href="#work-technical" className="text-xs text-muted hover:underline">{records.length - 2} more below</a> : null}
              </span>
            ) : <span className="text-muted">No evidence recorded yet</span>}
          </RelatedItem>
        </dl>
      </section>

      <section id="work-next-step" className="rounded-xl border border-hair bg-panel p-5">
        <div className="text-xs font-semibold text-muted">{nextStep.recorded ? "Recorded reason" : "Where it stands"}</div>
        <h2 className="mt-1 text-lg font-semibold text-ink">{nextStep.title}</h2>
        <p className="mt-3 max-w-read whitespace-pre-line text-sm leading-relaxed text-ink">{nextStep.reason}</p>
        {nextStep.next ? <p className="mt-2 max-w-read text-sm leading-relaxed text-muted">{nextStep.next}</p> : null}
      </section>

      <section className="rounded-xl border border-hair bg-panel p-5" aria-label="History">
        <h2 className="mb-3 text-base font-semibold text-ink">History</h2>
        {history.length ? (
          <>
            <ol className="flex flex-col gap-2">
              {history.slice(0, HISTORY_SHOWN).map((entry, index) => <HistoryRow key={index} entry={entry} nowMs={nowMs} />)}
            </ol>
            {history.length > HISTORY_SHOWN ? (
              <details className="mt-3 text-sm">
                <summary className="cursor-pointer font-medium text-muted">Show all {history.length} changes</summary>
                <ol className="mt-2 flex flex-col gap-2">
                  {history.slice(HISTORY_SHOWN).map((entry, index) => <HistoryRow key={index} entry={entry} nowMs={nowMs} />)}
                </ol>
              </details>
            ) : null}
          </>
        ) : <EmptyState>No changes are recorded for this item.</EmptyState>}
      </section>

      {arr(work.artifact_ids).some((ref) => String(ref).startsWith("visual-campaign:")) ? <Suspense fallback={<p className="text-sm text-muted">Loading visual outputs…</p>}><WorkVisuals workId={id} /></Suspense> : null}

      <div className="grid gap-4 lg:grid-cols-2">
        <section className="rounded-xl border border-hair bg-panel p-5">
          <h2 className="mb-2 text-base font-semibold text-ink">What success looks like</h2>
          <p className="text-sm leading-relaxed text-muted">{expectedProof || "Not recorded."}</p>
        </section>
        {["claimed", "running", "in_progress", "verifying"].includes(state) ? (
          <section className="rounded-xl border border-hair bg-panel p-5">
            <h2 className="mb-2 text-base font-semibold text-ink">Can an agent run it now?</h2>
            <p className="text-sm font-medium text-ink">{execution.status}</p>
            <p className="mt-2 text-sm leading-relaxed text-muted">{execution.detail}</p>
          </section>
        ) : null}
      </div>
      <details className="rounded-xl border border-hair bg-panel p-5">
        <summary className="cursor-pointer text-sm font-semibold text-ink">Why it exists and what it must not do</summary>
        <div className="mt-4 grid gap-4"><TextBlock title="Why this work exists" body={whyExists} />
          <BulletList items={[...(packet?.boundaries ?? []), ...nonGoals]} empty="No limits were recorded for this work." />
        </div>
      </details>
      <details id="work-technical" className="rounded-xl border border-hair bg-panel p-5">
        <summary className="cursor-pointer text-sm font-semibold text-ink">For agents: commands and technical record</summary>
        <div className="mt-4 grid gap-5">
          <p className="text-sm text-muted">Nothing here changes the work from this page. These are for an agent to copy.</p>
          <WorkActionRow data={actionRow} mutationsReady={mutationsReady} dense />
          {!["completed", "failed", "cancelled"].includes(state) ? (
            <div className="max-w-read"><AgentHandoff title={workTitleFull(work)} context={nextStep.reason} reference={id} /></div>
          ) : null}
          <ReadChip read={workbenchRead} />
          {hasCopy(workbenchRead)
            ? <ReadFailedBanner read={workbenchRead} label="supplemental work details" />
            : <ReadNotice read={workbenchRead} label="supplemental work details" />}
          <RecordLinks items={evidence} empty="No evidence references have been attached yet." />
          <TextBlock title="Work id" body={id} />
          {receiptId ? <div><div className="section-label mb-1">Result record</div><RecordLinks items={[receiptId]} empty="" /></div> : null}
          {transitionId ? <div><div className="section-label mb-1">Recorded change</div><RecordLinks items={[transitionId]} empty="" /></div> : null}
          {execution.selection ? <TextBlock title="Selected tool and model" body={execution.selection} /> : null}
          {execution.contextPackId ? <div><div className="section-label mb-1">What the agent was given</div><RecordLinks items={[execution.contextPackId]} empty="" /></div> : null}
          <div><div className="section-label mb-1">Run attempts</div><RecordLinks items={execution.runAttemptIds} empty="No run attempts were recorded." /></div>
          {packet?.path ? <TextBlock title="Task source" body={shortText(packet.path, 260)} /> : null}
        </div>
      </details>
    </div>
  );
}

function HistoryRow({ entry, nowMs }: { entry: ReturnType<typeof workHistory>[number]; nowMs: number }) {
  return (
    <li className="rounded-lg bg-soft px-3 py-2 text-sm">
      <div className="flex flex-wrap items-baseline gap-x-2">
        <span className="font-medium text-ink">{entry.what}</span>
        {entry.who ? <span className="text-muted">by {entry.who}</span> : null}
        <span className="ml-auto text-xs tabular-nums text-faint">{entry.atMs !== null ? `${plainDay(entry.atMs)} · ${daysAgo(entry.atMs, nowMs)}` : "time not recorded"}</span>
      </div>
      {entry.note ? <p className="mt-1 text-sm leading-relaxed text-muted">{entry.note}</p> : null}
    </li>
  );
}

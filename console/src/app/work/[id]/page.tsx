import Link from "next/link";
import { PageHeader } from "@/components/kit";
import { AgentHandoff } from "@/components/AgentHandoff";
import { workNextStep } from "@/lib/work-next-step";
import { notFound } from "next/navigation";

import { EmptyState, StateBadge } from "@/components/ui";
import { WorkDetailActions } from "@/components/work/WorkDetailActions";
import { arr, getWork, getWorkLeases, str } from "@/lib/compound";
import { consoleMutationsEnabled } from "@/lib/console-mutations";
import { computeConsoleMutationReadiness } from "@/lib/instance-pin";
import { buildActionRow } from "@/lib/work-pipeline";
import {
  enrichWorkItem,
  findWorkbenchItem,
  packetForSurface,
  shortText,
} from "@/lib/work-data";
import { selectedContinueView, selectedWorkLease } from "@/lib/work-selected-continue";

export const dynamic = "force-dynamic";

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

function CodeList({ items, empty }: { items: string[]; empty: string }) {
  if (!items.length) return <EmptyState>{empty}</EmptyState>;
  return (
    <ul className="flex flex-col gap-1.5">
      {items.map((ref) => (
        <li key={ref} className="mono break-all text-xs leading-relaxed text-ink-2">
          {ref}
        </li>
      ))}
    </ul>
  );
}

export default async function WorkDetail({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const work = await getWork(id);
  if (!work) notFound();
  const leases = await getWorkLeases();
  const execution = selectedContinueView(work, selectedWorkLease(leases, id));

  const workbenchItem = await findWorkbenchItem(id);
  const merged = await enrichWorkItem({ ...(workbenchItem ?? {}), ...work, work_id: id });
  const packet = merged.packet ?? (await packetForSurface(str(work.canonical_surface)));
  const state = str(work.state);
  const evidence = arr(work.evidence_refs).map(String);
  const nonGoals = arr(work.non_goals).map(String);
  const ids = (work.ids ?? {}) as Record<string, unknown>;
  const receiptId = str(workbenchItem?.receipt_id) || str(work.receipt_id);
  const transitionId = str(workbenchItem?.transition_id) || str(ids.transition_id) || str(work.transition_id);
  const actionContext = ((merged.action_context ?? work.action_context ?? {}) || {}) as Record<string, unknown>;
  const nextStep = workNextStep(work);
  const whyExists =
    str(actionContext.title) ||
    str(actionContext.action_summary) ||
    str(work.review_trigger) ||
    str(merged.open_reason) ||
    str(work.open_reason) ||
    str(work.canonical_surface) ||
    "This work was created from Compound-owned evidence or a reviewed action.";
  const expectedProof =
    str(actionContext.proof) ||
    str(work.success_predicate) ||
    str(merged.success_predicate) ||
    "Proof expectation has not been surfaced for this work item.";

  const actionRow = buildActionRow(id, state);
  const mutationsReady = computeConsoleMutationReadiness(consoleMutationsEnabled()).ready;

  return (
    <div className="flex flex-col gap-6">
      <Link href="/work" className="text-sm font-medium text-muted hover:text-ink">← All work</Link>
      <PageHeader name="Work" title={packet?.title || str(work.desired_outcome) || "Work details"}
        action={{ label: "See next step", href: "#work-next-step" }}
        right={<><StateBadge state={state} /><span>Assigned to {nextStep.owner}</span></>} />

      <section id="work-next-step" tabIndex={-1} className="scroll-mt-6 rounded-xl border border-hair bg-panel p-5">
        <div className="text-xs font-semibold text-muted">Next step</div>
        <h2 className="mt-1 text-lg font-semibold text-ink">{nextStep.title}</h2>
        <p className="mt-3 max-w-read whitespace-pre-line text-sm leading-relaxed text-ink">{nextStep.reason}</p>
        <p className="mt-2 max-w-read text-sm leading-relaxed text-muted">{nextStep.next}</p>
        <div className="mt-4 border-t border-hair pt-4">
          <WorkDetailActions workId={id} actionRow={actionRow} mutationsReady={mutationsReady} />
        </div>
        {!["completed", "failed", "cancelled"].includes(state) ? <details className="mt-4 border-t border-hair pt-4 text-sm">
          <summary className="cursor-pointer font-medium text-ink">Continue with an agent</summary>
          <div className="mt-3 max-w-read"><AgentHandoff title={str(work.desired_outcome) || packet?.title || id} context={nextStep.reason} reference={id} /></div>
        </details> : null}
      </section>

      <div className="grid gap-4 lg:grid-cols-2">
        <section className="rounded-xl border border-hair bg-panel p-5">
          <h2 className="mb-2 text-base font-semibold text-ink">What success looks like</h2>
          <p className="text-sm leading-relaxed text-muted">{expectedProof}</p>
        </section>
        <section className="rounded-xl border border-hair bg-panel p-5">
          <h2 className="mb-2 text-base font-semibold text-ink">Execution status</h2>
          <p className="text-sm font-medium text-ink">{execution.status}</p>
          <p className="mt-2 text-sm leading-relaxed text-muted">{execution.detail}</p>
        </section>
      </div>
      <details className="rounded-xl border border-hair bg-panel p-5">
        <summary className="cursor-pointer text-sm font-semibold text-ink">Background and boundaries</summary>
        <div className="mt-4 grid gap-4"><TextBlock title="Why this work exists" body={whyExists} />
          <BulletList items={[...(packet?.boundaries ?? []), ...nonGoals]} empty="No additional boundaries were recorded for this task." />
        </div>
      </details>
      <details className="rounded-xl border border-hair bg-panel p-5">
        <summary className="cursor-pointer text-sm font-semibold text-ink">Evidence and technical details</summary>
        <div className="mt-4 grid gap-4">
          <CodeList items={evidence} empty="No evidence references have been attached yet." />
          <TextBlock title="Task reference" body={id} />
          {receiptId ? <TextBlock title="Result reference" body={receiptId} /> : null}
          {transitionId ? <TextBlock title="Recorded change" body={transitionId} /> : null}
          {execution.selection ? <TextBlock title="Selected agent and model" body={execution.selection} /> : null}
          {execution.contextPackId ? <TextBlock title="Context reference" body={execution.contextPackId} /> : null}
          <CodeList items={execution.runAttemptIds} empty="No execution attempts were recorded." />
          {packet?.path ? <TextBlock title="Task source" body={shortText(packet.path, 260)} /> : null}
        </div>
      </details>
    </div>
  );
}

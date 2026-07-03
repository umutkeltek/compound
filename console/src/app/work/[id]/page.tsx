import { notFound } from "next/navigation";

import { DecisionForm } from "@/components/DecisionForm";
import { Card, Chip, EmptyState, SectionLabel, StateBadge } from "@/components/ui";
import { arr, getWork, str } from "@/lib/compound";
import { specFor } from "@/lib/decision-registry";
import {
  enrichWorkItem,
  findWorkbenchItem,
  packetForSurface,
  shortText,
} from "@/lib/work-data";

export const dynamic = "force-dynamic";

export default async function WorkDetail({ params }: { params: Promise<{ id: string }> }) {
  const { id } = await params;
  const work = await getWork(id);
  if (!work) notFound();

  const workbenchItem = await findWorkbenchItem(id);
  const merged = await enrichWorkItem({ ...(workbenchItem ?? {}), ...work, work_id: id });
  const packet = merged.packet ?? (await packetForSurface(str(work.canonical_surface)));
  const state = str(work.state);
  const evidence = arr(work.evidence_refs).map(String);
  const nonGoals = arr(work.non_goals).map(String);
  const ids = (work.ids ?? {}) as Record<string, unknown>;
  const receiptId = str(workbenchItem?.receipt_id) || str(work.receipt_id);
  const transitionId = str(workbenchItem?.transition_id) || str(ids.transition_id) || str(work.transition_id);

  const workSpec = specFor("work-advance");
  const lifecycleOptions = workSpec?.optionsFor ? workSpec.optionsFor(merged) : [];
  const lifecycleFormSpec = workSpec
    ? {
        area: workSpec.area,
        options: lifecycleOptions,
        fields: workSpec.fields,
        defaultOption: workSpec.defaultOption ?? lifecycleOptions[0]?.value,
      }
    : null;

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-start justify-between gap-4">
        <div>
          <div className="mb-2 flex flex-wrap items-center gap-2">
            <StateBadge state={state} />
            {str(work.owner) ? <Chip tone="violet">{str(work.owner)}</Chip> : null}
            {str(work.claimed_by) ? <Chip tone="blue">{str(work.claimed_by)}</Chip> : null}
          </div>
          <h1 className="max-w-4xl text-[22px] font-semibold leading-tight tracking-tight">
            {packet?.title || str(work.canonical_surface) || id}
          </h1>
          <p className="mt-1 mono text-ink-3">{id}</p>
        </div>
      </div>

      <div className="grid gap-4 lg:grid-cols-[1fr_340px]">
        <div className="flex flex-col gap-4">
          <Card title="Objective" accent="blue" glyph="O">
            <p className="text-[13px] leading-relaxed text-ink-2">
              {packet?.objective || str(work.desired_outcome) || "No objective surfaced."}
            </p>
          </Card>

          <Card title="Success" accent="green" glyph="S">
            <p className="text-[13px] leading-relaxed text-ink-2">
              {packet?.success || str(work.success_predicate) || "No success predicate surfaced."}
            </p>
          </Card>

          <Card title="Boundaries" accent="orange" glyph="B">
            {(packet?.boundaries.length || nonGoals.length) ? (
              <ul className="flex flex-col gap-2">
                {[...(packet?.boundaries ?? []), ...nonGoals].map((boundary) => (
                  <li
                    key={boundary}
                    className="rounded-lg bg-slate-50 px-3 py-2 text-[12.5px] leading-relaxed text-ink-2"
                  >
                    {boundary}
                  </li>
                ))}
              </ul>
            ) : (
              <EmptyState>No boundaries found.</EmptyState>
            )}
          </Card>
        </div>

        <div className="flex flex-col gap-4">
          <Card title="Lifecycle" accent="orange" glyph="L">
            {lifecycleFormSpec && lifecycleOptions.length > 0 ? (
              <DecisionForm
                formSpec={lifecycleFormSpec}
                itemId={id}
                argvPreview={lifecycleOptions[0]?.argv ?? null}
              />
            ) : (
              <EmptyState>Terminal — no further transitions.</EmptyState>
            )}
          </Card>

          <Card title="Evidence" accent="violet" glyph="E">
            {evidence.length ? (
              <ul className="flex flex-col gap-1.5">
                {evidence.map((ref) => (
                  <li key={ref} className="mono break-all text-[11.5px] leading-relaxed text-ink-2">
                    {ref}
                  </li>
                ))}
              </ul>
            ) : (
              <EmptyState>No evidence refs surfaced.</EmptyState>
            )}
          </Card>

          <Card title="Receipts" accent="green" glyph="R">
            {receiptId || transitionId ? (
              <div className="flex flex-col gap-2">
                {receiptId ? (
                  <div>
                    <div className="section-label mb-1">Receipt</div>
                    <div className="mono break-all text-ink-2">{receiptId}</div>
                  </div>
                ) : null}
                {transitionId ? (
                  <div>
                    <div className="section-label mb-1">Transition</div>
                    <div className="mono break-all text-ink-2">{transitionId}</div>
                  </div>
                ) : null}
              </div>
            ) : (
              <EmptyState>No terminal receipt yet.</EmptyState>
            )}
          </Card>

          {packet?.path ? (
            <div>
              <SectionLabel>Packet Source</SectionLabel>
              <p className="mono break-all text-[11.5px] leading-relaxed text-ink-3">
                {shortText(packet.path, 260)}
              </p>
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}

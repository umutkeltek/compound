import { hasCopy, PageHeader, ReadChip, ReadNotice, readsPending } from "@/components/kit";
import { PendingRefresh } from "@/components/PendingRefresh";
import { arr, HEAVY_PROJECTION_TIMEOUT_MS, readProjection, str } from "@/lib/compound";
import { workTitle } from "@/lib/work-title";
import { RepresentationRack, type Evidence } from "@/representation";

export const dynamic = "force-dynamic";

/**
 * Representation — the live consumer of the representation cluster (packet
 * 2026-07-11-representation-cluster-v1). Maps the workbench projection into
 * normalized `Evidence` and lets the cluster choose a shape per item, so the
 * six-shape vocabulary is exercised on REAL projection data (not a fixture).
 *
 * DEVIATION (documented in the handoff): the packet names home/work as the
 * mount. Those surfaces are being edited by live parallel lanes
 * (operator-language-surface, ask-triage, W0/1A); a self-owned route is the
 * zero-collision live consumer. Lifting the rack into the contested home/work
 * board — and wiring the act affordance through the governed path — is left as
 * an orchestrator landing-gate decision (acts stay read-only here: no button
 * that looks live but does nothing, which would re-introduce pf1).
 */
export default async function RepresentationPage() {
  // Non-blocking: the same cached copy the Work page reads, or "reading".
  const read = await readProjection(["workbench", "--json"], { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS });
  const workbench = read.value;
  const open = arr(workbench?.open);

  const evidence: Evidence[] = open.slice(0, 24).map((raw): Evidence => {
    const row = (raw ?? {}) as Record<string, unknown>;
    const receiptRefs = arr(row.receipt_refs).map(String).filter(Boolean);
    return {
      id: str(row.id) || str(row.work_id) || "work_unknown",
      // The work's plain title (lib/work-title.ts), never its id; the id stays on the card as small text.
      subject: str(row.subject) || workTitle(row),
      state: str(row.state),
      expected_state: str(row.expected_state) || undefined,
      observed_state: str(row.observed_state) || undefined,
      success_predicate: str(row.success_predicate) || str(row.desired_outcome) || undefined,
      receipt_refs: receiptRefs,
      last_receipt_at: str(row.updated_at) || undefined,
      summary: str(row.desired_outcome) || str(row.title) || undefined,
      // Read-only in v1: no act is dispatched from this route yet (see header).
      action: null,
    };
  });

  return (
    <div className="flex flex-col gap-6">
      <PendingRefresh pending={readsPending(read)} stamp={Date.now()} />
      <PageHeader name="View library" right={<ReadChip read={read} />} />
      <p className="text-sm text-muted" style={{ maxWidth: "var(--read-max)" }}>
        A workbench for the agents that build this console: it shows the shapes the console can use to explain a work
        item, drawn on your open work. It is not a list of views you can choose, and nothing here changes anything.
      </p>
      {hasCopy(read) ? <RepresentationRack evidence={evidence} /> : <ReadNotice read={read} label="open work" />}
    </div>
  );
}

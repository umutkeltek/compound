import { PageHeader } from "@/components/kit";
import { LineageSearch } from "@/components/map/LineageSearch";
import { Card, FreshnessChip } from "@/components/ui";

export const dynamic = "force-dynamic";

export default function LineageIndex() {
  return (
    <div className="flex flex-col gap-6">
      <PageHeader name="Map" title="Evidence history" lead="Trace where a record came from and which decisions use it."
        action={{ label: "Find a record", href: "#lineage-search" }} right={<FreshnessChip source="compound system lineage --json" />} />
      <div id="lineage-search" tabIndex={-1} className="scroll-mt-6" />

      <Card>
        <p className="mb-3 text-[12.5px] text-ink-2">
          Paste any id from the system — a memory candidate (<code className="mono">cand_…</code>), a claim
          (<code className="mono">cl_…</code> / <code className="mono">claim_…</code> /{" "}
          <code className="mono">DEC-…</code>), evidence (<code className="mono">ev_…</code>), work
          (<code className="mono">work_…</code>), or an entity token — to trace its full path.
        </p>
        <LineageSearch />
      </Card>
    </div>
  );
}

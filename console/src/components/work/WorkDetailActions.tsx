"use client";

import { useState } from "react";

import { Button } from "@/components/kit";
import { TraceDrawer } from "@/components/work/TraceDrawer";
import { WorkActionRow } from "@/components/work/WorkActionRow";
import type { WorkActionRowData } from "@/lib/work-pipeline";

/**
 * Replaces the /work/[id] "Manual state controls" that were buried in a closed
 * <details> (the audit's headline finding). The same governed action row is now
 * inline and prominent, and a first-class "View activity" button raises the
 * envelope-spine drawer — the id page and the board share one trace surface.
 */
export function WorkDetailActions({
  workId,
  actionRow,
  mutationsReady,
}: {
  workId: string;
  actionRow: WorkActionRowData;
  mutationsReady: boolean;
}) {
  const [traceOpen, setTraceOpen] = useState(false);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h2 className="text-md font-semibold text-ink">Progress and activity</h2>
          <p className="text-xs text-muted">See the latest updates, attempts and results.</p>
        </div>
        <Button variant="primary" tone="ink" onClick={() => setTraceOpen(true)}>
          View activity
        </Button>
      </div>

      {mutationsReady ? <WorkActionRow data={actionRow} mutationsReady={mutationsReady} dense /> : <details className="text-xs text-muted"><summary className="cursor-pointer">Advanced task controls</summary><div className="mt-3"><WorkActionRow data={actionRow} mutationsReady={false} dense /></div></details>}

      <TraceDrawer workId={traceOpen ? workId : null} onClose={() => setTraceOpen(false)} mutationsReady={mutationsReady} />
    </div>
  );
}

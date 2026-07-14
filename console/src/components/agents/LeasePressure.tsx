import { CapsuleColumn, ClockIcon, SectionHeader } from "@/components/kit";

import { leasePressureBars, type ExecutorRow } from "./executor-model";

/**
 * Lease pressure (DESIGN.md §9.3 CapsuleColumn) — the ONE place on Agents with a
 * genuine capacity semantic: each executor's oldest lease age measured against
 * the staleness threshold (default 48h). A capsule fills toward the ceiling and
 * turns seam→crit as it approaches/passes it, so the fleet's lease hygiene reads
 * as a shape, not a column of numbers. Skipped entirely when no executor holds a
 * timed lease (never forced — §9.3 rule).
 */
export function LeasePressure({
  executors,
  staleAfterSeconds,
}: {
  executors: ExecutorRow[];
  staleAfterSeconds: number;
}) {
  const { bars, thresholdHours } = leasePressureBars(executors, staleAfterSeconds);
  if (!bars.length) return null;

  return (
    <section className="rounded-xl bg-panel p-4 shadow-sm">
      <SectionHeader
        tone="seam"
        icon={<ClockIcon />}
        label="Lease pressure"
        sub={`oldest lease vs ${thresholdHours}h threshold`}
      />
      <div className="mt-4 flex flex-wrap items-end gap-4">
        {bars.map((bar) => (
          <CapsuleColumn
            key={bar.id}
            value={bar.ageHours}
            max={bar.ceilingHours}
            tone={bar.tone}
            unit="hours held"
            caption={`${bar.label} · ${bar.ageLabel}`}
            label={bar.shortLabel}
            extraRows={[{ label: "leases", value: bar.leaseCount, tone: "neutral" }]}
          />
        ))}
      </div>
    </section>
  );
}

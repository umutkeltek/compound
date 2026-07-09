import { CapsuleColumn, ClockIcon, SectionHeader, type Tone } from "@/components/kit";

import { durationLabel, type ExecutorRow } from "./executor-model";

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
  const leased = executors.filter((e) => e.leaseCount > 0 && e.oldestLeaseAgeSeconds !== null);
  if (!leased.length) return null;

  const thresholdHours = Math.max(1, Math.round(staleAfterSeconds / 3600));

  return (
    <section className="rounded-xl bg-panel p-4 shadow-sm">
      <SectionHeader
        tone="seam"
        icon={<ClockIcon />}
        label="Lease pressure"
        sub={`oldest lease vs ${thresholdHours}h threshold`}
      />
      <div className="mt-4 flex flex-wrap items-end gap-4">
        {leased.map((executor) => {
          const ageSeconds = executor.oldestLeaseAgeSeconds ?? 0;
          const ageHours = Math.round((ageSeconds / 3600) * 10) / 10;
          const ratio = ageSeconds / staleAfterSeconds;
          const tone: Tone = ratio >= 1 ? "crit" : ratio >= 0.75 ? "seam" : "work";
          return (
            <CapsuleColumn
              key={executor.id || executor.label}
              value={ageHours}
              max={thresholdHours}
              tone={tone}
              unit="hours held"
              caption={`${executor.label} · ${durationLabel(ageSeconds)}`}
              label={executor.label.length > 6 ? `${executor.label.slice(0, 6)}…` : executor.label}
              extraRows={[{ label: "leases", value: executor.leaseCount, tone: "neutral" }]}
            />
          );
        })}
      </div>
    </section>
  );
}

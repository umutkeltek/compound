import { CopyCmd } from "@/components/CopyCmd";
import { EmptyDoctrine, Panel, SectionLabel } from "@/components/briefing/primitives";
import { StatusChip } from "@/components/kit";
import type { SelfHealing } from "@/lib/briefing";

/**
 * Self-healing panel (redesign plan Part 3 below-the-fold): home-doctor hygiene
 * flags with the exact repair command. The repair verbs are NOT console-
 * certified one-click acts, so this stays honest — it names the drift and hands
 * the operator the command to run, never a fake button. Clean home → doctrine
 * empty state (DESIGN.md §7.9).
 */
export function SelfHealingPanel({ health }: { health: SelfHealing }) {
  return (
    <section>
      <SectionLabel
        right={
          health.ok ? (
            <StatusChip tone="good" label="healthy" title="compound home doctor --json" />
          ) : (
            <StatusChip tone="seam" label={`${health.flags.length} to heal`} title="compound home doctor --json" />
          )
        }
      >
        Self-healing
      </SectionLabel>

      {health.ok ? (
        <Panel className="px-6 py-6">
          <EmptyDoctrine>Clean home. Nothing to heal{health.checkedDirs ? ` — ${health.checkedDirs} intact` : ""}.</EmptyDoctrine>
        </Panel>
      ) : (
        <Panel className="overflow-hidden">
          {health.flags.map((flag, index) => (
            <div
              key={flag.key}
              className={`flex flex-wrap items-center justify-between gap-3 px-5 py-3.5 ${index > 0 ? "border-t border-hair" : ""}`}
              style={{ borderLeftWidth: "3px", borderLeftColor: flag.tone === "crit" ? "var(--crit-fg)" : "var(--seam-fg)" }}
            >
              <div className="flex min-w-0 items-baseline gap-2">
                <span className={`text-md font-bold tabular-nums ${flag.tone === "crit" ? "text-crit" : "text-seam"}`}>
                  {flag.count.toLocaleString("en-US")}
                </span>
                <span className="text-sm text-ink">{flag.label}</span>
              </div>
              <CopyCmd cmd={flag.command} compact />
            </div>
          ))}
        </Panel>
      )}
    </section>
  );
}

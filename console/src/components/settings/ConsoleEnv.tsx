import { DeltaPill, GearIcon } from "@/components/kit";
import type { AttentionReport } from "@/lib/attention";
import { num } from "@/lib/compound";

import { buildCoverage, type AreaCoverage } from "./data";
import { Card, CountChip, DoctrineNote, KV, SettingsSection } from "./primitives";

const COVERAGE_TONE: Record<AreaCoverage, string> = {
  bespoke: "text-good",
  generic: "text-work",
  fallback: "text-seam",
};

const COVERAGE_LABEL: Record<AreaCoverage, string> = {
  bespoke: "one-click (bespoke)",
  generic: "one-click (generic)",
  fallback: "copy-only fallback",
};

/**
 * Group 6 — Console & environment. Live mutation-gate booleans, and THE
 * transparency table: every wired bespoke area + every generic-certified verb
 * cross-referenced against the live review areas, so the operator sees exactly
 * which areas have a one-click act vs an honest copy-only fallback. Burn-down
 * chips track the fallback debt toward zero.
 */
export function ConsoleEnv({
  mutationsEnabled,
  privateFallbacks,
  attention,
  generatedAt,
}: {
  mutationsEnabled: boolean;
  privateFallbacks: boolean;
  attention: AttentionReport | null;
  generatedAt?: string;
}) {
  const coverage = buildCoverage(attention);
  const needsYouGroups = num(attention?.counts?.needs_you_groups);
  const actionableNow = num(attention?.counts?.actionable_now);
  const fallbackGroups = num(attention?.counts?.fallback_groups);

  return (
    <SettingsSection
      index={6}
      icon={<GearIcon />}
      tone="neutral"
      title="Console & environment"
      question="Can this console mutate, and which review areas actually have a one-click act?"
      source="compound attention --json"
      generatedAt={generatedAt}
    >
      <div className="grid gap-4 md:grid-cols-2">
        <Card title="Mutation gates" provenance="local" seamTone={mutationsEnabled ? "seam" : "good"}>
          <KV k="consoleMutationsEnabled()" v={mutationsEnabled ? "true" : "false"} />
          <KV k="privateFallbacksEnabled()" v={privateFallbacks ? "true" : "false"} />
          <DoctrineNote>
            <span className="mt-2 block">
              {mutationsEnabled
                ? "Mutations are enabled — governed acts run through /api/decision/execute behind the argv allowlist. Every act is still a human click (DEC-0021: nothing auto-runs)."
                : "Read-only. With the mutation gate off, no act can run from the console; every governed act shows as its copy-command instead."}
            </span>
          </DoctrineNote>
        </Card>

        <Card title="Coverage burn-down">
          <div className="flex flex-wrap gap-2">
            <CountChip n={needsYouGroups} label="need you" tone={needsYouGroups > 0 ? "seam" : "good"} title="counts.needs_you_groups" />
            <CountChip n={actionableNow} label="one-click" tone="muted" title="counts.actionable_now" />
            <CountChip n={fallbackGroups} label="fallback" tone={fallbackGroups > 0 ? "seam" : "good"} title="counts.fallback_groups" />
          </div>
          <DoctrineNote>
            <span className="mt-3 block">
              {fallbackGroups > 0
                ? `${fallbackGroups.toLocaleString("en-US")} group${fallbackGroups === 1 ? "" : "s"} still fall back to copy-only. The client fallback net retires when this reaches 0.`
                : "No fallback groups. Every live area resolves to a wired act."}
            </span>
          </DoctrineNote>
        </Card>
      </div>

      {/* THE transparency table. */}
      <Card
        title="Decision-registry coverage"
        badge={
          <span className="flex items-center gap-1.5">
            <CountChip n={coverage.oneClickAreas} label="live one-click" tone="good" />
            <CountChip n={coverage.fallbackAreas} label="live fallback" tone={coverage.fallbackAreas > 0 ? "seam" : "good"} />
            {/* Burn-down lead: one-click areas minus fallback areas — positive =
                winning the debt burn-down. Derived from live coverage, not faked. */}
            <DeltaPill
              value={coverage.oneClickAreas - coverage.fallbackAreas}
              showZero
              title="live one-click areas minus live fallback areas — the coverage lead over copy-only debt"
            />
          </span>
        }
      >
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs">
            <thead>
              <tr className="border-b border-hair text-2xs uppercase tracking-wide text-faint">
                <th className="py-1.5 pr-3 font-semibold">area</th>
                <th className="py-1.5 pr-3 font-semibold">bespoke</th>
                <th className="py-1.5 pr-3 font-semibold">generic</th>
                <th className="py-1.5 pr-3 font-semibold">live</th>
                <th className="py-1.5 font-semibold">coverage</th>
              </tr>
            </thead>
            <tbody className="mono tabular-nums">
              {coverage.rows.map((row) => (
                <tr key={row.area} className="border-b border-hair last:border-b-0">
                  <td className="py-1.5 pr-3 text-ink" title={row.source ? `source: ${row.source}` : undefined}>
                    {row.area}
                  </td>
                  <td className="py-1.5 pr-3 text-muted">{row.bespoke ? "✓" : "—"}</td>
                  <td className="py-1.5 pr-3 text-muted">{row.generic ? "✓" : "—"}</td>
                  <td className="py-1.5 pr-3 text-muted">{row.live ? row.liveCount.toLocaleString("en-US") : "—"}</td>
                  <td className={`py-1.5 ${COVERAGE_TONE[row.coverage]}`}>{COVERAGE_LABEL[row.coverage]}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
        <div className="mt-3 grid gap-3 border-t border-hair pt-3 md:grid-cols-2">
          <div>
            <span className="mb-1.5 block text-2xs font-semibold uppercase tracking-wide text-faint">
              bespoke specs ({coverage.bespokeAreas.length})
            </span>
            <div className="flex flex-wrap gap-1">
              {coverage.bespokeAreas.map((b) => (
                <span
                  key={b.area}
                  title={`source: ${b.source}`}
                  className="mono rounded-pill border border-good-line bg-good-soft px-2 py-0.5 text-2xs text-good"
                >
                  {b.area}
                </span>
              ))}
            </div>
          </div>
          <div>
            <span className="mb-1.5 block text-2xs font-semibold uppercase tracking-wide text-faint">
              generic-certified ({coverage.genericAreas.length})
            </span>
            <div className="flex flex-wrap gap-1">
              {coverage.genericAreas.map((area) => (
                <span
                  key={area}
                  className="mono rounded-pill border border-work-line bg-work-soft px-2 py-0.5 text-2xs text-work"
                >
                  {area}
                </span>
              ))}
            </div>
          </div>
        </div>
        <div className="mt-3">
          <DoctrineNote>
            Adding a bespoke one-click for a fallback area is one registry entry (REGISTRY in decision-registry.ts);
            certifying a generic verb is one allowlist row (GENERIC_ACTION_ALLOWLIST in generic-actions.ts). This
            table is the honest ledger of that debt.
          </DoctrineNote>
        </div>
      </Card>
    </SettingsSection>
  );
}

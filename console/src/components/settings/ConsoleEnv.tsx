import { DeltaPill, GearIcon } from "@/components/kit";
import type { AttentionReport } from "@/lib/attention";
import { FeedDegraded } from "@/components/kit/FeedDegraded";
import { num, type LiveRead } from "@/lib/compound";

import { buildCoverage, type AreaCoverage } from "./data";
import { ConsoleActsSentence } from "./ConsoleActsSentence";
import { Card, CountChip, DoctrineNote, ForAgents, KV, SettingsSection } from "./primitives";

const COVERAGE_TONE: Record<AreaCoverage, string> = {
  bespoke: "text-good",
  generic: "text-work",
  fallback: "text-seam",
};

const COVERAGE_LABEL: Record<AreaCoverage, string> = {
  bespoke: "server adapter (bespoke)",
  generic: "server adapter (generic)",
  fallback: "copy-only (no adapter)",
};

/**
 * This console: what it can do from the browser, in the one shared sentence
 * (CONSOLE_ACTS_SENTENCE), and which kinds of decision have a server-side
 * action behind them. The switches behind that answer and the coverage table
 * are a technical record, folded shut for agents. This is backend coverage,
 * not a claim that the standard browser may invoke it.
 */
export function ConsoleEnv({
  mutationsEnabled,
  mutationFlagEnabled,
  instancePinned,
  standardClientAuthorized,
  privateFallbacks,
  attention,
  attentionRead,
  generatedAt,
}: {
  mutationsEnabled: boolean;
  mutationFlagEnabled: boolean;
  instancePinned: boolean;
  standardClientAuthorized: boolean;
  privateFallbacks: boolean;
  attention: AttentionReport | null;
  attentionRead: LiveRead;
  generatedAt?: string;
}) {
  const coverage = buildCoverage(attention);
  const raw = attentionRead.value;
  const coverageKnown = attention && raw?.schema_version === "compound.attention.v0.1" &&
    Array.isArray(raw.groups) && ["needs_you_groups", "actionable_now", "fallback_groups"].every((key) => {
      const value = (raw.counts as Record<string, unknown> | undefined)?.[key];
      return Number.isInteger(value) && Number(value) >= 0;
    });
  const needsYouGroups = num(attention?.counts?.needs_you_groups);
  const serverActionable = num(attention?.counts?.actionable_now);
  const fallbackGroups = num(attention?.counts?.fallback_groups);

  return (
    <SettingsSection
      index={6}
      icon={<GearIcon />}
      tone="neutral"
      title="This console"
      question="What can you do from this browser, and which decisions have an action behind them?"
      source="compound attention --json"
      generatedAt={attentionRead.status === "live" && coverageKnown ? generatedAt : undefined}
    >
      <Card title="What this console can do">
        <p className="text-sm leading-relaxed text-ink"><ConsoleActsSentence /></p>
        <ForAgents label="For agents: the switches behind this">
          <KV k="effective readiness" v={mutationsEnabled ? "ready" : "read-only"} />
          <KV k="mutation flag" v={mutationFlagEnabled ? "enabled" : "disabled"} />
          <KV k="instance pin" v={instancePinned ? "explicit" : "missing"} />
          <KV k="standard client" v={standardClientAuthorized ? "authorized" : "projection-only"} />
          <KV k="private run-record reads" v={privateFallbacks ? "on" : "off"} />
        </ForAgents>
      </Card>

      <Card title="Decisions with an action behind them">
        {attentionRead.status === "loading" ? (
          <p className="text-sm text-muted">Checking which kinds of decision have an action behind them…</p>
        ) : !coverageKnown || attentionRead.status === "unreachable" ? (
          <FeedDegraded label="Coverage" source="compound attention --json" lastGoodAt={attentionRead.lastGoodAt} />
        ) : (
          <>
            {attentionRead.status === "stale" ? <p className="mb-2 text-sm text-seam">From the last good read; it may have changed since.</p> : null}
            <div className="flex flex-wrap gap-2">
              <CountChip n={needsYouGroups} label="wait for you" tone={needsYouGroups > 0 ? "seam" : "good"} title="counts.needs_you_groups" />
              <CountChip n={serverActionable} label="have an action ready" tone="muted" title="counts.actionable_now" />
              <CountChip n={fallbackGroups} label="have no action yet" tone={fallbackGroups > 0 ? "seam" : "good"} title="counts.fallback_groups" />
            </div>
            <DoctrineNote>
              <span className="mt-3 block">
                {fallbackGroups > 0
                  ? `${fallbackGroups.toLocaleString("en-US")} of the decisions waiting for you have no action behind them yet; an agent carries them out after you decide.`
                  : "Every kind of decision waiting for you has an action behind it. The browser still only shows them."}
              </span>
            </DoctrineNote>
          </>
        )}
      </Card>

      {/* The transparency table, folded shut for agents. */}
      {coverageKnown && attentionRead.status !== "unreachable" ? (
      <ForAgents label="For agents: which decision kinds have a server adapter">
      <Card
        title="Server-adapter coverage"
        badge={
          <span className="flex items-center gap-1.5">
            <CountChip n={coverage.serverAdapterAreas} label={attentionRead.status === "stale" ? "last-known adapters" : "live adapters"} tone="good" />
            <CountChip n={coverage.fallbackAreas} label={attentionRead.status === "stale" ? "last-known fallback" : "live fallback"} tone={coverage.fallbackAreas > 0 ? "seam" : "good"} />
            {/* Burn-down lead: server-adapter areas minus fallback areas —
                positive means adapter coverage leads copy-only debt. */}
            <DeltaPill
              value={coverage.serverAdapterAreas - coverage.fallbackAreas}
              showZero
              title="live server-adapter areas minus live fallback areas — the coverage lead over copy-only debt"
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
      </Card>
      </ForAgents>
      ) : null}
    </SettingsSection>
  );
}

import Link from "next/link";

import { num, type Json } from "@/lib/compound";

import { RouteIcon } from "@/components/kit";

import { ForAgents, KV, SettingsSection } from "./primitives";

/**
 * Group 3 — Routing & models. A read-only lane/route-evidence summary plus the
 * promotion-review backlog count, then a deep-link into the full Routing & cost
 * screen (a parallel lane owns that page). Deliberately light: this group
 * orients, it does not duplicate the routing surface.
 */
export function RoutingModels({
  routeEvidence,
  promotionCount,
  generatedAt,
}: {
  routeEvidence: Json | null;
  promotionCount: number | null;
  generatedAt?: string;
}) {
  const evidenceCount = num(routeEvidence?.route_evidence_count);
  const providerLedger = num(routeEvidence?.provider_ledger_evidence_count);
  const totalTokens = num(routeEvidence?.total_tokens);
  const costMicros = num(routeEvidence?.total_cost_micros);
  const statusCounts = (routeEvidence?.status_counts ?? {}) as Record<string, number>;
  const providerCounts = (routeEvidence?.provider_counts ?? {}) as Record<string, number>;
  const providers = Object.entries(providerCounts).sort((a, b) => b[1] - a[1]);

  return (
    <SettingsSection
      index={3}
      icon={<RouteIcon />}
      tone="route"
      title="Models"
      question="Which models agents use is on Models and cost."
      source="compound route evidence-authority --json"
      generatedAt={generatedAt}
    >
      <p className="text-sm leading-relaxed text-ink">
        {routeEvidence === null
          ? "Model usage could not be read."
          : `${evidenceCount.toLocaleString("en-US")} model calls are on record from ${providers.length.toLocaleString("en-US")} ${providers.length === 1 ? "provider" : "providers"}.`}{" "}
        Which model does which work, what it costs and the proposed changes are on{" "}
        <Link href="/routing" className="font-semibold text-work hover:underline">Models and cost</Link>.
      </p>
      <ForAgents label="For agents: technical record">
        <KV k="route_evidence_count" v={evidenceCount.toLocaleString("en-US")} />
        <KV k="provider_ledger_rows" v={providerLedger} />
        <KV k="total_tokens" v={totalTokens.toLocaleString("en-US")} />
        <KV k="status" v={Object.entries(statusCounts).map(([status, count]) => `${status}:${count}`).join("  ") || "—"} />
        <KV k="providers" v={providers.map(([provider, count]) => `${provider}:${count}`).join("  ") || "—"} />
      </ForAgents>
    </SettingsSection>
  );
}

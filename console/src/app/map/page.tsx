import Link from "next/link";

import { SystemMap } from "@/components/map/SystemMap";
import { SystemNodeCard } from "@/components/map/SystemNodeCard";
import { Chip, SectionLabel } from "@/components/ui";
import type { LensGraph, LensId } from "@/lib/map/lens";
import { isLiveLens } from "@/lib/map/lens";
import { buildRegistryLens } from "@/lib/map/lenses/registry";
import { buildRuntimeLens } from "@/lib/map/lenses/runtime";
import { loadTopology } from "@/lib/map/topology";
import { LANE_META, LANE_ORDER, type Lane } from "@/lib/map/types";

export const dynamic = "force-dynamic";

async function buildInitial(lens: LensId): Promise<LensGraph> {
  return lens === "registry" ? buildRegistryLens({ group: "kind" }) : buildRuntimeLens();
}

export default async function MapPage({
  searchParams,
}: {
  searchParams: Promise<{ lens?: string }>;
}) {
  const sp = await searchParams;
  const lens: LensId = sp.lens && isLiveLens(sp.lens) ? sp.lens : "runtime";
  const initial = await buildInitial(lens);

  const topo = await loadTopology();
  const byLane = (lane: Lane) => topo.nodes.filter((n) => n.lane === lane);

  return (
    <div className="flex flex-col gap-8">
      <SystemMap initial={initial} lens={lens} />

      {/* demoted: the static lane topology (how the substrate is organized), kept
          for orientation but no longer the hero — the live map above is. */}
      <details className="rounded-xl border border-line bg-canvas/40 px-4 py-3">
        <summary className="cursor-pointer text-[13px] font-semibold text-ink-2">
          Substrate topology · capture → storage → meaning → outward
        </summary>
        <div className="mt-4 flex flex-col gap-5">
          <section className="flex flex-wrap gap-2">
            <Link
              href="/map/memory"
              className="rounded-lg border border-line bg-white px-3 py-1.5 text-[12px] font-medium text-ink-2 hover:bg-canvas/60"
            >
              Memory graph →
            </Link>
            <Link
              href="/map/lineage"
              className="rounded-lg border border-line bg-white px-3 py-1.5 text-[12px] font-medium text-ink-2 hover:bg-canvas/60"
            >
              Lineage trace →
            </Link>
          </section>

          <div className="grid gap-4 lg:grid-cols-4">
            {LANE_ORDER.map((lane) => {
              const meta = LANE_META[lane];
              const nodes = byLane(lane);
              return (
                <section key={lane} className="flex flex-col gap-3">
                  <div className="rounded-xl border border-line bg-white px-3 py-2.5">
                    <div className="flex items-center gap-2">
                      <span className="text-[15px]">{meta.glyph}</span>
                      <span className="text-[13px] font-semibold text-ink">{meta.label}</span>
                    </div>
                    <p className="mt-1 text-[11px] leading-snug text-ink-3">{meta.blurb}</p>
                  </div>
                  <div className="flex flex-col gap-2.5">
                    {nodes.map((node) => (
                      <SystemNodeCard key={node.id} node={node} />
                    ))}
                  </div>
                </section>
              );
            })}
          </div>

          <section>
            <SectionLabel>Data flow</SectionLabel>
            <div className="flex flex-wrap items-center gap-2 text-[11.5px] text-ink-3">
              <Chip tone="green">{topo.edges.filter((e) => e.status === "live").length} live edges</Chip>
              <Chip tone="orange">{topo.edges.filter((e) => e.status === "planned").length} planned</Chip>
            </div>
          </section>
        </div>
      </details>
    </div>
  );
}

import Link from "next/link";
import { PageHeader, ReadChip, ReadNotice, ReadFailedBanner, hasCopy, readsPending } from "@/components/kit";
import { PendingRefresh } from "@/components/PendingRefresh";
import { ProjectWorkspace } from "@/components/projects/ProjectWorkspace";
import { HEAVY_PROJECTION_TIMEOUT_MS, readProjection } from "@/lib/compound";
import { buildWorkspace } from "@/lib/project-workspace";
import type { ReadView } from "@/components/kit/ReadState";

export const dynamic = "force-dynamic";
const viewOf = ({ value: _value, ...read }: Awaited<ReturnType<typeof readProjection>>): ReadView => read;

export default async function ProjectsPage({ searchParams }: {
  searchParams: Promise<{ scope?: string; work?: string; view?: string }>;
}) {
  const params = await searchParams;
  const [work, inventory, dependencies, bench] = await Promise.all([
    readProjection(["work", "list", "--json"], { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS }),
    readProjection(["system", "inventory", "--kind", "product", "--json"], { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS }),
    readProjection(["system", "graph", "--kind", "product", "--json"], { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS, enabled: !!params.scope }),
    readProjection(["workbench", "--json"], { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS, enabled: !!params.scope }),
  ]);
  const model = buildWorkspace(work.value, inventory.value, bench.value, params.scope, params.work);
  return (
    <div className="console-stage flex flex-col gap-6">
      <PendingRefresh pending={readsPending(work, inventory, params.scope ? dependencies : null, params.scope ? bench : null)} stamp={Date.now()} />
      <PageHeader name="Projects" lead="Follow a project from its recorded goals to work, dependencies and evidence."
        action={{ label: "Choose a project", href: "#project-scope" }} right={<ReadChip read={work} />} />
      <ReadFailedBanner read={work} label="work records" />
      {hasCopy(work) && !model.workKnown ? <p role="status" className="rounded-xl border border-seam-line bg-seam-soft p-4 text-sm text-seam">The work read did not contain a valid work list. Project totals are unknown.</p> : null}
      {!hasCopy(work) ? <ReadNotice read={work} label="work records" /> : null}
      <ProjectWorkspace model={model} scope={params.scope || ""} view={params.view === "map" ? "map" : "work"}
        inventory={inventory.value} dependencies={dependencies.value}
        reads={{ work: viewOf(work), inventory: viewOf(inventory), dependencies: viewOf(dependencies), bench: viewOf(bench) }} />
      <p className="text-xs leading-relaxed text-muted">
        Projects and areas come from explicit work scope, recorded repository bindings and observed product inventory.
        Work without a link stays visible. <Link href="/sources" className="font-medium text-work hover:underline">Review sources</Link>
        {" · "}<Link href="/work" className="font-medium text-work hover:underline">All work</Link>
      </p>
    </div>
  );
}

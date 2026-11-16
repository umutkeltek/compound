"use client";
import Link from "next/link";
import dynamic from "next/dynamic";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { ReadChip, ReadNotice, StateBadge } from "@/components/kit";
import type { ReadView } from "@/components/kit/ReadState";
import { dependencyLinks, projectHref, workGraph } from "@/lib/project-workspace";
import type { WorkspaceModel, ProjectWork, RecordValue } from "@/lib/project-workspace";
import { WorkInspector } from "./WorkInspector";

const FocusMap = dynamic(() => import("./ProjectFocusMap"), {
  ssr: false, loading: () => <p role="status" className="rounded-xl bg-soft p-6 text-sm text-muted">Opening the focused map…</p>,
});
const record = (value: unknown): RecordValue => value && typeof value === "object" && !Array.isArray(value) ? value as RecordValue : {};

export function ProjectWorkspace({ model, scope, view, inventory, dependencies, reads }: {
  model: WorkspaceModel; scope: string; view: "work" | "map"; inventory: unknown; dependencies: unknown;
  reads: { work: ReadView; inventory: ReadView; dependencies: ReadView; bench: ReadView };
}) {
  const router = useRouter();
  const [query, setQuery] = useState("");
  const [limit, setLimit] = useState(18);
  const [projectLimit, setProjectLimit] = useState(18);
  const [projectQuery, setProjectQuery] = useState("");
  const [includeCompleted, setIncludeCompleted] = useState(false);
  const visibleProjects = model.projects.filter((p) => (includeCompleted || p.open > 0 || p.kind === "product" || !model.workKnown) && (p.label + " " + p.kind + " " + p.id).toLocaleLowerCase().includes(projectQuery.toLocaleLowerCase()));
  const project = model.project;
  const selected = model.selected;
  const links = dependencyLinks(dependencies, scope);
  const work = model.work.filter((w) => (w.title + " " + w.owner + " " + w.state).toLocaleLowerCase().includes(query.toLocaleLowerCase()));
  const depSource = record(dependencies);
  const graphSnapshot = record(depSource.snapshot);
  const observed = typeof graphSnapshot.observed_at === "string" && Number.isFinite(Date.parse(graphSnapshot.observed_at)) ? graphSnapshot.observed_at : "";
  const depsKnown = Array.isArray(depSource.edge_list);
  const inventoryKnown = Array.isArray(record(inventory).resources);
  const name = project?.label || "Choose a project";
  return (
    <>
      <div className="flex flex-col gap-2 rounded-xl border border-hair bg-panel p-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="min-w-0 flex-1">
          <label htmlFor="project-scope" className="mb-1 block text-xs font-semibold text-muted">Project or area</label>
          <select id="project-scope" value={scope} onChange={(event) => { setQuery(""); setLimit(18); router.push(projectHref(event.target.value, "", view), { scroll: false }); }}
            className="w-full rounded-lg border border-hair bg-panel px-3 py-2.5 text-sm font-medium text-ink sm:max-w-sm">
            <option value="">All projects and areas</option>
            {scope && !project ? <option value={scope}>Project not found in these reads</option> : null}
            {model.projects.map((p) => <option key={p.id} value={p.id}>{p.label}{model.workKnown ? " · " + p.open + " open" : ""}</option>)}
          </select>
        </div>
        <div className="text-xs text-muted"><ReadChip read={reads.inventory} /><p className="mt-1">Project inventory</p></div>
      </div>

      {!scope ? (
        <section aria-label="Projects and areas" className="flex flex-col gap-3">
          <div><h2 className="text-lg font-semibold text-ink">Projects and areas</h2><p className="mt-1 text-sm text-muted">Choose one to see the work and evidence that explicitly belong to it.</p></div>
          {!inventoryKnown ? <ReadNotice read={reads.inventory} label="project inventory" /> : null}
          <div className="flex flex-wrap items-center gap-3"><label className="sr-only" htmlFor="project-search">Search projects</label><input id="project-search" value={projectQuery} onChange={(event) => { setProjectQuery(event.target.value); setProjectLimit(18); }} placeholder="Find a project or area…" className="rounded-lg border border-hair bg-panel px-3 py-2 text-sm" /><label className="flex items-center gap-2 text-xs text-muted"><input type="checkbox" checked={includeCompleted} onChange={(event) => { setIncludeCompleted(event.target.checked); setProjectLimit(18); }} />Include completed areas</label></div>
          {visibleProjects.length ? <><div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
            {visibleProjects.slice(0, projectLimit).map((p) => <Link key={p.id} href={projectHref(p.id)} className="group rounded-xl border border-hair bg-panel p-5 transition-colors hover:border-work focus-visible:outline focus-visible:outline-2 focus-visible:outline-focus">
              <div className="mb-3 text-xs text-faint">{p.kind}</div><h3 className="text-base font-semibold text-ink group-hover:text-work">{p.label}</h3>
              <p className="mt-3 text-sm text-muted">{model.workKnown ? p.open + " open" : "Work totals unknown"}{model.workKnown && p.attention ? " · " + p.attention + " need attention" : ""}</p>
              <span className="mt-4 inline-block text-xs font-semibold text-work">Open project →</span>
            </Link>)}
          </div>{visibleProjects.length > projectLimit ? <button onClick={() => setProjectLimit((n) => n + 18)} className="self-start rounded-lg border border-hair px-3 py-2 text-sm font-medium text-ink">Show more projects ({visibleProjects.length - projectLimit} remaining)</button> : null}</> : model.projects.length ? <p className="text-sm text-muted">No projects match these filters. Search another name or include completed areas.</p> : model.workKnown && inventoryKnown ? <div className="rounded-xl border border-dashed border-hair p-6"><h3 className="font-semibold text-ink">No linked projects yet</h3><p className="mt-2 max-w-read text-sm text-muted">Choose a project when creating work, or connect a source with a product record. No app or growth connection is required.</p><Link href="/sources" className="mt-3 inline-block text-sm font-medium text-work">Review sources →</Link></div> : null}
        </section>
      ) : !project ? <div role="status" className="rounded-xl border border-hair p-5 text-sm text-muted">This project is not present in the available records. <Link className="font-medium text-work" href="/projects">Choose another project</Link>.</div> : (
        <>
          <nav aria-label="Project breadcrumb" className="flex flex-wrap items-center gap-2 text-sm text-muted"><Link href="/projects" className="hover:text-work">Projects</Link><span aria-hidden>/</span><span className="font-medium text-ink">{name}</span></nav>
          <section aria-label="Project overview" className="grid gap-3 sm:grid-cols-3">
            {[["Open work", project.open], ["Needs attention", project.attention], ["Completed work", project.completed]].map(([label, value]) => <div key={label} className="rounded-xl border border-hair bg-panel p-4"><p className="text-xs text-muted">{label}</p><p className="mt-2 text-2xl font-semibold tabular-nums text-ink">{model.workKnown ? value : "—"}</p></div>)}
          </section>
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-hair pb-3">
            <div className="flex items-center gap-1" aria-label="Project views">
              <Link href={projectHref(scope, selected?.id, "")} aria-current={view === "work" ? "page" : undefined} className={"rounded-lg px-3 py-2 text-sm font-semibold " + (view === "work" ? "bg-strong text-panel" : "text-muted hover:bg-soft")}>Work and goals</Link>
              <Link href={projectHref(scope, selected?.id, "map")} aria-current={view === "map" ? "page" : undefined} className={"rounded-lg px-3 py-2 text-sm font-semibold " + (view === "map" ? "bg-strong text-panel" : "text-muted hover:bg-soft")}>Focused map</Link>
            </div>
            <p className="text-xs text-muted">Selection is kept in the page link.</p>
          </div>
          {model.requestedMissing ? <p role="status" className="rounded-xl border border-seam-line bg-seam-soft p-4 text-sm text-seam">The requested work item is not linked to this project in these records. Choose one below.</p> : null}
          <div className="grid items-start gap-5 xl:grid-cols-[minmax(0,1fr)_360px]">
            <div className="min-w-0 flex flex-col gap-4">
              {view === "map" && selected ? <FocusMap key={selected.id} graph={workGraph(selected)} /> : (
                <>
                  <div><h2 className="text-lg font-semibold text-ink">Work toward this project</h2><p className="mt-1 text-sm text-muted">Each item carries its own recorded outcome. A separate project goal is not projected here.</p></div>
                  {model.work.length ? <><label className="sr-only" htmlFor="project-work-search">Search project work</label><input id="project-work-search" value={query} onChange={(event) => { setQuery(event.target.value); setLimit(18); }} placeholder="Search work, owner or state…" className="w-full rounded-lg border border-hair bg-panel px-3 py-2.5 text-sm" />
                    <div className="overflow-hidden rounded-xl border border-hair bg-panel">{work.slice(0, limit).map((w) => <WorkRow key={w.id} work={w} selected={selected?.id === w.id} href={projectHref(scope, w.id, view)} />)}</div>
                    {!work.length ? <p role="status" className="text-sm text-muted">No work matches this search.</p> : null}
                    {work.length > limit ? <button onClick={() => setLimit((n) => n + 18)} className="self-start rounded-lg border border-hair px-3 py-2 text-sm font-medium text-ink">Show more work ({work.length - limit} remaining)</button> : null}
                  </> : model.workKnown ? <p className="rounded-xl border border-dashed border-hair p-5 text-sm text-muted">No work is explicitly linked to this project yet.</p> : <ReadNotice read={reads.work} label="project work" />}
                </>
              )}
              {view === "map" && model.work.length ? <details className="rounded-xl border border-hair p-4"><summary className="cursor-pointer text-sm font-medium text-ink">Choose another work item</summary><div className="mt-3 max-h-80 overflow-auto">{model.work.slice(0, limit).map((w) => <WorkRow key={w.id} work={w} selected={selected?.id === w.id} href={projectHref(scope, w.id, "map")} />)}</div>{model.work.length > limit ? <button onClick={() => setLimit((n) => n + 18)} className="mt-2 text-sm font-medium text-work">Show more work</button> : null}</details> : null}
            </div>
            <WorkInspector work={selected} benchRead={reads.bench} />
          </div>

          <section aria-label="Shared dependencies" className="rounded-xl border border-hair bg-panel p-5">
            <div className="flex flex-wrap items-center justify-between gap-2"><h2 className="text-base font-semibold text-ink">Shared dependencies</h2><ReadChip read={reads.dependencies} /></div>
            <p className="mt-2 text-sm text-muted">Direct inventory links for this project. These describe infrastructure relationships, not task prerequisites.</p>
            {graphSnapshot.state === "missing" ? <p className="mt-3 text-sm text-muted">No inventory observation is recorded. Dependencies are unknown.</p> : !depsKnown ? <div className="mt-3"><ReadNotice read={reads.dependencies} label="project dependencies" /></div> : links.links.length ? <div className="mt-4 overflow-x-auto"><table className="w-full text-left text-sm"><caption className="sr-only">Recorded dependencies and their evidence</caption><thead className="text-xs text-muted"><tr><th scope="col" className="pb-2 pr-3">From</th><th scope="col" className="pb-2 pr-3">Relationship</th><th scope="col" className="pb-2 pr-3">To</th><th scope="col" className="pb-2">Evidence</th></tr></thead><tbody>{links.links.map((link, index) => <tr key={index} className="border-t border-hair"><td className="py-3 pr-3">{link.fromLabel}</td><td className="py-3 pr-3">{link.relation}</td><td className="py-3 pr-3">{link.toLabel}</td><td className="py-3 text-xs text-muted"><span>{link.confidence}{link.carried ? " · retained from an earlier read" : ""}</span>{link.evidence ? <details className="mt-1"><summary className="cursor-pointer">Source</summary><p className="mt-1 max-w-xs break-words">{link.evidence}</p></details> : null}</td></tr>)}</tbody></table></div> : <p className="mt-3 text-sm text-muted">No direct dependency links for this project were returned. This does not establish that it has no dependencies.</p>}
            {links.omitted || depSource.edge_list_truncated ? <p className="mt-3 text-xs text-muted">The relationship list is bounded. More links may exist in the source inventory.</p> : null}
            <p className="mt-3 text-xs text-muted">Inventory observed: {observed ? new Date(observed).toLocaleString() : "time not reported"}{graphSnapshot.state === "stale" ? " · observation is stale" : ""}. Refresh time is separate from observation time.</p>
          </section>
        </>
      )}
    </>
  );
}

function WorkRow({ work, selected, href }: { work: ProjectWork; selected: boolean; href: string }) {
  return <Link href={href} scroll={false} aria-current={selected ? "true" : undefined} className={"block border-b border-hair p-4 last:border-b-0 focus-visible:outline focus-visible:outline-2 focus-visible:outline-focus " + (selected ? "bg-work-soft" : "hover:bg-soft")}>
    <div className="mb-2 flex flex-wrap items-center justify-between gap-2"><StateBadge state={work.state} /><span className="text-xs text-muted">{work.ownerLabel || "Owner not recorded"}</span></div>
    <h3 className="line-clamp-2 text-sm font-semibold leading-relaxed text-ink">{work.title}</h3>
    <p className="mt-2 line-clamp-2 text-xs leading-relaxed text-muted">{work.next || (work.state === "completed" ? "Open the result and verification references." : "Open the recorded outcome and proof needed.")}</p>
  </Link>;
}

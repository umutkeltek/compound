import Link from "next/link";
import type { DecideGroup } from "@/lib/decide-model";
import { evidenceDestination } from "@/components/memory/presentation";

/** Recommendation is attributed to its author; the default action is not a recommendation. */
export function DecisionContext({ group }: { group: DecideGroup }) {
  const recommendation = group.brief?.options.find((option) => option.value === group.brief?.recommendation.option);
  const work = new Map<string, { href: string; label: string }>();
  for (const member of group.members) {
    if (member.href?.startsWith("/work/")) work.set(member.href, { href: member.href, label: member.label === member.id ? "Open related work" : member.label });
  }
  for (const evidence of group.evidence) {
    const destination = evidenceDestination(evidence.ref);
    if (destination?.href.startsWith("/work/") && !work.has(destination.href)) work.set(destination.href, { ...destination, label: "Open related work" });
  }
  return (
    <section className="mt-3 grid gap-3 rounded-md border border-hair bg-soft px-3 py-3 text-xs" aria-label="Decision context">
      <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1"><span className="font-semibold text-ink">Current owner</span><span className="break-words text-muted">{group.face.owner || "Not recorded — inspect the review source before acting."}</span></div>
      {recommendation ? (
        <div><p className="font-semibold text-ink">Prepared recommendation: {recommendation.label || recommendation.value}</p>
          <p className="mt-1 leading-relaxed text-muted">{group.brief?.recommendation.rationale || "No rationale was recorded. Review the attached evidence before choosing."}</p>
          <p className="mt-1 text-2xs text-faint">Prepared by {group.brief?.preparedBy || "an unspecified author"}; awaiting your choice.</p>
        </div>
      ) : <p className="leading-relaxed text-muted">No recommendation is recorded. Compare the choices and evidence before deciding.</p>}
      {work.size ? <div><p className="font-semibold text-ink">Related work</p><ul className="mt-1 grid gap-1">{[...work.values()].map((item) => <li key={item.href}><Link href={item.href} className="font-semibold text-work hover:underline">{item.label} →</Link></li>)}</ul></div> : null}
    </section>
  );
}

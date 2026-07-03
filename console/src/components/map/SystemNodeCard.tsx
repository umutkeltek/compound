import { AccentSquare, Chip } from "@/components/ui";
import { STATUS_META, type SystemNode } from "@/lib/map/types";

/**
 * One system in the topology. Honest about status: live nodes show their real
 * volume; intake-only / planned nodes show a dashed marker so it's clear what
 * is wired today vs intended.
 */
export function SystemNodeCard({ node }: { node: SystemNode }) {
  const status = STATUS_META[node.status];
  return (
    <div
      className={`flex flex-col gap-2 rounded-xl bg-white p-3.5 ${
        status.dashed ? "border border-dashed border-line" : "border border-line"
      }`}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="flex min-w-0 items-center gap-2">
          <AccentSquare accent={node.accent}>{node.glyph}</AccentSquare>
          <div className="min-w-0">
            <div className="truncate text-[12.5px] font-semibold text-ink">{node.name}</div>
            <div className="mono text-[10px] text-ink-3">{node.id}</div>
          </div>
        </div>
        <Chip tone={status.tone} title={node.status}>{status.label}</Chip>
      </div>

      <p className="text-[11.5px] leading-snug text-ink-2">{node.description}</p>

      {node.volume ? (
        <div className="section-label text-[10px]">{node.volume}</div>
      ) : null}

      {node.evidenceRefs?.length ? (
        <details className="group">
          <summary className="cursor-pointer text-[10.5px] text-ink-3 hover:text-ink-2">
            {node.status === "intake-only" || node.status === "reference-only"
              ? `review · ${node.evidenceRefs[0]}`
              : `${node.evidenceRefs.length} source${node.evidenceRefs.length === 1 ? "" : "s"}`}
          </summary>
          <ul className="mt-1 flex flex-col gap-0.5">
            {node.evidenceRefs.map((ref) => (
              <li key={ref} className="mono truncate text-[10px] text-ink-3" title={ref}>
                {ref}
              </li>
            ))}
          </ul>
        </details>
      ) : null}
    </div>
  );
}

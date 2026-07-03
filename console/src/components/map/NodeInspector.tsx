"use client";

import Link from "next/link";

import { CopyCmd } from "@/components/CopyCmd";
import { Chip } from "@/components/ui";
import type { LensNode } from "@/lib/map/lens";

/*
 * Slide-over inspector for a clicked node. Shows the REAL object, a copyable
 * `compound …` command (never auto-run — DEC-0021), proof refs, and — the
 * truth contract — the projection sources this node came from.
 */
export function NodeInspector({
  node,
  onClose,
}: {
  node: LensNode | null;
  onClose: () => void;
}) {
  if (!node) return null;

  return (
    <aside className="fixed right-0 top-0 z-40 flex h-screen w-[380px] flex-col gap-4 overflow-y-auto border-l border-line bg-white/95 px-5 py-5 shadow-xl backdrop-blur">
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="mono text-[10px] font-semibold uppercase tracking-wide text-ink-3">
            {node.kind}
          </div>
          <h2 className="mt-0.5 text-[15px] font-semibold leading-tight text-ink">{node.label}</h2>
          {node.sublabel ? (
            <div className="mono mt-0.5 text-[11px] text-ink-3">{node.sublabel}</div>
          ) : null}
        </div>
        <button
          onClick={onClose}
          className="rounded-lg border border-line px-2 py-0.5 text-[12px] text-ink-2 hover:bg-canvas/60"
        >
          close
        </button>
      </div>

      <div className="flex flex-wrap items-center gap-1.5">
        {node.status ? <Chip tone="gray">{node.status}</Chip> : null}
        {node.group ? <Chip tone="gray">{node.group}</Chip> : null}
      </div>

      {node.cli ? (
        <div>
          <div className="section-label mb-1.5">Command (copy, never auto-run)</div>
          <CopyCmd cmd={node.cli} />
        </div>
      ) : null}

      {node.proofRefs?.length ? (
        <div>
          <div className="section-label mb-1.5">Proof refs</div>
          <div className="flex flex-wrap gap-1.5">
            {node.proofRefs.map((ref) =>
              /^work_[a-z0-9]+$/.test(ref) ? (
                <Link key={ref} href={`/work/${ref}`}>
                  <Chip tone="blue">{ref}</Chip>
                </Link>
              ) : (
                <Chip key={ref} tone="gray">{ref}</Chip>
              ),
            )}
          </div>
        </div>
      ) : null}

      <div>
        <div className="section-label mb-1.5">Projection source</div>
        <ul className="flex flex-col gap-1">
          {node.sources.map((s, i) => (
            <li key={i} className="mono text-[10.5px] leading-snug text-ink-3">
              {s}
            </li>
          ))}
        </ul>
      </div>

      <div className="min-h-0">
        <div className="section-label mb-1.5">Raw object</div>
        <pre className="mono max-h-[40vh] overflow-auto rounded-lg border border-line bg-slate-50 p-3 text-[10px] leading-snug text-ink-2">
          {JSON.stringify(node.raw ?? {}, null, 2)}
        </pre>
      </div>
    </aside>
  );
}

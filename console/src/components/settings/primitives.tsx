import type { ReactNode } from "react";

import { CopyCmd } from "@/components/CopyCmd";
import { FreshnessChip } from "@/components/kit";

/**
 * Settings shared primitives (redesign plan Part 3 "Settings — 7 groups").
 *
 * The whole area obeys one doctrine: the console shows EFFECTIVE AUTHORITY
 * (read-mostly). File-owned state is edited via import commands, not web
 * buttons; a mutation with no governed console path is shown as its exact
 * copy-command, never a fabricated act. Every group carries a FreshnessChip
 * naming the projection it read (§7.8 "every card declares provenance").
 *
 * Pure/token-only and node-free, so both server group components and the one
 * client disclosure can render them. Tokens only — no arbitrary values (§7).
 */

const SEAM_VAR: Record<string, string> = {
  seam: "var(--seam-fg)",
  crit: "var(--crit-fg)",
  good: "var(--good-fg)",
  work: "var(--work-fg)",
  memory: "var(--memory-fg)",
  route: "var(--route-fg)",
  capability: "var(--cap-fg)",
};

export type SeamTone = keyof typeof SEAM_VAR;

/** A numbered, danger-ordered settings group with a provenance chip. */
export function SettingsSection({
  index,
  title,
  question,
  source,
  generatedAt,
  children,
}: {
  index: number;
  title: string;
  question: string;
  source?: string;
  generatedAt?: string;
  children: ReactNode;
}) {
  return (
    <section className="flex flex-col gap-4" id={`group-${index}`}>
      <div className="flex flex-wrap items-end justify-between gap-3 border-b border-hair pb-2.5">
        <div className="flex items-baseline gap-3">
          <span className="mono text-xs font-semibold tabular-nums text-faint">
            {String(index).padStart(2, "0")}
          </span>
          <div className="min-w-0">
            <h2 className="text-md font-semibold text-ink">{title}</h2>
            <p className="text-xs text-muted">{question}</p>
          </div>
        </div>
        {source ? <FreshnessChip generatedAt={generatedAt} source={source} /> : null}
      </div>
      {children}
    </section>
  );
}

/** Read-only authority card; optional left seam tone marks a domain/severity. */
export function Card({
  title,
  badge,
  seamTone,
  children,
}: {
  title?: ReactNode;
  badge?: ReactNode;
  seamTone?: SeamTone;
  children: ReactNode;
}) {
  return (
    <div
      className="rounded-lg border border-hair bg-panel p-4 shadow-sm"
      style={seamTone ? { borderLeftWidth: "3px", borderLeftColor: SEAM_VAR[seamTone] } : undefined}
    >
      {title || badge ? (
        <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
          {title ? <h3 className="text-sm font-semibold text-ink">{title}</h3> : <span />}
          {badge}
        </div>
      ) : null}
      {children}
    </div>
  );
}

/** One mono/tabular key → value row inside a card. */
export function KV({ k, v, mono = true }: { k: ReactNode; v: ReactNode; mono?: boolean }) {
  return (
    <div className="flex items-baseline justify-between gap-4 border-b border-hair py-1.5 last:border-b-0">
      <span className="shrink-0 text-xs text-muted">{k}</span>
      <span className={`min-w-0 text-right text-xs tabular-nums text-ink ${mono ? "mono truncate" : ""}`}>{v}</span>
    </div>
  );
}

/**
 * The honest file-owned footer: this state is not editable from the console —
 * it is imported from its source file. Shows the exact command to copy.
 */
export function FileOwnedFoot({ cmd, note }: { cmd: string; note?: ReactNode }) {
  return (
    <div className="mt-3 flex flex-col gap-1.5 border-t border-hair pt-2.5">
      <span className="text-2xs leading-relaxed text-faint">
        {note ?? "File-owned. The console shows effective authority; edit at the source, then re-import."}
      </span>
      <CopyCmd cmd={cmd} />
    </div>
  );
}

/**
 * Honest "not wired" row: a projection that errored or needs args gets NO fake
 * card — it states the gap plainly and hands over the exact inspect command.
 */
export function InspectRow({ label, cmd, reason }: { label: string; cmd: string; reason?: ReactNode }) {
  return (
    <div className="rounded-lg border border-dashed border-hair bg-page p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <span className="text-sm text-muted">{label}</span>
        <span className="mono rounded-pill border border-hair bg-soft px-2 py-0.5 text-2xs font-semibold text-faint">
          not wired
        </span>
      </div>
      {reason ? <p className="mt-1 text-xs leading-relaxed text-faint">{reason}</p> : null}
      <div className="mt-2">
        <CopyCmd cmd={cmd} />
      </div>
    </div>
  );
}

/** Small constitution-voice note (§7.9 doctrine-as-copy). */
export function DoctrineNote({ children }: { children: ReactNode }) {
  return <p className="text-xs leading-relaxed text-muted">{children}</p>;
}

/** Count chip: mono/tabular, quiet by default, amber when it flags attention. */
export function CountChip({
  n,
  label,
  tone = "muted",
  title,
}: {
  n: number;
  label: string;
  tone?: "muted" | "seam" | "good" | "crit";
  title?: string;
}) {
  const text =
    tone === "seam" ? "text-seam" : tone === "good" ? "text-good" : tone === "crit" ? "text-crit" : "text-muted";
  return (
    <span
      title={title}
      className={`mono inline-flex items-center gap-1 rounded-pill border border-hair bg-soft px-2.5 py-0.5 text-2xs font-semibold tabular-nums ${text}`}
    >
      <span className="tabular-nums">{n.toLocaleString("en-US")}</span> {label}
    </span>
  );
}

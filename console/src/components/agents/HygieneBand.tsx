"use client";

import { useState } from "react";

import { ConfirmDialog, FreshnessChip } from "@/components/kit";

import { durationLabel, hygieneStaleCount, type HygieneSummary } from "./executor-model";

/**
 * Lease-hygiene band — THE operator win of the Agents screen (plan Part 3,
 * "the 74-stale-leases fix"). Stale/dead leases are a lane-health class, so when
 * present this pins to the top of the screen with the DESIGN.md §0 admission
 * seam (3px amber left seam + amber wash) — scanning that edge answers "is
 * anything stuck?". When the fleet is clean it collapses to a quiet green
 * clean-seam confirmation with doctrine copy, never "No data".
 *
 * REPAIR — honest by construction. The clean argv exists
 * (`compound home archive-history --kind lease`), but the console's ONE
 * governed endpoint (/api/decision/execute) can only execute review-item- or
 * registry-backed actions: there is no `reviews --json` area exposing
 * archive-history, and turning it into a real one-click needs a bespoke
 * `home-hygiene` DecisionAreaSpec in decision-registry.ts — a file this lane is
 * coordination-locked out of. A button that POSTed anyway would 400 ("unknown
 * decision area") — the "landed ≠ working" trap. So the repair is a
 * confirm-required disclosure (the kit ConfirmDialog) that states the honest
 * consequence, shows the exact argv, and COPIES it for the terminal. When the
 * decision-registry lane wires the area, this promotes to a true GovernedAct
 * with one edit here.
 */

const REPAIR_ARGV = ["home", "archive-history", "--kind", "lease"];
const REPAIR_COMMAND = `compound ${REPAIR_ARGV.join(" ")}`;

export function HygieneBand({
  summary,
  generatedAt,
  staleAfterSeconds,
}: {
  summary: HygieneSummary;
  generatedAt: string;
  staleAfterSeconds: number;
}) {
  const [open, setOpen] = useState(false);
  const [copied, setCopied] = useState(false);
  const staleCount = hygieneStaleCount(summary);
  const staleAfterLabel = durationLabel(staleAfterSeconds);

  async function copyRepair() {
    try {
      await navigator.clipboard.writeText(REPAIR_COMMAND);
      setCopied(true);
    } catch {
      /* clipboard unavailable — the argv preview in the dialog is still visible */
    }
    setOpen(false);
    window.setTimeout(() => setCopied(false), 2000);
  }

  if (staleCount === 0) {
    return (
      <section className="rounded-lg border border-good-line bg-good-soft px-4 py-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex items-center gap-2">
            <span className="h-2 w-2 rounded-full bg-good" aria-hidden />
            <p className="text-sm font-semibold text-good">Lane health is clean. No stale leases.</p>
          </div>
          <FreshnessChip generatedAt={generatedAt} source="compound executors --json" />
        </div>
        <p className="mono mt-1 pl-4 text-2xs text-muted">
          {summary.totalLeaseCount} live {summary.totalLeaseCount === 1 ? "lease" : "leases"} · stale after{" "}
          {staleAfterLabel}
        </p>
      </section>
    );
  }

  return (
    <>
      <section className="admission-seam rounded-lg border border-seam-line bg-panel px-4 py-4 shadow-sm">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div className="flex items-center gap-2">
            <h2 className="text-md font-semibold text-ink">Lease hygiene</h2>
            <span className="rounded-pill border border-seam-line bg-seam-soft px-2 py-0.5 text-xs font-semibold text-seam tabular-nums">
              {staleCount} stale
            </span>
          </div>
          <FreshnessChip generatedAt={generatedAt} source="compound executors --json" />
        </div>

        <p className="mt-2 text-sm leading-relaxed text-muted">
          {staleCount} stale {staleCount === 1 ? "lease" : "leases"}
          {summary.oldestAgeSeconds > 0 ? (
            <>
              {" "}
              · oldest <span className="mono text-seam">{durationLabel(summary.oldestAgeSeconds)}</span>
            </>
          ) : null}
          {summary.affectedExecutors.length ? <> · affecting {summary.affectedExecutors.join(", ")}</> : null}. A stale
          lease holds work no one is running — clear it to let the fleet read true.
        </p>

        <div className="mt-3 flex flex-wrap items-center gap-3">
          <button
            type="button"
            onClick={() => setOpen(true)}
            className="inline-flex items-center gap-1.5 rounded-md border border-seam-line bg-seam-soft px-3 py-2 text-xs font-semibold text-seam hover:brightness-105"
          >
            Archive stale leases…
          </button>
          {copied ? <span className="text-2xs font-semibold text-good">command copied</span> : null}
          <span className="mono text-2xs text-faint">{REPAIR_COMMAND}</span>
        </div>
      </section>

      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title="Archive stale leases"
        body="Archives terminal work history and stale leases so the fleet reads clean. Nothing is deleted — restore with `compound home restore-history`."
        policyNote="No governed one-click is wired for home maintenance yet (it needs a home-hygiene decision area). Copy the command and run it in your terminal; it stays a human, confirmed action."
        argv={REPAIR_ARGV}
        confirmLabel="Copy command"
        cancelLabel="Cancel"
        onConfirm={() => void copyRepair()}
      />
    </>
  );
}

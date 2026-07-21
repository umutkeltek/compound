"use client";

import { EmptyState, SectionHeader, StatusChip } from "@/components/kit";
import { PulseIcon } from "@/components/kit/icons";
import { useLiveFleet } from "@/lib/stream/useLiveFleet";
import type { TrafficReport, TrafficWork } from "@/lib/traffic";

/*
 * Real-time multi-agent traffic board. Server page seeds `report` (the fleet +
 * run-attempt "how executed" join from lib/traffic); this client component
 * overlays the LIVE push plane (useLiveFleet → compound stream --follow) so a
 * work's state + actor attribution update the instant a move lands — push, not
 * the 30s reconcile poll. Honesty contracts (packet 2026-07-20):
 *  - stream degraded → render the seed with a "showing last known" chip, never blank;
 *  - run-attempt door unreachable → "execution detail unavailable", never invented;
 *  - a work shows live state/actor only when the stream actually carries it.
 */

const STATUS_COPY: Record<"connecting" | "live" | "degraded", { label: string; tone: "work" | "muted" | "seam" }> = {
  live: { label: "live", tone: "work" },
  connecting: { label: "connecting…", tone: "muted" },
  degraded: { label: "offline — showing last known", tone: "seam" },
};

function stateClass(state: string): string {
  switch (state) {
    case "running":
      return "text-work";
    case "verifying":
    case "reviewing":
      return "text-seam";
    case "failed":
      return "text-crit";
    case "closed":
      return "text-good";
    default:
      return "text-muted";
  }
}

function ExecutionDetail({ work, present }: { work: TrafficWork; present: boolean }) {
  if (!present) {
    return <span className="text-2xs text-faint italic">execution detail unavailable</span>;
  }
  if (!work.harness && !work.runStatus) {
    return <span className="text-2xs text-faint">no run-attempt</span>;
  }
  return (
    <span className="mono text-2xs text-muted">
      {work.harness || "?"}
      {work.runStatus ? <span className={stateClass(work.runStatus)}> · {work.runStatus}</span> : null}
      {work.actor ? <span> · {work.actor}</span> : null}
      {work.retryDepth > 0 ? <span className="text-seam"> · ↻{work.retryDepth}</span> : null}
    </span>
  );
}

export function TrafficBoard({ report }: { report: TrafficReport }) {
  const { status, moves } = useLiveFleet();
  const statusCopy = STATUS_COPY[status];
  const liveCount = report.lanes.reduce(
    (n, lane) => n + lane.inFlight.filter((w) => moves.has(w.workId)).length,
    0,
  );
  const totalInFlight = report.inFlightCount;

  return (
    <div className="flex flex-col gap-4">
      <section className="rounded-xl bg-panel p-4 shadow-sm">
        <div className="flex flex-wrap items-center gap-x-6 gap-y-2">
          <SectionHeader
            tone="work"
            icon={<PulseIcon />}
            label="Multi-agent traffic"
            sub="who is executing what, live"
            right={<StatusChip tone={statusCopy.tone} label={statusCopy.label} pulse={status === "live"} />}
          />
          <div className="ml-auto flex items-center gap-4 text-2xs tabular-nums">
            <span className="text-faint">
              <span className="mono text-base font-bold text-ink">{totalInFlight}</span> in flight
            </span>
            <span className="text-faint">
              <span className="mono text-base font-bold text-work">{report.runningCount}</span> running
            </span>
            <span className="text-faint">
              <span className="mono text-base font-bold text-seam">{report.needsMeCount}</span> need you
            </span>
            <span className="text-faint">
              <span className="mono text-base font-bold text-work">{liveCount}</span> live now
            </span>
          </div>
        </div>
        {status === "degraded" ? (
          <p className="mt-3 rounded-md border border-hair bg-soft px-3 py-2 text-2xs text-seam">
            Live stream offline — showing the last reconciled fleet. It refreshes on the polling cadence until the
            stream reconnects.
          </p>
        ) : null}
      </section>

      {report.lanes.length === 0 ? (
        <EmptyState>No agents reporting — the executor fleet projection returned no lanes.</EmptyState>
      ) : (
        <div className="grid grid-cols-1 gap-4 lg:grid-cols-2 xl:grid-cols-3">
          {report.lanes.map((lane) => (
            <section key={lane.agentId} className="flex flex-col rounded-xl border border-hair bg-panel p-4 shadow-sm">
              <div className="flex items-center gap-2">
                <span className="text-md font-semibold text-ink">{lane.agentLabel}</span>
                <span className="mono text-2xs text-faint">{lane.agentStatus}</span>
                <span className="ml-auto flex items-center gap-2 text-2xs tabular-nums">
                  <span className="text-work">{lane.running} run</span>
                  <span className={lane.needsMe > 0 ? "text-seam" : "text-faint"}>{lane.needsMe} need-you</span>
                </span>
              </div>

              {lane.inFlight.length === 0 ? (
                <p className="mt-3 text-2xs text-faint">No in-flight work.</p>
              ) : (
                <ul className="mt-3 flex flex-col gap-1.5">
                  {lane.inFlight.map((work) => {
                    const live = moves.get(work.workId);
                    const shownState = live ? live.to : work.state;
                    return (
                      <li
                        key={work.workId}
                        className={`rounded-md border px-2.5 py-1.5 ${
                          live ? "border-work-line bg-work-soft" : "border-hair bg-soft"
                        }`}
                      >
                        <div className="flex items-center gap-2">
                          {live ? (
                            <span
                              className="inline-block h-1.5 w-1.5 shrink-0 animate-pulse rounded-full bg-work"
                              aria-label="live move"
                            />
                          ) : null}
                          <span className="truncate text-sm text-ink" title={work.title}>
                            {work.title}
                          </span>
                          <span className={`mono ml-auto shrink-0 text-2xs font-semibold ${stateClass(shownState)}`}>
                            {shownState || "—"}
                          </span>
                        </div>
                        <div className="mt-1 flex items-center gap-2">
                          <span className="mono text-2xs text-faint">{work.workId}</span>
                          {live ? <span className="text-2xs text-work">live · {live.by || "moved"}</span> : null}
                        </div>
                        <div className="mt-1">
                          <ExecutionDetail work={work} present={report.runAttemptsPresent} />
                        </div>
                      </li>
                    );
                  })}
                </ul>
              )}
            </section>
          ))}
        </div>
      )}
    </div>
  );
}

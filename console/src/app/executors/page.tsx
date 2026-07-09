import Link from "next/link";

import { Card, Chip, EmptyState, FreshnessChip, Stat, StateBadge, type Accent } from "@/components/ui";
import { arr, getExecutors, num, str, type Json } from "@/lib/compound";
import { ago } from "@/lib/time";
import { shortText } from "@/lib/work-data";

export const dynamic = "force-dynamic";

function accent(value: string): Accent {
  if (["blue", "green", "orange", "pink", "violet", "red", "gray"].includes(value)) return value as Accent;
  return "gray";
}

function textList(value: unknown): string {
  return arr(value).map((item) => str(item)).filter(Boolean).join(", ");
}

function workTitle(work: Json): string {
  return str(work.title) || str(work.success_predicate) || str(work.desired_outcome) || str(work.work_id);
}

function optionalNumber(value: unknown): number | null {
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

function durationLabel(seconds: number | null): string {
  if (seconds === null) return "";
  if (seconds < 90) return `${Math.round(seconds)}s`;
  if (seconds < 90 * 60) return `${Math.round(seconds / 60)}m`;
  if (seconds < 36 * 3600) return `${Math.round(seconds / 3600)}h`;
  return `${Math.round(seconds / 86400)}d`;
}

function WorkRef({ work }: { work: Json }) {
  const workID = str(work.work_id);
  return (
    <li className="rounded-lg border border-line px-3 py-2">
      <div className="flex items-center justify-between gap-2">
        {workID ? (
          <Link href={`/work/${workID}`} className="mono text-[10.5px] text-ink-3 hover:underline">
            {workID}
          </Link>
        ) : (
          <span className="mono text-[10.5px] text-ink-3">work unknown</span>
        )}
        <StateBadge state={str(work.state)} />
      </div>
      <p className="mt-1 text-[12px] leading-relaxed text-ink-2">{shortText(workTitle(work), 150)}</p>
      {str(work.next_action) ? (
        <p className="mt-1 text-[11.5px] leading-relaxed text-ink-3">Next: {shortText(str(work.next_action), 160)}</p>
      ) : null}
    </li>
  );
}

function OutcomeLine({ label, work, tone = "text-ink-3" }: { label: string; work: Json | null; tone?: string }) {
  if (!work) return null;
  return (
    <p className={`text-[11.5px] leading-relaxed ${tone}`}>
      {label}: {shortText(workTitle(work), 110)}
    </p>
  );
}

function ExecutorCard({ executor }: { executor: Json }) {
  const tone = accent(str(executor.tone));
  const currentWork = arr(executor.current_work);
  const leases = arr(executor.leases);
  const lastHookEvent =
    executor.last_hook_event && typeof executor.last_hook_event === "object" ? (executor.last_hook_event as Json) : null;
  const lastSuccess = executor.last_success && typeof executor.last_success === "object" ? (executor.last_success as Json) : null;
  const lastFailure = executor.last_failure && typeof executor.last_failure === "object" ? (executor.last_failure as Json) : null;
  const nextAssigned = executor.next_assigned && typeof executor.next_assigned === "object" ? (executor.next_assigned as Json) : null;
  const waitReason = str(executor.wait_reason);
  const oldestLeaseAge = optionalNumber(executor.oldest_lease_age_seconds);
  const routeEvidenceCount = optionalNumber(executor.route_evidence_count);
  const routeEvidenceVisible =
    executor.route_evidence_present === true || (str(executor.id) === "switchback_api" && (routeEvidenceCount ?? 0) > 0);

  return (
    <Card
      title={str(executor.label) || str(executor.id)}
      accent={tone}
      glyph="E"
      right={<Chip tone={tone}>{str(executor.status) || "unknown"}</Chip>}
    >
      <div className="flex flex-col gap-3">
        <div className="grid gap-2 sm:grid-cols-4">
          <div>
            <div className="section-label mb-1">Assigned</div>
            <div className="text-[18px] font-semibold leading-none">{num(executor.assigned_count)}</div>
          </div>
          <div>
            <div className="section-label mb-1">Running</div>
            <div className="text-[18px] font-semibold leading-none">{num(executor.running_count)}</div>
          </div>
          <div>
            <div className="section-label mb-1">Ready</div>
            <div className="text-[18px] font-semibold leading-none">{num(executor.ready_count)}</div>
          </div>
          <div>
            <div className="section-label mb-1">Leases</div>
            <div className="text-[18px] font-semibold leading-none">{num(executor.lease_count)}</div>
          </div>
        </div>

        <div className="grid gap-3 sm:grid-cols-2">
          <div>
            <div className="section-label mb-1">Status source</div>
            <p className="text-[12px] leading-relaxed text-ink-3">{str(executor.status_source) || "unknown"}</p>
          </div>
          {waitReason ? (
            <div>
              <div className="section-label mb-1">Wait reason</div>
              <p className="text-[12px] leading-relaxed text-ink-3">{waitReason}</p>
            </div>
          ) : null}
          {lastHookEvent ? (
            <div>
              <div className="section-label mb-1">Last hook</div>
              <p className="text-[12px] leading-relaxed text-ink-3">
                {str(lastHookEvent.hook) || "hook"} · {str(lastHookEvent.status) || "unknown"}
                {ago(lastHookEvent.created_at) ? ` · ${ago(lastHookEvent.created_at)}` : ""}
              </p>
            </div>
          ) : null}
          {oldestLeaseAge !== null ? (
            <div>
              <div className="section-label mb-1">Oldest lease</div>
              <p className="text-[12px] leading-relaxed text-ink-3">{durationLabel(oldestLeaseAge)}</p>
            </div>
          ) : null}
          {routeEvidenceVisible ? (
            <div>
              <div className="section-label mb-1">Route evidence</div>
              <p className="text-[12px] leading-relaxed text-ink-3">
                {routeEvidenceCount ?? 0} {routeEvidenceCount === 1 ? "row" : "rows"}
              </p>
            </div>
          ) : null}
        </div>

        {textList(executor.best_for) ? (
          <div>
            <div className="section-label mb-1">Best for</div>
            <p className="text-[12px] leading-relaxed text-ink-3">{textList(executor.best_for)}</p>
          </div>
        ) : null}

        <div className="grid gap-3 sm:grid-cols-2">
          {textList(executor.can_use_tools) ? (
            <div>
              <div className="section-label mb-1">Tools</div>
              <p className="text-[12px] leading-relaxed text-ink-3">{textList(executor.can_use_tools)}</p>
            </div>
          ) : null}
          {textList(executor.cost_limits) ? (
            <div>
              <div className="section-label mb-1">Cost / limits</div>
              <p className="text-[12px] leading-relaxed text-ink-3">{textList(executor.cost_limits)}</p>
            </div>
          ) : null}
        </div>

        {str(executor.projection_note) ? (
          <p className="rounded-lg border border-line bg-slate-50 px-3 py-2 text-[11.5px] leading-relaxed text-ink-3">
            {str(executor.projection_note)}
          </p>
        ) : null}

        {nextAssigned ? (
          <p className="text-[11.5px] leading-relaxed text-ink-3">
            Next assigned: {shortText(workTitle(nextAssigned), 120)}
          </p>
        ) : null}

        {currentWork.length ? (
          <div>
            <div className="section-label mb-2">Current work</div>
            <ul className="flex flex-col gap-2">
              {currentWork.slice(0, 5).map((work, index) => (
                <WorkRef key={str(work.work_id) || `${str(executor.id)}-${index}`} work={work} />
              ))}
            </ul>
          </div>
        ) : (
          <EmptyState>No assigned work.</EmptyState>
        )}

        {leases.length ? (
          <p className="text-[11.5px] leading-relaxed text-ink-3">
            Active leases:{" "}
            {leases
              .map((lease) => {
                const workID = str(lease.work_id);
                if (!workID) return "";
                const age = durationLabel(optionalNumber(lease.age_seconds));
                return age ? `${workID} (${age})` : workID;
              })
              .filter(Boolean)
              .join(", ")}
          </p>
        ) : null}

        {lastSuccess || lastFailure ? (
          <div className="grid gap-2 sm:grid-cols-2">
            <OutcomeLine label="Last success" work={lastSuccess} />
            <OutcomeLine label="Last failure" work={lastFailure} tone="text-red-500" />
          </div>
        ) : null}
      </div>
    </Card>
  );
}

export default async function ExecutorsPage() {
  const projection = await getExecutors();
  const counts = (projection?.counts && typeof projection.counts === "object" ? projection.counts : {}) as Json;
  const executors = arr(projection?.executors);

  return (
    <div className="flex flex-col gap-6">
      <div className="flex items-end justify-between gap-4">
        <div>
          <h1 className="text-[22px] font-semibold tracking-tight">Executors</h1>
          <p className="mt-0.5 text-[12.5px] text-ink-3">
            Projection-backed board from Workbench, leases, and profile guide. Status source is shown per row.
          </p>
        </div>
        <FreshnessChip generatedAt={str(projection?.generated_at)} source="compound executors --json" />
      </div>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <Stat label="Running" value={num(counts.running)} accent="blue" glyph=">" sub="claimed / in flight" />
        <Stat label="Ready" value={num(counts.ready)} accent="green" glyph="R" sub="can be assigned" />
        <Stat label="Open" value={num(counts.open)} accent="orange" glyph="O" sub="non-terminal work" />
        <Stat label="Leases" value={num(counts.leases)} accent="violet" glyph="L" sub="live + stale" />
      </div>

      {projection ? (
        <Card title="Authority" accent="gray" glyph="A">
          <div className="grid gap-3 text-[12px] leading-relaxed text-ink-3 md:grid-cols-3">
            <div>
              <div className="section-label mb-1">Authority</div>
              <p>{str(projection.authority_ref)}</p>
            </div>
            <div>
              <div className="section-label mb-1">Sources</div>
              <p>{textList(projection.source_refs)}</p>
            </div>
            <div>
              <div className="section-label mb-1">Next command</div>
              <p className="mono">{arr(projection.next_command).map((part) => str(part)).filter(Boolean).join(" ")}</p>
            </div>
          </div>
        </Card>
      ) : null}

      {executors.length ? (
        <div className="grid gap-4 lg:grid-cols-2">
          {executors.map((executor) => (
            <ExecutorCard key={str(executor.id)} executor={executor} />
          ))}
        </div>
      ) : (
        <EmptyState>No executor projection available.</EmptyState>
      )}
    </div>
  );
}

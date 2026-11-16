"use client";

import Link from "next/link";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useMemo, useState } from "react";

import { EmptyState } from "@/components/kit";
import { EvidenceReference } from "@/components/memory/EvidenceReference";
import { evidenceDestination } from "@/components/memory/presentation";
import { ForAgents } from "@/components/settings/primitives";
import { agentDisplayName } from "@/lib/harness-label";
import { ruleKindWords, ruleRecordedBy, sentence } from "@/lib/plain-words";
import { filterStandingLaw, type StandingLawKind, type StandingLawRecord } from "@/lib/standing-law";

/*
 * The searchable list of rules in force. Client-side so the <4-interaction
 * journey holds: land on the page → type → see the rule (no round-trip).
 * Kind chips filter and add up to the total; the chosen kind lives in the
 * address (`?kind=`), so reload, Back and a pasted link keep it. Each row is
 * the rule's own words + its kind + date; opening one shows what it
 * replaces, when it ends, who recorded it and a link to its history. Raw
 * paths and ids sit behind "For agents".
 */

function KindBadge({ kind }: { kind: StandingLawKind }) {
  const tone =
    kind === "grant"
      ? "border-good-line bg-good-soft text-good"
      : kind === "freeze"
        ? "border-work-line bg-work-soft text-work"
        : kind === "ruling"
          ? "border-seam-line bg-seam-soft text-seam"
          : "border-hair bg-soft-2 text-muted";
  return (
    <span className={`inline-flex shrink-0 items-center rounded-pill border px-2 py-0.5 text-2xs font-bold uppercase tracking-wide ${tone}`}>
      {ruleKindWords(kind).one}
    </span>
  );
}

function DisclosureRow({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1 sm:flex-row sm:gap-3">
      <span className="w-40 shrink-0 text-xs font-semibold text-faint">{label}</span>
      <div className="min-w-0 flex-1 text-xs text-muted">{children}</div>
    </div>
  );
}

function RecordCard({ record }: { record: StandingLawRecord }) {
  const date = record.date ? record.date.slice(0, 10) : "";
  const recordedBy = ruleRecordedBy(record.reviewedBy, (raw) => agentDisplayName(raw));
  const history = evidenceDestination(record.claimId) ?? evidenceDestination(record.decisionId);
  const technical = [
    record.sourcePath ? `source: ${record.sourcePath}` : "",
    record.claimId ? `claim: ${record.claimId}` : "",
    record.decisionId ? `decision: ${record.decisionId}` : "",
    record.owner ? `owner: ${record.owner}` : "",
    record.reviewedBy ? `recorded by: ${record.reviewedBy}` : "",
  ].filter(Boolean);
  // A native disclosure: its open state is the browser's, so an auto-refresh
  // re-render never closes a row he opened.
  return (
    <details className="card group p-4" id={record.claimId || undefined}>
      <summary className="flex cursor-pointer list-none items-start gap-3">
        <KindBadge kind={record.kind} />
        <span className="min-w-0 flex-1 text-left">
          <span className="block text-sm font-semibold leading-snug text-ink">{record.title || "This rule has no recorded wording"}</span>
          <span className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-2xs text-faint">
            {date ? <span className="tabular-nums">{date}</span> : null}
            {record.scope ? <span>applies to: {record.scope}</span> : null}
            {record.corroborationCount > 0 ? (
              <span className="tabular-nums" title="times this rule was restated later">
                restated {record.corroborationCount === 1 ? "once" : `${record.corroborationCount} times`}
              </span>
            ) : null}
            {record.supersedes.length ? <span className="text-muted">replaces {record.supersedes.length}</span> : null}
          </span>
        </span>
        <span aria-hidden className="shrink-0 rounded-md border border-hair px-2 py-0.5 text-2xs text-muted group-open:hidden">details</span>
        <span aria-hidden className="hidden shrink-0 rounded-md border border-hair px-2 py-0.5 text-2xs text-muted group-open:inline">hide</span>
      </summary>

        <div className="mt-3 flex flex-col gap-3 border-t border-hair pt-3">
          {record.supersedes.length ? (
            <DisclosureRow label="Replaces">
              <ul className="flex flex-col gap-1">
                {record.supersedes.map((sup) => (
                  <li key={sup.decisionId || sup.claimId} className="flex flex-wrap items-center gap-2">
                    <span className="text-faint line-through">{sup.title || "an earlier rule"}</span>
                    <span className="rounded-pill border border-hair bg-soft px-1.5 text-2xs text-faint">replaced</span>
                  </li>
                ))}
              </ul>
            </DisclosureRow>
          ) : null}

          {record.falsifier ? <DisclosureRow label="This rule ends when">{record.falsifier}</DisclosureRow> : null}
          {record.reviewTrigger ? <DisclosureRow label="Look at it again when">{record.reviewTrigger}</DisclosureRow> : null}

          <DisclosureRow label="Where it came from">
            <div className="flex flex-col gap-1">
              <span>
                {sentence(ruleKindWords(record.kind).one)}
                {recordedBy ? `, recorded by ${recordedBy}` : "; who recorded it is not on file"}
                {date ? ` on ${date}` : ""}.
              </span>
              {history ? (
                <Link href={history.href} className="font-semibold text-work hover:underline">Read its history →</Link>
              ) : (
                <span className="text-faint">No history page exists for this record.</span>
              )}
            </div>
          </DisclosureRow>

          {record.corroborationEvidence.length ? (
            <DisclosureRow label="Restated in">
              <ul className="flex flex-col gap-1">
                {record.corroborationEvidence.map((ev) => (
                  <li key={ev}><EvidenceReference reference={ev} /></li>
                ))}
              </ul>
            </DisclosureRow>
          ) : null}

          {record.keywords.length || record.domains.length ? (
            <DisclosureRow label="Keywords">{[...record.keywords, ...record.domains].join(", ")}</DisclosureRow>
          ) : null}

          <ForAgents label="For agents: technical record">
            {technical.length ? (
              <ul className="mono flex flex-col gap-0.5 break-all text-2xs text-faint">
                {technical.map((line) => <li key={line}>{line}</li>)}
              </ul>
            ) : null}
          </ForAgents>
        </div>
    </details>
  );
}

export function StandingLawBrowser({
  records,
  byKind,
}: {
  records: StandingLawRecord[];
  byKind: { kind: string; count: number }[];
}) {
  const router = useRouter();
  const pathname = usePathname();
  const searchParams = useSearchParams();
  const kind = searchParams?.get("kind") ?? "";
  const [query, setQuery] = useState("");

  const filtered = useMemo(() => filterStandingLaw(records, query, kind), [records, query, kind]);

  function chooseKind(next: string) {
    const params = new URLSearchParams(searchParams?.toString() ?? "");
    if (next) params.set("kind", next);
    else params.delete("kind");
    const qs = params.toString();
    router.replace(`${pathname ?? "/standing-law"}${qs ? `?${qs}` : ""}#accepted-rules`, { scroll: false });
  }

  const chip = (value: string, label: string, count: number) => {
    const active = kind === value;
    return (
      <button
        key={value || "all"}
        type="button"
        aria-pressed={active}
        onClick={() => chooseKind(active && value ? "" : value)}
        className={`inline-flex items-center gap-1 rounded-pill border px-2.5 py-0.5 text-xs font-semibold transition-colors ${
          active ? "border-strong bg-strong text-panel" : "border-hair bg-panel text-muted hover:bg-soft"
        }`}
      >
        <span className="tabular-nums">{count.toLocaleString("en-US")}</span> {label}
      </button>
    );
  };

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="Show rules of one kind">
        {chip("", "all", records.length)}
        {byKind.map((row) => chip(row.kind, row.count === 1 ? ruleKindWords(row.kind).one : ruleKindWords(row.kind).many, row.count))}
      </div>
      <div className="flex flex-wrap items-center gap-2">
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Search rules — did I already decide this?"
          aria-label="Search rules"
          className="w-full max-w-md rounded-lg border border-hair bg-panel px-3 py-1.5 text-sm text-ink outline-none placeholder:text-faint focus:border-ink/40 sm:w-80"
        />
        <span className="text-2xs text-faint tabular-nums">
          {filtered.length.toLocaleString("en-US")} of {records.length.toLocaleString("en-US")} rules shown
        </span>
      </div>

      {filtered.length ? (
        <div className="flex flex-col gap-2.5">
          {filtered.map((record) => (
            <RecordCard key={record.claimId || record.decisionId} record={record} />
          ))}
        </div>
      ) : records.length ? (
        <EmptyState>
          No rule matches{query.trim() ? ` “${query.trim()}”` : ""}{kind ? ` among ${ruleKindWords(kind).many}` : ""}. Clear the search{kind ? " or the kind" : ""} to see every rule.
        </EmptyState>
      ) : (
        <EmptyState>No rules are in force yet. A decision you record shows up here.</EmptyState>
      )}
    </div>
  );
}

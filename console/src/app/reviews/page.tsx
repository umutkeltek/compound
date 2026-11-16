import Link from "next/link";

import { DecideQueue } from "@/components/decide/DecideQueue";
import { CheckIcon, ClockIcon, hasCopy, PageHeader, plainAge, ReadChip, ReadFailedBanner, ReadNotice, readsPending } from "@/components/kit";
import { PendingRefresh } from "@/components/PendingRefresh";
import { attentionNeedsYouGroups, readAttention, type AttentionGroup } from "@/lib/attention";
import { arr, HEAVY_PROJECTION_TIMEOUT_MS, num, readProjection, str, type Json, type LiveRead, type ProjectionStatus } from "@/lib/compound";
import { ago } from "@/lib/time";
import { countDecisions, type DecisionCount, unit, waitingOnYou } from "@/lib/counts";
import { cookies } from "next/headers";
import { operatorSessionReadiness } from "@/lib/operator-session";
import { buildDecideGroups, type DecideAct, type DecideGroup, type ScopedDecisionEvidence } from "@/lib/decide-model";
import { ATTENTION_SOURCED_GENERIC_AREAS } from "@/lib/generic-actions";
import { areaOf, bandOf, enrichAttentionGroups, externalReviewRows, groupsForBand } from "@/lib/review-data";
import { plainArea, plainText, TERMS } from "@/lib/plain-words";

export const dynamic = "force-dynamic";

/** Filed items shown when one kind is open. */
const FILED_SHOWN = 100;

/* ---------- item index + sample resolution (unchanged data contract) ----------
 * The Decide page renders the SAME ordered needs-you group set as
 * `compound attention --json`. Reviews only enrich those identities with item
 * detail. Phase 3b only changes how those groups are PRESENTED and ACTED on
 * (kit cards, honest batch modes, keyboard triage) — not which groups show.
 */

function buildItemIndex(reviews: Json | null): Map<string, Json> {
  const map = new Map<string, Json>();
  for (const item of arr(reviews?.items)) {
    const id = str(item.id) || str(item.review_id);
    if (id) map.set(id, item);
  }
  return map;
}

function sampleForGroup(group: Json, itemByID: Map<string, Json>): Json {
  const sample = group.sample;
  if (sample && typeof sample === "object") return sample as Json;
  for (const id of arr(group.item_ids).map(String)) {
    const item = itemByID.get(id);
    if (item) return item;
  }
  return {
    id: str(group.key),
    area: areaOf(group),
    readiness: str(group.readiness),
    status: str(group.status),
    next_action: str(group.operator_summary),
    operator_summary: str(group.operator_summary),
    operator_unlock: str(group.operator_unlock),
    group_key: str(group.key),
    evidence_refs: arr(group.evidence_refs),
  };
}

function groupCreatedAt(group: Json, itemByID: Map<string, Json>): string {
  const sample = group.sample;
  const sampleCreated = sample && typeof sample === "object" ? str((sample as Json).created_at) : "";
  if (sampleCreated) return sampleCreated;
  let min = "";
  for (const id of arr(group.item_ids).map(String)) {
    const created = str(itemByID.get(id)?.created_at);
    if (created && (!min || created < min)) min = created;
  }
  return min || str(group.created_at);
}

/* ---------- send the browser only what it shows ----------
 * Every decision row needs its title, state and reason; only the OPEN
 * decision needs its members, evidence and prepared analysis. The rest are
 * sent without them and load when that decision is opened (`?decision=<key>`
 * re-renders this page on the server with that decision's detail). This is
 * what keeps the first load of Decide small however many items wait.
 */
function withoutDetail(act: DecideAct): DecideAct {
  return act.mode === "batch-enumerable" ? { ...act, members: [] } : act;
}

function listOnly(group: DecideGroup): DecideGroup {
  return { ...group, members: [], evidence: [], brief: null, act: withoutDetail(group.act) };
}

/**
 * Why the rest wait, counted from what the last check of each decision found
 * (lib/counts.ts `countDecisions`), with that check's age when it is older
 * than the live window. Never "paused": an old check is only old.
 */
function standingSentence(counted: DecisionCount, listAgeMs: number | null): string {
  const n = (value: number) => value.toLocaleString("en-US");
  const has = (value: number) => (value === 1 ? "has" : "have");
  const is = (value: number) => (value === 1 ? "is" : "are");
  const waitingOnTheCheck = Math.max(0, counted.found.decidable - counted.readyNow);
  const parts = [
    waitingOnTheCheck ? `${n(waitingOnTheCheck)} could be decided when last checked` : "",
    counted.found.noButton ? `${n(counted.found.noButton)} ${has(counted.found.noButton)} no button for their kind of decision yet` : "",
    counted.found.incomplete ? `${n(counted.found.incomplete)} ${has(counted.found.incomplete)} an incomplete approval record` : "",
    counted.found.itemByItem ? `${n(counted.found.itemByItem)} ${is(counted.found.itemByItem)} decided item by item` : "",
    counted.found.held ? `${n(counted.found.held)} ${is(counted.found.held)} held by the approval rules` : "",
    counted.found.other ? `${n(counted.found.other)} wait for another reason, said on the row` : "",
  ].filter(Boolean);
  if (!parts.length) return "";
  const list = parts.length === 1 ? parts[0] : `${parts.slice(0, -1).join(", ")} and ${parts[parts.length - 1]}`;
  if (counted.checkedEarlier === 0) return `${sentenceCase(list)}.`;
  const age = listAgeMs !== null ? ` (${plainAge(listAgeMs)})` : "";
  return `At the last check${age}: ${list}. Opening one checks it again in a few seconds.`;
}

function sentenceCase(value: string): string {
  return value ? value[0].toUpperCase() + value.slice(1) : value;
}

/* ---------- the opened decision checks its own evidence ----------
 * The list (`attention --json`) takes about a minute to read, and the gate
 * shows a button only from evidence read in the last 25 seconds, so a button
 * read from the list would almost never show. The opened decision reads its
 * own area instead (`reviews --json --area <area>`, about a second; the same
 * read the action path resolves against) through the non-blocking read, and
 * that copy is its evidence when it is fresher than the list's. The gate is
 * unchanged. Decisions that do not come from the review queue (held work,
 * attention-only areas) keep the list's evidence.
 */
async function scopedEvidence(
  group: AttentionGroup | null,
  listLastGoodAt: string | null,
): Promise<{ evidence: ScopedDecisionEvidence; read: LiveRead } | null> {
  if (!group || group.source !== "reviews" || !group.area || ATTENTION_SOURCED_GENERIC_AREAS.has(group.area)) return null;
  const read = await readProjection(["reviews", "--json", "--area", group.area]);
  const hasCopyNow = read.status === "live" || read.status === "stale";
  const checking = hasCopyNow ? read.status !== "live" && read.updating : read.status === "loading" || read.updating;
  const fresher = hasCopyNow && Boolean(read.value) && (!listLastGoodAt || (read.lastGoodAt ?? "") > listLastGoodAt);
  if (!fresher || !read.value) {
    return { read, evidence: { key: group.key, status: null, group: null, itemByID: null, generatedAt: "", source: read.source, checking } };
  }
  const own = arr(read.value.groups).filter((candidate: Json) => str(candidate.key) === group.key);
  const [enriched] = enrichAttentionGroups([group], own);
  return {
    read,
    evidence: {
      key: group.key,
      status: read.status as ProjectionStatus,
      group: enriched ?? null,
      itemByID: buildItemIndex(read.value),
      generatedAt: read.generatedAt ?? read.lastGoodAt ?? "",
      source: read.source,
      checking,
    },
  };
}

/* ---------- watching (informational — no buttons, on purpose) ---------- */
type WatchingEntry = { key: string; area: string; summary: string; count: number; createdAt: string };

function WatchingRow({ entry }: { entry: WatchingEntry }) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 border-b border-hair px-3 py-2.5 last:border-b-0">
      <div className="flex min-w-0 items-center gap-2">
        <span className="shrink-0 rounded-sm bg-soft-2 px-1.5 py-0.5 text-2xs font-semibold uppercase tracking-wide text-faint" title={entry.area}>
          {plainArea(entry.area)}
        </span>
        <span className="min-w-0 truncate text-sm text-muted">{entry.summary}</span>
      </div>
      <div className="flex shrink-0 items-center gap-2">
        {entry.count > 1 ? <span className="mono text-2xs tabular-nums text-faint">{unit(entry.count, "item")}</span> : null}
        <span className="text-xs tabular-nums text-faint">{entry.createdAt ? ago(entry.createdAt) : ""}</span>
      </div>
    </div>
  );
}

export default async function Decide({
  searchParams,
}: {
  searchParams: Promise<{ decision?: string | string[]; filed?: string | string[] }>;
}) {
  // Non-blocking reads: the page renders at once from the cached copies (or
  // says they are being read) and never waits for compound.
  const [reviewsRead, attentionProjection, params] = await Promise.all([
    readProjection(["reviews", "--json"], { timeoutMs: HEAVY_PROJECTION_TIMEOUT_MS }),
    readAttention(),
    searchParams,
  ]);
  const reviews = reviewsRead.value;
  const { read: attentionRead, report: attention } = attentionProjection;
  const waiting = waitingOnYou(attention);
  const itemByID = buildItemIndex(reviews);
  const mutationsEnabled = await operatorSessionReadiness((await cookies()).toString());
  const requestedKey = Array.isArray(params.decision) ? str(params.decision[0]) : str(params.decision);

  const externalRows = externalReviewRows(reviews);
  const externalWatching = externalRows.filter((r) => bandOf(r) === "watching");

  // Attention owns both membership and order. Reviews only supply richer
  // samples for identities already selected by that projection.
  const reviewEnrichmentGroups = [
    ...arr(reviews?.groups).filter((group: Json) => bandOf(group) === "needs_you"),
    ...groupsForBand(reviews, "needs_you", attention),
  ];
  const needsYou = attentionNeedsYouGroups(attention);
  const orderedGroups = enrichAttentionGroups(needsYou, reviewEnrichmentGroups);
  const scoped = await scopedEvidence(needsYou.find((group) => group.key === requestedKey) ?? null, attentionRead.lastGoodAt);
  const decideGroups = buildDecideGroups(
    orderedGroups,
    itemByID,
    attention,
    (g) => sampleForGroup(g, itemByID),
    attentionRead.status === "loading" ? "unreachable" : attentionRead.status,
    scoped?.evidence ?? null,
  );
  // Feature-detect a queue-level `systemic_blocker` reason the projection
  // may start carrying directly; DecideQueue also computes the
  // all-identical-reasons case itself from decideGroups, so the banner is
  // correct with or without this field.
  const systemicBlockerReason = str(reviews?.systemic_blocker) || null;

  // The open decision: the one asked for, else the first. Only it carries detail.
  const openKey = decideGroups.some((group) => group.key === requestedKey) ? requestedKey : decideGroups[0]?.key ?? "";
  const queueGroups = decideGroups.map((group) => (group.key === openKey ? group : listOnly(group)));
  const counted = countDecisions(decideGroups);

  const watchingEntries: WatchingEntry[] = [...groupsForBand(reviews, "watching", attention), ...externalWatching]
    .map((row: Json) => ({
      key: str(row.key) || str(row.id) || str(row.review_id),
      area: areaOf(row),
      summary: plainText(str(row.operator_summary)) || "Still watching.",
      count: num(row.count) || arr(row.item_ids).length || 1,
      createdAt: groupCreatedAt(row, itemByID),
    }))
    .sort((a, b) => (a.createdAt || "9999").localeCompare(b.createdAt || "9999"));

  // Filed: a count, then one row per kind (never one chip per item or per raw path).
  const filedItems = arr(reviews?.items).filter((it) => bandOf(it) === "filed");
  const filedByKind = new Map<string, number>();
  for (const item of filedItems) {
    const kind = plainArea(areaOf(item) || "");
    filedByKind.set(kind, (filedByKind.get(kind) ?? 0) + 1);
  }
  const filedKinds = [...filedByKind.entries()].sort((a, b) => b[1] - a[1]);
  // One kind opens at a time (`?filed=<kind>`), newest first, at most FILED_SHOWN rows.
  const filedParam = Array.isArray(params.filed) ? str(params.filed[0]) : str(params.filed);
  const filedKind = filedByKind.has(filedParam) ? filedParam : "";
  const filedOfKind = filedKind
    ? filedItems
        .filter((item) => plainArea(areaOf(item) || "") === filedKind)
        .sort((a, b) => str(b.created_at).localeCompare(str(a.created_at)))
    : [];

  const attentionKnown = hasCopy(attentionRead);
  const reviewsKnown = hasCopy(reviewsRead);
  const pending = readsPending(attentionRead, reviewsRead, scoped?.read ?? null);
  const panelDetailOpen = Boolean(decideGroups.find((group) => group.key === requestedKey)?.panel);

  const answer = !waiting ? <ReadNotice read={attentionRead} label="what waits for you" />
    : waiting.groups <= 0 ? <p>Nothing is waiting for your decision.</p>
    : <p><strong className="font-semibold text-seam">{unit(waiting.groups, TERMS.decision.name)}</strong> waiting · {unit(waiting.items, TERMS.item.name)} to review</p>;

  return (
    <div className={`flex flex-col ${panelDetailOpen ? "gap-3" : "gap-6"}`}>
      <PendingRefresh pending={pending} stamp={Date.now()} />
      {panelDetailOpen ? null : (
        <PageHeader
          name="Decide"
          action={waiting && waiting.groups === 0 ? { label: "See work in progress", href: "/live" } : undefined}
          answer={answer}
          right={<ReadChip read={attentionRead} />}
        />
      )}
      {panelDetailOpen ? null : <p className="max-w-read text-sm text-muted">Review the recommendation and what each action changes. <Link href="/work?lens=table&filter=blocked" className="font-semibold text-work hover:underline">Open blocked work →</Link> <Link href="/memory#memory-review" className="ml-3 font-semibold text-work hover:underline">Review proposed memories →</Link></p>}

      <section id="decision-queue" tabIndex={-1} className="scroll-mt-6 flex flex-col gap-3" aria-label="Decisions">
        {attentionKnown ? <ReadFailedBanner read={attentionRead} label="what waits for you" /> : null}
        {attentionKnown && decideGroups.length > 0 ? (
          <DecideQueue
            groups={queueGroups}
            detailKeys={openKey ? [openKey] : []}
            mutationsEnabled={mutationsEnabled}
            initialSelectedKey={requestedKey}
            systemicBlockerReason={systemicBlockerReason}
          />
        ) : null}
      </section>

      {watchingEntries.length ? (
        <details className="group overflow-hidden rounded-lg border border-hair bg-panel shadow-sm">
          <summary className="flex cursor-pointer list-none items-center gap-3 px-4 py-3 marker:hidden">
            <span className="inline-flex h-6 w-6 items-center justify-center rounded-sm bg-soft-2 text-sm text-muted"><ClockIcon /></span>
            <span className="flex-1 text-sm text-muted">
              <span className="font-semibold text-ink">Watching</span> · {TERMS.watching.means}
            </span>
            <span className="mono text-2xs tabular-nums text-faint">{watchingEntries.length}</span>
            <span aria-hidden className="text-sm text-faint transition-transform group-open:rotate-90">›</span>
          </summary>
          <div className="border-t border-hair">
            {watchingEntries.map((entry, index) => (
              <WatchingRow key={entry.key || index} entry={entry} />
            ))}
          </div>
        </details>
      ) : null}

      <details id="filed" open={Boolean(filedKind)} className="group scroll-mt-20 overflow-hidden rounded-lg border border-hair bg-panel shadow-sm">
        <summary className="flex cursor-pointer list-none items-center gap-3 px-4 py-3 marker:hidden">
          <span className="inline-flex h-6 w-6 items-center justify-center rounded-sm bg-good-soft text-sm text-good"><CheckIcon /></span>
          <span className="flex-1 text-sm text-muted">
            <span className="font-semibold text-ink">Filed</span> · {TERMS.filed.means}
          </span>
          {/* Unknown (reviews not read yet, or failed) is "…", never 0. */}
          <span className="mono text-2xs tabular-nums text-faint">{reviewsKnown ? unit(filedItems.length, "item") : "…"}</span>
          <span aria-hidden className="text-sm text-faint transition-transform group-open:rotate-90">›</span>
        </summary>
        <div className="border-t border-hair px-4 py-3">
          {!reviewsKnown ? (
            <ReadNotice read={reviewsRead} label="filed items" />
          ) : filedKinds.length ? (
            <ul className="grid gap-x-6 gap-y-1 sm:grid-cols-2">
              {filedKinds.map(([kind, count]) => (
                <li key={kind} className="border-b border-hair">
                  <Link
                    href={`/reviews?${new URLSearchParams({ ...(requestedKey ? { decision: requestedKey } : {}), filed: kind }).toString()}#filed`}
                    scroll={false}
                    className={`flex items-baseline justify-between gap-3 py-1.5 text-sm hover:text-ink ${kind === filedKind ? "font-semibold text-ink" : "text-muted"}`}
                  >
                    <span>{kind}</span>
                    <span className="mono text-2xs tabular-nums text-faint">{count.toLocaleString("en-US")}</span>
                  </Link>
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-xs text-faint">Nothing is filed yet. <Link href="/work" className="font-semibold text-work hover:underline">Follow current work →</Link></p>
          )}
          {filedKind && reviewsKnown ? (
            <div className="mt-4">
              <div className="text-xs font-semibold text-ink">
                {filedKind} · showing {Math.min(FILED_SHOWN, filedOfKind.length).toLocaleString("en-US")} of {unit(filedOfKind.length, "item")}, newest first
              </div>
              <ul className="mt-2 divide-y divide-hair rounded-md border border-hair">
                {filedOfKind.slice(0, FILED_SHOWN).map((item, index) => (
                  <li key={str(item.id) || index} className="flex flex-wrap items-baseline justify-between gap-x-3 gap-y-0.5 px-3 py-2">
                    <span className="min-w-0 flex-1 text-sm text-muted">
                      {plainText(str(item.operator_summary) || str(item.next_action) || str(item.title)) || "No summary was written for this item."}
                    </span>
                    <span className="shrink-0 text-2xs tabular-nums text-faint">{str(item.created_at) ? ago(str(item.created_at)) : ""}</span>
                    <span className="mono w-full truncate text-2xs text-faint">{str(item.id) || str(item.review_id)}</span>
                  </li>
                ))}
              </ul>
            </div>
          ) : reviewsKnown && filedKinds.length ? (
            <p className="mt-3 text-2xs text-faint">Pick a kind to see its filed items.</p>
          ) : null}
        </div>
      </details>
      <details className="text-xs text-muted"><summary className="cursor-pointer">Queue status & browser access</summary><p className="mt-2 max-w-read">{standingSentence(counted, attentionRead.ageMs)} {mutationsEnabled ? "Actions appear only when their current owner permits them." : "Recording actions is unavailable in this browser. You can still inspect evidence and related work."} <Link href="/settings" className="font-semibold text-work hover:underline">Check console access →</Link></p></details>
    </div>
  );
}

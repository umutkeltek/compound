import type { ReactNode } from "react";

import { DecideQueue } from "@/components/decide/DecideQueue";
import { FreshnessChip, PageHeader } from "@/components/kit";
import { getAttention, type AttentionReport } from "@/lib/attention";
import { arr, getReviews, num, str, type Json } from "@/lib/compound";
import { ago, toDate } from "@/lib/time";
import { consoleMutationsEnabled } from "@/lib/console-mutations";
import { buildDecideGroups, domainForArea, type DecideGroup } from "@/lib/decide-model";
import { areaPlain } from "@/lib/glossary";
import { areaOf, bandOf, externalReviewRows, groupsForBand } from "@/lib/review-data";

export const dynamic = "force-dynamic";

const EXTERNAL_AREA = "external-repo-intake";

/* ---------- item index + sample resolution (unchanged data contract) ----------
 * The Decide page renders the SAME needs-you group set the attention smoke
 * models: backend `compound reviews --json` groups + the client fallback net +
 * one external card. Ordering follows the ONE `compound attention --json`
 * projection. Phase 3b only changes how those groups are PRESENTED and ACTED
 * on (kit cards, honest batch modes, keyboard triage) — not which groups show.
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

/* ---------- ordering follows the ONE attention projection ---------- */
function orderByAttention(groups: Json[], attention: AttentionReport | null, itemByID: Map<string, Json>): Json[] {
  const rankByKey = new Map<string, number>();
  const rankByArea = new Map<string, number>();
  arr(attention?.groups).forEach((g: Json, index: number) => {
    const key = str(g.key);
    if (key && !rankByKey.has(key)) rankByKey.set(key, index);
    const area = str(g.area);
    if (area && !rankByArea.has(area)) rankByArea.set(area, index);
  });
  const UNRANKED = Number.MAX_SAFE_INTEGER;
  const rankOf = (group: Json): number => {
    const byKey = rankByKey.get(str(group.key));
    if (byKey != null) return byKey;
    return rankByArea.get(areaOf(group)) ?? UNRANKED;
  };
  return groups
    .map((group, index) => ({ group, index, rank: rankOf(group) }))
    .sort((a, b) => {
      if (a.rank !== b.rank) return a.rank - b.rank;
      const ageA = groupCreatedAt(a.group, itemByID) || "9999";
      const ageB = groupCreatedAt(b.group, itemByID) || "9999";
      if (ageA !== ageB) return ageA.localeCompare(ageB);
      return a.index - b.index;
    })
    .map((entry) => entry.group);
}

function sortByAge(rows: Json[], itemByID: Map<string, Json>): Json[] {
  return rows
    .slice()
    .sort((a, b) => (groupCreatedAt(a, itemByID) || "9999").localeCompare(groupCreatedAt(b, itemByID) || "9999"));
}

function asWatchingEntry(row: Json): Json {
  return {
    ...row,
    count: num(row.count) || 1,
    item_ids: arr(row.item_ids).length ? row.item_ids : [str(row.id) || str(row.review_id)],
  };
}

/* ---------- external needs-you collapses to ONE honest card ----------
 * Each external repo is decided from its filled review artifact (a prepared-YAML
 * flow, genuinely not a one-click), so this card is honest copy + a member list.
 */
function externalDecideGroup(externalNeedsYou: Json[], attention: AttentionReport | null): DecideGroup | null {
  if (!externalNeedsYou.length) return null;
  return {
    key: "external-repo-intake|needs-you",
    area: EXTERNAL_AREA,
    areaLabel: "external repo intake",
    areaPlain: areaPlain(EXTERNAL_AREA),
    domain: domainForArea(EXTERNAL_AREA),
    glyph: "E",
    title: "Shared repos awaiting your keep / reference / reject call",
    unlock: "",
    body: "Each repo is decided from its own review artifact — fill the review, then record it. Nothing is cloned, installed, or run from this card.",
    consequences: {
      approve: "your decision + rationale go on the permanent record and steer what happens to the repo",
      hold: "the repos stay in this queue untouched",
    },
    chips: [],
    count: externalNeedsYou.length,
    createdAt: "",
    ageLabel: "",
    old: false,
    sampleId: str(externalNeedsYou[0]?.id) || EXTERNAL_AREA,
    members: externalNeedsYou.map((r) => ({
      id: str(r.id) || str(r.review_id),
      label: str(r.target_path) || str(r.operator_summary) || str(r.id),
      argv: null,
      actionId: "",
    })),
    evidence: [],
    freshness: { generatedAt: str(attention?.generated_at), source: "compound attention --json" },
    act: { mode: "copy", command: "compound reviews --area external-repo-intake", reason: "" },
  };
}

/* ---------- header metric chip ---------- */
function MetricChip({ children, title, tone = "muted" }: { children: ReactNode; title?: string; tone?: "muted" | "seam" }) {
  return (
    <span
      title={title}
      className={`mono inline-flex items-center gap-1 rounded-pill border border-hair bg-soft px-2.5 py-0.5 text-2xs font-semibold tabular-nums ${
        tone === "seam" ? "text-seam" : "text-muted"
      }`}
    >
      {children}
    </span>
  );
}

/* ---------- watching band (informational — no buttons, on purpose) ---------- */
function WatchingRow({ entry, itemByID }: { entry: Json; itemByID: Map<string, Json> }) {
  const summary = str(entry.operator_summary) || "Still watching.";
  const area = areaOf(entry);
  const count = num(entry.count) || arr(entry.item_ids).length || 1;
  const createdAt = groupCreatedAt(entry, itemByID);
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 border-b border-hair px-3 py-2.5 last:border-b-0">
      <div className="flex min-w-0 items-center gap-2">
        {area ? (
          <span
            className="shrink-0 rounded-sm bg-soft-2 px-1.5 py-0.5 text-2xs font-semibold uppercase tracking-wide text-faint"
            title={areaPlain(area)}
          >
            {area.replace(/-/g, " ")}
          </span>
        ) : null}
        <span className="min-w-0 truncate text-sm text-muted">{summary}</span>
      </div>
      <div className="flex shrink-0 items-center gap-2">
        {count > 1 ? <span className="mono text-2xs tabular-nums text-faint">{count}</span> : null}
        <span className="text-xs tabular-nums text-faint">{createdAt ? ago(createdAt) : ""}</span>
      </div>
    </div>
  );
}

export default async function Decide() {
  const [reviews, attention] = await Promise.all([getReviews(), getAttention()]);
  const itemByID = buildItemIndex(reviews);
  const mutationsEnabled = consoleMutationsEnabled();

  const externalRows = externalReviewRows(reviews);
  const externalNeedsYou = externalRows.filter((r) => bandOf(r) !== "watching");
  const externalWatching = externalRows.filter((r) => bandOf(r) === "watching");

  // The needs-you set (backend groups + client fallback net), ordered by the
  // ONE attention projection — same set the attention-consistency smoke models.
  const orderedGroups = orderByAttention(groupsForBand(reviews, "needs_you", attention), attention, itemByID);
  const modelGroups = buildDecideGroups(orderedGroups, itemByID, attention, (g) => sampleForGroup(g, itemByID));
  const extGroup = externalDecideGroup(externalNeedsYou, attention);
  const decideGroups: DecideGroup[] = extGroup ? [extGroup, ...modelGroups] : modelGroups;

  const watchingEntries = sortByAge(
    [...groupsForBand(reviews, "watching", attention), ...externalWatching.map(asWatchingEntry)],
    itemByID,
  );

  const allItems = arr(reviews?.items);
  const filedItems = allItems.filter((it) => bandOf(it) === "filed");
  const filedTotal = filedItems.length;
  const filedByAreaMap = new Map<string, number>();
  for (const item of filedItems) {
    const area = areaOf(item) || "unclassified";
    filedByAreaMap.set(area, (filedByAreaMap.get(area) ?? 0) + 1);
  }
  const filedByArea = [...filedByAreaMap.entries()].sort((a, b) => b[1] - a[1]);

  const attnNeedsYouGroups = num(attention?.counts?.needs_you_groups);
  const actionableNow = num(attention?.counts?.actionable_now);
  const fallbackGroups = num(attention?.counts?.fallback_groups);

  const needsYouItemTotal =
    modelGroups.reduce((sum, g) => sum + g.count, 0) + externalNeedsYou.length;
  const empty = decideGroups.length === 0;

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        breadcrumb="operate / decide"
        title="Decide"
        right={
          <>
            <MetricChip
              tone="seam"
              title="compound attention --json · counts.needs_you_groups — the single 'needs you' count the nav badge and briefing pill also show"
            >
              {attnNeedsYouGroups.toLocaleString("en-US")} need you
            </MetricChip>
            <MetricChip title="compound attention --json · counts.actionable_now — needs-you groups with a wired one-click/batch act">
              {actionableNow.toLocaleString("en-US")} one-click
            </MetricChip>
            <MetricChip
              tone={fallbackGroups > 0 ? "seam" : "muted"}
              title="compound attention --json · counts.fallback_groups — groups the projection had to fallback-group (writer debt; the client safety net retires when this hits 0)"
            >
              {fallbackGroups.toLocaleString("en-US")} fallback
            </MetricChip>
            <FreshnessChip generatedAt={str(attention?.generated_at)} source="compound attention --json" />
            <FreshnessChip generatedAt={str(reviews?.generated_at)} source="compound reviews --json" />
          </>
        }
      />

      <section className="flex flex-col gap-3">
        <div className="flex items-baseline justify-between gap-2">
          <h2 className="text-md font-semibold text-ink">Needs you</h2>
          <p className="text-xs tabular-nums text-faint">
            {empty
              ? `Nothing waits on you · ${filedTotal} filed`
              : `${needsYouItemTotal.toLocaleString("en-US")} item${needsYouItemTotal === 1 ? "" : "s"} in ${decideGroups.length} decision${decideGroups.length === 1 ? "" : "s"}`}
          </p>
        </div>

        {empty ? (
          <p className="rounded-lg border border-dashed border-hair bg-page px-4 py-8 text-center text-sm text-muted">
            Clean seam. Nothing waits on you. {filedTotal} record{filedTotal === 1 ? "" : "s"} filed.
          </p>
        ) : (
          <DecideQueue groups={decideGroups} mutationsEnabled={mutationsEnabled} />
        )}
      </section>

      {watchingEntries.length ? (
        <section className="flex flex-col gap-3">
          <div className="flex items-baseline justify-between gap-2">
            <h2 className="text-md font-semibold text-ink">Watching</h2>
            <span className="mono text-2xs tabular-nums text-faint">{watchingEntries.length}</span>
          </div>
          <p className="text-xs text-faint">Informational holds — nothing to decide yet, no buttons on purpose.</p>
          <div className="overflow-hidden rounded-lg border border-hair bg-panel">
            {watchingEntries.map((entry, index) => (
              <WatchingRow key={str(entry.key) || str(entry.id) || index} entry={entry} itemByID={itemByID} />
            ))}
          </div>
        </section>
      ) : null}

      <section>
        <div className="mb-2 flex items-baseline justify-between gap-2">
          <h2 className="text-sm font-semibold text-muted">Filed</h2>
          <span className="text-xs tabular-nums text-faint">
            {filedTotal} record{filedTotal === 1 ? "" : "s"} filed
          </span>
        </div>
        {filedByArea.length ? (
          <div className="flex flex-wrap gap-2">
            {filedByArea.map(([area, count]) => (
              <span
                key={area}
                title={areaPlain(area)}
                className="rounded-md border border-hair bg-panel px-2.5 py-1 text-xs text-muted"
              >
                {area.replace(/-/g, " ")} <span className="mono tabular-nums text-faint">· {count}</span>
              </span>
            ))}
          </div>
        ) : (
          <p className="text-xs text-faint">Nothing filed yet.</p>
        )}
      </section>
    </div>
  );
}

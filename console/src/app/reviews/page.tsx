import { CopyCmd } from "@/components/CopyCmd";
import { DecisionCard } from "@/components/DecisionCard";
import { Card, Chip, EmptyState, FreshnessChip } from "@/components/ui";
import { ago, arr, getReviews, num, reviewCommand, str, type Json } from "@/lib/compound";
import { areaPlain } from "@/lib/glossary";
import { loadReviewCard, type ReviewCardData } from "@/lib/review-detail";
import { externalReviewRows } from "@/lib/review-data";

export const dynamic = "force-dynamic";

/**
 * Reviews as decisions, not counts (EP-U9 direction): everything that needs
 * the operator renders as a plain-language card grouped by area, each area
 * carrying its glossary one-liner. External-repo-intake keeps its one-click
 * form; other areas render read-only cards with the consequence pair and the
 * exact command as the secondary affordance (surface, never execute).
 */

const NEEDS_DECISION = new Set(["review_required", "needs_review", "unknown", "open", ""]);

function ReviewItemCard({ card, cmd }: { card: ReviewCardData; cmd: string }) {
  return (
    <div className="rounded-xl border border-line bg-white px-4 py-3">
      <div className="flex items-start justify-between gap-2">
        <span className="min-w-0 break-words text-[13px] font-semibold leading-snug text-ink">
          {card.headline}
        </span>
        <div className="flex shrink-0 items-center gap-1">
          {card.confidence ? <Chip tone="orange">conf {card.confidence}</Chip> : null}
          {card.readiness ? <Chip tone="gray">{card.readiness.replace(/_/g, " ")}</Chip> : null}
        </div>
      </div>
      {card.body ? (
        <p className="mt-1.5 text-[12px] leading-relaxed text-ink-2">{card.body}</p>
      ) : null}
      {card.warnings.length ? (
        <div className="mt-1.5 flex flex-wrap gap-1">
          {card.warnings.map((w) => (
            <span key={w} className="rounded-md bg-canvas px-1.5 py-0.5 text-[10px] text-ink-3">
              {w}
            </span>
          ))}
        </div>
      ) : null}
      <div className="mt-2 rounded-lg bg-canvas px-2.5 py-1.5 text-[11px] leading-relaxed">
        <div className="text-ink-2">
          <span className="font-semibold text-emerald-700">if you act</span>
          <span className="text-ink-3"> → </span>
          {card.nextAction || "your recorded decision unblocks this area's next step — nothing runs from this card"}
        </div>
        <div className="text-ink-3">
          <span className="font-semibold">if you wait</span> → nothing happens; it stays in this queue
        </div>
      </div>
      <div className="mt-2 flex flex-wrap items-center gap-2">
        <CopyCmd cmd={cmd} compact />
        <span className="text-[10.5px] text-ink-3">
          {card.evidenceCount ? `${card.evidenceCount} evidence ref${card.evidenceCount === 1 ? "" : "s"}` : "no evidence refs"}
          {card.createdAt ? ` · ${ago(card.createdAt)}` : ""}
        </span>
        <code className="mono min-w-0 truncate text-[9.5px] text-ink-3">{card.id}</code>
      </div>
    </div>
  );
}

export default async function Reviews() {
  const reviews = await getReviews();
  const counts = (reviews?.counts ?? {}) as Record<string, any>;
  const byArea = (counts.by_area ?? {}) as Record<string, number>;
  const externalRows = externalReviewRows(reviews);

  // Everything the operator must decide, grouped by area (external intake keeps
  // its interactive card path below).
  const pending = arr(reviews?.items).filter((row: Json) => {
    const area = str(row.area) || str(row.review_area);
    if (!area || area === "external-repo-intake") return false;
    return NEEDS_DECISION.has(str(row.readiness) || str(row.status));
  });

  const byAreaPending = new Map<string, Json[]>();
  for (const row of pending) {
    const area = str(row.area) || str(row.review_area);
    byAreaPending.set(area, [...(byAreaPending.get(area) ?? []), row]);
  }
  const orderedAreas = [...byAreaPending.entries()].sort((a, b) => b[1].length - a[1].length);

  const CARD_CAP = 4;
  const areaSections = await Promise.all(
    orderedAreas.map(async ([area, rows]) => ({
      area,
      total: rows.length,
      cards: await Promise.all(rows.slice(0, CARD_CAP).map(loadReviewCard)),
      rows: rows.slice(0, CARD_CAP),
    })),
  );

  const quiet = Object.entries(byArea)
    .filter(([area]) => !byAreaPending.has(area) && area !== "external-repo-intake")
    .sort((a, b) => b[1] - a[1]);

  return (
    <div className="flex flex-col gap-8">
      <div className="flex items-end justify-between gap-4">
        <div>
          <h1 className="text-[22px] font-semibold tracking-tight">Reviews</h1>
          <p className="mt-0.5 text-[12.5px] text-ink-3">
            {num(counts.review_required)} need your decision · {num(counts.total)} total · everything else is quiet
          </p>
        </div>
        <FreshnessChip generatedAt={str(reviews?.generated_at)} source="compound reviews --json" />
      </div>

      {externalRows.length ? (
        <Card
          title="Shared repos awaiting your call"
          accent="blue"
          glyph="E"
          right={<Chip tone="orange">{externalRows.length} open</Chip>}
        >
          <p className="mb-3 text-[11.5px] text-ink-3">{areaPlain("external-repo-intake")}</p>
          <div className="grid gap-3 lg:grid-cols-2">
            {externalRows.map((row) => (
              <DecisionCard key={str(row.id)} area="external-repo-intake" item={row} />
            ))}
          </div>
        </Card>
      ) : null}

      {areaSections.length ? (
        areaSections.map(({ area, total, cards, rows }) => (
          <section key={area}>
            <div className="mb-1 flex items-baseline justify-between gap-2">
              <h2 className="text-[14px] font-semibold text-ink">
                {area.replace(/-/g, " ")}
              </h2>
              <Chip tone={total ? "orange" : "gray"}>{total} waiting</Chip>
            </div>
            <p className="mb-3 text-[11.5px] text-ink-3">{areaPlain(area)}</p>
            <div className="grid gap-3 lg:grid-cols-2">
              {cards.map((card, i) => (
                <ReviewItemCard key={card.id || i} card={card} cmd={reviewCommand(rows[i])} />
              ))}
            </div>
            {total > CARD_CAP ? (
              <p className="mt-2 text-[11px] text-ink-3">
                +{total - CARD_CAP} more in this area — <code className="mono">compound reviews --area {area}</code>
              </p>
            ) : null}
          </section>
        ))
      ) : !externalRows.length ? (
        <EmptyState>Nothing needs a decision right now.</EmptyState>
      ) : null}

      {quiet.length ? (
        <section>
          <h2 className="mb-2 text-[13px] font-semibold text-ink-2">Quiet areas (nothing waiting on you)</h2>
          <div className="flex flex-wrap gap-2">
            {quiet.map(([area, count]) => (
              <span
                key={area}
                title={areaPlain(area)}
                className="rounded-lg border border-line bg-white px-2.5 py-1 text-[11px] text-ink-3"
              >
                {area.replace(/-/g, " ")} · {count}
              </span>
            ))}
          </div>
        </section>
      ) : null}
    </div>
  );
}

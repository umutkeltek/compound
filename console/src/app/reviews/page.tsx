import { DecisionCard } from "@/components/DecisionCard";
import { Accent, Card, Chip, EmptyState, FreshnessChip, Stat } from "@/components/ui";
import { getReviews, num, str } from "@/lib/compound";
import { externalReviewRows } from "@/lib/review-data";

export const dynamic = "force-dynamic";

const AREA_ACCENTS: Accent[] = ["blue", "violet", "orange", "green", "pink"];

export default async function Reviews() {
  const reviews = await getReviews();
  const counts = (reviews?.counts ?? {}) as Record<string, any>;
  const byArea = (counts.by_area ?? {}) as Record<string, number>;
  const rows = externalReviewRows(reviews);

  return (
    <div className="flex flex-col gap-8">
      <div className="flex items-end justify-between gap-4">
        <div>
          <h1 className="text-[22px] font-semibold tracking-tight">Reviews</h1>
          <p className="mt-0.5 text-[12.5px] text-ink-3">
            {num(counts.total)} in queue / {num(counts.review_required)} need your decision / {num(counts.ok)} ok
          </p>
        </div>
        <FreshnessChip generatedAt={str(reviews?.generated_at)} source="compound reviews --json" />
      </div>

      <div className="grid grid-cols-2 gap-4 lg:grid-cols-4">
        {Object.entries(byArea)
          .sort((a, b) => b[1] - a[1])
          .slice(0, 8)
          .map(([area, count], index) => (
            <Stat
              key={area}
              label={area}
              value={count}
              accent={AREA_ACCENTS[index % AREA_ACCENTS.length]}
              glyph={area[0]?.toUpperCase()}
            />
          ))}
      </div>

      <Card
        title="Needs your decision"
        accent="orange"
        glyph="!"
        right={<Chip tone={rows.length ? "orange" : "green"}>{rows.length} open</Chip>}
      >
        {rows.length ? (
          <div className="grid gap-3 lg:grid-cols-2">
            {rows.map((row) => (
              <DecisionCard key={str(row.id)} area="external-repo-intake" item={row} />
            ))}
          </div>
        ) : num(counts.review_required) ? (
          <div className="flex flex-col gap-2">
            <p className="text-[12.5px] text-ink-2">
              The report shows {num(counts.review_required)} review(s) required, but no external intake rows were
              resolved for inline approval.
            </p>
          </div>
        ) : (
          <EmptyState>Nothing needs a decision right now.</EmptyState>
        )}
      </Card>
    </div>
  );
}

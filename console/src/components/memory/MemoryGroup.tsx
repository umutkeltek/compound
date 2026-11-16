import { unit } from "@/lib/counts";
import { ago } from "@/lib/time";
import { AdjudicationCard } from "./AdjudicationCard";
import type { AdjudicationCardModel } from "./model";

/**
 * Several proposed memories with one subject and one source, folded into one
 * row: how many, what they are about, where they came from and how old. The
 * cards open inside it. A single card renders on its own. A link to one card
 * (`/memory#<id>`) opens its group: browsers reveal a closed `<details>` that
 * holds the link target.
 */
export function MemoryGroup({ models, mutationsEnabled }: { models: AdjudicationCardModel[]; mutationsEnabled: boolean }) {
  const first = models[0];
  if (!first) return null;
  if (models.length === 1) return <AdjudicationCard model={first} mutationsEnabled={mutationsEnabled} />;
  const newest = models.map((m) => m.createdAt).filter(Boolean).sort().at(-1) ?? "";
  return (
    <details className="admission-seam rounded-lg border border-hair bg-panel px-4 py-3 shadow-sm" data-memory-group={models.length}>
      <summary className="cursor-pointer list-none">
        <span className="text-2xs font-bold uppercase tracking-wide text-faint">{unit(models.length, first.kindLabel.toLowerCase(), "proposed memories")} from one source</span>
        <span className="mt-0.5 block text-md font-semibold leading-snug text-ink">
          {first.about ? <>About {first.about.label}, from </> : <>From </>}
          {first.from.text}
        </span>
        <span className="mt-1 block text-xs text-muted">
          {newest ? <>Newest added {ago(newest)}. </> : null}
          Open to read each one; each waits for its own choice.
        </span>
      </summary>
      <div className="mt-3 flex flex-col gap-3">
        {models.map((model) => (
          <AdjudicationCard key={model.id} model={model} mutationsEnabled={mutationsEnabled} />
        ))}
      </div>
    </details>
  );
}

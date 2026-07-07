import { SkeletonBlock, SkeletonLine } from "@/components/Skeleton";

const STAGES = ["Signal", "Suggest", "Add to work", "Assign", "Run", "Proof", "Learn"];

export default function FlowLoading() {
  return (
    <main className="mx-auto max-w-[1400px] px-5 py-6">
      <div className="mb-4 flex flex-wrap items-end justify-between gap-2">
        <div className="flex flex-col gap-2">
          <SkeletonLine className="h-4 w-16" />
          <SkeletonLine className="h-3 w-80" />
        </div>
        <SkeletonBlock className="h-6 w-56 rounded-full" />
      </div>

      <div className="overflow-x-auto pb-3">
        <div className="flex min-w-[1220px] gap-3">
          {STAGES.map((stage) => (
            <section key={stage} className="w-[172px] shrink-0 grow">
              <div className="mb-2 flex items-center gap-1.5">
                <span className="h-[7px] w-[7px] rounded-full bg-line" />
                <SkeletonLine className="h-2.5 w-16" />
              </div>
              <SkeletonLine className="mb-2 h-2.5 w-full" />
              <div className="flex flex-col gap-2">
                <SkeletonBlock className="h-14 rounded-xl" />
                <SkeletonBlock className="h-14 rounded-xl" />
              </div>
            </section>
          ))}
        </div>
      </div>
    </main>
  );
}

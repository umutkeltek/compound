import { SkeletonBlock, SkeletonHeader, SkeletonLine } from "@/components/Skeleton";

const LANES = ["Inbox", "Ready", "Running", "Needs me"];

export default function WorkLoading() {
  return (
    <div className="flex flex-col gap-6">
      <SkeletonHeader />
      <section>
        <SkeletonLine className="mb-3 h-3 w-28" />
        <div className="overflow-x-auto pb-2">
          <div className="flex min-w-max gap-4">
            {LANES.map((lane) => (
              <div key={lane} className="flex w-[328px] shrink-0 flex-col gap-3">
                <div className="flex items-center justify-between gap-2 px-1">
                  <SkeletonLine className="h-3 w-20" />
                  <SkeletonBlock className="h-5 w-8 rounded-full" />
                </div>
                <SkeletonBlock className="h-24 rounded-xl" />
                <SkeletonBlock className="h-24 rounded-xl" />
              </div>
            ))}
          </div>
        </div>
      </section>
      <section>
        <SkeletonLine className="mb-3 h-3 w-40" />
        <div className="grid gap-3 lg:grid-cols-2">
          <SkeletonBlock className="h-20 rounded-xl" />
          <SkeletonBlock className="h-20 rounded-xl" />
        </div>
      </section>
    </div>
  );
}

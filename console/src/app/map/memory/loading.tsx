import { SkeletonBlock, SkeletonHeader } from "@/components/Skeleton";

export default function MemoryGraphLoading() {
  return (
    <div className="flex flex-col gap-6">
      <SkeletonHeader />
      <SkeletonBlock className="h-10 w-full rounded-lg" />
      <div className="flex flex-wrap items-center gap-2">
        {[0, 1, 2, 3, 4].map((i) => (
          <SkeletonBlock key={i} className="h-5 w-20 rounded-full" />
        ))}
      </div>
      <div className="card p-5">
        <SkeletonBlock className="h-[420px] w-full rounded-xl" />
      </div>
    </div>
  );
}

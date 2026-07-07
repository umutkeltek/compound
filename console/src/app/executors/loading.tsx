import { SkeletonBlock, SkeletonCard, SkeletonHeader, SkeletonLine } from "@/components/Skeleton";

export default function ExecutorsLoading() {
  return (
    <div className="flex flex-col gap-6">
      <SkeletonHeader />
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {[0, 1, 2, 3].map((i) => (
          <div key={i} className="card flex flex-col gap-2 p-5">
            <SkeletonLine className="h-3 w-16" />
            <SkeletonBlock className="h-7 w-10 rounded-md" />
          </div>
        ))}
      </div>
      <SkeletonCard />
      <div className="grid gap-4 lg:grid-cols-2">
        <SkeletonBlock className="h-64 rounded-2xl" />
        <SkeletonBlock className="h-64 rounded-2xl" />
      </div>
    </div>
  );
}

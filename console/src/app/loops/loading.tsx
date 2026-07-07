import { SkeletonBlock, SkeletonLine } from "@/components/Skeleton";

export default function LoopsLoading() {
  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-col gap-2">
        <SkeletonLine className="h-5 w-48" />
        <SkeletonLine className="h-3 w-full max-w-xl" />
      </div>

      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        {[0, 1, 2, 3].map((i) => (
          <div key={i} className="card flex flex-col gap-2 p-5">
            <SkeletonLine className="h-3 w-16" />
            <SkeletonBlock className="h-7 w-10 rounded-md" />
          </div>
        ))}
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        {[0, 1, 2, 3].map((i) => (
          <SkeletonBlock key={i} className="h-40 rounded-xl" />
        ))}
      </div>
    </div>
  );
}

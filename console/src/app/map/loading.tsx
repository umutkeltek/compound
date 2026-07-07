import { SkeletonBlock, SkeletonLine } from "@/components/Skeleton";

export default function MapLoading() {
  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div className="flex flex-col gap-2">
          <SkeletonLine className="h-5 w-40" />
          <SkeletonLine className="h-3 w-96" />
        </div>
        <div className="flex items-center gap-2">
          <SkeletonBlock className="h-6 w-32 rounded-full" />
          <SkeletonBlock className="h-7 w-16 rounded-lg" />
        </div>
      </div>
      <div className="flex flex-wrap items-center gap-3">
        <SkeletonBlock className="h-7 w-20 rounded-full" />
        <SkeletonBlock className="h-7 w-24 rounded-full" />
        <SkeletonBlock className="h-7 w-24 rounded-full" />
      </div>
      <div className="flex h-[600px] items-center justify-center rounded-xl border border-line bg-[#0b0f19] text-[12.5px] text-slate-400">
        loading map...
      </div>
    </div>
  );
}

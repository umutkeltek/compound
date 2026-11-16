import { SkeletonBlock, SkeletonLine } from "@/components/Skeleton";

/**
 * The shared loading frame for pages without their own: the shape every page
 * has (header, purpose line, tab row, a few blocks), not any one page's
 * layout, so it never shows a layout the page will not have.
 */
export default function PageLoading() {
  return (
    <div className="flex flex-col gap-6" data-skeleton="page" aria-busy="true" aria-label="Loading the page">
      <div className="flex flex-col gap-3">
        <SkeletonLine className="h-8 w-64" />
        <SkeletonLine className="h-3 w-full max-w-xl" />
        <div className="flex gap-5 border-b border-hair pb-2">
          <SkeletonLine className="h-3 w-16" />
          <SkeletonLine className="h-3 w-20" />
          <SkeletonLine className="h-3 w-14" />
        </div>
      </div>
      <SkeletonBlock className="h-28 w-full" />
      <SkeletonBlock className="h-40 w-full" />
      <SkeletonBlock className="h-40 w-full" />
    </div>
  );
}

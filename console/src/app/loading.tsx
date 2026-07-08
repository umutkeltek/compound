import { SkeletonBlock, SkeletonLine } from "@/components/Skeleton";

/** Mirrors the briefing home: greeting + attention chip, insight line, DECIDE rows + rail, KNOWS strip. */
export default function DashboardLoading() {
  return (
    <div className="mx-auto max-w-[1060px] px-7 pt-14 pb-[72px]">
      <header className="flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
        <div className="flex flex-col gap-2">
          <SkeletonLine className="h-8 w-56" />
          <SkeletonLine className="h-3 w-40" />
        </div>
        <SkeletonBlock className="h-7 w-40 rounded-full" />
      </header>

      <section className="my-[52px] max-w-[44ch]">
        <SkeletonLine className="h-6 w-full" />
        <SkeletonLine className="mt-2 h-6 w-4/5" />
      </section>

      <div className="grid gap-7 lg:grid-cols-[minmax(0,3fr)_minmax(300px,2fr)] lg:items-start">
        <section>
          <SkeletonLine className="mb-4 h-3 w-20" />
          <div className="overflow-hidden rounded-[14px] border border-line bg-white">
            {[0, 1, 2, 3].map((row) => (
              <div
                key={row}
                className={`flex items-center justify-between gap-4 px-5 py-4 ${row > 0 ? "border-t border-line/70" : ""}`}
              >
                <div className="flex min-w-0 flex-1 flex-col gap-2">
                  <SkeletonLine className="h-4 w-3/4" />
                  <SkeletonLine className="h-3 w-1/2" />
                </div>
                <SkeletonBlock className="h-8 w-20 rounded-lg" />
              </div>
            ))}
          </div>
        </section>

        <div className="flex flex-col gap-10">
          {["Agents", "World Delta"].map((label) => (
            <section key={label}>
              <SkeletonLine className="mb-4 h-3 w-16" />
              <div className="flex flex-col gap-2 rounded-[14px] border border-line bg-white p-4">
                <SkeletonLine className="h-3 w-full" />
                <SkeletonLine className="h-3 w-4/5" />
                <SkeletonLine className="h-3 w-3/5" />
              </div>
            </section>
          ))}
        </div>
      </div>

      <section className="mt-14">
        <SkeletonLine className="mb-4 h-3 w-16" />
        <div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-4">
          {[0, 1, 2, 3].map((i) => (
            <div key={i} className="flex flex-col gap-2">
              <SkeletonBlock className="h-7 w-14 rounded-md" />
              <SkeletonLine className="h-3 w-24" />
            </div>
          ))}
        </div>
      </section>
    </div>
  );
}

"use client";

import { useRouter } from "next/navigation";
import { useEffect, useState, useTransition } from "react";

export function RefreshControl() {
  const router = useRouter();
  const [auto, setAuto] = useState(true);
  const [tick, setTick] = useState(0);
  const [pending, startTransition] = useTransition();

  useEffect(() => {
    if (!auto) return;
    const id = setInterval(() => {
      startTransition(() => router.refresh());
      setTick((t) => t + 1);
    }, 30_000);
    return () => clearInterval(id);
  }, [auto, router]);

  return (
    <div className="flex items-center gap-2" data-tick={tick}>
      <button
        onClick={() => setAuto(!auto)}
        className={`rounded-full px-2.5 py-1 text-[11.5px] font-medium transition-colors ${
          auto ? "bg-emerald-50 text-emerald-700" : "bg-slate-100 text-slate-500"
        }`}
        title="Refresh every 30 seconds"
      >
        auto 30s {auto ? "on" : "off"}
      </button>
      <button
        onClick={() => startTransition(() => router.refresh())}
        disabled={pending}
        className="rounded-full bg-slate-100 px-2.5 py-1 text-[11.5px] font-medium text-slate-600 transition-colors hover:bg-slate-200 disabled:cursor-wait disabled:opacity-60"
      >
        {pending ? "refreshing..." : "refresh"}
      </button>
    </div>
  );
}

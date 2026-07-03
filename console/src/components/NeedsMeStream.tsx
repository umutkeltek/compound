"use client";

import { type ReactNode, useState } from "react";

import { FilterChipRow } from "@/components/FilterChipRow";
import { EmptyState } from "@/components/ui";

/**
 * A unified, filterable stream of everything needing the operator. Each slot
 * (reviews / work / memory) is server-rendered cards passed in as a named
 * ReactNode prop — the client side only toggles which slot is visible.
 */
export function NeedsMeStream({
  slots,
  counts,
}: {
  slots: Record<string, ReactNode>;
  counts: Record<string, number>;
}) {
  const types = Object.keys(slots).filter((t) => (counts[t] ?? 0) > 0);
  const total = types.reduce((n, t) => n + (counts[t] ?? 0), 0);
  const [filter, setFilter] = useState("all");

  if (total === 0) {
    return <EmptyState>Nothing needs you right now — queue is clear.</EmptyState>;
  }

  const options = [
    { value: "all", label: "All" },
    ...types.map((t) => ({ value: t, label: t.charAt(0).toUpperCase() + t.slice(1) })),
  ];
  const chipCounts = { all: total, ...counts };

  return (
    <div className="flex flex-col gap-3">
      <FilterChipRow options={options} active={filter} counts={chipCounts} onChange={setFilter} />
      <div className="grid gap-3 lg:grid-cols-2">
        {filter === "all"
          ? types.map((t) => <div key={t} className="contents">{slots[t]}</div>)
          : (slots[filter] ?? <EmptyState>Nothing in this filter.</EmptyState>)}
      </div>
    </div>
  );
}

"use client";

import { useRouter } from "next/navigation";
import { FormEvent, useState } from "react";

export function MemorySeedSearch({ initial }: { initial: string }) {
  const router = useRouter();
  const [q, setQ] = useState(initial);

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const seed = q.trim();
    if (seed) router.push(`/map/memory?seed=${encodeURIComponent(seed)}`);
  }

  return (
    <form onSubmit={submit} className="flex gap-2">
      <input
        value={q}
        onChange={(e) => setQ(e.target.value)}
        placeholder="seed: an entity slug (umut-os, knowledge:arxiv:…) or a claim id (cl_…)"
        className="min-w-0 flex-1 rounded-lg border border-line bg-white px-3 py-2 text-[12.5px] text-ink outline-none focus:border-slate-300"
      />
      <button type="submit" className="rounded-lg bg-ink px-3 py-2 text-[12px] font-semibold text-white">
        Show
      </button>
    </form>
  );
}

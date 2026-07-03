"use client";

import { useRouter } from "next/navigation";
import { FormEvent, useState } from "react";

import { CopyCmd } from "@/components/CopyCmd";
import { REVIEW_DECISIONS } from "@/lib/review-yaml";

type Result = {
  ok: boolean;
  stdout?: string;
  stderr?: string;
};

export function ReviewDecisionForm({
  reviewId,
  command,
  defaultDecision,
  defaultRationale,
  defaultConfidence,
}: {
  reviewId: string;
  command: string;
  defaultDecision: string;
  defaultRationale: string;
  defaultConfidence: string;
}) {
  const router = useRouter();
  const [result, setResult] = useState<Result | null>(null);
  const [pending, setPending] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setPending(true);
    setResult(null);

    const form = new FormData(event.currentTarget);
    const response = await fetch("/api/reviews/record", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        id: reviewId,
        decision: form.get("decision"),
        rationale: form.get("rationale"),
        confidence: form.get("confidence"),
      }),
    });
    const body = (await response.json()) as Result;
    setResult(body);
    setPending(false);
    if (body.ok) router.refresh();
  }

  return (
    <form onSubmit={submit} className="mt-3 flex flex-col gap-3 border-t border-line pt-3">
      <div className="grid gap-3 md:grid-cols-[1fr_120px]">
        <label className="flex flex-col gap-1">
          <span className="section-label">Decision</span>
          <select
            name="decision"
            defaultValue={defaultDecision || "keep_needs_review"}
            className="rounded-lg border border-line bg-white px-3 py-2 text-[12.5px] text-ink outline-none focus:border-slate-300"
          >
            {REVIEW_DECISIONS.map((decision) => (
              <option key={decision} value={decision}>
                {decision}
              </option>
            ))}
          </select>
        </label>
        <label className="flex flex-col gap-1">
          <span className="section-label">Confidence</span>
          <input
            name="confidence"
            type="number"
            min="0"
            max="1"
            step="0.01"
            defaultValue={defaultConfidence}
            className="rounded-lg border border-line bg-white px-3 py-2 text-[12.5px] text-ink outline-none focus:border-slate-300"
          />
        </label>
      </div>
      <label className="flex flex-col gap-1">
        <span className="section-label">Rationale</span>
        <textarea
          name="rationale"
          required
          defaultValue={defaultRationale}
          placeholder="Write the operator rationale. This becomes the recorded evidence for the decision."
          className="min-h-[88px] resize-y rounded-lg border border-line bg-white px-3 py-2 text-[12.5px] leading-relaxed text-ink outline-none focus:border-slate-300"
        />
      </label>
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="submit"
          disabled={pending}
          className="rounded-lg bg-ink px-3 py-2 text-[12px] font-semibold text-white disabled:cursor-wait disabled:opacity-55"
        >
          {pending ? "Recording..." : "Record decision"}
        </button>
        <CopyCmd cmd={command} compact />
      </div>
      {result ? (
        <div
          className={`rounded-lg border px-3 py-2 text-[12px] leading-relaxed ${
            result.ok
              ? "border-emerald-200 bg-emerald-50 text-emerald-700"
              : "border-red-200 bg-red-50 text-red-600"
          }`}
        >
          {result.ok ? "Recorded." : result.stderr || result.stdout || "Command failed."}
        </div>
      ) : null}
    </form>
  );
}

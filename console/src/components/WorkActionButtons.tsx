"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";

import { CopyCmd } from "@/components/CopyCmd";

type WorkAction = "admit" | "cancel";

type Result = {
  ok: boolean;
  stdout?: string;
  stderr?: string;
};

export function WorkActionButtons({
  workId,
  admitCommand,
  cancelCommand,
}: {
  workId: string;
  admitCommand: string;
  cancelCommand: string;
}) {
  const router = useRouter();
  const [confirming, setConfirming] = useState<WorkAction | null>(null);
  const [pending, setPending] = useState(false);
  const [result, setResult] = useState<Result | null>(null);

  const command = confirming === "cancel" ? cancelCommand : admitCommand;

  async function execute(action: WorkAction) {
    setPending(true);
    setResult(null);
    const response = await fetch("/api/work/act", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ id: workId, action }),
    });
    const body = (await response.json()) as Result;
    setResult(body);
    setPending(false);
    setConfirming(null);
    if (body.ok) router.refresh();
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          onClick={() => setConfirming("admit")}
          className="rounded-lg bg-ink px-3 py-2 text-[12px] font-semibold text-white"
        >
          Admit
        </button>
        <button
          type="button"
          onClick={() => setConfirming("cancel")}
          className="rounded-lg border border-line bg-white px-3 py-2 text-[12px] font-semibold text-ink-2 hover:bg-slate-50"
        >
          Cancel
        </button>
      </div>
      {confirming ? (
        <div className="rounded-lg border border-line bg-slate-50 p-3">
          <div className="mb-2 flex flex-wrap items-center gap-2">
            <span className="text-[12px] font-semibold text-ink">Confirm {confirming}</span>
            <CopyCmd cmd={command} />
          </div>
          <div className="flex items-center gap-2">
            <button
              type="button"
              disabled={pending}
              onClick={() => execute(confirming)}
              className="rounded-lg bg-ink px-3 py-1.5 text-[12px] font-semibold text-white disabled:cursor-wait disabled:opacity-55"
            >
              {pending ? "Running..." : `Run ${confirming}`}
            </button>
            <button
              type="button"
              disabled={pending}
              onClick={() => setConfirming(null)}
              className="rounded-lg px-3 py-1.5 text-[12px] font-semibold text-ink-3 hover:bg-white"
            >
              Keep pending
            </button>
          </div>
        </div>
      ) : null}
      {result ? (
        <div
          className={`rounded-lg border px-3 py-2 text-[12px] leading-relaxed ${
            result.ok
              ? "border-emerald-200 bg-emerald-50 text-emerald-700"
              : "border-red-200 bg-red-50 text-red-600"
          }`}
        >
          {result.ok ? result.stdout || "Done." : result.stderr || result.stdout || "Command failed."}
        </div>
      ) : null}
    </div>
  );
}

"use client";

import { useRouter } from "next/navigation";
import { useState } from "react";

import { CliCaption } from "@/components/CopyCmd";

type Result = { ok: boolean; stdout?: string; stderr?: string };

export function DispatchStepButton({
  workId,
  actor,
  command,
}: {
  workId: string;
  actor: string;
  command: string[];
}) {
  const router = useRouter();
  const [pending, setPending] = useState(false);
  const [result, setResult] = useState<Result | null>(null);

  async function run() {
    setPending(true);
    setResult(null);
    const response = await fetch("/api/decision/execute", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({
        area: "work-dispatch",
        itemId: workId,
        option: "dispatch_step",
        fields: { actor },
      }),
    });
    const body = (await response.json()) as Result;
    setResult(body);
    setPending(false);
    if (body.ok) router.refresh();
  }

  return (
    <div className="flex flex-col gap-2 rounded-xl border border-emerald-100 bg-emerald-50 px-3 py-2">
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          disabled={pending}
          onClick={run}
          className="rounded-lg bg-emerald-600 px-3 py-2 text-[12px] font-semibold text-white disabled:cursor-wait disabled:opacity-55"
        >
          {pending ? "Continuing..." : "Continue"}
        </button>
        <CliCaption argv={command.length ? command.slice(1) : null} />
      </div>
      <p className="text-[11.5px] leading-relaxed text-emerald-800">
        Moves the next safe lifecycle step. Does not run task commands.
      </p>
      {result ? (
        <div
          className={`rounded-lg border px-3 py-2 text-[12px] leading-relaxed ${
            result.ok
              ? "border-emerald-200 bg-white text-emerald-700"
              : "border-red-200 bg-red-50 text-red-600"
          }`}
        >
          {result.ok ? "Continued." : result.stderr || result.stdout || "Command failed."}
        </div>
      ) : null}
    </div>
  );
}

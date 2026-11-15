"use client";

import { useState } from "react";
import { Button } from "@/components/kit";

/** A deliberate clipboard handoff, never presented as a scheduled execution. */
export function AgentHandoff({ title, context, reference }: { title: string; context?: string; reference: string }) {
  const [status, setStatus] = useState<"idle" | "copied" | "failed">("idle");
  const brief = `Help move this forward: ${title}\n${context ? `${context}\n` : ""}Reference: ${reference}\nRecheck the current record and permissions. Resolve what you can within the existing authorization, verify the result, and record the next step. If blocked, identify the exact missing dependency. Do not assume this brief grants new permissions.`;
  async function copy() {
    try {
      await navigator.clipboard.writeText(brief);
      setStatus("copied");
    } catch { setStatus("failed"); }
  }
  return (
    <div className="grid gap-2">
      <Button onClick={copy}>Copy task for an agent</Button>
      <p role="status" className="text-xs leading-relaxed text-muted">
        {status === "copied" ? "Copied. Paste it into your agent conversation to continue. Nothing has started yet."
          : status === "failed" ? "Copy did not work. Open the task below and copy it manually."
            : "Includes the task and its reference. Copying does not start a run."}
      </p>
      <details className="text-xs text-muted"><summary className="cursor-pointer">View task</summary><pre className="mt-2 whitespace-pre-wrap break-words rounded-md bg-soft p-3 font-sans leading-relaxed">{brief}</pre></details>
    </div>
  );
}

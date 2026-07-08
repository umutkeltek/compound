"use client";

import { useState } from "react";

/**
 * Terminal escape hatch: copy the exact CLI command. Primary operator buttons
 * may execute allowlisted local commands through the governed decision API.
 */
export function CopyCmd({ cmd, compact = false }: { cmd: string; compact?: boolean }) {
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(cmd);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard unavailable — show the command anyway */
    }
  }

  return (
    <button
      onClick={copy}
      title={cmd}
      className="group inline-flex max-w-full items-center gap-2 rounded-lg border border-line bg-slate-50 px-2.5 py-1.5 text-left transition-colors hover:bg-slate-100"
    >
      {!compact && (
        <code className="mono truncate text-[11.5px] text-ink-2">{cmd}</code>
      )}
      <span
        className={`shrink-0 text-[11px] font-semibold ${
          copied ? "text-emerald-600" : "text-slate-400 group-hover:text-slate-600"
        }`}
      >
        {copied ? "copied" : "copy"}
      </span>
    </button>
  );
}

/**
 * The demoted CopyCmd: a small, subdued "what will happen" caption showing the
 * exact CLI a one-click button runs — transparency + a terminal escape hatch.
 * NOT the primary affordance (the button is). Kept by DEC-0021.
 */
export function CliCaption({ argv }: { argv: string[] | null }) {
  const [copied, setCopied] = useState(false);
  if (!argv || argv.length === 0) return null;
  const cmd = `compound ${argv.join(" ")}`;

  async function copy() {
    try {
      await navigator.clipboard.writeText(cmd);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard unavailable */
    }
  }

  return (
    <button
      onClick={copy}
      title={`runs: ${cmd}`}
      className="group mt-2 inline-flex max-w-full items-center gap-1.5 text-left"
    >
      <span className="mono truncate text-[10.5px] text-ink-3">$ {cmd}</span>
      <span
        className={`shrink-0 text-[10px] font-semibold ${
          copied ? "text-emerald-600" : "text-slate-300 group-hover:text-slate-500"
        }`}
      >
        {copied ? "copied" : "copy"}
      </span>
    </button>
  );
}

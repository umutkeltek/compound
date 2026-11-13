"use client";

import { useState } from "react";

/** A labelled copy button: says what it copies, shows the full command on hover, never runs it. */
export function CopyButton({ text, label, copiedLabel = "Copied" }: { text: string; label: string; copiedLabel?: string }) {
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(text);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      /* clipboard unavailable — the title still shows the command */
    }
  }

  return (
    <button
      type="button"
      onClick={copy}
      title={`Copies: ${text}`}
      className="inline-flex shrink-0 items-center gap-1.5 rounded-lg border border-hair bg-panel px-2.5 py-1 text-xs font-medium text-muted shadow-sm transition-colors hover:bg-soft hover:text-ink"
    >
      <svg viewBox="0 0 16 16" className="h-3.5 w-3.5" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden>
        <rect x="5" y="5" width="8.5" height="8.5" rx="1.5" />
        <path d="M10.5 5V3.5A1.5 1.5 0 0 0 9 2H3.5A1.5 1.5 0 0 0 2 3.5V9a1.5 1.5 0 0 0 1.5 1.5H5" />
      </svg>
      <span className={copied ? "text-good" : ""}>{copied ? copiedLabel : label}</span>
    </button>
  );
}

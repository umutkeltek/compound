"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useTransition } from "react";
import { PageHeader } from "@/components/kit";

/**
 * A page that failed while it was being built. Nothing was changed; the rest
 * of the console still works. "Try again" asks the server for the page anew.
 * The error's reference stays in a closed note for agents.
 */
export default function PageError({ error, reset }: { error: Error & { digest?: string }; reset: () => void }) {
  const router = useRouter();
  const [pending, startTransition] = useTransition();
  return (
    <div className="flex flex-col gap-6" data-page-error>
      <PageHeader
        title="This page could not be shown"
        lead="Something failed while this page was being built. Nothing was changed. Try again; if it keeps failing, the other pages still work."
        action={null}
      />
      <div className="flex flex-wrap items-center gap-3">
        <button
          type="button"
          disabled={pending}
          onClick={() => startTransition(() => { router.refresh(); reset(); })}
          className="inline-flex min-h-11 items-center justify-center rounded-md border border-strong bg-strong px-4 py-2 text-sm font-semibold text-panel transition-colors hover:bg-ink disabled:cursor-wait disabled:opacity-60"
        >
          {pending ? "Trying again…" : "Try again"}
        </button>
        <Link href="/" className="text-sm font-semibold text-work hover:underline">Go to Today →</Link>
      </div>
      {error.digest ? (
        <details className="text-xs text-muted">
          <summary className="cursor-pointer">Details for agents</summary>
          <p className="mt-2">Error reference <span className="mono">{error.digest}</span>, in the console server log.</p>
        </details>
      ) : null}
    </div>
  );
}

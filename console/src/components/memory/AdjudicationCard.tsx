"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useMemo, useRef, useState } from "react";

import { Button, Field, IdRef } from "@/components/kit";
import { ForAgents } from "@/components/settings/primitives";
import { evidenceHistoryHref } from "@/lib/links";
import { MEMORY_CHOICE_HANDOFF } from "@/lib/plain-words";
import { ago } from "@/lib/time";
import type { AdjudicationCardModel } from "./model";

/*
 * One proposed memory or proposed rule waiting for the operator's choice.
 *
 * The standard browser cannot record the choice (console readiness is
 * projection-only), so the card does not show a chooser or a button that
 * cannot work. It says what each choice would do, one plain sentence on who
 * records it (an agent, by the command folded under "For agents"), and where
 * the statement came from. The live chooser below renders only when console
 * readiness says the browser may act; it posts the SAME payload the Decide
 * queue posts to the one governed endpoint.
 *
 * The card's `id` is the candidate id, so `/memory#<id>` lands on it.
 */

type ExecuteResult = { ok: boolean; status?: number; stdout?: string; stderr?: string };
type ActState = "idle" | "pending" | "confirmed" | "error";

/** Option accent (registry Accent) → semantic token dot — tokens only, no raw hex. */
const DOT_TONE: Record<string, string> = {
  green: "bg-good",
  orange: "bg-seam",
  red: "bg-crit",
  blue: "bg-work",
  violet: "bg-memory",
  pink: "bg-product",
  gray: "bg-faint",
};

function LiveChooser({ model }: { model: AdjudicationCardModel }) {
  const router = useRouter();
  const { formSpec } = model;
  const [option, setOption] = useState(formSpec.defaultOption ?? formSpec.options[0]?.value ?? "");
  const [state, setState] = useState<ActState>("idle");
  const [message, setMessage] = useState<string | null>(null);
  const formRef = useRef<HTMLFormElement | null>(null);
  const label = (value: string) => model.choices.find((c) => c.value === value)?.label ?? value;
  const visibleFields = useMemo(
    () => formSpec.fields.filter((f) => !f.showForOptions || f.showForOptions.includes(option)),
    [formSpec.fields, option],
  );

  async function submit() {
    if (formRef.current) {
      for (const f of visibleFields) {
        if (!f.required) continue;
        const value = new FormData(formRef.current).get(f.name);
        if (!String(value ?? "").trim()) {
          setState("error");
          setMessage(`${f.label} is required.`);
          return;
        }
      }
    }
    setState("pending");
    setMessage(null);
    const fields: Record<string, string> = {};
    if (formRef.current) {
      const data = new FormData(formRef.current);
      for (const f of formSpec.fields) fields[f.name] = String(data.get(f.name) ?? "");
    }
    try {
      const response = await fetch("/api/decision/execute", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify({ area: formSpec.area, itemId: model.id, option, fields }),
      });
      const parsed = (await response.json().catch(() => null)) as ExecuteResult | null;
      const result: ExecuteResult = parsed ?? { ok: false, stderr: `Request failed with ${response.status}` };
      if (result.ok) {
        setState("confirmed");
        router.refresh();
        window.setTimeout(() => setState("idle"), 900);
        return;
      }
      setState("error");
      setMessage(result.stderr || result.stdout || "Command failed.");
    } catch (error) {
      setState("error");
      setMessage(error instanceof Error ? error.message : "Request failed.");
    }
  }

  const stateClass = state === "pending" ? "action-pending" : state === "confirmed" ? "action-confirmed" : state === "error" ? "action-error" : "";
  return (
    <div className="mt-3 flex flex-col gap-2">
      <div className="grid gap-1.5 sm:grid-cols-2">
        {formSpec.options.map((opt) => {
          const choice = model.choices.find((c) => c.value === opt.value);
          const isSel = opt.value === option;
          return (
            <button
              key={opt.value}
              type="button"
              onClick={() => setOption(opt.value)}
              aria-pressed={isSel}
              className={`flex flex-col gap-0.5 rounded-md border px-3 py-2 text-left transition-colors focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-focus ${
                isSel ? "border-strong bg-soft-2" : "border-hair bg-panel hover:bg-soft"
              }`}
            >
              <span className="flex items-center gap-1.5">
                <span className={`h-2 w-2 shrink-0 rounded-full ${DOT_TONE[opt.tone] ?? DOT_TONE.gray}`} />
                <span className="text-sm font-semibold text-ink">{choice?.label ?? opt.label}</span>
              </span>
              <span className="text-xs leading-snug text-muted">{choice?.means ?? opt.description}</span>
            </button>
          );
        })}
      </div>
      <form ref={formRef} className={visibleFields.length ? "flex flex-col gap-3" : "hidden"}>
        {visibleFields.map((f) => (
          <Field key={f.name} field={f} />
        ))}
      </form>
      <div>
        <Button variant="primary" disabled={state === "pending"} onClick={() => void submit()} className={stateClass}>
          {state === "pending" ? "Recording…" : state === "confirmed" ? "Choice recorded" : `Record · ${label(option)}`}
        </Button>
      </div>
      {state === "error" && message ? <p role="alert" className="break-words text-xs leading-snug text-crit">{message}</p> : null}
    </div>
  );
}

export function AdjudicationCard({
  model,
  mutationsEnabled,
}: {
  model: AdjudicationCardModel;
  mutationsEnabled: boolean;
}) {
  const age = model.createdAt ? ago(model.createdAt) : "";
  const commands = model.choices.filter((c) => c.command);
  return (
    <article id={model.id} tabIndex={-1} className="admission-seam scroll-mt-6 rounded-lg border border-hair bg-panel px-4 py-3.5 shadow-sm">
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <span className="text-2xs font-bold uppercase tracking-wide text-faint">{model.kindLabel}</span>
          <h3 className="mt-0.5 text-md font-semibold leading-snug text-ink">{model.headline}</h3>
          {model.fullStatement !== model.headline && model.fullStatement ? (
            <details className="mt-2 text-sm text-muted"><summary className="cursor-pointer font-medium">Read full statement</summary><p className="mt-2 whitespace-pre-wrap leading-relaxed">{model.fullStatement}</p></details>
          ) : null}
        </div>
        <span className="shrink-0 rounded-pill border border-seam-line bg-seam-soft px-2.5 py-0.5 text-xs font-semibold text-seam">
          {model.stateLabel}
        </span>
      </div>

      {/* one provenance line: about · from · age */}
      <p className="mt-2 flex flex-wrap items-baseline gap-x-2 gap-y-1 text-xs leading-relaxed text-muted" aria-label="What it is about and where it came from">
        {model.about ? (
          <span>
            About:{" "}
            {model.about.href ? (
              <Link href={model.about.href} title={model.about.raw} className="font-semibold text-work hover:underline">{model.about.label}</Link>
            ) : (
              <span className="font-semibold text-ink" title={model.about.raw}>{model.about.label}</span>
            )}
          </span>
        ) : null}
        {model.about ? <span aria-hidden className="text-faint">·</span> : null}
        <span>
          From:{" "}
          {model.from.href ? (
            <Link href={model.from.href} className="font-semibold text-work hover:underline">{model.from.text}</Link>
          ) : (
            <span className="font-semibold text-ink">{model.from.text}</span>
          )}
        </span>
        {age ? <span aria-hidden className="text-faint">·</span> : null}
        {age ? <span className="tabular-nums">added {age}</span> : null}
      </p>

      {mutationsEnabled ? (
        <LiveChooser model={model} />
      ) : (
        <>
          <dl className="mt-3 grid gap-x-3 gap-y-1 rounded-md bg-soft px-3 py-2 text-xs leading-relaxed sm:grid-cols-[auto_1fr]">
            {model.choices.map((choice) => (
              <div key={choice.value} className="contents">
                <dt className="font-semibold text-ink">{choice.label}</dt>
                <dd className="text-muted">{choice.means}</dd>
              </div>
            ))}
          </dl>
          <p className="mt-2 text-xs leading-relaxed text-muted">{MEMORY_CHOICE_HANDOFF}</p>
        </>
      )}

      <div className="mt-3 flex flex-wrap items-center gap-x-4 gap-y-2 text-xs font-semibold">
        <Link href={evidenceHistoryHref(model.id)} className="text-work hover:underline">Read its history →</Link>
        <Link href={`/memory?graph=${encodeURIComponent(model.seed)}#memory-graph`} scroll className="text-work hover:underline">Show in graph →</Link>
        <span className="ml-auto font-normal"><IdRef id={model.id} kind="candidate" /></span>
      </div>

      <ForAgents
        label="For agents: record a choice, and the technical record"
        cmds={mutationsEnabled ? [] : commands.map((c) => c.command)}
      >
        {model.technical.length ? (
          <dl className="mono grid gap-x-3 gap-y-0.5 text-2xs sm:grid-cols-[auto_1fr]">
            {model.technical.map((row) => (
              <div key={row.label} className="contents">
                <dt className="text-faint">{row.label}</dt>
                <dd className="break-all text-muted">{row.value}</dd>
              </div>
            ))}
          </dl>
        ) : null}
        {!mutationsEnabled && commands.length ? (
          <p className="text-2xs text-faint">One command per choice, in the order above: {commands.map((c) => c.label).join(", ")}.</p>
        ) : null}
      </ForAgents>
    </article>
  );
}

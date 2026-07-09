"use client";

import * as Dialog from "@radix-ui/react-dialog";
import { useRouter } from "next/navigation";
import { useEffect, useRef, useState } from "react";

import { CopyCmd } from "@/components/CopyCmd";
import { Button, Field } from "@/components/kit";
import type { DecideAct } from "@/lib/decide-model";
import type { FieldSpec } from "@/lib/decision-registry";

type ExecuteResult = { ok: boolean; status?: number; stdout?: string; stderr?: string };
type ActState = "idle" | "pending" | "confirmed" | "error";

/** The three governed single-primary acts (batch-enumerable renders its own UI). */
type SingleAct = Extract<DecideAct, { mode: "bespoke" | "batch-atomic" | "generic" }>;

function argvPreview(act: SingleAct): string[] | null {
  return act.argv ?? null;
}

function actFields(act: SingleAct): FieldSpec[] {
  return act.mode === "bespoke" ? act.fields : [];
}

/**
 * One governed primary act. Click → (confirm dialog if required) → POST to the
 * one governed endpoint /api/decision/execute → optimistic pending/confirm/error
 * (DESIGN.md §5), then router.refresh() so the queue re-reads live truth.
 *
 * Payloads honor each pathway's contract:
 *   - bespoke / batch-atomic  → {area, itemId, option, fields}  (registry path)
 *   - generic                 → {area, itemId, actionId, confirmed:true}  (Sonnet
 *                               generic pathway; a 403 = area not allowlisted →
 *                               we fall back to the honest copy command).
 *
 * `registerTrigger` lets the parent queue fire this act from the `a` key without
 * ever auto-firing: `a` calls open(), which opens the dialog when confirmation
 * is required rather than executing.
 */
export function GovernedAct({
  act,
  mutationsEnabled,
  registerTrigger,
  onExecuted,
}: {
  act: SingleAct;
  mutationsEnabled: boolean;
  registerTrigger?: (fn: (() => void) | null) => void;
  onExecuted?: (result: ExecuteResult) => void;
}) {
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [state, setState] = useState<ActState>("idle");
  const [message, setMessage] = useState<string | null>(null);
  const [fellBackToCopy, setFellBackToCopy] = useState(false);
  const formRef = useRef<HTMLFormElement | null>(null);

  const fields = actFields(act);
  const preview = argvPreview(act);

  function trigger() {
    if (!mutationsEnabled) return; // gated off → button is disabled; no-op
    if (act.requiresConfirmation || fields.length > 0) setOpen(true);
    else void submit();
  }

  useEffect(() => {
    registerTrigger?.(trigger);
    return () => registerTrigger?.(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [act, mutationsEnabled]);

  function buildBody(): Record<string, unknown> {
    if (act.mode === "generic") {
      return { area: act.area, itemId: act.itemId, actionId: act.actionId, confirmed: true };
    }
    const collected: Record<string, string> = {};
    if (act.mode === "batch-atomic") Object.assign(collected, act.fields);
    if (formRef.current) {
      const data = new FormData(formRef.current);
      for (const [k, v] of data.entries()) collected[k] = typeof v === "string" ? v : "";
    }
    return { area: act.area, itemId: act.itemId, option: act.option, fields: collected };
  }

  async function submit() {
    // A required field that is still empty must block the POST (the endpoint
    // rejects it too, but stop here so the operator sees which field).
    if (formRef.current) {
      for (const f of fields) {
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
    try {
      const response = await fetch("/api/decision/execute", {
        method: "POST",
        headers: { "content-type": "application/json" },
        body: JSON.stringify(buildBody()),
      });
      const parsed = (await response.json().catch(() => null)) as ExecuteResult | null;
      const result: ExecuteResult = parsed
        ? { ...parsed, status: response.status }
        : { ok: false, status: response.status, stderr: `Request failed with ${response.status}` };
      onExecuted?.(result);
      if (result.ok) {
        setState("confirmed");
        setOpen(false);
        router.refresh();
        window.setTimeout(() => setState("idle"), 900);
        return;
      }
      // Generic pathway: a 403 means the area is not allowlisted for one-click
      // — fall back to the honest copy command rather than pretending.
      if (act.mode === "generic" && response.status === 403) {
        setFellBackToCopy(true);
        setOpen(false);
        setState("idle");
        return;
      }
      setState("error");
      setMessage(result.stderr || result.stdout || "Command failed.");
      window.setTimeout(() => setState("idle"), 1600);
    } catch (error) {
      setState("error");
      setMessage(error instanceof Error ? error.message : "Request failed.");
      window.setTimeout(() => setState("idle"), 1600);
    }
  }

  if (fellBackToCopy && act.mode === "generic") {
    return (
      <div className="flex flex-col items-start gap-1">
        <CopyCmd cmd={act.copyCommand} compact />
        <span className="text-2xs text-faint">No one-click action wired yet for this area.</span>
      </div>
    );
  }

  const stateClass =
    state === "pending"
      ? "action-pending"
      : state === "confirmed"
        ? "action-confirmed"
        : state === "error"
          ? "action-error"
          : "";

  return (
    <div className="flex flex-col items-start gap-1.5">
      <Button
        variant="primary"
        disabled={!mutationsEnabled || state === "pending"}
        onClick={trigger}
        className={stateClass}
        title={mutationsEnabled ? undefined : "Set COMPOUND_CONSOLE_MUTATIONS=1 to act from the console."}
      >
        {state === "pending" ? "Working…" : act.label}
      </Button>
      {state === "error" && message ? <span className="text-2xs leading-snug text-crit">{message}</span> : null}

      <Dialog.Root open={open} onOpenChange={setOpen}>
        <Dialog.Portal>
          <Dialog.Overlay className="fixed inset-0 z-50 bg-strong/35 backdrop-blur-sm motion-reduce:backdrop-blur-none" />
          <Dialog.Content className="fixed top-1/2 left-1/2 z-50 w-full max-w-md -translate-x-1/2 -translate-y-1/2 rounded-lg border border-hair bg-panel p-5 shadow-lg focus:outline-none">
            <Dialog.Title className="text-md font-semibold text-ink">{act.label}</Dialog.Title>
            <Dialog.Description className="mt-2 text-sm leading-relaxed text-muted">
              This runs a governed <code className="mono">compound</code> command. Each target gets its own
              PolicyDecision.
            </Dialog.Description>

            {act.mode === "batch-atomic" ? (
              <div className="mt-3 rounded-md border border-seam-line bg-seam-soft px-3 py-2 text-xs leading-relaxed text-seam">
                Applies to every one of the {act.memberCount} member
                {act.memberCount === 1 ? "" : "s"} together — members cannot be excluded one by one.
              </div>
            ) : null}

            {act.mode !== "generic" && act.policyNote ? (
              <div className="mt-3 rounded-md border border-hair bg-soft px-3 py-2 text-xs leading-relaxed text-muted">
                {act.policyNote}
              </div>
            ) : null}

            <form ref={formRef} className="mt-3 flex flex-col gap-3">
              {fields.map((f) => (
                <Field key={f.name} field={f} />
              ))}
            </form>

            {preview && preview.length ? (
              <pre className="mono mt-3 overflow-x-auto rounded-md border border-hair bg-soft px-3 py-2 text-2xs leading-relaxed text-muted">
                compound {preview.join(" ")}
              </pre>
            ) : null}

            <div className="mt-4 flex justify-end gap-2">
              <Dialog.Close asChild>
                <Button variant="secondary">Cancel</Button>
              </Dialog.Close>
              <Button variant="primary" disabled={state === "pending"} onClick={() => void submit()}>
                {state === "pending" ? "Working…" : "Confirm"}
              </Button>
            </div>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    </div>
  );
}

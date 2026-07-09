"use client";

import * as Dialog from "@radix-ui/react-dialog";

import { Button, type ButtonVariant } from "./Button";

/**
 * Governed-mutation confirmation (DESIGN.md §3): title, body, policy_note,
 * a mono argv preview block, confirm/cancel. Confirm uses Button primary;
 * danger variant when the action is destructive. Radix Dialog for correct
 * focus/keyboard/aria — styled entirely with tokens.
 */
export type ConfirmDialogProps = {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  body?: string;
  policyNote?: string;
  argv?: string[] | null;
  destructive?: boolean;
  confirmLabel?: string;
  cancelLabel?: string;
  pending?: boolean;
  onConfirm: () => void;
};

export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  body,
  policyNote,
  argv,
  destructive = false,
  confirmLabel = "Confirm",
  cancelLabel = "Cancel",
  pending = false,
  onConfirm,
}: ConfirmDialogProps) {
  const confirmVariant: ButtonVariant = destructive ? "danger" : "primary";
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-strong/35 backdrop-blur-sm motion-reduce:backdrop-blur-none" />
        <Dialog.Content className="fixed top-1/2 left-1/2 z-50 w-full max-w-md -translate-x-1/2 -translate-y-1/2 rounded-lg border border-hair bg-panel p-5 shadow-lg focus:outline-none">
          <Dialog.Title className="text-md font-semibold text-ink">{title}</Dialog.Title>
          {body ? (
            <Dialog.Description className="mt-2 text-sm leading-relaxed text-muted">{body}</Dialog.Description>
          ) : null}

          {policyNote ? (
            <div className="mt-3 rounded-md border border-seam-line bg-seam-soft px-3 py-2 text-xs leading-relaxed text-seam">
              {policyNote}
            </div>
          ) : null}

          {argv && argv.length ? (
            <pre className="mono mt-3 overflow-x-auto rounded-md border border-hair bg-soft px-3 py-2 text-2xs leading-relaxed text-muted">
              compound {argv.join(" ")}
            </pre>
          ) : null}

          <div className="mt-4 flex justify-end gap-2">
            <Dialog.Close asChild>
              <Button variant="secondary">{cancelLabel}</Button>
            </Dialog.Close>
            <Button variant={confirmVariant} disabled={pending} onClick={onConfirm}>
              {pending ? "Working…" : confirmLabel}
            </Button>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}

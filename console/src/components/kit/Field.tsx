"use client";

import type { FieldSpec } from "@/lib/decision-registry";

/**
 * Renders the existing FieldSpec shape (src/lib/decision-registry.ts) as a
 * labeled, tokens-only form control with a focus-visible ring.
 */
const INPUT_CLASS =
  "rounded-md border border-hair bg-panel px-3 py-2 text-sm text-ink outline-none " +
  "focus-visible:outline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-focus";

function FieldControl({ field }: { field: FieldSpec }) {
  if (field.type === "textarea") {
    return (
      <textarea
        name={field.name}
        required={field.required}
        defaultValue={field.defaultValue}
        placeholder={field.placeholder}
        className={`min-h-20 resize-y ${INPUT_CLASS}`}
      />
    );
  }
  if (field.type === "select") {
    return (
      <select name={field.name} defaultValue={field.defaultValue} className={INPUT_CLASS}>
        {(field.options ?? []).map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </select>
    );
  }
  return (
    <input
      name={field.name}
      type={field.type === "number" ? "number" : "text"}
      min={field.min}
      max={field.max}
      step={field.step}
      required={field.required}
      defaultValue={field.defaultValue}
      placeholder={field.placeholder}
      className={INPUT_CLASS}
    />
  );
}

export function Field({ field }: { field: FieldSpec }) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-2xs font-bold uppercase tracking-wide text-faint">
        {field.label}
        {field.required ? <span className="text-crit"> *</span> : null}
      </span>
      <FieldControl field={field} />
    </label>
  );
}

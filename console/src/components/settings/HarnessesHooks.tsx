import { arr, num, str, type Json } from "@/lib/compound";

import { parseHarnessList } from "./data";
import { Card, CountChip, DoctrineNote, KV, SettingsSection } from "./primitives";

/**
 * Group 5 — Harnesses & hooks. Harness capability facts (parsed from the text
 * `compound harness list`) and the installed-hooks lock summary. Read-only:
 * hooks are clients, not brains — the adapter enforces governance, the harness
 * just carries the fixture.
 */
export function HarnessesHooks({
  harnessText,
  hooks,
  generatedAt,
}: {
  harnessText: string;
  hooks: Json | null;
  generatedAt?: string;
}) {
  const harnesses = parseHarnessList(harnessText);
  const hookCount = num(hooks?.hook_count);
  const hookHarnesses = arr(hooks?.harnesses).map(String);
  const readiness = arr(hooks?.pre_tool_use_readiness);

  return (
    <SettingsSection
      index={5}
      title="Harnesses & hooks"
      question="Which harnesses can compound govern, and where must the adapter enforce the gate?"
      source="compound contract hooks-authority"
      generatedAt={generatedAt}
    >
      <Card title="Harness registry" badge={<CountChip n={harnesses.length} label="harnesses" tone="muted" />}>
        {harnesses.length ? (
          <div className="flex flex-col gap-3">
            {harnesses.map((harness) => (
              <div key={harness.name} className="border-b border-hair pb-3 last:border-b-0 last:pb-0">
                <div className="flex flex-wrap items-baseline justify-between gap-2">
                  <span className="mono text-sm font-semibold text-ink">{harness.name}</span>
                  {harness.fallback ? (
                    <span className="mono rounded-pill border border-hair bg-soft px-2 py-0.5 text-2xs text-faint">
                      fallback
                    </span>
                  ) : null}
                </div>
                {harness.capabilities ? (
                  <p className="mt-0.5 text-2xs text-muted">{harness.capabilities}</p>
                ) : null}
                {harness.hooks ? (
                  <p className="mono mt-1 text-2xs leading-relaxed text-faint">{harness.hooks}</p>
                ) : null}
                {harness.limitations.length ? (
                  <ul className="mt-1 flex flex-col gap-0.5">
                    {harness.limitations.map((limitation, index) => (
                      <li key={index} className="text-2xs text-muted">
                        · {limitation}
                      </li>
                    ))}
                  </ul>
                ) : null}
              </div>
            ))}
          </div>
        ) : (
          <pre className="mono overflow-x-auto rounded-md border border-hair bg-soft p-3 text-2xs text-muted">
            {harnessText || "compound harness list produced no output"}
          </pre>
        )}
      </Card>

      <Card title="Installed-hooks lock" badge={<CountChip n={hookCount} label="hooks" tone="muted" />}>
        <KV k="installed harnesses" v={hookHarnesses.length ? hookHarnesses.join(", ") : "—"} mono={false} />
        {readiness.length ? (
          <div className="mt-2 overflow-x-auto">
            <table className="w-full text-left text-xs">
              <thead>
                <tr className="border-b border-hair text-2xs uppercase tracking-wide text-faint">
                  <th className="py-1.5 pr-3 font-semibold">harness</th>
                  <th className="py-1.5 pr-3 font-semibold">pre-tool gate</th>
                  <th className="py-1.5 font-semibold">status</th>
                </tr>
              </thead>
              <tbody className="mono">
                {readiness.map((row: Json, index: number) => {
                  const nativeBlock = Boolean(row.native_block) && Boolean(row.installed);
                  return (
                    <tr key={str(row.harness) || index} className="border-b border-hair last:border-b-0">
                      <td className="py-1.5 pr-3 text-ink">{str(row.harness)}</td>
                      <td className="py-1.5 pr-3">
                        <span className={nativeBlock ? "text-good" : "text-seam"}>
                          {nativeBlock ? "native block" : "adapter required"}
                        </span>
                      </td>
                      <td className="py-1.5 text-muted">{str(row.status).replace(/_/g, " ")}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        ) : null}
        <div className="mt-3 border-t border-hair pt-2.5">
          <DoctrineNote>
            Hooks are clients, not brains. Where a harness cannot block natively, the adapter must enforce the gate —
            a missing native hook is a wrapper requirement, never a governance hole.
          </DoctrineNote>
        </div>
      </Card>
    </SettingsSection>
  );
}

/*
 * ledger-to-facts.ts — Memory layer: distilled ledger -> a `## Facts` fence page.
 *
 * The fence is the system-of-record for the distilled positions. The CLI ingests
 * the page via engine.put(). Use type:note — the rented engine (gbrain) refuses to
 * extract facts from type:decision-ledger (eligibility gate); a real last-mile gap
 * the spike surfaced.
 */
import { readFileSync, writeFileSync, existsSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";

const HOME = process.env.HOME || "";
const BRAIN = process.env.COMPOUND_BRAIN || join(HOME, "brain");

const conf = (s: string) => (/high/i.test(s) ? "0.9" : /low/i.test(s) ? "0.5" : "0.7");
const kindOf = (c: string) => (/\b(always|never|by default|prefer|don'?t|stop)\b/i.test(c) ? "preference" : "commitment");
const esc = (s: string) => s.replace(/\|/g, "\\|").replace(/\n/g, " ").trim();

export function ledgerToFactsPage(opts: {
  ledger: string;
  slug: string;
  title: string;
}): { outPath: string; slug: string; count: number } {
  const text = readFileSync(opts.ledger, "utf8");
  const session = (text.match(/generated_from:\s*(.+)/)?.[1] || "distilled-session").trim();
  const sourceTag = `distill:${session.split("/").pop()?.replace(/\.jsonl$/, "") || "session"}`;
  const today = new Date().toISOString().slice(0, 10);

  const rows: string[] = [];
  let i = 0;
  for (const b of text.split(/\n- \*\*\[SELECT\]\*\* /).slice(1)) {
    const lines = b.split("\n");
    const claim = lines[0].trim();
    if (!claim) continue;
    const meta = lines.find((l) => l.includes("confidence:")) || "";
    const domain = (meta.match(/domain:\s*([^\s·]+)/)?.[1] || "").trim();
    i++;
    rows.push(
      `| ${i} | ${esc(claim)} | ${kindOf(claim)} | ${conf(meta)} | private | ${/high/i.test(meta) ? "high" : "medium"} | ${today} |  | ${sourceTag} | ${domain ? `domain:${domain}` : ""} |`,
    );
  }

  const page = `---
title: "${opts.title}"
type: note
category: decision-ledger
updated: "${today}"
source_session: "${session}"
generated_by: compound/ledger-to-facts
status: active
---

# ${opts.title}

Distilled, evidence-backed positions, extracted by the compound Memory distiller from
\`${session}\`. Derived artifact — do not hand-edit the fence.

## Facts

<!--- gbrain:facts:begin -->
| # | claim | kind | confidence | visibility | notability | valid_from | valid_until | source | context |
|---|-------|------|------------|------------|------------|------------|-------------|--------|---------|
${rows.join("\n")}
<!--- gbrain:facts:end -->
`;

  const outPath = join(BRAIN, `${opts.slug}.md`);
  if (!existsSync(dirname(outPath))) mkdirSync(dirname(outPath), { recursive: true });
  writeFileSync(outPath, page);
  return { outPath, slug: opts.slug, count: rows.length };
}

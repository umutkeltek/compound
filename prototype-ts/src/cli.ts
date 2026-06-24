#!/usr/bin/env bun
/*
 * compound — personal OS. Memory layer entry point.
 *   distill <session> -> deep-distill a whole conversation into durable positions
 *   pack "<topic>"    -> assemble one evidence-backed context pack from your history
 *   ingest --slug ... -> ledger -> Facts page -> rented engine (recall)
 */
import { spawnSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { gbrainEngine } from "./engine/gbrain.ts";
import { buildPack } from "./memory/pack.ts";
import { ledgerToFactsPage } from "./memory/ledger-to-facts.ts";

const HERE = join(fileURLToPath(import.meta.url), "..");
const HOME = process.env.HOME || "";
const [cmd, ...rest] = process.argv.slice(2);
const argOf = (f: string) => {
  const h = rest.find((a) => a.startsWith(`${f}=`));
  return h ? h.slice(f.length + 1) : undefined;
};

function usage() {
  console.error(`compound — personal OS (Memory layer)

usage:
  compound distill <session.jsonl> [--ledger] [--slug=...]    deep-distill a conversation -> positions
  compound pack    "<topic>" [--limit=N]                      assemble an evidence-backed context pack
  compound ingest  --slug=<s> --title="<t>" [--ledger=path]   ledger -> Facts page -> engine (recall)`);
}

if (cmd === "distill") {
  // heavy LLM job — run the proven distiller as a subprocess (keeps its streaming progress)
  const r = spawnSync("bun", [join(HERE, "memory/distill.ts"), ...rest], { stdio: "inherit" });
  process.exit(r.status ?? 1);
} else if (cmd === "pack") {
  const topic = rest.find((a) => !a.startsWith("--"));
  if (!topic) { usage(); process.exit(2); }
  const limit = argOf("--limit");
  const out = await buildPack(gbrainEngine, topic, {
    limit: limit ? Number(limit) : undefined,
    ledger: argOf("--ledger"),
  });
  console.log(out);
} else if (cmd === "ingest") {
  const slug = argOf("--slug");
  if (!slug) { console.error("ingest needs --slug=<slug>"); process.exit(2); }
  const ledger = argOf("--ledger") || join(HOME, ".compound/distill/decision-ledger.generated.md");
  const { outPath, count } = ledgerToFactsPage({ ledger, slug, title: argOf("--title") || "Distilled positions" });
  console.error(`wrote ${count} facts → ${outPath}`);
  const res = await gbrainEngine.put(slug, readFileSync(outPath, "utf8"));
  console.error(res.ok ? `ingested into ${gbrainEngine.name}` : `ingest failed: ${res.note}`);
} else {
  usage();
  process.exit(cmd ? 2 : 0);
}

/*
 * local engine adapter — the swappable seam. local engine is RENTED here, not owned: it's an
 * adopted upstream project that disappoints on synthesis (its "dream" distills by
 * reading only the first + last message). Compound owns the deep Memory spine and
 * rents local engine only for storage/embedding/retrieval, behind this one module.
 *
 * To swap engines: implement the Engine interface elsewhere and pass it in.
 * Nothing in src/memory imports local engine directly — only this file shells out to it.
 */
import { execFileSync } from "node:child_process";
import type { Engine, Hit, Fact } from "./types.ts";

const ENGINE_BIN = process.env.COMPOUND_ENGINE_BIN || "";

export const cliEngine: Engine = {
  name: "local engine",

	async query(q, opts) {
		if (!ENGINE_BIN) return [];
		const limit = String(opts?.limit ?? 12);
    try {
      const out = execFileSync(ENGINE_BIN, ["query", q, "--limit", limit, "--detail", "low"], {
        encoding: "utf8",
        timeout: 90_000,
        maxBuffer: 8 * 1024 * 1024,
      });
      const hits: Hit[] = [];
      for (const line of out.split("\n")) {
        const m = line.match(/^\[([\d.]+)\]\s+(\S+)\s+--\s+(.*)$/);
        if (m) hits.push({ score: Number(m[1]), slug: m[2], snippet: m[3].slice(0, 200) });
      }
      return hits;
    } catch (e: any) {
      console.error(`[local engine.query] ${e?.message || e}`);
      return [];
    }
  },

	async put(slug, markdown) {
		if (!ENGINE_BIN) return { ok: false, note: "COMPOUND_ENGINE_BIN is not configured" };
		try {
      execFileSync(ENGINE_BIN, ["put", slug], { input: markdown, encoding: "utf8", timeout: 90_000 });
      return { ok: true };
    } catch (e: any) {
      return { ok: false, note: e?.message || String(e) };
    }
  },

	async recall(entity) {
		if (!ENGINE_BIN) return [];
		try {
      const out = execFileSync(ENGINE_BIN, ["recall", entity], { encoding: "utf8", timeout: 60_000 });
      const facts: Fact[] = [];
      for (const line of out.split("\n")) {
        const t = line.trim();
        if (t && !/^no matching facts/i.test(t)) facts.push({ claim: t });
      }
      return facts;
    } catch (e: any) {
      console.error(`[local engine.recall] ${e?.message || e}`);
      return [];
    }
  },
};

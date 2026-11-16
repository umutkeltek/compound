import assert from "node:assert/strict";
import { runEvidenceCardSelfTest } from "../src/lib/evidence-card.selftest.ts";
import { runEvidenceCardRenderSelfTest } from "../src/components/memory/EvidenceCard.selftest.tsx";
import { runMemoryPresentationSelfTest } from "../src/components/memory/MemoryPresentation.selftest.tsx";

const lib = runEvidenceCardSelfTest();
const render = runEvidenceCardRenderSelfTest();
const presentationChecks = runMemoryPresentationSelfTest();

const failed = [...lib.details, ...render.details].filter((d) => d.startsWith("FAIL"));
if (failed.length) {
  for (const line of failed) console.error(line);
  assert.fail(`${failed.length} evidence-card selftest check(s) failed`);
}

console.log(
  `evidence-card smoke ok: helper=${lib.details.length} checks; render=${render.details.length} checks; memory/decision presentation=${presentationChecks} checks`,
);

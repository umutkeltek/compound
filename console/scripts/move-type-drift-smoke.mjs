import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import path from "node:path";

const consoleRoot = process.cwd();
const repoRoot = path.resolve(consoleRoot, "..");

const envelopeSource = await readFile(path.join(repoRoot, "internal/envelope/envelope.go"), "utf8");
const registrySource = await readFile(path.join(consoleRoot, "src/lib/decision-registry.ts"), "utf8");

function parseGoMoveTypes(source) {
  const match = source.match(/MoveTypes\s*=\s*\[\]string\{([^}]+)\}/m);
  assert.ok(match, "internal/envelope MoveTypes enum not found");
  return [...match[1].matchAll(/"([^"]+)"/g)].map((m) => m[1]);
}

function parseConsoleMoveTypes(source) {
  const match = source.match(/export const MOVE_TYPES\s*=\s*\[([\s\S]*?)\];/m);
  assert.ok(match, "console MOVE_TYPES registry not found");
  return [...match[1].matchAll(/value:\s*"([^"]+)"/g)].map((m) => m[1]);
}

const spine = parseGoMoveTypes(envelopeSource);
const consoleTypes = parseConsoleMoveTypes(registrySource);

assert.deepEqual(
  consoleTypes,
  spine,
  `console MOVE_TYPES drifted from internal/envelope MoveTypes\nconsole=${consoleTypes.join(",")}\nspine=${spine.join(",")}`,
);

console.log(`move-type drift smoke ok: ${consoleTypes.join(", ")}`);

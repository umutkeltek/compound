import assert from "node:assert/strict";
import { readdir, readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";

const consoleRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const sourceRoot = path.join(consoleRoot, "src");

const ownerExceptions = new Set(["lib/compound.ts"]);
const forbiddenWriters = /\b(writeFile|appendFile|rm|unlink|rename|mkdir)\b/;

async function sourceFiles(dir) {
  const entries = await readdir(dir, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      files.push(...(await sourceFiles(full)));
    } else if (entry.isFile() && /\.(ts|tsx)$/.test(entry.name)) {
      files.push(full);
    }
  }
  return files;
}

const violations = [];

for (const file of await sourceFiles(sourceRoot)) {
  const rel = path.relative(sourceRoot, file);
  const source = await readFile(file, "utf8");
  if (ownerExceptions.has(rel)) continue;

  const reachesPrivateRuntime = source.includes("COMPOUND_HOME") || source.includes("docs_private");
  if (reachesPrivateRuntime && !source.includes("privateFallbacksEnabled")) {
    violations.push(`${rel}: private runtime read must be gated by privateFallbacksEnabled()`);
  }

  if (rel === "app/api/document/route.ts") {
    if (!source.includes("consolePrivateReadBlockedResponse")) {
      violations.push(`${rel}: private document reads must require local operator request guard`);
    }
    if (!source.includes('scope: "private_local"')) {
      violations.push(`${rel}: private document responses must label private_local scope`);
    }
  }

  if (forbiddenWriters.test(source)) {
    violations.push(`${rel}: console source must not write runtime/private files directly`);
  }
}

assert.deepEqual(violations, []);
console.log("private runtime guard smoke ok: console private readers are opt-in");

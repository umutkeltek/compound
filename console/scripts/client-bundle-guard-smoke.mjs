import { readdir, readFile } from "node:fs/promises";
import { existsSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

/**
 * Client-bundle guard for the console (regression 9c75d29). A `'use client'`
 * component that VALUE-imports a module whose static import graph transitively
 * reaches a `node:` builtin drags server-only code into the client bundle and
 * breaks the build. The original break: `DecideQueue` value-imported
 * `genericReviewResolutionActs` from `@/lib/decide-model`, whose graph reaches
 * `@/lib/compound` -> `node:child_process`; the fix moved the value into
 * `@/lib/decide-resolution-acts` and left only an `import type` of `decide-model`.
 *
 * No ESLint is configured in this console yet, so this deterministic static
 * scan is the enforcement (mirrors scripts/ai-boundary-smoke.mjs). It joins the
 * `compound proof-check` console gate list (cmd/compound/proof_check.go).
 *
 * THE LAW: starting from every `'use client'` entry under the scan root, no
 * VALUE-import chain may reach a `node:` builtin. `import type`, `export type`,
 * and dynamic `import('...')` are NOT value edges and are not followed.
 *
 * Usage: node --experimental-strip-types client-bundle-guard-smoke.mjs [scanRoot]
 * scanRoot defaults to console/src; the `@/` alias resolves to scanRoot.
 */

const consoleRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const scanRoot = path.resolve(process.argv[2] ?? path.join(consoleRoot, "src"));

// Static `import ... from "spec"` / `export ... from "spec"`; group 1 is a
// leading `type` keyword (whole-statement type-only), group 2 the specifier.
// `import { type X, y } from` is a value edge (the `type` sits inside braces,
// not right after import/export, so group 1 does not capture it).
const FROM_SPECIFIER_RE = /\b(?:import|export)\s+(type\s+)?[^;'"]*?\bfrom\s*["']([^"']+)["']/g;
// Side-effect `import "spec"` (bundler still loads the module).
const SIDE_EFFECT_RE = /\bimport\s+["']([^"']+)["']/g;

const EXTENSIONS = [".ts", ".tsx"];
const INDEX_FILES = ["index.ts", "index.tsx"];

// Strip block and line comments so import-like text in a comment (e.g. a doc
// comment that quotes `import { x } from "@/lib/compound"`) is not read as a
// real value edge. Line comments preceded by `:` are kept so `https://` in a
// string is untouched.
function stripComments(source) {
  return source.replace(/\/\*[\s\S]*?\*\//g, "").replace(/(^|[^:])\/\/[^\n]*/g, "$1");
}

async function sourceFiles(dir) {
  const entries = await readdir(dir, { withFileTypes: true });
  const files = [];
  for (const entry of entries) {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) files.push(...(await sourceFiles(full)));
    else if (entry.isFile() && /\.(ts|tsx)$/.test(entry.name)) files.push(full);
  }
  return files;
}

/** Value-import specifiers only (skips `import type`/`export type`/dynamic import). */
function valueSpecifiers(rawSource) {
  const source = stripComments(rawSource);
  const found = [];
  for (const match of source.matchAll(FROM_SPECIFIER_RE)) {
    if (match[1]) continue; // leading `type ` -> type-only statement, not a value edge
    found.push(match[2]);
  }
  for (const match of source.matchAll(SIDE_EFFECT_RE)) {
    found.push(match[1]);
  }
  return found;
}

/** Resolve a local specifier to a source file, or null for external/unresolved. */
function resolveLocal(fromFile, specifier) {
  let base;
  if (specifier.startsWith("@/")) base = path.join(scanRoot, specifier.slice(2));
  else if (specifier.startsWith(".")) base = path.resolve(path.dirname(fromFile), specifier);
  else return null; // bare package specifier (react, next/…, @radix-ui/…)
  if (/\.(ts|tsx)$/.test(base) && existsSync(base)) return base;
  for (const ext of EXTENSIONS) {
    if (existsSync(base + ext)) return base + ext;
  }
  for (const index of INDEX_FILES) {
    const candidate = path.join(base, index);
    if (existsSync(candidate)) return candidate;
  }
  return null;
}

const importCache = new Map();
async function specifiersOf(file) {
  if (!importCache.has(file)) {
    importCache.set(file, valueSpecifiers(await readFile(file, "utf8")));
  }
  return importCache.get(file);
}

/** DFS from one client entry; returns the first value chain reaching a `node:` builtin, or null. */
async function firstNodeReach(entry) {
  const visited = new Set();
  async function walk(file, trail) {
    if (visited.has(file)) return null;
    visited.add(file);
    for (const specifier of await specifiersOf(file)) {
      if (specifier.startsWith("node:")) {
        return [...trail, specifier];
      }
      const local = resolveLocal(file, specifier);
      if (!local) continue;
      const hit = await walk(local, [...trail, path.relative(scanRoot, local).split(path.sep).join("/")]);
      if (hit) return hit;
    }
    return null;
  }
  return walk(entry, [path.relative(scanRoot, entry).split(path.sep).join("/")]);
}

const CLIENT_DIRECTIVE_RE = /^\s*["']use client["'];/;

const violations = [];
for (const file of await sourceFiles(scanRoot)) {
  const source = await readFile(file, "utf8");
  if (!CLIENT_DIRECTIVE_RE.test(source)) continue;
  const chain = await firstNodeReach(file);
  if (chain) violations.push(chain.join(" -> "));
}

if (violations.length > 0) {
  console.error(`client-bundle guard FAILED: ${violations.length} client entr${violations.length === 1 ? "y" : "ies"} value-import a node: builtin (server-only code in the client bundle):`);
  for (const chain of violations) console.error(`  ${chain}`);
  console.error("fix: move the value export behind a type-only import, or into a client-safe module (see regression 9c75d29).");
  process.exit(1);
}

console.log(`client-bundle guard ok: no 'use client' entry under ${path.relative(consoleRoot, scanRoot) || "."} value-imports a node: builtin`);

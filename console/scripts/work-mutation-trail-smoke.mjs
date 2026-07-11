import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdtempSync, readdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

const BIN = process.env.COMPOUND_BIN;
assert.ok(BIN, "set COMPOUND_BIN to a freshly built compound binary");
const home = mkdtempSync(join(tmpdir(), "compound-console-work-trail-"));
process.env.COMPOUND_HOME = home;
process.env.COMPOUND_CONSOLE_MUTATIONS = "1";
function compound(args) { return execFileSync(BIN, args, { env: { ...process.env, COMPOUND_HOME: home }, encoding: "utf8" }); }
function create(id) { compound(["work", "create", "--work-id", id, "--owner", "console-fixture", "--predicate", `console mutation ${id} reaches server state`, "--by", "console-smoke", "--state", "proposed"]); }
function listState(id) {
  const output = compound(["work", "show", id, "--json"]);
  const data = JSON.parse(output.slice(output.indexOf("{"), output.lastIndexOf("}") + 1));
  return data.state ?? "missing";
}

compound(["home", "init", "--local"]);
const cases = [
  { id: "work_consoleadmit01", option: "admitted", label: "Add to work" },
  { id: "work_consolecancel01", option: "cancelled", label: "Cancel", fields: { summary: "operator cancelled fixture" } },
  { id: "work_consolehold01", option: "held", label: "Needs me" },
];
for (const item of cases) create(item.id);
const beforeFiles = readdirSync(home, { recursive: true }).length;
const { POST } = await import("../src/app/api/decision/execute/route.ts");
for (const item of cases) {
  const response = await POST(new Request("http://localhost/api/decision/execute", {
    method: "POST",
    headers: { "content-type": "application/json", origin: "http://localhost", host: "localhost" },
    body: JSON.stringify({ area: "work-advance", itemId: item.id, option: item.option, fields: item.fields ?? {}, confirmed: true }),
  }));
  const payload = await response.json();
  assert.equal(response.status, 200, `${item.label}: ${JSON.stringify(payload)}`);
  assert.equal(payload.ok, true, `${item.label} must execute through the governed route`);
  assert.equal(listState(item.id), item.option, `${item.label} must persist ${item.option} server-side`);
}
const afterFiles = readdirSync(home, { recursive: true }).length;
assert.ok(afterFiles > beforeFiles, `mutation trail must grow (${beforeFiles} -> ${afterFiles})`);
console.log(`work mutation-trail smoke ok: Add to work, Cancel, and Needs me persisted server-side (${beforeFiles} -> ${afterFiles} files)`);

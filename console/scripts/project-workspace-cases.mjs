import assert from "node:assert/strict";
import { buildWorkspace, workScopeRefs, workGraph, dependencyLinks, projectHref, UNLINKED_SCOPE } from "../src/lib/project-workspace.ts";

const row = (id, state, scope = {}, extra = {}) => ({ id, state, scope, owner: "fixture-agent", desired_outcome: "A useful fixture outcome", success_predicate: "The expected change is observed", ...extra });
const work = [
  row("work_a", "held", { project_id: "research" }, { canonical_surface: "project:research", ids: { context_pack_id: "ctx_real" }, run_attempt_ids: ["ratt_known"] }),
  row("work_b", "completed", {}, { canonical_surface: "project:research", verification_ids: ["check:attached"] }),
  row("work_c", "ready", {}, { desired_outcome: "research appears in this title, but is not scope" }),
  row("work_d", "verifying", { project_id: "other" }),
];
const inventory = { resources: [{ id: "product:fixture-service", kind: "product", label: "Fixture service" }] };
let model = buildWorkspace({ work }, inventory, { open: [{ work_id: "work_a", next_action: "Review the recorded hold", state: "running" }] }, "project:research", "work_a");
assert.equal(model.project.total, 2, "duplicate explicit refs must not double-count work");
assert.equal(model.project.attention, 1);
assert.equal(model.project.open, 1);
assert.equal(model.selected.state, "held", "stale summary state must not override the work record");
assert.equal(model.selected.next, "Review the recorded hold");
assert.equal(model.selected.context, "ctx_real", "actual context identity must survive");
assert.deepEqual(model.work.map((w) => w.id), ["work_a", "work_b"]);
assert.equal(model.unlinked, 1, "task prose must never assign a scope");
assert.equal(model.projects.find((p) => p.id === "product:fixture-service").total, 0, "registered empty projects survive");
assert.deepEqual(workScopeRefs({ scope: { project_id: "research", product_id: "fixture-service" } }), ["project:research", "product:fixture-service"]);
assert.deepEqual(workScopeRefs({ canonical_surface: "packet:fixture" }), []);
assert.equal(buildWorkspace({ work }, {}, {}, "project:research", "work_d").selected, null, "cross-project IDs must not select another task");
assert.equal(buildWorkspace({ work }, {}, {}, "project:research", "work_missing").requestedMissing, true);
assert.equal(buildWorkspace({ work }, {}, {}, UNLINKED_SCOPE).work[0].id, "work_c");
assert.equal(buildWorkspace(null, inventory, {}).workKnown, false, "failed reads must not mean an empty home");
assert.equal(buildWorkspace({ work: [] }, inventory, {}).workKnown, true);
assert.equal(buildWorkspace({ work: "corrupt" }, {}, {}).workKnown, false);
assert.equal(projectHref("project:a/b", "work_a").includes("scope=project%3Aa%2Fb"), true, "scope links encode exact identity");

const selected = model.selected;
const graph = workGraph({ ...selected, attempts: Array.from({ length: 800 }, (_, i) => "ratt_" + i) });
assert.ok(graph.nodes.length <= 12, "focused work must remain readable at realistic history scale");
assert.ok(graph.edges.every((e) => graph.nodes.some((n) => n.id === e.source) && graph.nodes.some((n) => n.id === e.target)));
assert.ok(graph.nodes.some((n) => n.id === "context:ctx_real"));
assert.ok(graph.nodes.every((n) => !n.status), "recorded work state is not a heartbeat");
assert.match(graph.note, /more references/);
assert.equal(workGraph({ ...selected, outcome: "", owner: "", context: "", policy: "", attempts: [], evidence: [], checks: [] }).nodes.length, 1, "absent relationships are not invented");
const deps = dependencyLinks({ edge_list: [
  { from: "project:research", to: "host:fixture", rel_title: "Runs on", confidence: "inferred", carried: true, evidence: "fixture observation" },
  { from: "project:other", to: "host:other", confidence: "observed" },
] }, "project:research");
assert.equal(deps.links.length, 1, "dependency views must preserve scope");
assert.equal(deps.links[0].confidence, "inferred", "inference must never be presented as observed");
assert.equal(deps.links[0].carried, true, "carried evidence must remain explicit");
console.log("project-workspace cases: identity, scope, honest absence, bounded graph and evidence passed");

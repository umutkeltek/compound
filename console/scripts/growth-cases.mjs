// Growth read-model cases: map a synthetic compound.product_growth.v0.2
// payload and check the words the pages build from it. The wired
// console-ads-smoke (scripts/ads-smoke.mjs) imports this module, so a failing
// case fails that gate. Alone: bun scripts/growth-cases.mjs
import assert from "node:assert/strict";

import { countStatuses, mapGrowthModel, unknownGates, STATUS_META } from "../src/lib/growth.ts";
import { profileSentence } from "../src/components/growth/AppGrowth.tsx";

const dim = (values, source) => ({ values, source });
const row = (area_id, status, extra = {}) => ({
  area_id,
  status,
  basis: "none",
  title: area_id,
  question: "?",
  forms: [],
  summary: "",
  reason: "",
  evidence: [],
  next_move: "",
  blocked_by: [],
  branch: "research",
  skills: [],
  ...extra,
});

const payload = {
  schema_version: "compound.product_growth.v0.2",
  generated_at: "2026-01-02T00:00:00Z",
  as_of: "2026-01-02",
  catalog: { schema_version: "compound.product_growth_catalog.v0.1", source: "embedded", overlay: "", overlay_error: "", groups: 2, areas: 3 },
  profile_dimensions: { kinds: ["ios_app", "website"], objectives: ["revenue", "adoption"] },
  groups: [
    { id: "strategy", title: "Strategy", question: "Why?", branch: "strategy" },
    { id: "monetization", title: "Monetization", question: "Money?", branch: "experiment" },
  ],
  areas: [
    { id: "strategy_value_proposition", number: 1, group: "strategy", title: "Promise", question: "?", applies: {}, needs: [], branch: "strategy", skills: [], kinds: {} },
    { id: "monetization_model", number: 2, group: "monetization", title: "Model", question: "?", applies: { objectives: ["revenue"] }, needs: ["strategy_value_proposition"], branch: "experiment", skills: ["pricing"], kinds: {} },
    { id: "monetization_pricing", number: 3, group: "monetization", title: "Pricing", question: "?", applies: { objectives: ["revenue"] }, needs: [], branch: "experiment", skills: [], kinds: {} },
  ],
  portfolio: { app_count: 1, apps_without_data: [], totals: {}, series: {}, allocation: [] },
  apps: [
    {
      product_id: "product:sample-app",
      name: "Sample App",
      aliases: [],
      kind: "unknown",
      essence: null,
      freshness: { latest_date: "", stale: false, sources: [] },
      kpis: {},
      series: {},
      funnel: [],
      profile: {
        kinds: dim([], "unknown"),
        audiences: dim([], "unknown"),
        revenue_models: dim([], "unknown"),
        objectives: dim([], "unknown"),
        stage: dim([], "unknown"),
        deferred: [],
        unknown: ["kinds", "audiences", "revenue_models", "objectives", "stage"],
      },
      audit: [
        row("strategy_value_proposition", "blind"),
        row("monetization_model", "unknown", { reason: "Not on file" }),
        row("monetization_pricing", "unknown", { reason: "Not on file" }),
      ],
      coverage: { measured: 0, partial: 0, stale: 0, blind: 1, not_applicable: 0, unknown: 2, deferred: 0, score: 0 },
      hypotheses: { open: 0, reviewed: 0 },
      next_moves: [
        { area_id: "", area_ids: [], kind: "set_profile", title: "Write the product profile", why: "2 areas cannot be judged.", unblocks: 0, blocked_by: [], branch: "strategy", skills: [], brief: "compound product import <product.yaml>" },
        {
          area_id: "monetization_model",
          area_ids: ["monetization_model", "monetization_pricing"],
          kind: "refresh_data",
          title: "Refresh App Store Connect sales",
          why: "The data stopped between 2025-12-01 and 2025-12-10. 2 areas use it: Model, Pricing.",
          unblocks: 0,
          blocked_by: [],
          branch: "experiment",
          skills: [],
          brief: "compound product growth --product product:sample-app --area monetization_model",
        },
        { area_id: "strategy_value_proposition", kind: "assess", title: "Assess: Promise", why: "?", unblocks: 0, blocked_by: [], branch: "strategy", skills: [], brief: "b" },
      ],
    },
  ],
};

const model = mapGrowthModel(payload);
assert.ok(model, "v0.2 payload maps");
assert.equal(model.catalog.areas, 3);
assert.equal(model.groups.length, 2);
assert.deepEqual(model.areas[1].applies, { objectives: ["revenue"] });

const app = model.apps[0];
assert.equal(app.nextMoves[0].kind, "set_profile");
assert.equal(app.nextMoves[0].areaId, "");
assert.deepEqual(app.nextMoves[0].areaIds, []);
// A folded refresh names every area it covers; area_id stays the first.
assert.deepEqual(app.nextMoves[1].areaIds, ["monetization_model", "monetization_pricing"]);
assert.equal(app.nextMoves[1].areaId, "monetization_model");
// A CLI without area_ids still yields the one area.
assert.deepEqual(app.nextMoves[2].areaIds, ["strategy_value_proposition"]);
assert.deepEqual(app.profile.unknown, ["kinds", "audiences", "revenue_models", "objectives", "stage"]);

// Only dimensions that really gate an area are named, and the count is the rows that are not on file.
assert.deepEqual(unknownGates(app, model.areas), { rows: 2, dims: ["objectives"] });
assert.equal(profileSentence(app, model.areas), "2 areas cannot be judged until the objective is on file.");

// A status the console does not know is shown as no data, never as better than the CLI said.
const odd = mapGrowthModel({ ...payload, apps: [{ ...payload.apps[0], audit: [row("strategy_value_proposition", "brand_new_status")] }] });
assert.equal(odd.apps[0].audit[0].status, "blind");
assert.equal(countStatuses(odd.apps[0].audit).blind, 1);

// Plain words, not internal ones.
assert.equal(STATUS_META.unknown.label, "Not on file");
assert.equal(STATUS_META.deferred.label, "Set aside");
assert.equal(STATUS_META.blind.label, "No data yet");

// v0.1 had `stages` and no profile: refuse it rather than draw an empty catalog.
assert.equal(mapGrowthModel({ ...payload, schema_version: "compound.product_growth.v0.1" }), null);
assert.equal(mapGrowthModel({ schema_version: "something.else" }), null);

console.log("growth cases ok: v0.2 maps, folded moves keep every area, profile sentence names only gating dimensions, unknown statuses stay gaps, v0.1 refused");

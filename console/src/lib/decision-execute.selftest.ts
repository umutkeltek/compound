/*
 * Falsifiers for the execute-route ↔ decide-model agreement on WHO may act.
 * The lived bug (2026-07-10): decide-model honestly offered "Apply 1 of 1"
 * (batch-enumerable, projection-named actionId) for route-promotions items
 * whose bespoke spec yields NO executable option (demote / hold_failing),
 * but executeDecision refused the generic pathway whenever a spec exists —
 * validating the absent option against an empty list and returning
 * `decision "" is not valid for this item's current state`. Every enumerable
 * Apply on a registry area was dead on arrival.
 *
 * These checks pin the agreement contract at the pure layer (no spawned CLI):
 * genericFallbackAllowed() must accept exactly the shape decide-model emits —
 * spec'd area, zero options for the item, projection actionId present — and
 * nothing else. Runs under bun via scripts/decision-execute-smoke.mjs
 * (decision-registry resolves `@/lib` path aliases).
 */

import { genericFallbackAllowed } from "./decision-execute";
import { specFor } from "./decision-registry";
import type { Json } from "./compound";

type Check = { name: string; ok: boolean; detail: string };

function routePromotionItem(recommendation: string): Json {
  return {
    id: "route_promotion_selftest01",
    area: "route-promotions",
    group_key: `route-promotions|${recommendation}|classify`,
    json_path: "/tmp/route_promotion_selftest01.json",
  };
}

export function runDecisionExecuteSelfTest(): { ok: boolean; details: string[] } {
  const checks: Check[] = [];
  const push = (name: string, ok: boolean, detail: string) => checks.push({ name, ok, detail });

  const spec = specFor("route-promotions");
  if (!spec || !spec.optionsFor) {
    return { ok: false, details: ["FAIL route-promotions spec or optionsFor missing"] };
  }

  // 1. The lived failing shape: demote recommendation → spec yields zero
  //    options → a projection actionId MUST fall through to the governed
  //    generic pathway instead of the empty-option 400.
  {
    const options = spec.optionsFor(routePromotionItem("demote"));
    push(
      "demote-zero-options-falls-through",
      options.length === 0 && genericFallbackAllowed(options, "route_promotion_record"),
      `options=${options.length} fallback=${genericFallbackAllowed(options, "route_promotion_record")}`,
    );
  }

  // 2. hold_failing — the second lived card, same contract.
  {
    const options = spec.optionsFor(routePromotionItem("hold_failing"));
    push(
      "hold-failing-zero-options-falls-through",
      options.length === 0 && genericFallbackAllowed(options, "route_promotion_record"),
      `options=${options.length}`,
    );
  }

  // 3. hold_insufficient_evidence: the bespoke spec CAN act (record_hold) —
  //    the bespoke path must keep winning; fallback must refuse even with an
  //    actionId present ("a bespoke spec always wins when it can act").
  {
    const options = spec.optionsFor(routePromotionItem("hold_insufficient_evidence"));
    push(
      "bespoke-wins-when-it-can-act",
      options.length === 1 && !genericFallbackAllowed(options, "route_promotion_record"),
      `options=${options.length} fallback=${genericFallbackAllowed(options, "route_promotion_record")}`,
    );
  }

  // 4. Zero options but NO actionId named → no fallback; the honest 400
  //    remains (nothing to execute generically).
  {
    const options = spec.optionsFor(routePromotionItem("demote"));
    push(
      "no-actionid-no-fallback",
      !genericFallbackAllowed(options, "") && !genericFallbackAllowed(options, "   "),
      "empty and whitespace actionIds both refused",
    );
  }

  const failed = checks.filter((c) => !c.ok);
  return {
    ok: failed.length === 0,
    details: checks.map((c) => `${c.ok ? "ok" : "FAIL"} ${c.name} — ${c.detail}`),
  };
}

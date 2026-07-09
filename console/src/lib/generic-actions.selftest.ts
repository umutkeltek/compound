import {
  GENERIC_ACTION_ALLOWLIST,
  resolveGenericActionFromProjection,
} from "./generic-actions";

/**
 * The depth test for Phase 3a (Plans/i-am-not-happy-rosy-floyd.md Part 4):
 * proves the three-gate generic-action executor actually gates, not just
 * typechecks. `scripts/generic-actions-smoke.mjs` runs this under `bun`
 * (generic-actions.ts imports `@/lib/decision-registry` via the `@/` path
 * alias, which plain `node --experimental-strip-types` cannot resolve; bun
 * resolves it natively from tsconfig paths).
 *
 * Every case here runs `resolveGenericActionFromProjection` against a
 * crafted, in-memory projection fixture — no on-disk compound runtime home,
 * no subprocess, no network. That is the point: an attacker (or a bug) gets
 * exactly one shot at each gate, and none of them should ever require a real
 * backend to prove.
 */
export type SelfTestResult = { ok: boolean; details: string[] };

export function runGenericActionsSelfTest(): SelfTestResult {
  const details: string[] = [];
  let ok = true;

  const fail = (msg: string) => {
    ok = false;
    details.push(`FAIL: ${msg}`);
  };
  const pass = (msg: string) => details.push(`ok: ${msg}`);

  // (a) Gate 1 — a shell-form action (argv[0] !== "compound") must be
  // rejected regardless of area, exactly like panel per-item "/bin/sh -lc
  // ..." forms stay copy-only forever.
  {
    const projection = {
      items: [
        {
          id: "shell_item",
          actions: [{ id: "danger", argv: ["/bin/sh", "-lc", "echo pwned"] }],
        },
      ],
    };
    const res = resolveGenericActionFromProjection(projection, "job-health", "shell_item", "danger");
    if (res.ok) fail("shell-form argv resolved ok:true instead of being rejected");
    else if (res.rejection.gate !== "gate-1-shell-form")
      fail(`shell-form argv rejected at "${res.rejection.gate}" instead of gate-1-shell-form`);
    else pass('shell-form argv (argv[0]="/bin/sh") rejected at gate-1-shell-form');
  }

  // (b) Gate 2 — an area with NO entry in GENERIC_ACTION_ALLOWLIST must be
  // rejected even with an otherwise clean compound-prefixed argv.
  {
    const uncertifiedArea = "totally-uncertified-area";
    if (GENERIC_ACTION_ALLOWLIST[uncertifiedArea]) {
      fail(`fixture area "${uncertifiedArea}" is unexpectedly allowlisted -- update the test`);
    } else {
      const projection = {
        items: [
          { id: "it1", actions: [{ id: "act1", argv: ["compound", "work", "close", "work_x"] }] },
        ],
      };
      const res = resolveGenericActionFromProjection(projection, uncertifiedArea, "it1", "act1");
      if (res.ok) fail("un-allowlisted area resolved ok:true instead of being rejected");
      else if (res.rejection.gate !== "gate-2-allowlist")
        fail(`un-allowlisted area rejected at "${res.rejection.gate}" instead of gate-2-allowlist`);
      else pass("un-allowlisted area rejected at gate-2-allowlist");
    }
  }

  // (c) Gate 2 — an allowlisted area whose resolved verb does not match any
  // of that area's certified prefixes must ALSO fail at gate 2 -- proves the
  // check is area+verb together, not just area presence.
  {
    const area = "job-health";
    if (!GENERIC_ACTION_ALLOWLIST[area]) {
      fail(`fixture assumes "${area}" is allowlisted but GENERIC_ACTION_ALLOWLIST disagrees -- update the test`);
    } else {
      const projection = {
        items: [
          {
            id: "it2",
            // Real compound verb ("authority dirty-review"), but not one of
            // job-health's certified prefixes (["authority","job-health-review"]).
            actions: [{ id: "act2", argv: ["compound", "authority", "dirty-review", "x.yaml"] }],
          },
        ],
      };
      const res = resolveGenericActionFromProjection(projection, area, "it2", "act2");
      if (res.ok) fail("mismatched verb for an allowlisted area resolved ok:true instead of being rejected");
      else if (res.rejection.gate !== "gate-2-allowlist")
        fail(`mismatched verb rejected at "${res.rejection.gate}" instead of gate-2-allowlist`);
      else pass('allowlisted area ("job-health") with a mismatched verb rejected at gate-2-allowlist');
    }
  }

  // (d) A well-formed, allowlisted action resolves cleanly with the argv read
  // straight from the (mocked) projection -- proves the happy path actually
  // works end to end, not just that gates reject.
  {
    const area = "job-health";
    const projection = {
      items: [
        {
          id: "jhp_test123456",
          actions: [
            {
              id: "record_job_health_review",
              label: "Record job-health review",
              argv: ["compound", "authority", "job-health-review", "/tmp/fixture.review.yaml"],
              requires_review: true,
              policy_note: "records a reviewed job-health decision; no roster mutation",
            },
          ],
        },
      ],
    };
    const res = resolveGenericActionFromProjection(
      projection,
      area,
      "jhp_test123456",
      "record_job_health_review",
    );
    if (!res.ok) {
      fail(`well-formed allowlisted action was rejected: ${res.rejection.gate} - ${res.rejection.message}`);
    } else if (res.action.argv.join(" ") !== "authority job-health-review /tmp/fixture.review.yaml") {
      fail(`resolved argv unexpected: ${JSON.stringify(res.action.argv)}`);
    } else if (!res.action.requiresReview) {
      fail("resolved action lost requires_review:true from the projection");
    } else {
      pass("well-formed allowlisted action resolves with the freshly-read argv and requiresReview intact");
    }
  }

  // (e) An unknown item id or action id must come back not-found, never
  // throw and never silently pick a different action.
  {
    const res1 = resolveGenericActionFromProjection({ items: [] }, "job-health", "missing", "missing");
    if (res1.ok || res1.rejection.gate !== "not-found") {
      fail(`unknown item id should be not-found, got ${JSON.stringify(res1)}`);
    } else {
      pass("unknown item id rejected as not-found");
    }

    const projection = {
      items: [{ id: "jhp_known", actions: [{ id: "record_job_health_review", argv: ["compound", "authority", "job-health-review", "x.yaml"] }] }],
    };
    const res2 = resolveGenericActionFromProjection(projection, "job-health", "jhp_known", "not_a_real_action");
    if (res2.ok || res2.rejection.gate !== "not-found") {
      fail(`unknown action id on a known item should be not-found, got ${JSON.stringify(res2)}`);
    } else {
      pass("unknown action id on a known item rejected as not-found (no fallback to another action)");
    }
  }

  // (f) A group-scoped safe_batch_action resolves the same way an item's
  // actions[] does, keyed by group.key instead of item.id -- proves the
  // "itemId is a group key" path the spec describes actually works, even
  // though none of the currently-allowlisted areas emit groups today.
  {
    const area = "job-health";
    const projection = {
      groups: [
        {
          key: "job-health|batch-key",
          safe_batch_action: {
            id: "record_job_health_review_batch",
            argv: ["compound", "authority", "job-health-review", "/tmp/batch.review.yaml"],
            requires_review: true,
          },
        },
      ],
    };
    const res = resolveGenericActionFromProjection(
      projection,
      area,
      "job-health|batch-key",
      "record_job_health_review_batch",
    );
    if (!res.ok) {
      fail(`group-scoped safe_batch_action should resolve, got ${JSON.stringify(res)}`);
    } else {
      pass("group-scoped safe_batch_action resolves when itemId is a group key");
    }
  }

  // Rationale-required areas (route-promotions / panel-verdicts): the CLI
  // requires --rationale, the projection never carries it, and the operator's
  // text is appended as its own argv element post-gates.
  {
    const projection = {
      items: [
        {
          id: "route_promotion_clean",
          actions: [
            {
              id: "route_promotion_record",
              argv: ["compound", "route", "promotion-record", "--review", "/tmp/rp.json"],
              requires_review: true,
            },
          ],
        },
      ],
    };
    const none = resolveGenericActionFromProjection(
      projection,
      "route-promotions",
      "route_promotion_clean",
      "route_promotion_record",
    );
    if (none.ok || none.rejection.gate !== "rationale-required") {
      fail(`missing rationale should reject at rationale-required, got ${JSON.stringify(none)}`);
    } else {
      pass("rationale-required area without rationale rejected at rationale-required");
    }
    const quirkyRationale = "held: day-2 bakeoff decides; '; $(echo injected) is just text here";
    const withText = resolveGenericActionFromProjection(
      projection,
      "route-promotions",
      "route_promotion_clean",
      "route_promotion_record",
      quirkyRationale,
    );
    if (!withText.ok) {
      fail(`rationale-supplied resolution should pass, got ${JSON.stringify(withText)}`);
    } else {
      const argv = withText.action.argv;
      const idx = argv.indexOf("--rationale");
      if (idx === -1 || argv[idx + 1] !== quirkyRationale) {
        fail(`rationale must be appended verbatim as one argv element, got ${JSON.stringify(argv)}`);
      } else {
        pass("operator rationale appended verbatim as a single argv element (no shell)");
      }
    }
    const placeholder = resolveGenericActionFromProjection(
      {
        items: [
          {
            id: "route_promotion_placeholder",
            actions: [
              {
                id: "route_promotion_record",
                argv: [
                  "compound",
                  "route",
                  "promotion-record",
                  "--review",
                  "/tmp/rp.json",
                  "--rationale",
                  "$RATIONALE",
                ],
                requires_review: true,
              },
            ],
          },
        ],
      },
      "route-promotions",
      "route_promotion_placeholder",
      "route_promotion_record",
      "real rationale",
    );
    if (placeholder.ok) {
      fail("projected argv already carrying --rationale must be refused (placeholder risk)");
    } else {
      pass("projected argv with a pre-baked --rationale refused (placeholder risk)");
    }
  }

  return { ok, details };
}

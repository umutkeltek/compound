import {
  adaptAttentionProjectionForArea,
  GENERIC_ACTION_ALLOWLIST,
  genericActionEffectKind,
  genericActionProjectionEligible,
  resolveGenericActionFromProjection,
  resolveGenericActionFromRead,
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

  if (
    genericActionEffectKind("job-health") === "record" &&
    genericActionEffectKind("panel-verdicts") === "record" &&
    genericActionEffectKind("observation-candidates") === "track" &&
    genericActionEffectKind("unknown-area") === null
  ) {
    pass("generic action effect labels distinguish certified record/track semantics and reject unknown areas");
  } else {
    fail("generic action effect labels drifted from certified area semantics");
  }

  // F4/F5: the lease-hygiene attention-sourced pathway. `compound attention
  // --json` groups[] carry a plain `action` field (not `safe_batch_action`
  // keyed items[]) -- adaptAttentionProjectionForArea() reshapes that into
  // the {groups:[{key, safe_batch_action}]} projection
  // resolveGenericActionFromProjection already understands, so the same
  // three gates apply without a bespoke reviews area existing for lease
  // hygiene.
  {
    const leaseHygieneAction = {
      id: "lease-hygiene.sweep",
      label: "Sweep leftover claim tags",
      argv: ["compound", "home", "archive-history", "--kind", "lease"],
      kind: "batch",
      requires_confirmation: true,
    };
    const attentionFixture = {
      groups: [
        { key: "lease-hygiene", area: "lease-hygiene", action: leaseHygieneAction },
        {
          key: "executor:codex",
          area: "job-health",
          action: null,
        },
      ],
    };

    // (g) The happy path: a well-formed attention-sourced action resolves ok
    // with the exact projected argv and a stable, non-empty revision.
    {
      const projection = adaptAttentionProjectionForArea(attentionFixture, "lease-hygiene");
      const res = resolveGenericActionFromProjection(
        projection,
        "lease-hygiene",
        "lease-hygiene",
        "lease-hygiene.sweep",
      );
      const res2 = resolveGenericActionFromProjection(
        projection,
        "lease-hygiene",
        "lease-hygiene",
        "lease-hygiene.sweep",
      );
      if (!res.ok) {
        fail(`lease-hygiene sweep action was rejected: ${res.rejection.gate} - ${res.rejection.message}`);
      } else if (res.action.argv.join(" ") !== "home archive-history --kind lease") {
        fail(`lease-hygiene resolved argv unexpected: ${JSON.stringify(res.action.argv)}`);
      } else if (!res.action.requiresReview) {
        fail("lease-hygiene resolved action lost requires_review from the attention action's requires_confirmation");
      } else if (!res.action.revision || !res2.ok || res.action.revision !== res2.action.revision) {
        fail(`lease-hygiene revision must be stable and non-empty across identical resolutions, got ${JSON.stringify([res, res2])}`);
      } else {
        pass("lease-hygiene attention-sourced action resolves ok with the exact argv and a stable revision");
      }
    }

    // (h) A forged action argv (a destructive verb the operator never saw
    // certified) must be rejected at gate-2 or gate-3, even keyed under the
    // real lease-hygiene area/key/actionId.
    {
      const forgedFixture = {
        groups: [
          {
            key: "lease-hygiene",
            area: "lease-hygiene",
            action: { ...leaseHygieneAction, argv: ["compound", "home", "restore-history"] },
          },
        ],
      };
      const projection = adaptAttentionProjectionForArea(forgedFixture, "lease-hygiene");
      const res = resolveGenericActionFromProjection(
        projection,
        "lease-hygiene",
        "lease-hygiene",
        "lease-hygiene.sweep",
      );
      if (res.ok) {
        fail("forged lease-hygiene argv (home restore-history) resolved ok:true instead of being rejected");
      } else if (res.rejection.gate !== "gate-2-allowlist" && res.rejection.gate !== "gate-3-global-allowlist") {
        fail(`forged lease-hygiene argv rejected at "${res.rejection.gate}" instead of gate-2/gate-3`);
      } else {
        pass(`forged lease-hygiene argv (home restore-history) rejected at ${res.rejection.gate}`);
      }

      // (i) The SAME forged argv under an uncertified area must also reject
      // at gate-2 -- proves certification is per-area, not a global bypass.
      const uncertifiedArea = "totally-uncertified-attention-area";
      if (GENERIC_ACTION_ALLOWLIST[uncertifiedArea]) {
        fail(`fixture area "${uncertifiedArea}" is unexpectedly allowlisted -- update the test`);
      } else {
        const uncertifiedProjection = adaptAttentionProjectionForArea(
          { groups: [{ key: "lease-hygiene", area: uncertifiedArea, action: { ...leaseHygieneAction, argv: ["compound", "home", "restore-history"] } }] },
          uncertifiedArea,
        );
        const res2 = resolveGenericActionFromProjection(
          uncertifiedProjection,
          uncertifiedArea,
          "lease-hygiene",
          "lease-hygiene.sweep",
        );
        if (res2.ok || res2.rejection.gate !== "gate-2-allowlist") {
          fail(`forged argv under an uncertified area should reject at gate-2-allowlist, got ${JSON.stringify(res2)}`);
        } else {
          pass("forged argv under an uncertified area rejected at gate-2-allowlist");
        }
      }
    }
  }

  // A3 (packet 2026-07-15-slice2A-skill-surfacing-verb): the skill-surfacing
  // area reads from `compound reviews --json --area skill-surfacing` (a
  // plain items[].actions[] projection, unlike lease-hygiene's
  // attention-adapted groups[]) -- proves the certified area resolves a
  // well-formed import action with a stable revision, and rejects a forged
  // argv for a real compound verb that is not skill-surfacing's certified
  // prefix.
  {
    const openItemPath = "/home/.compound/reviews/skill-surfacing/2026-07-09-session-mining-backfill-feedback.json";
    const projection = {
      items: [
        {
          id: "ssfb_20260709_session_backfill",
          area: "skill-surfacing",
          actions: [
            {
              id: "skill-surfacing.import",
              label: "Fold these misses into the capability graph",
              argv: ["compound", "capability", "import-surfacing-misses", openItemPath],
              requires_review: true,
            },
          ],
        },
      ],
    };

    // (j) The happy path: a well-formed skill-surfacing import action
    // resolves ok with the exact projected argv and a stable, non-empty
    // revision.
    {
      const res = resolveGenericActionFromProjection(
        projection,
        "skill-surfacing",
        "ssfb_20260709_session_backfill",
        "skill-surfacing.import",
      );
      const res2 = resolveGenericActionFromProjection(
        projection,
        "skill-surfacing",
        "ssfb_20260709_session_backfill",
        "skill-surfacing.import",
      );
      if (!res.ok) {
        fail(`skill-surfacing import action was rejected: ${res.rejection.gate} - ${res.rejection.message}`);
      } else if (res.action.argv.join(" ") !== `capability import-surfacing-misses ${openItemPath}`) {
        fail(`skill-surfacing resolved argv unexpected: ${JSON.stringify(res.action.argv)}`);
      } else if (!res.action.requiresReview) {
        fail("skill-surfacing resolved action lost requires_review from the projection");
      } else if (!res.action.revision || !res2.ok || res.action.revision !== res2.action.revision) {
        fail(`skill-surfacing revision must be stable and non-empty across identical resolutions, got ${JSON.stringify([res, res2])}`);
      } else {
        pass("skill-surfacing import action resolves ok with the exact argv and a stable revision");
      }
    }

    // (k) A forged action argv -- a real compound verb ("capability
    // skill-plan") that is NOT skill-surfacing's certified prefix -- must be
    // rejected at gate-2, even keyed under the real item/action id.
    {
      const forgedProjection = {
        items: [
          {
            id: "ssfb_20260709_session_backfill",
            area: "skill-surfacing",
            actions: [
              {
                id: "skill-surfacing.import",
                argv: ["compound", "capability", "skill-plan"],
                requires_review: true,
              },
            ],
          },
        ],
      };
      const res = resolveGenericActionFromProjection(
        forgedProjection,
        "skill-surfacing",
        "ssfb_20260709_session_backfill",
        "skill-surfacing.import",
      );
      if (res.ok) {
        fail("forged skill-surfacing argv (capability skill-plan) resolved ok:true instead of being rejected");
      } else if (res.rejection.gate !== "gate-2-allowlist") {
        fail(`forged skill-surfacing argv rejected at "${res.rejection.gate}" instead of gate-2-allowlist`);
      } else {
        pass("forged skill-surfacing argv (capability skill-plan) rejected at gate-2-allowlist");
      }
    }
  }

  // F5: the client-side honesty probe agrees with the certified allowlist
  // for the lease-hygiene area.
  {
    const eligible = genericActionProjectionEligible("lease-hygiene", [
      "compound",
      "home",
      "archive-history",
      "--kind",
      "lease",
    ]);
    const wrongShellForm = genericActionProjectionEligible("lease-hygiene", [
      "home",
      "archive-history",
      "--kind",
      "lease",
    ]);
    const wrongVerb = genericActionProjectionEligible("lease-hygiene", ["compound", "home", "restore-history"]);
    if (eligible && !wrongShellForm && !wrongVerb) {
      pass("genericActionProjectionEligible agrees with the lease-hygiene allowlist: certified argv true, argv[0]!=='compound' and wrong verb both false");
    } else {
      fail(
        `genericActionProjectionEligible drifted for lease-hygiene: eligible=${eligible} wrongShellForm=${wrongShellForm} wrongVerb=${wrongVerb}`,
      );
    }
  }

  // A3: the client-side honesty probe agrees with the certified allowlist
  // for the skill-surfacing area.
  {
    const eligible = genericActionProjectionEligible("skill-surfacing", [
      "compound",
      "capability",
      "import-surfacing-misses",
      "/home/.compound/reviews/skill-surfacing/2026-07-09-session-mining-backfill-feedback.json",
    ]);
    const wrongVerb = genericActionProjectionEligible("skill-surfacing", ["compound", "capability", "skill-plan"]);
    if (eligible && !wrongVerb) {
      pass("genericActionProjectionEligible agrees with the skill-surfacing allowlist: certified argv true, wrong verb false");
    } else {
      fail(`genericActionProjectionEligible drifted for skill-surfacing: eligible=${eligible} wrongVerb=${wrongVerb}`);
    }
  }

  // B5: the harness-edit-proposals area. internal/director annotates each open
  // harness-edit card with ONE action {id:"harness-edit.whitelist", argv:
  // ["compound","authority","harness-edit-review",<card>,"--decision",
  // "whitelist","--json"]} -- the CLI REQUIRES --rationale, so the projected
  // argv deliberately omits it and the operator's text is appended post-gates.
  {
    const area = "harness-edit-proposals";
    const projection = {
      items: [
        {
          id: "dirprop_selftest",
          actions: [
            {
              id: "harness-edit.whitelist",
              label: "Allow edits to this surface",
              argv: [
                "compound",
                "authority",
                "harness-edit-review",
                "/tmp/reviews/harness-edit-proposals/dirprop_selftest.json",
                "--decision",
                "whitelist",
                "--json",
              ],
              requires_review: true,
            },
          ],
        },
      ],
    };

    // (b5-a) Without a rationale the CLI-required flag is missing -> rejected at
    // rationale-required (NOT silently run with a placeholder).
    const none = resolveGenericActionFromProjection(
      projection,
      area,
      "dirprop_selftest",
      "harness-edit.whitelist",
    );
    if (none.ok || none.rejection.gate !== "rationale-required") {
      fail(`harness-edit whitelist without rationale should reject at rationale-required, got ${JSON.stringify(none)}`);
    } else {
      pass("harness-edit-proposals without rationale rejected at rationale-required");
    }

    // (b5-b) With a rationale it resolves ok and the operator's text is the LAST
    // argv element (appended verbatim, never a shell string).
    const rationale = "agents may trim dead offers here; $(echo x) is just text";
    const withText = resolveGenericActionFromProjection(
      projection,
      area,
      "dirprop_selftest",
      "harness-edit.whitelist",
      rationale,
    );
    if (!withText.ok) {
      fail(`harness-edit whitelist with rationale should pass, got ${JSON.stringify(withText)}`);
    } else {
      const argv = withText.action.argv;
      if (argv[argv.length - 2] !== "--rationale" || argv[argv.length - 1] !== rationale) {
        fail(`harness-edit final argv must END with ["--rationale", <text>], got ${JSON.stringify(argv)}`);
      } else if (!argv.includes("--json") || argv.includes("--decision") === false) {
        fail(`harness-edit resolved argv lost its projected flags: ${JSON.stringify(argv)}`);
      } else {
        pass("harness-edit-proposals with rationale resolves ok; argv ends with [--rationale, <text>]");
      }
    }

    // (b5-c) A forged argv (a real but UNcertified verb for this area) keyed
    // under the same area/item/actionId is rejected at gate-2 -- certification is
    // per-area+verb, not "any authority subcommand".
    const forged = resolveGenericActionFromProjection(
      {
        items: [
          {
            id: "dirprop_selftest",
            actions: [
              {
                id: "harness-edit.whitelist",
                argv: ["compound", "authority", "dirty-review", "/tmp/x.yaml"],
                requires_review: true,
              },
            ],
          },
        ],
      },
      area,
      "dirprop_selftest",
      "harness-edit.whitelist",
      "rationale supplied",
    );
    if (forged.ok || forged.rejection.gate !== "gate-2-allowlist") {
      fail(`forged harness-edit argv (authority dirty-review) should reject at gate-2-allowlist, got ${JSON.stringify(forged)}`);
    } else {
      pass("forged harness-edit argv (authority dirty-review) rejected at gate-2-allowlist");
    }

    // (b5-d) The effect label + client-side honesty probe agree with the
    // certified allowlist for this area.
    if (genericActionEffectKind(area) !== "record") {
      fail(`harness-edit-proposals effect kind = ${genericActionEffectKind(area)}, want record`);
    } else if (
      !genericActionProjectionEligible(area, [
        "compound",
        "authority",
        "harness-edit-review",
        "/tmp/x.json",
        "--decision",
        "whitelist",
        "--json",
      ]) ||
      genericActionProjectionEligible(area, ["compound", "authority", "dirty-review", "/tmp/x.yaml"])
    ) {
      fail("genericActionProjectionEligible drifted for harness-edit-proposals");
    } else {
      pass("harness-edit-proposals effect kind is record and the eligibility probe agrees with the allowlist");
    }
  }

  // (j) The cold-cache honesty gate. The act path always resolves against a
  // cold read (decision-execute invalidates the read cache first), and
  // `compound attention --json` can outlast its timeout — an `unreachable`
  // read must reject as "the projection did not answer", NEVER as
  // "not-found". Collapsing the two was the cold-cache act 404: the operator
  // clicked a rendered, real button and was told the item did not exist.
  {
    const read = { status: "unreachable" as const, value: null, error: "compound command timed out" };
    const res = resolveGenericActionFromRead(read, "lease-hygiene", "lease-hygiene", "lease-hygiene.sweep");
    if (res.ok) {
      fail("unreachable projection read resolved ok:true instead of being rejected");
    } else if (res.rejection.gate !== "projection-unavailable") {
      fail(`unreachable projection read rejected at "${res.rejection.gate}" instead of projection-unavailable`);
    } else if (!res.rejection.message.includes("nothing was run")) {
      fail(`projection-unavailable message must say nothing ran, got: ${res.rejection.message}`);
    } else {
      pass("unreachable projection read rejects at projection-unavailable (slow door), never not-found");
    }
  }

  // (k) A `stale` read still carries the last-good value: resolution proceeds
  // through the same three gates (staleness of the ACTION is guarded by the
  // revision match in decision-execute.ts, not by refusing to resolve).
  {
    const read = {
      status: "stale" as const,
      value: {
        groups: [
          {
            key: "lease-hygiene",
            area: "lease-hygiene",
            action: {
              id: "lease-hygiene.sweep",
              label: "Sweep leftover claim tags",
              argv: ["compound", "home", "archive-history", "--kind", "lease"],
              kind: "batch",
              requires_confirmation: true,
            },
          },
        ],
      },
      error: "latest refresh attempt failed",
    };
    const res = resolveGenericActionFromRead(read, "lease-hygiene", "lease-hygiene", "lease-hygiene.sweep");
    if (!res.ok) {
      fail(`stale read with a last-good value failed to resolve: ${res.rejection.gate} - ${res.rejection.message}`);
    } else if (res.action.argv.join(" ") !== "home archive-history --kind lease") {
      fail(`stale read resolved unexpected argv: ${JSON.stringify(res.action.argv)}`);
    } else {
      pass("stale read with a last-good value still resolves (revision match stays the freshness guard)");
    }
  }

  return { ok, details };
}

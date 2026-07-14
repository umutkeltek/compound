import { arr, type Json, runJson, str } from "@/lib/compound";
import { argvMatchesAllowlist } from "@/lib/decision-registry";

/*
 * The generic governed-action pathway — Phase 3a of the console redesign
 * (Plans/i-am-not-happy-rosy-floyd.md Part 4, "Closing the 91-item action
 * gap"). Extends `POST /api/decision/execute` (decision-execute.ts) so a
 * review item that has NO bespoke `DecisionAreaSpec` in decision-registry.ts
 * can still be executed one-click, by trusting only the backend's own
 * freshly-resolved `actions[]` (per item) / `safe_batch_action` (per group)
 * projection — never client-supplied argv.
 *
 * Request contract (unchanged endpoint, additive fields):
 *   POST /api/decision/execute
 *   { area, itemId, actionId, expectedRevision, confirmed? }
 *   -> looks up `compound reviews --json --area <area>` fresh, finds the item
 *      (by `items[].id`) or group (by `groups[].key`), finds the action (by
 *      `actions[].id` or `safe_batch_action.id`), and — if it clears gates
 *      1-3 below and any required confirmation — runs the resolved argv
 *      through the same governed `runCompound` + cache-invalidate path the
 *      bespoke registry uses.
 *
 * This path only ever engages when `area` has NO entry in the bespoke
 * REGISTRY (decision-execute.ts checks `specFor(area)` first; bespoke always
 * wins). Adding a bespoke spec for an area removes it from this path
 * automatically — no change needed here.
 *
 * Three gates, in order, each with a distinct rejection reason:
 *   1. `gate-1-shell-form`     — resolved argv[0] must be literally
 *      "compound". Panel per-item actions are deliberately unexecutable
 *      `/bin/sh -lc ...` shell forms (see decision-registry.ts's
 *      "panel-apply" comment) — they must stay copy-only forever, from any
 *      area, even a certified one.
 *   2. `gate-2-allowlist`      — the area must be a key in
 *      GENERIC_ACTION_ALLOWLIST AND the bare argv (argv with the leading
 *      "compound" stripped) must match one of that area's certified
 *      verb-prefixes. An area absent from the map is never executable
 *      generically, full stop — deliberate, never blanket.
 *   3. `gate-3-global-allowlist` — defense in depth: the SAME bare argv must
 *      also match the flat, area-agnostic GENERIC_GLOBAL_ARGV_ALLOWLIST
 *      below (reusing the exported `argvMatchesAllowlist` mechanics
 *      decision-registry.ts already uses for every bespoke spec). Extended
 *      only with the exact verbs the seed areas need.
 *
 * Plus, independent of the three gates: if the resolved action's
 * `requires_review` is true, the request must carry `{confirmed: true}`
 * (the UI's ConfirmDialog sets this after showing label/policy_note/argv) or
 * the caller gets 428.
 */

export type GenericActionGate =
  | "gate-1-shell-form"
  | "gate-2-allowlist"
  | "gate-3-global-allowlist"
  | "rationale-required"
  | "not-found";

export type ResolvedGenericAction = {
  /** argv WITHOUT the leading "compound" — ready for runCompound(). */
  argv: string[];
  actionId: string;
  label: string;
  requiresReview: boolean;
  policyNote: string;
  revision: string;
};

export type GenericActionRejection = {
  gate: GenericActionGate;
  message: string;
};

export type GenericActionResolution =
  | { ok: true; action: ResolvedGenericAction }
  | { ok: false; rejection: GenericActionRejection };

/**
 * Gate 2 — per-area certification. Seeded 2026-07-09 by reading the LIVE
 * `compound reviews --json --area <area>` output and the Go annotators that
 * build it (verified against source, not just the projection, since a review
 * area can have zero live review_required items at seed time):
 *
 *  - "job-health": internal/authority/job_health_review.go emits
 *    `actions: [{id: "record_job_health_review",
 *      argv: ["compound","authority","job-health-review",<review.yaml>]}]`
 *    on every review_required item. Verified live 2026-07-09 (area had 6
 *    items, 0 review_required at seed time, all recorded/resolved) against
 *    the Go source, which is unconditional for any review_required item.
 *  - "source-control-cleanup": internal/authority/sourcecontrol_plan.go
 *    emits `actions: [{id: "record_source_control_cleanup_review",
 *      argv: ["compound","authority","dirty-review",<review.yaml>]}]` on
 *    every review_required item. Verified live 2026-07-09 (area had 3 items,
 *    0 review_required at seed time) against the Go source.
 *  - "observation-candidates": internal/observation/review.go emits
 *    `actions: [{id: "plan_observation_lens_review",
 *      argv: ["compound","panel","plan","--target-kind",
 *      "capability_observation","--target",<id>,"--domain",<d>,"--task",<t>,
 *      "--json"]}]` on every review_required item (a panel *plan*, not an
 *      apply — read-only planning, safe to certify). Verified live
 *      2026-07-09 (area had 2 items, 0 review_required at seed time) against
 *      the Go source.
 *
 *  - "route-promotions": internal/routeevidence/review_queue.go emits
 *    `actions: [{id: "route_promotion_record",
 *      argv: ["compound","route","promotion-record","--review",<path>]}]` on
 *    every review_required item (landed same-change with this allowlist
 *    entry; the earlier Actions=nil gap is closed). --rationale is REQUIRED
 *    by the CLI and deliberately absent from the projected argv — the
 *    rationale-append rule below supplies it from operator input.
 *  - "panel-verdicts": internal/panel/review_queue.go emits one clean
 *    per-member `panel_verdict_record_member` action
 *    (argv ["compound","panel","verdict-record","--review",<path>,
 *    "--reviewed-by",<actor>]) beside its copy-only shell choices (landed
 *    eeaab41). Same rationale-append rule applies.
 *  - "lease-hygiene": NOT a reviews area at all -- internal/attention emits
 *    ONE cross-executor `lease-hygiene` needs_you group (packet:
 *    lease-hygiene-verb) carrying a `safe_batch_action`-shaped `action`
 *    field: `{id: "lease-hygiene.sweep", argv: ["compound","home",
 *    "archive-history","--kind","lease"]}`. See ATTENTION_SOURCED_GENERIC_AREAS
 *    below for how this area's actions get resolved from `compound attention
 *    --json` instead of `compound reviews --json --area <area>`.
 */
export const GENERIC_ACTION_ALLOWLIST: Record<string, string[][]> = {
  "job-health": [["authority", "job-health-review"]],
  "source-control-cleanup": [["authority", "dirty-review"]],
  "observation-candidates": [["panel", "plan"]],
  "route-promotions": [["route", "promotion-record"]],
  "panel-verdicts": [["panel", "verdict-record"]],
  "lease-hygiene": [["home", "archive-history"]],
};

export type GenericActionEffectKind = "apply" | "record" | "track";

const GENERIC_ACTION_EFFECT_KIND: Record<string, GenericActionEffectKind> = {
  "job-health": "record",
  "source-control-cleanup": "record",
  "observation-candidates": "track",
  "route-promotions": "record",
  "panel-verdicts": "record",
  "lease-hygiene": "apply",
};

export function genericActionEffectKind(area: string): GenericActionEffectKind | null {
  return GENERIC_ACTION_EFFECT_KIND[area] ?? null;
}

/**
 * Stable optimistic-concurrency token shared by Attention (requires_confirmation)
 * and Reviews (requires_review). The readable JSON payload is intentional: the
 * token is an exact comparison value, not a secret or an authority signature.
 */
export function genericActionRevision(rawAction: unknown): string {
  const action = rawAction && typeof rawAction === "object" ? rawAction as Json : {};
  const requiresReview = Boolean(action.requires_review ?? action.requires_confirmation);
  return `generic-action-v1:${JSON.stringify([
    str(action.id),
    str(action.label),
    arr(action.argv).map(String),
    requiresReview,
  ])}`;
}

/**
 * Areas whose CLI REQUIRES --rationale: the backend deliberately omits it
 * from the projected argv so no placeholder text can ever run. The operator
 * supplies the rationale in the confirmation UI; it is appended as its own
 * argv element (["--rationale", <text>]) — an exec argv array, never a
 * shell string, so arbitrary rationale text is safe by construction.
 * Resolution REJECTS ("rationale-required") when the operator supplied none.
 */
export const RATIONALE_REQUIRED_AREAS: ReadonlySet<string> = new Set([
  "route-promotions",
  "panel-verdicts",
]);

/**
 * Gate 3 — flat, area-agnostic defense in depth. Reuses the exported
 * `argvMatchesAllowlist` function decision-registry.ts already uses for
 * every bespoke spec's own `argvAllowlist`; this array is local to the
 * generic pathway and extended only with the exact verbs the seed areas
 * above need, never blanket.
 */
const GENERIC_GLOBAL_ARGV_ALLOWLIST: string[][] = [
  ["authority", "job-health-review"],
  ["authority", "dirty-review"],
  ["panel", "plan"],
  ["route", "promotion-record"],
  ["panel", "verdict-record"],
  ["home", "archive-history"],
];

/** Client-side honesty probe using the same two allowlists as execution. */
export function genericActionProjectionEligible(area: string, rawArgv: unknown): boolean {
  const argv = arr(rawArgv).map(String);
  if (argv.length < 2 || argv[0] !== "compound") return false;
  const bare = argv.slice(1);
  const areaAllowlist = GENERIC_ACTION_ALLOWLIST[area];
  return Boolean(
    areaAllowlist &&
      argvMatchesAllowlist(bare, areaAllowlist) &&
      argvMatchesAllowlist(bare, GENERIC_GLOBAL_ARGV_ALLOWLIST),
  );
}

function actionFromItem(item: Json, actionId: string): Json | null {
  return arr(item.actions).find((a) => str(a.id) === actionId) ?? null;
}

/** Locate the item (by `items[].id`) or group (by `groups[].key`) carrying itemId. */
function findItemOrGroup(
  projection: Json | null,
  itemId: string,
): { item: Json | null; group: Json | null } {
  const item = arr(projection?.items).find((it) => str(it.id) === itemId) ?? null;
  if (item) return { item, group: null };
  const group = arr(projection?.groups).find((g) => str(g.key) === itemId) ?? null;
  return { item: null, group };
}

/**
 * Pure core: resolves + gates an action against an already-fetched
 * projection. Kept side-effect-free and injectable so
 * generic-actions.selftest.ts can exercise every gate with a crafted
 * fixture — no on-disk compound runtime home, no subprocess, no network.
 */
export function resolveGenericActionFromProjection(
  projection: Json | null,
  area: string,
  itemId: string,
  actionId: string,
  rationale?: string,
): GenericActionResolution {
  const { item, group } = findItemOrGroup(projection, itemId);

  let action: Json | null = null;
  if (item) {
    action = actionFromItem(item, actionId);
  } else if (group && str((group.safe_batch_action as Json | undefined)?.id) === actionId) {
    action = (group.safe_batch_action as Json) ?? null;
  }

  if (!action) {
    return {
      ok: false,
      rejection: { gate: "not-found", message: `no action "${actionId}" found for ${area}/${itemId}` },
    };
  }

  const rawArgv = arr(action.argv).map(String);

  // Gate 1: clean compound argv only. Shell forms (argv[0] !== "compound",
  // e.g. panel per-item "/bin/sh -lc ...") stay copy-only forever.
  if (rawArgv.length < 2 || rawArgv[0] !== "compound") {
    return {
      ok: false,
      rejection: {
        gate: "gate-1-shell-form",
        message: `action "${actionId}" is not a clean compound command; copy it instead of running it here`,
      },
    };
  }

  const bareArgv = rawArgv.slice(1);

  // Gate 2: area must be certified AND the resolved verb must match one of
  // that area's certified prefixes.
  const areaAllowlist = GENERIC_ACTION_ALLOWLIST[area];
  if (!areaAllowlist || !argvMatchesAllowlist(bareArgv, areaAllowlist)) {
    return {
      ok: false,
      rejection: {
        gate: "gate-2-allowlist",
        message: `area "${area}" is not certified for generic execution of "${bareArgv.join(" ")}"`,
      },
    };
  }

  // Gate 3: defense-in-depth flat check, independent of gate 2's per-area map.
  if (!argvMatchesAllowlist(bareArgv, GENERIC_GLOBAL_ARGV_ALLOWLIST)) {
    return {
      ok: false,
      rejection: {
        gate: "gate-3-global-allowlist",
        message: `command "${bareArgv.join(" ")}" is not in the global generic-action allowlist`,
      },
    };
  }

  // Rationale-append rule: runs AFTER the gates (appending flags cannot
  // change the verb the gates certified). Only for areas whose CLI requires
  // --rationale; the projected argv never carries one (asserted below so a
  // future backend placeholder can't sneak past).
  let finalArgv = bareArgv;
  if (RATIONALE_REQUIRED_AREAS.has(area)) {
    if (bareArgv.includes("--rationale")) {
      return {
        ok: false,
        rejection: {
          gate: "gate-2-allowlist",
          message: `projected argv for "${actionId}" unexpectedly carries --rationale; refusing to run a placeholder rationale`,
        },
      };
    }
    const text = (rationale ?? "").trim();
    if (!text) {
      return {
        ok: false,
        rejection: {
          gate: "rationale-required",
          message: `action "${actionId}" requires an operator rationale; it becomes provenance on the recorded review`,
        },
      };
    }
    finalArgv = [...bareArgv, "--rationale", text];
  }

  return {
    ok: true,
    action: {
      argv: finalArgv,
      actionId,
      label: str(action.label),
      // Normalized the same way genericActionRevision already is: an
      // attention-sourced action carries `requires_confirmation`, never
      // `requires_review` (that field belongs to the reviews projection).
      // Falling back keeps the 428-confirm gate in decision-execute.ts
      // enforcing for both projection shapes without that file knowing which
      // one it got.
      requiresReview: Boolean(action.requires_review ?? action.requires_confirmation),
      policyNote: str(action.policy_note),
      revision: genericActionRevision(action),
    },
  };
}

/**
 * Areas whose generic actions come from `compound attention --json` (a
 * cross-source needs_you projection) instead of `compound reviews --json
 * --area <area>` (a per-area review queue) -- there is no bespoke reviews
 * area for these; the group IS the executable unit. See
 * adaptAttentionProjectionForArea for the shape translation.
 */
export const ATTENTION_SOURCED_GENERIC_AREAS: ReadonlySet<string> = new Set(["lease-hygiene"]);

/**
 * Reshapes `compound attention --json`'s `groups[]` (each carrying a plain
 * `action` field) into the `{groups:[{key, safe_batch_action}]}` shape
 * resolveGenericActionFromProjection already understands from the reviews
 * projection -- so the same three gates apply unmodified. Pure and
 * deterministic: filtering by `area` and renaming `action` to
 * `safe_batch_action` changes nothing else about the action payload, so
 * genericActionRevision (which normalizes `requires_review ??
 * requires_confirmation`) yields the identical token whether it runs over
 * the client's rendered attention action or this server-side adaptation.
 */
export function adaptAttentionProjectionForArea(attention: Json | null, area: string): Json {
  const groups = arr(attention?.groups)
    .filter((raw) => str((raw as Json).area) === area)
    .map((raw) => {
      const g = raw as Json;
      return { key: str(g.key), safe_batch_action: g.action ?? null };
    });
  return { groups };
}

/**
 * Server-side re-resolution. NEVER trusts client-supplied argv: re-fetches
 * the owning projection fresh (through the same memoized `runJson` every
 * read path uses) and resolves the action from that live projection, so a
 * stale/forged client argv can never be replayed. Attention-sourced areas
 * (ATTENTION_SOURCED_GENERIC_AREAS) read `compound attention --json` through
 * the adapter above; every other area keeps reading `compound reviews --json
 * --area <area>` as before.
 */
export async function resolveGenericAction(
  area: string,
  itemId: string,
  actionId: string,
  rationale?: string,
): Promise<GenericActionResolution> {
  if (ATTENTION_SOURCED_GENERIC_AREAS.has(area)) {
    const attention = await runJson(["attention", "--json"]);
    const projection = adaptAttentionProjectionForArea(attention, area);
    return resolveGenericActionFromProjection(projection, area, itemId, actionId, rationale);
  }
  const projection = await runJson(["reviews", "--json", "--area", area]);
  return resolveGenericActionFromProjection(projection, area, itemId, actionId, rationale);
}

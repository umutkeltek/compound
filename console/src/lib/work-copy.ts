/**
 * Work-page operator copy — the single source for the Work view's empty and
 * degraded section states (the two ambient cards + the read-only console
 * notice). Lifted out of inline JSX so the copy has ONE testable seam.
 *
 * LEGIBILITY LAW (console legibility spec §2 G-series, operator-legibility
 * standard):
 *   - Every `prose` / plain string speaks operator language: it names the
 *     CONSEQUENCE and a PLAIN action — never a raw CLI command, flag, id, or
 *     path (G3). The operator should never have to read a command to
 *     understand a section's state.
 *   - The exact command lives ONLY in a `command` field, rendered beside the
 *     prose as a CopyCmd affordance — moved out of the sentence, never deleted.
 *
 * `console/scripts/work-legibility-smoke.mjs` imports this model and fails if
 * any non-`command` string reads as a CLI invocation, or if a command it lifted
 * out of prose has gone missing.
 */

/** A degraded-state notice: operator prose (CLI-free) + the exact command it
 *  offers as a copy affordance. */
export type DegradedNotice = {
  /** Operator language: the consequence and a plain action. Contains no CLI. */
  prose: string;
  /** The exact command, surfaced as a CopyCmd affordance beside the prose. */
  command: string;
};

export const WORK_COPY = {
  laneTraffic: {
    /** No route-evidence rows yet — informational, nothing to act on. */
    noTraffic:
      "No model activity has been recorded yet. This fills in as agents run.",
    /** Account-health feed never imported: capsules can't be colored by health. */
    noAccountProjection: {
      prose:
        "Account health is not connected yet. Check the account feed to see which agents are available.",
      command: "compound route import-account-projection",
    },
  },
  transitions: {
    /** Stream gate reports offline: last-known chips shown, nothing lost. */
    offline:
      "Live stream is offline. Reopen this view once it's back — nothing here is lost, only paused.",
    /** Stream connected, no moves have crossed yet. */
    empty: "No transitions streaming yet. They appear here the moment work moves.",
  },
  /** The whole console is read-only until launched with an operator home. */
  consoleReadOnly: {
    prose:
      "This browser is read-only. You can review progress and prepare an agent handoff, but cannot start or change tasks here.",
    command: "compound console --operator",
  },
} as const;

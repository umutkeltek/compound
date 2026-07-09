import type { AiConsole, ViewContext, ViewProposal } from "./types";

/**
 * Static fixture — deterministic, no network, no LLM, no gateway call. Proves
 * the `ViewProposal` shape resolves through `view-registry.ts` before any real
 * adapter exists. `actionProposals` stays empty: this phase never proposes a
 * mutation, only views.
 */
const FIXTURE_PROPOSAL: ViewProposal = {
  components: [
    { view: "stat-tile", props: { label: "Needs you", value: 3, sub: "reviews" } },
    { view: "state-badge", props: { state: "needs_review" } },
  ],
  actionProposals: [],
};

export const stubAiConsole: AiConsole = {
  proposeView(_query: string, _ctx: ViewContext): Promise<ViewProposal> {
    return Promise.resolve(FIXTURE_PROPOSAL);
  },
};

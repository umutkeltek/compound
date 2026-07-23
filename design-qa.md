# Panel Proof Continuity — Design QA

## Comparison target

Selected Product Design direction: **Option 3, Proof Continuity**. The implementation applies that direction to the existing Compound Decide surface and keeps the existing left navigation, theme, tokens, components, and projection-first language.

- Source visual truth: `${HOME}/.codex/visualizations/2026/07/23/019f8ed7-eb8a-7c43-8ed5-6d341033bc64/proof-continuity-design-qa/proof-continuity-source-normalized.png`
- Browser-rendered implementation: `${HOME}/.codex/visualizations/2026/07/23/019f8ed7-eb8a-7c43-8ed5-6d341033bc64/proof-continuity-design-qa/proof-continuity-implementation-pass5-clean-1440x1024.png`
- Full combined comparison, source left and implementation right: `${HOME}/.codex/visualizations/2026/07/23/019f8ed7-eb8a-7c43-8ed5-6d341033bc64/proof-continuity-design-qa/proof-continuity-comparison-pass5-clean.png`

## Viewport and normalization

- Source pixels: `1440 × 1024`.
- Implementation pixels: `1440 × 1024`.
- CSS viewport: `1440 × 1024`.
- Device scale factor: `1`.
- No density or scale normalization was required.
- State: dark theme; Decide; one live Panel run; `needs_human`; first claim; Human review selected; no operator choice selected; Technical depth collapsed.
- Both artifacts show stale/non-actionable owner evidence. Their exact freshness ages and policy identifiers differ because the implementation uses the current live projections rather than mock data.

## Focused comparison evidence

Focused comparisons were required because the lifecycle states, evidence labels, lens findings, and choice effects are too small to judge reliably from the full `2880 × 1024` comparison alone.

- Claim, authority boundary, and eight-stage trace: `${HOME}/.codex/visualizations/2026/07/23/019f8ed7-eb8a-7c43-8ed5-6d341033bc64/proof-continuity-design-qa/proof-continuity-focus-trace-pass5.png`
- Rationale, findings, evidence, choices, disabled action, and Technical depth: `${HOME}/.codex/visualizations/2026/07/23/019f8ed7-eb8a-7c43-8ed5-6d341033bc64/proof-continuity-design-qa/proof-continuity-focus-choice-pass5.png`

## Findings

No actionable P0, P1, or P2 finding remains.

- Fonts and typography: the implementation reuses Compound's existing sans and mono families and type tokens. Claims and operator decisions retain the strongest hierarchy; identifiers, freshness, and proof references remain mono. Line clamps only affect secondary evidence detail and retain the full value in `title` or Technical depth.
- Spacing and layout rhythm: the selected three-part frame—claim and proof trace, human-review evidence, operator choice—fits inside the `1440 × 1024` viewport with Technical depth visible and collapsed. The safe review-only action is a compact disclosure rather than an always-expanded form.
- Colors and visual tokens: current, complete, blocked, unknown, absent, stale, and evidence states reuse Compound's blue, green, amber, red, and muted token grammar. Every state also has a text label.
- Image quality and asset fidelity: the source contains no raster product imagery, custom logo, illustration, or decorative asset that requires extraction. The implementation reuses Compound's existing logo and UI icon/component system; no placeholder or generated asset was substituted.
- Copy and content: the current claim, run, candidate, evidence refs, lens findings, policy decision, and authority work are read from Compound projections. The implementation does not copy invented mock consequences, receipts, transitions, history, or authority.
- Information hierarchy: one claim remains primary. Panel synthesis is explicitly evidence rather than authority. Human review is current; governed action and PolicyDecision are separated; Receipt and Transition remain visibly absent.
- Interaction and accessibility: run/disposition selection, Previous/Next claim navigation, stage selection, operator radio choices, the disabled composite action, the review-only safe-action disclosure, authority Work link, and Technical depth all have native interactive semantics and visible focus. The composite review-plus-memory action remains disabled because no safe owner adapter exists.
- Projection churn: when Attention and Reviews refresh with different member IDs for the same Panel run/disposition, the claim stays in the Proof Continuity treatment as evidence-only, receives a visible alignment warning, and strips all action authority until exact membership realigns.

Accepted intentional differences:

- The generated direction draws a connected lifecycle rail. The implementation uses Compound's existing compact interactive stage cards so every stage remains selectable, responsive, and text-labeled without introducing a bespoke diagram component.
- The mock says `Open in Work (future)`. Current backend data projects `work_review_area_panel_verdicts`, so the implementation truthfully enables `Open review authority work`.
- The mock uses abbreviated example effects and evidence. The implementation shows the current owner-projected choice effects and evidence refs, with additional detail moved to Technical depth.
- The implementation keeps the existing persistent navigation and global console toolbar unchanged, as requested.

## Browser verification

Fresh verification used the Codex in-app Browser against the local `/reviews` deep link on the isolated preview port.

Primary interactions tested:

- Previous and Next change the claim and return to the original claim.
- Needs human, Park, Promote, and Reject are run-scoped Panel lenses; Park changed both the claim and the deep-link URL, and returning to Needs human restored the selected group.
- Selecting Promote changes the primary button label to `Apply Promote`; it remains disabled.
- Selecting PolicyDecision updates the pressed stage and its announced state.
- Safe review recording opens as a separate review-evidence-only disclosure when current authority exists; it never enables the combined memory adjudication.
- Technical depth opens and closes.
- Manual refresh preserved the selected Proof Continuity route and current claim.
- Browser console warnings/errors after the interaction pass: `0`.

## Verification

- `bunx tsc --noEmit`: passed.
- `bun scripts/decide-model-brief-smoke.mjs`: passed.
- `bun scripts/decide-decision-feedback-smoke.mjs`: passed.
- `bun scripts/attention-consistency-smoke.mjs`: passed.
- `bunx next build`: passed; all seven static-generation steps and route optimization completed.

## Comparison history

1. The first coded pass established the claim-first layout, eight proof stages, owner evidence, lens findings, choices, and fail-closed composite action.
2. A same-viewport review found P2 vertical-density drift: the live safe-action form and duplicate paused-state message pushed Technical depth below the `1440 × 1024` frame. Choice rows were compacted, duplicate boundary copy was merged, and the safe review-only action became a collapsed disclosure. Post-fix evidence: `proof-continuity-implementation-pass5-clean-1440x1024.png`.
3. Live refresh exposed a P1 continuity failure: when Attention and Reviews temporarily named different member IDs, the selected Panel group lost its claim detail and fell back to the generic decision card. A red-first falsifier now proves that matching run/disposition detail survives as evidence-only, with actions stripped and the mismatch labeled until exact membership realigns.
4. The final full and focused combined comparisons found no remaining actionable P0/P1/P2 difference. Remaining visual differences are intentional existing-component, current-data, and backend-truth constraints.

final result: passed

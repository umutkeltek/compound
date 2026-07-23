# Compound Console Experience Depth — Design QA

## Comparison target

The approved concepts are visual direction for six existing Compound surfaces, not permission to fabricate history, consequences, dependencies, or execution authority. The implementation therefore preserves the concepts' hierarchy and interaction model while applying Compound's simple-to-advanced disclosure rule and current projection contract.

Source visual truth (`1487 × 1058` pixels each):

- Today causal focus: `${HOME}/.codex/generated_images/019f89b4-32b7-73b0-b3b5-8a042496a6bc/exec-8e228ffd-598b-4c9f-afcb-1167870fdeee.png`
- Live operations: `${HOME}/.codex/generated_images/019f89b4-32b7-73b0-b3b5-8a042496a6bc/exec-9f946656-49d7-4ab3-af89-c377dd66d1b6.png`
- Work table and Ask Compound: `${HOME}/.codex/generated_images/019f89b4-32b7-73b0-b3b5-8a042496a6bc/exec-177e9c92-9235-46fb-af13-cf54c58abeec.png`
- Work space: `${HOME}/.codex/generated_images/019f89b4-32b7-73b0-b3b5-8a042496a6bc/exec-b3032ddd-82cc-4585-b31c-c03395e2c2f3.png`
- Decide comparison: `${HOME}/.codex/generated_images/019f89b4-32b7-73b0-b3b5-8a042496a6bc/exec-ed04b105-0031-473f-84e0-f2bec6bdce06.png`
- Work flow: `${HOME}/.codex/generated_images/019f89b4-32b7-73b0-b3b5-8a042496a6bc/exec-ee01867b-6cc7-4a4d-a56a-1770455f9f7e.png`

Rendered implementation evidence:

- Today, Investigate depth: `${HOME}/.codex/visualizations/2026/07/22/019f89b4-32b7-73b0-b3b5-8a042496a6bc/implementation-qa/today-focus-pass4.jpg`
- Live: `${HOME}/.codex/visualizations/2026/07/22/019f89b4-32b7-73b0-b3b5-8a042496a6bc/implementation-qa/live-pass2.jpg`
- Work table: `${HOME}/.codex/visualizations/2026/07/22/019f89b4-32b7-73b0-b3b5-8a042496a6bc/implementation-qa/work-table-pass2.jpg`
- Ask Compound composed view: `${HOME}/.codex/visualizations/2026/07/22/019f89b4-32b7-73b0-b3b5-8a042496a6bc/implementation-qa/work-table-ask-pass2.jpg`
- Work flow: `${HOME}/.codex/visualizations/2026/07/22/019f89b4-32b7-73b0-b3b5-8a042496a6bc/implementation-qa/work-flow-pass3.jpg`
- Work space: `${HOME}/.codex/visualizations/2026/07/22/019f89b4-32b7-73b0-b3b5-8a042496a6bc/implementation-qa/work-space-pass5.jpg`
- Decide comparison: `${HOME}/.codex/visualizations/2026/07/22/019f89b4-32b7-73b0-b3b5-8a042496a6bc/implementation-qa/decide-compare-pass2.jpg`
- Mobile Work table: `${HOME}/.codex/visualizations/2026/07/22/019f89b4-32b7-73b0-b3b5-8a042496a6bc/implementation-qa/work-table-mobile-pass2.jpg`
- Current needs-first preview: `docs_private/marketing/console-fidelity-qa/{today,live,work,decide-compare}-preview.png`
- Current Ask composed result: `docs_private/marketing/console-fidelity-qa/work-ask-result-preview.png`

## Viewport and normalization

- Desktop CSS viewport: `1536 × 1080`; implementation captures: `1536 × 1080` at density `1`, except the full-page Decide evidence (`1536 × 3480`), whose first `1536 × 1080` viewport was used for the full-view comparison.
- Responsive check: `390 × 844` at density `1`. Document width remained `390` for Work and Live. The final Space canvas keeps its `1180`-pixel workfield inside a `332`-pixel internal scroller; document width remains `390`.
- Current in-app verification viewport: `1280 × 720` at density `1`. Sources were top-cropped to `1487 × 837` and normalized to `1280 × 720` for the latest combined comparison.
- Sources are `1487 × 1058`. Full comparisons fit source and implementation viewport into equal halves. Focused comparisons use semantic crops from each artifact and equal-width normalization; no finding is based on the small source/viewport density difference.
- State alignment: Today uses `Investigate` or `Understand`, Work uses the named `Table`, `Flow`, and `Space` lenses, Decide uses `Compare`, Live uses its default `Overview`, and Ask Compound shows the completed “Why is work blocked?” composition.

## Combined comparison evidence

Left side is the approved concept; right side is the rendered implementation.

Full-view comparisons:

- Today: `${HOME}/.codex/visualizations/2026/07/22/019f89b4-32b7-73b0-b3b5-8a042496a6bc/implementation-qa/comparisons/today-pass4.jpg`
- Live: `${HOME}/.codex/visualizations/2026/07/22/019f89b4-32b7-73b0-b3b5-8a042496a6bc/implementation-qa/comparisons/live.jpg`
- Work table: `${HOME}/.codex/visualizations/2026/07/22/019f89b4-32b7-73b0-b3b5-8a042496a6bc/implementation-qa/comparisons/work-table.jpg`
- Work table with Ask: `${HOME}/.codex/visualizations/2026/07/22/019f89b4-32b7-73b0-b3b5-8a042496a6bc/implementation-qa/comparisons/work-table-ask.jpg`
- Work flow: `${HOME}/.codex/visualizations/2026/07/22/019f89b4-32b7-73b0-b3b5-8a042496a6bc/implementation-qa/comparisons/work-flow.jpg`
- Work space: `${HOME}/.codex/visualizations/2026/07/22/019f89b4-32b7-73b0-b3b5-8a042496a6bc/implementation-qa/comparisons/work-space-pass5.jpg`
- Decide: `${HOME}/.codex/visualizations/2026/07/22/019f89b4-32b7-73b0-b3b5-8a042496a6bc/implementation-qa/comparisons/decide.jpg`
- Current fidelity pass: `docs_private/marketing/console-fidelity-qa/compare-{today,live,work-ask,decide}.png`

Focused comparisons were required because the concepts contain dense tables, lifecycle nodes, evidence rows, and small state labels:

- Today causal path: `${HOME}/.codex/visualizations/2026/07/22/019f89b4-32b7-73b0-b3b5-8a042496a6bc/implementation-qa/comparisons/focus-today-pass4.jpg`
- Live timeline: `${HOME}/.codex/visualizations/2026/07/22/019f89b4-32b7-73b0-b3b5-8a042496a6bc/implementation-qa/comparisons/focus-live.jpg`
- Work table: `${HOME}/.codex/visualizations/2026/07/22/019f89b4-32b7-73b0-b3b5-8a042496a6bc/implementation-qa/comparisons/focus-work-table.jpg`
- Work flow: `${HOME}/.codex/visualizations/2026/07/22/019f89b4-32b7-73b0-b3b5-8a042496a6bc/implementation-qa/comparisons/focus-work-flow.jpg`
- Work space: `${HOME}/.codex/visualizations/2026/07/22/019f89b4-32b7-73b0-b3b5-8a042496a6bc/implementation-qa/comparisons/focus-work-space.jpg`
- Decide comparison: `${HOME}/.codex/visualizations/2026/07/22/019f89b4-32b7-73b0-b3b5-8a042496a6bc/implementation-qa/comparisons/focus-decide.jpg`

## Findings

No actionable P0, P1, or P2 finding remains.

- Typography: the implementation consistently uses Compound's existing sans and mono families, optical weights, hierarchy, truncation, and line heights. Dense identifiers remain mono; user-facing causal copy remains readable sans. No concept-only display typeface was introduced.
- Spacing and layout rhythm: the existing navigation, content frame, panel radii, borders, and density tokens are preserved. All new lenses contain their own horizontal overflow; persistent navigation and document width do not overflow at tablet or mobile widths.
- Colors and visual tokens: brighter dark-mode values retain the existing semantic grammar. Amber remains intervention/waiting, green remains live/available, blue remains selected/actionable, purple remains evidence/decision context, and harness identity receives stable domain accents. No decorative color invents state.
- Image and asset fidelity: the concepts contain no raster product assets, logos, illustrations, or non-standard icons that needed extraction. The implementation uses the existing Compound mark and icon system; lifecycle rails and cards are native UI, not asset substitutes.
- Copy and content: real Compound projections replace concept placeholder records. Missing consequences, dependencies, execution detail, and historical paths are explicitly labeled unknown, unavailable, or not projected. Tambo is named only as a replaceable composition adapter and receives no mutation authority.
- Interactions and affordances: Table filter, search, and progressive loading work; rows open the shared trace; Flow and Space update the URL; Space selection updates the persistent inspector; Decide Compare renders known and unknown consequences; Ask Compound composes only registered views and shows loading, result, and error states; `/flow` redirects to `/work?lens=flow`.
- Accessibility: controls use native buttons, tabs, labels, dialog semantics, visible focus treatment, and accessible card labels. State is not carried by color alone in table, inspector, decision, or Ask views.

Accepted intentional differences:

- The concepts show richer historical-looking connectors. The implementation draws current lifecycle position only because Compound does not project a trustworthy dependency history for these records.
- Today keeps a calm greeting and live decision seam above the causal Focus Path rather than fabricating one universal operator question.
- Live makes the truthful harness workfield the Understand-level hero and moves denser operations detail to Investigate.
- Decide displays “Consequence not projected by the owner” instead of inventing the concept's example consequences.

## Browser and responsive verification

- Checked Today depth switching, Work lens URL state, Table filter/search/show-more, trace opening and rapid close-plus-lens switching, Space card selection, Decide Compare and evidence tabs, Ask open/close, route-aware suggestions, loading, composed result, and `/flow` redirect.
- The initial route pass reported no browser console warnings or errors. The current in-app pass also returned an empty warning/error log after Work, Live, Decide, and Ask interactions.
- Desktop document width: `1536` at a `1536` viewport. Tablet Work document width: `1024` at `1024`. Mobile Work and Live document width: `390` at `390`. Final mobile Space document width: `390` at `390`, with spatial overflow contained inside the lens.

## Comparison history

1. First pass found P2 information-order and responsive issues: ambient transitions appeared before the selected Work lens, Work tabs/table could widen the document, and Live's primary response waited on slow coordination evidence. The lens moved first, ambient evidence moved to Investigate depth, overflow was contained, and coordination now streams behind the primary Live response. Post-fix evidence: `live-pass2.jpg`, `work-table-pass2.jpg`, and `work-table-mobile-pass2.jpg`.
2. Second pass found P2 density drift: Live showed too many timeline rows, and Flow/Space showed six cards per lane. Live was capped at six priority rows with a complete-Work link; Flow and Space now prioritize three current/proof-bound items per lane. Post-fix evidence: `live-pass2.jpg`, `work-flow-pass3.jpg`, and `work-space-pass3.jpg`.
3. Combined source-versus-build review found a P2 distinction problem: Flow and Space were truthful but visually too similar. Space was rebuilt around harness rooms, current-position rails, and a persistent selected-work inspector. The first revised capture then exposed a P1 overlap between stacked cards; cards were compressed into readable single-line current-state rows with full details in the inspector. Post-fix evidence: `work-space-pass5.jpg`, `comparisons/work-space-pass5.jpg`, and `comparisons/focus-work-space.jpg`.
4. Needs-first review found P1/P2 hierarchy drift: Pipeline overpowered Work, Live's canvas sat below secondary operations detail, Decide did not privilege comparison when consequences existed, and Ask prompts were generic. Table now defaults to Needs focus, the Live workfield is primary, consequence-bearing decisions open on Compare, and Ask is route-aware.
5. Interaction verification found a P1 deep-link race and a P1 Space visibility issue. A synchronous query snapshot prevents trace resurrection during rapid lens changes, and the selected-work inspector stays visible while its lifecycle plane scrolls.
6. Final combined review found Ask too modal at 680px with a blurred source surface. The sidecar is now 560px with a quiet non-blurred contextual scrim; the composed result and source Work table remain legible together.
7. Final same-state comparison found no actionable P0/P1/P2 differences. Remaining differences are explicit truth, current data, and progressive-disclosure constraints, not unresolved visual defects.

## Implementation checklist

- [x] Seven top-level surfaces preserved.
- [x] Six approved concepts integrated into existing pages/lenses.
- [x] Real projection data only; unknowns stay unknown.
- [x] Simple-to-advanced disclosure preserved.
- [x] Primary interactions, loading, and error states work.
- [x] Desktop, tablet, and mobile overflow checked.
- [x] Full and focused combined-image comparisons completed.
- [x] No actionable P0/P1/P2 finding remains.

final result: passed

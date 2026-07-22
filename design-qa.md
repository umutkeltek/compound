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

## Viewport and normalization

- Desktop CSS viewport: `1536 × 1080`; implementation captures: `1536 × 1080` at density `1`, except the full-page Decide evidence (`1536 × 3480`), whose first `1536 × 1080` viewport was used for the full-view comparison.
- Responsive check: `390 × 844` at density `1`. Document width remained `390` for Work and Live. The final Space canvas keeps its `1180`-pixel workfield inside a `332`-pixel internal scroller; document width remains `390`.
- Sources are `1487 × 1058`. Full comparisons fit source and implementation viewport into equal `768 × 550` halves. Focused comparisons use semantic crops from each artifact and equal-width normalization; no finding is based on the small source/viewport density difference.
- State alignment: Today uses `Investigate`, Work uses the named `Table`, `Flow`, and `Space` lenses, Decide uses `Compare`, Live uses its default `Overview`, and Ask Compound shows the completed “Why is work blocked?” composition.

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
- Colors and visual tokens: state colors map to existing Compound tokens. Amber remains intervention/waiting, green remains live/available, blue remains selected/actionable, and purple remains evidence/decision context. No decorative color invents state.
- Image and asset fidelity: the concepts contain no raster product assets, logos, illustrations, or non-standard icons that needed extraction. The implementation uses the existing Compound mark and icon system; lifecycle rails and cards are native UI, not asset substitutes.
- Copy and content: real Compound projections replace concept placeholder records. Missing consequences, dependencies, execution detail, and historical paths are explicitly labeled unknown, unavailable, or not projected. Tambo is named only as a replaceable composition adapter and receives no mutation authority.
- Interactions and affordances: Table filter and search work; rows open the shared trace; Flow and Space update the URL; Space selection updates the persistent inspector; Decide Compare renders known and unknown consequences; Ask Compound composes only registered views and shows an error state on failure; `/flow` redirects to `/work?lens=flow`.
- Accessibility: controls use native buttons, tabs, labels, dialog semantics, visible focus treatment, and accessible card labels. State is not carried by color alone in table, inspector, decision, or Ask views.

Accepted intentional differences:

- The concepts show richer historical-looking connectors. The implementation draws current lifecycle position only because Compound does not project a trustworthy dependency history for these records.
- Today moves the detailed causal path to `Investigate` depth so the default briefing stays calm. The focused evidence confirms the same activity → leverage seam → projected unlock hierarchy.
- Live caps the primary timeline at six priority rows and links to complete Work rather than recreating a second backlog.
- Decide displays “Consequence not projected by the owner” instead of inventing the concept's example consequences.

## Browser and responsive verification

- Checked Today depth switching, Work lens URL state, Table filter/search, trace opening, Space card selection, Decide Compare, Ask open/close, two registered-view compositions, and `/flow` redirect.
- The initial route pass reported no browser console warnings or errors. After the final Space refinement, a `pageerror` watcher captured no error during Space selection and Ask composition; the production preview emitted no runtime error.
- Desktop document width: `1536` at a `1536` viewport. Tablet Work document width: `1024` at `1024`. Mobile Work and Live document width: `390` at `390`. Final mobile Space document width: `390` at `390`, with spatial overflow contained inside the lens.

## Comparison history

1. First pass found P2 information-order and responsive issues: ambient transitions appeared before the selected Work lens, Work tabs/table could widen the document, and Live's primary response waited on slow coordination evidence. The lens moved first, ambient evidence moved to Investigate depth, overflow was contained, and coordination now streams behind the primary Live response. Post-fix evidence: `live-pass2.jpg`, `work-table-pass2.jpg`, and `work-table-mobile-pass2.jpg`.
2. Second pass found P2 density drift: Live showed too many timeline rows, and Flow/Space showed six cards per lane. Live was capped at six priority rows with a complete-Work link; Flow and Space now prioritize three current/proof-bound items per lane. Post-fix evidence: `live-pass2.jpg`, `work-flow-pass3.jpg`, and `work-space-pass3.jpg`.
3. Combined source-versus-build review found a P2 distinction problem: Flow and Space were truthful but visually too similar. Space was rebuilt around harness rooms, current-position rails, and a persistent selected-work inspector. The first revised capture then exposed a P1 overlap between stacked cards; cards were compressed into readable single-line current-state rows with full details in the inspector. Post-fix evidence: `work-space-pass5.jpg`, `comparisons/work-space-pass5.jpg`, and `comparisons/focus-work-space.jpg`.
4. Final same-state comparison found no actionable P0/P1/P2 differences. Remaining differences are the explicit truth and progressive-disclosure constraints listed above, not unresolved visual defects.

## Implementation checklist

- [x] Seven top-level surfaces preserved.
- [x] Six approved concepts integrated into existing pages/lenses.
- [x] Real projection data only; unknowns stay unknown.
- [x] Simple-to-advanced disclosure preserved.
- [x] Primary interactions and error states work.
- [x] Desktop, tablet, and mobile overflow checked.
- [x] Full and focused combined-image comparisons completed.
- [x] No actionable P0/P1/P2 finding remains.

final result: passed

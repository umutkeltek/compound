# Compound console experience — design QA

Date: 2026-07-22

Scope: `console/**` on `codex/compound-console-experience-20260722`

Viewport: 1512 × 982, dark and light themes

## Product contract

- Seven needs-led surfaces are present: Today, Live, Work, Decide, Knowledge, System, Outcomes.
- Existing route URLs remain stable beneath contextual secondary navigation.
- Progressive depth is limited to the overview surfaces that have an explicit contract: Today and Live.
- Live animation is evidence-gated; configured harnesses alone never appear active.
- Ask Compound composes registered views only. Mutation authority remains outside the model boundary.

## Automated checks

- `bunx tsc --noEmit`: passed.
- `bunx next build`: passed; `/live` and every preserved route are in the production route manifest.
- Browser console and page error inventory after the route walk: empty.

## Browser route walk

| Route | Observed heading/result |
|---|---|
| `/` | Good evening (Today briefing) |
| `/live` | Live |
| `/work` | Work |
| `/reviews` | Decide |
| `/executors` | Agents |
| `/traffic` | Coordination |
| `/loops` | Feedback loops |
| `/memory` | Memory |
| `/standing-law` | Standing law |
| `/map` | Capabilities |
| `/map/lineage` | Lineage |
| `/sources` | Sources |
| `/routing` | Routing & cost |
| `/settings` | Settings |
| `/representation` | Representation |
| `/flow` | redirects to `/work` |
| `/work/work_e2700bcffc13` | real work trace rendered |
| `/map/lineage/work_e2700bcffc13` | lineage detail rendered |

## Interaction checks

- Live harness node → evidence drawer: passed.
- Ask Compound suggestion → registered “Needs you” view: passed.
- Depth disclosure: Understand shows 3/9 supporting regions, Glance 0/9, Technical 9/9: passed.
- Theme preference: Light and Dark both update the root theme and persisted preference: passed.
- Primary and contextual secondary navigation: passed during the route walk.

## Visual comparison

Compared against the approved adaptive-drawer, intent-canvas, and object-inspector concepts.
The implemented system preserves their useful grammar: dark observability canvas, cool domain
accents, amber operator seams, evidence drawers, explicit freshness, and readable machine identity.
It intentionally uses a truth-first 2D harness graph rather than decorative 3D: depth comes from
selection and evidence disclosure, while motion appears only for a reported live run.

Marketing capture baseline:
`~/.codex/visualizations/2026/07/22/019f89b4-32b7-73b0-b3b5-8a042496a6bc/marketing/live-dark.png`

## Verdict

final result: passed

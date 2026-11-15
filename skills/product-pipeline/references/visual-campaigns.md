# Executable visual campaigns

This workflow is consumed by the visuals branch. It uses the existing work,
product, release and signal owners. There is no second scheduler or campaign store.
The command prepares and checks artifacts; the governed executor runs image tools,
composition, capture and platform adapters. Preparation does not authorize publishing.

1. Read `compound product essence PRODUCT --json`, the current growth/hypothesis
   context and source evidence. Create one work-owned `campaign.json` using
   `compound.visual_campaign.v0.1` (fields: `internal/product/visual_campaign.go`).
   Preserve source revisions in `context.evidence_refs`. Define the audience,
   problem, truthful value and constraints. Establish one shared concept, palette,
   lighting, typography and layout. Propose scenes with distinct purposes, settings,
   actions and composition. A social game may call for friends; a private utility
   may call for a quiet individual moment. Neither becomes a global default.
2. Add actual image files with SHA256, role and rights evidence. Each scene needs
   style/product references. Every locale needs truthful localized app captures and
   transcreated copy, font and text direction. Never label an English capture as a
   localized one. Get exact dimensions, format, alpha, file-size and content rules
   from dated platform evidence. Record when these rules must be refreshed.
3. Run `compound product campaign prompts campaign.json`. Its JSON contains stable
   prompt IDs, actual file attachments, shared design instructions, scene-specific
   direction and separate locale/composition/export specifications. Resolve relative
   image paths against the campaign directory before attaching to a tool. Attach
   the files; merely mentioning them in text does not count. Choose the least-cost
   authorized image route that meets the brief. If comparing routes, send equivalent
   requests and references, preserve actual model identity/receipts/cost when known,
   and blind the creative review. Do not invent unavailable backend identity.
4. Generate language-light artwork. Composite original app captures and localized
   text deterministically using the project's existing screenshot/layout renderer.
   Reuse artwork across locales where appropriate; verify cultural fit, RTL layout,
   line breaks, glyph coverage and safe areas. The CLI does not paint pixels or
   provide OCR. An unsupported OCR language is unresolved, never a passing review.
   Inspect exported pixels at native size and thumbnail size. Upscaling alone does
   not prove native detail. Record each output's hash, prompt ID, actual attachments,
   model/render receipt and evidence-backed review of coherence, copy/layout, capture
   fidelity, platform rules and detail. Review is explicitly an attestation; the
   machine validates dimensions, file integrity, reference identity and completeness.
5. Run `compound product campaign check campaign.json`. Nonzero means repair the
   named failures. Missing exports remain visible, so interrupted work resumes by
   generating only missing/failed cells. Save the successful report, campaign JSON,
   references and asset bytes using the existing work artifact mechanism. Its
   `campaign_sha256` is the exact version. Never edit a version used in an experiment:
   new copy, scene, model output or asset means a new hash and experiment assignment.
6. Before exposure, predeclare the experiment ID/hypothesis, metric, control-set
   hash, fixed window, allocation, minimum samples and minimum absolute lift. Use a
   distinct experiment metric (for example `experiment_download_conversion`) so a
   treatment subgroup is never mistaken for the whole product's conversion metric.
   Record platform assignment binding both asset-set identities, product, locale,
   target and registration time. Check current release authority and existing tests.
   Missing experiment adapter or assignment evidence means prepared, not running.
7. Collect one observation per locale/target after the fixed window. The current
   adapter supports independent unique-user binary conversion counts, a two-arm
   concurrent comparison, a fixed horizon and 95% Newcombe/Wilson intervals. Each
   user contributes one exposure and at most one conversion. Sessions, repeated
   impressions, sequential peeking, multiple treatments, clusters and revenue need
   a suitable measurement adapter. Do not rename those denominators to fit.
   Preserve raw source bytes with SHA256, assignment evidence and confounders.
   The source is a `compound.campaign_exposure.v0.1` normalized export with
   `observation` (the same fields, but an empty `source`) and `evidence_refs` to
   the original platform export. Its counts and assignment must exactly match
   the observation being measured; arbitrary cited bytes cannot substantiate it.
   Record the whole package's effect; do not credit individual scenes or models.
8. Run `compound product campaign measure campaign.json observation.json`. Identity,
   window, counts, sample size and allocation checks precede a conclusion. Sparse,
   confounded or observational evidence cannot declare a causal win. Intervals are
   per stratum, not multiplicity-adjusted portfolio claims. If several locales are
   tested, predeclare the primary locale or use a separate multiplicity-aware review.
   The CLI verifies supplied evidence and bytes, not the honesty of a platform export.
9. Under the existing governed work, import the checked result with
   `compound product signal --campaign campaign.json --observation observation.json`.
   The existing product signal owner rejects conflicting results for the same
   experiment/locale/target. Product loops consume it, and
   `compound product review-plan --hypothesis ID --signal ID` prepares the existing
   hypothesis review. Retain/rollback/next-test goes through the existing action and
   release workflow. Generation success, creative preference and commercial impact
   remain separate evidence. Imported methods earn broader use only through relevant
   examples and repeated observed results, not prompt-completeness scores.

`campaign prompts`, `check` and `measure` are read-only and return JSON to stdout.
They do not initialize COMPOUND_HOME. `measure ... --signal` emits the native signal
document for inspection. All file-writing, rendering and release effects remain
governed work actions. Tests in `visual_campaign_test.go` execute synthetic image,
locale, corruption, false-win, signal import, hypothesis and growth-consumer cases;
they are software evidence, not a real conversion experiment.

# Epistemics Contract

Compound treats epistemic doctrine as an enforced contract, not ambient prose. The rows below are deliberately machine-parseable by `compound architecture doctor --strict`: each principle names the existing implementing object family and at least one CLI verb that proves the branch is observable.

| n | principle | implementing_object | proof_command |
|---|---|---|---|
| 1 | Expectations become explicit contracts before execution, so later agents can inspect included and excluded context instead of guessing. | internal/contract, internal/harness | compound contract compile |
| 2 | Work has one root identity from intent through receipt and transition; reports without a closed proof chain do not count as completion. | internal/work, internal/envelope | compound workbench |
| 3 | Determinism changes when evidence changes; contradiction evidence must reopen review instead of leaving stale promoted meaning ambient. | internal/mem | compound synthesis sweep |
| 4 | Capability selection is evidence-aware and auto-surfacing only; offers never execute without WorkEnvelope and policy proof. | internal/capgraph, internal/panel | compound capability surface |
| 5 | Routing is an epistemic choice as well as a cost choice; route evidence is never task success until joined to receipt state delta. | internal/routeevidence, internal/product | compound route outcome-review |
| 6 | History becomes evidence, evidence becomes adjudicated claims; stored does not mean true, and only promoted meaning enters ambient recall. | internal/knowledge, internal/distiller, internal/candidates | compound memory recall |
| 7 | Public core stays generic while external capability enters only through gated intake, review, and no-promotion evidence before activation. | internal/arch, internal/capgraph | compound architecture doctor |

Known open gaps stay packet-sized: expectation claims outside product loops belong to the EffectiveContract stage; directional generic/specific recall belongs to the directional graph packet; full scope lattice content belongs to the later scope-lattice work. These are not separate doctrine engines.

The matching importable CapabilityGraph doctrine nodes live in `fixtures/skills/epistemics-doctrine.yaml`. They use the existing `compound.skill_manifest.v0.1` adapter and `kind: doctrine`, so they can enter the graph through the same reviewed capability import path as other capability rows.

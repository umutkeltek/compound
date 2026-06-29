---
id: DEC-0042
title: Gate source mutations
status: accepted
domains: [architecture, governance]
supersedes: [DEC-0041]
replaced_by:
falsifier: A source adapter can mutate an owner without a policy decision.
review_trigger: Re-check after first source-specific adapter writes through a gate.
owner: compound
scope: public-core
---

# Gate source mutations

Source-specific adapters must enter Compound through evidence and policy gates.
The importer records the markdown as evidence first, then projects the decision
lifecycle without giving the source file direct authority.

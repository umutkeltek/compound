---
schema_version: compound.knowledge_page.v0.1
id: kg-compression-progress-paper-20261027
title: Driven by Compression Progress (arXiv 0812.4360), interestingness as the first derivative of compressibility
entity: compression-progress
owner: architecture
visibility: contextual
claims:
  - predicate: store_everything
    object: keep the entire raw history of actions, observations and reward signals; it is the only basis of everything that can later be known, so every explanation is built over the stored history and storage is never the place to save effort
    observation_date: "2009-04-15T00:00:00Z"
    claim_type: constraint
    confidence: 0.95
    derivation: extracted
    promotion_basis: curated_page
    qualifiers:
      domain: compression_progress
      source: arxiv-0812.4360v2
    evidence_quote: If you can store the data, do not throw it away!
  - predicate: compressor_is_explanation
    object: an adaptive compressor or predictor of the stored history is the agent's explanation of that history; any regularity may be used, and the shorter the description the more regular and better explained the data (the paper calls this subjective beauty)
    observation_date: "2009-04-15T00:00:00Z"
    claim_type: architecture
    confidence: 0.95
    derivation: extracted
    promotion_basis: curated_page
    qualifiers:
      domain: compression_progress
      source: arxiv-0812.4360v2
    evidence_quote: The compressed version of the data can be viewed as its simplifying explanation.
  - predicate: interestingness_is_first_derivative
    object: interestingness is the first derivative of compressibility; the reward is the number of bits saved when the compressor improves, computed by testing the old and the new compressor on the same history, and a compressor that already performs well earns nothing for that alone
    observation_date: "2009-04-15T00:00:00Z"
    claim_type: architecture
    confidence: 0.95
    derivation: extracted
    promotion_basis: curated_page
    qualifiers:
      domain: compression_progress
      source: arxiv-0812.4360v2
    evidence_quote: The important thing are the improvements of the compressor, not its compression performance per se.
  - predicate: error_reward_trap
    object: rewarding prediction error (the 1990 system) silently assumes the predictor improves wherever its error is high; in noisy environments the system then concentrates on the parts where error stays high forever because of noise, randomness or the predictor's own limits, so compressibility never improves; the 1991 fix rewards improvement instead
    observation_date: "2009-04-15T00:00:00Z"
    claim_type: constraint
    confidence: 0.95
    derivation: extracted
    promotion_basis: curated_page
    qualifiers:
      domain: compression_progress
      source: arxiv-0812.4360v2
    evidence_quote: one should not focus on the errors of the predictor, but on its improvements.
  - predicate: boredom_has_two_kinds
    object: data that is already compressible and data that will apparently stay incompressible (noise, or too costly to learn compared with other things) both yield zero progress and are boring; only still unknown but learnable regularity is interesting, and Shannon surprise is rejected because white noise is maximally surprising and teaches nothing
    observation_date: "2009-04-15T00:00:00Z"
    claim_type: constraint
    confidence: 0.95
    derivation: extracted
    promotion_basis: curated_page
    qualifiers:
      domain: compression_progress
      source: arxiv-0812.4360v2
    evidence_quote: Neither the arbitrary nor the fully predictable is truly novel or surprising
  - predicate: discovery_is_large_compression_step
    object: a discovery is an unusually large compression step; one short new law, like gravity, re-explains many earlier observations at once, so the size of a discovery is how much old history it absorbs
    observation_date: "2009-04-15T00:00:00Z"
    claim_type: architecture
    confidence: 0.9
    derivation: extracted
    promotion_basis: curated_page
    qualifiers:
      domain: compression_progress
      source: arxiv-0812.4360v2
    evidence_quote: An unusually large compression breakthrough deserves the name discovery.
  - predicate: no_ideal_ratio
    object: there is no fixed ideal ratio of expected to unexpected input; interestingness is the change in the bits needed to encode the data relative to this observer's current knowledge and the limits of its learning algorithm, so it cannot be read off the data alone and the same signal can interest one observer and bore another
    observation_date: "2009-04-15T00:00:00Z"
    claim_type: constraint
    confidence: 0.85
    derivation: extracted
    promotion_basis: curated_page
    qualifiers:
      domain: compression_progress
      source: arxiv-0812.4360v2
    evidence_quote: our alternative approach does not have to postulate an objective ideal ratio of this kind.
  - predicate: predictor_of_improvements
    object: the 1991 system added an explicit adaptive model of the predictor's improvements; noise produced wildly varying targets but in the long run did not change the predictor's parameters much, the improvement model learned that, and curiosity reward followed expected long-term predictor change instead of current error
    observation_date: "2009-04-15T00:00:00Z"
    claim_type: architecture
    confidence: 0.9
    derivation: extracted
    promotion_basis: curated_page
    qualifiers:
      domain: compression_progress
      source: arxiv-0812.4360v2
    evidence_quote: This model essentially learned to predict the predictor’s changes.
  - entity: compound
    predicate: attention_follows_error_volume
    object: every attention signal Compound serves today (the operator meter line, the overhead-tax report, the attention projection, battery verdicts) counts volume, so an explained red counts the same as a new one; of 221 failed proof results between 2026-10-11 and 2026-10-27 that carry a report artifact, a naive first-failing-line signature puts most under about 16 signatures (one missing result.junit.xml line 22 times across 4 consumers, a killed-by-signal line 7 times), and a private classification on 2026-10-25 found 60 of 80 reds (75 percent) were not code faults
    observation_date: "2026-10-27T00:00:00Z"
    claim_type: status
    confidence: 0.8
    derivation: inferred
    promotion_basis: curated_page
    qualifiers:
      domain: attention_signal
      source: compound-proof-results-20261027
    evidence_quote: the system will concentrate its search on those parts of the environment where it can always get high prediction errors due to noise or randomness
  - entity: compound
    predicate: explanations_live_outside_compound_stores
    object: the explanations of Compound's recurring reds live in private reports under docs_private/ and in one harness's auto-memory; Compound's own explanation stores (findings and the negatives projection) hold almost none of them, so no Compound reader can tell an explained red from a new one
    observation_date: "2026-10-27T00:00:00Z"
    claim_type: architecture
    confidence: 0.8
    derivation: inferred
    promotion_basis: curated_page
    qualifiers:
      domain: attention_signal
      source: compound-findings-negatives-20261027
    evidence_quote: which should be able to observe the current state of the adaptive compressor
  - entity: compound
    predicate: assimilated_memory_still_served
    object: 73 of 416 harness memory notes describe something already fixed, superseded or landed and are still served every session; nothing cools a note once its subject is assimilated, so ambient recall spends attention on data that is already compressible
    observation_date: "2026-10-27T00:00:00Z"
    claim_type: status
    confidence: 0.85
    derivation: inferred
    promotion_basis: curated_page
    qualifiers:
      domain: attention_signal
      source: harness-memory-index-20261027
    evidence_quote: It will get bored by things that already are subjectively compressible.
  - entity: compound
    predicate: discoveries_happen_by_hand
    object: Compound's largest compression steps were made by hand and never measured; a program map on 2026-10-24 superseded 8 memory notes, one group-writable (0775) checkout ancestor explained 32 act-test reds, and the finding that reds came from the machine and not the code explained about 50 of 80; no record says how many old observations each explanation absorbed
    observation_date: "2026-10-27T00:00:00Z"
    claim_type: status
    confidence: 0.85
    derivation: inferred
    promotion_basis: curated_page
    qualifiers:
      domain: attention_signal
      source: compound-discovery-history-20261027
    evidence_quote: Discoveries correspond to large data compression improvements
---

# Driven by Compression Progress (arXiv 0812.4360), interestingness as the first derivative of compressibility

Curated from Schmidhuber, *Driven by Compression Progress*, arXiv 0812.4360v2 (revised 15 April
2009), read in full on 2026-10-27. It maps the paper's key ideas onto Compound's stores.

## In one paragraph

Compound should point attention at failures that recur and are not yet explained, and measure
learning by how many old observations a new explanation absorbs. Today every attention signal
counts reds, and an explained red counts the same as a new one. The paper names this mistake: a
learner rewarded for error spends itself on noise it can never compress. A learner rewarded for
*improvement* (bits saved when its explanation of the stored history gets shorter) goes where
regularity is unknown but learnable, and is bored by both the known and pure noise.

## The paper against Compound today (measured 2026-10-27)

| Paper | Compound today | Verdict |
|---|---|---|
| Store everything | Event store with immutable `pfrs_`, `rec_` and `ratt_` records, content-addressed artifacts, receipts | Done |
| Keep a compressor (the explanation) | Memory claims and positions, `inbox/failure-patterns`, the `negatives` projection, lane beliefs, findings | Partial. These are explanations, but nothing measures how much history each one explains |
| Reward compression progress | The operator meter line, the overhead-tax report, the attention projection and battery verdicts all count volume: reds, holds, asks, commits, "19 proposals waiting" | **Missing. This is the gap.** Every red counts the same, explained or not |
| Error-reward trap (1990) | 221 failed proof results (2026-10-11 to 10-27) carry a report artifact. A naive first-failing-line signature shows about 16 signatures cover most of them: `result.junit.xml: no such file` 22 times across 4 consumers, `signal: killed` 7 times, `TestLiveUserPromptScaleStaysInsideNativeBudget` 8 times, `pii.phone.e164` on fixture digits 3 times. A private classification on 10-25 found 60 of 80 reds (75%) were not code faults | In the trap. Attention follows error volume, so 75% of it goes to noise or to reds already explained in a private report or one agent's memory |
| Boredom | Explanations live in private reports under `docs_private/` and in one harness's auto-memory, where 73 of 416 notes describe something already fixed, superseded or landed and are still served every session. Compound's own stores hold almost none of its red explanations | Missing |
| Discovery | A program map on 10-24 superseded 8 memory notes. One group-writable (0775) checkout ancestor explained 32 act-test reds. "The reds were the machine, not the code" explained about 50 of 80 | Happens by hand, never measured |

## What changes in Compound

Failed results group by signature: the first failing test, package timeout, resource kill or
error line, normalized so it carries no path or hash. Each signature is in one of three states.

| State | Meaning | Attention |
|---|---|---|
| `assimilated` | A finding names the signature id exactly (topic or `signature:` evidence ref), and its `repository:` refs cover every repository the signature was seen in (naming none covers a signature seen in one; `repository:*` declares a repository-independent cause such as a redact false positive or a load timeout, and `repositories_total` says how many the capped list hides) | None. No progress is left to earn |
| `learnable` | No explanation, and the signature recurs across different work items or days | First. The steep part of the learning curve |
| `singleton` | Seen once, or one work retried within a day; no explanation | Wait. Regularity and noise cannot be told apart yet, however many retries |

Rank follows that column: every `learnable` signature before any `singleton`, then
`assimilated`. It is attention by recurrence, not severity; a single data-integrity failure
can need action at once and this projection does not judge that. A `resource_shape` hint marks
timeouts and killed processes; within a state they rank after regular shapes because the
signature alone says less, though load, a leak or a broken host can sit behind one. **Progress** is the first derivative: the signatures that became `assimilated`
since the previous build, each weighted by its occurrences. One explanation that absorbs many
failed results is the paper's discovery. The headline is the assimilated share, now and before.

**What is not transferable as-is.** Compound cannot measure a compressor in bits, so occurrences
absorbed stand in for bits saved. The signature is a naive heuristic: two causes can share one,
one cause can split across two. A note that only names the test (`TestAlpha still fails, cause
unknown`), a negatives record that quotes the signature, and an exact finding whose scope misses a
repository are `related` evidence, listed on the signature but never counted as assimilated: a
mention is not an explanation, and an explanation is not a verified repair. `learnable` is a
crude prior, not a learned model.

## Slices, smallest first

Each slice names the executor whose next branch changes and the consumer that proves it is
alive; a slice without a live consumer is a document, not a change.

1. **`internal/interest`, `compound interest build|list` (this landing).** Read-only projection
   over failed `pfrs_` results, report artifacts, findings and negatives. **Executor:** whoever
   triages reds next, operator or agent, starts from the top `learnable` signature, not the
   newest red. **Alive when:** the private meter line carries the projection's explained share
   and learnable count, read from `projections/interest.json`.
2. **Boredom pass over ambient recall.** Cool memory claims whose subject is already fixed,
   superseded or landed; the seam is `internal/mem` temperature. **Executor:** every harness
   session, whose recall stops spending budget on assimilated notes (73 of 416 today).
   **Alive when:** a rebuilt temperature projection ranks them below current claims.
3. **Orient join.** Surface the top `learnable` signature from the slice 1 projection at
   `compound orient`, joined like `appendNegatives` in `internal/contract/contract.go`
   (display-only, fail-open). **Executor:** any executor orienting in that repository sees the
   recurring red first. **Alive when:** a matching task's pack carries it and a disjoint one not.
4. **Interestingness for the candidate inbox and proposals.** Rank by observations absorbed
   (occurrences behind linked `signature:<id>` refs), not by age. **Executor:** the reviewer,
   whose next adjudication goes to the candidate that explains the most history, not the oldest
   of "19 proposals waiting". **Alive when:** `compound attention` orders review items that way.

## Provenance

- Quotes are brief and verbatim from arXiv 0812.4360v2. Compound numbers were measured 2026-10-27
  over the event store, proof report artifacts and the harness memory index (60 of 80 is a private
  classification from 2026-10-25); inferred claims are hypotheses the slice 1 projection tests.

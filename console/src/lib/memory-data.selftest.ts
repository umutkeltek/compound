/*
 * Falsifier for the keep-aside-drain lane (work_4033b98c5837, standing-law
 * queue side). Before this fix, `compound memory adjudicate --verdict park`
 * left no trace distinguishing "operator explicitly kept this aside" from "sat
 * parked since ingestion, nobody has looked" — so a card the operator dismissed
 * kept reappearing in the /memory standing-law "needs your call" queue forever.
 *
 * isStandingLawCandidate (memory-data.ts) is the authority filter behind that
 * queue. Run via scripts/memory-data-smoke.mjs.
 */

import { isStandingLawCandidate } from "./memory-data";

type Check = { name: string; ok: boolean; detail: string };

export function runMemoryDataSelfTest(): { ok: boolean; details: string[] } {
  const checks: Check[] = [];
  const push = (name: string, ok: boolean, detail: string) => checks.push({ name, ok, detail });

  // A raw harvested parked decision candidate (cand_9df96833602e's shape
  // BEFORE the operator ever ran `memory adjudicate` on it) — no
  // adjudicated_by. Must still render: nobody has ruled on it yet.
  const rawHarvested = {
    id: "cand_9df96833602e",
    entity: "decision:brain-80e65c8a4ccd",
    claim_type: "decision",
    state: "parked",
  };
  push(
    "untouched-parked-decision-still-renders",
    isStandingLawCandidate(rawHarvested) === true,
    `isStandingLawCandidate(untouched) = ${isStandingLawCandidate(rawHarvested)}`,
  );

  // The SAME candidate after the operator runs `compound memory adjudicate
  // <id> --verdict park` (AdjudicateAsOperator stamps adjudicated_by). Must
  // leave the needs-your-call queue — it stays parked as evidence, but the
  // operator already ruled "keep aside".
  const operatorParked = { ...rawHarvested, adjudicated_by: "operator", adjudicated_at: "2026-07-13T12:00:00Z" };
  push(
    "operator-adjudicated-park-excluded",
    isStandingLawCandidate(operatorParked) === false,
    `isStandingLawCandidate(operator-parked) = ${isStandingLawCandidate(operatorParked)}`,
  );

  // A second real falsifier id (cand_77d12450595e), same shape, same result —
  // the predicate is general, not keyed to one candidate.
  const operatorParked2 = {
    id: "cand_77d12450595e",
    entity: "decision:brain-9614adef7d64",
    claim_type: "decision",
    state: "parked",
    adjudicated_by: "operator",
    adjudicated_at: "2026-07-13T12:05:00Z",
  };
  push(
    "second-operator-adjudicated-park-excluded",
    isStandingLawCandidate(operatorParked2) === false,
    `isStandingLawCandidate(operator-parked-2) = ${isStandingLawCandidate(operatorParked2)}`,
  );

  // An automated (non-operator) park — e.g. panel apply — never stamps
  // adjudicated_by (mem.Store.Adjudicate, not AdjudicateAsOperator), so it
  // keeps rendering exactly like the untouched case.
  const automatedPark = { ...rawHarvested, state: "parked" };
  push(
    "automated-unstamped-park-still-renders",
    isStandingLawCandidate(automatedPark) === true,
    `isStandingLawCandidate(automated) = ${isStandingLawCandidate(automatedPark)}`,
  );

  // Whitespace-only adjudicated_by must not accidentally exclude (defensive:
  // the field is trimmed before the emptiness check).
  const whitespaceOnly = { ...rawHarvested, adjudicated_by: "   " };
  push(
    "whitespace-adjudicated-by-does-not-exclude",
    isStandingLawCandidate(whitespaceOnly) === true,
    `isStandingLawCandidate(whitespace) = ${isStandingLawCandidate(whitespaceOnly)}`,
  );

  // An operator-adjudicated candidate that is NOT parked (e.g. promoted) is
  // excluded on state alone — the adjudicated_by check must not be the only
  // gate keeping non-parked rows out.
  const promoted = { ...operatorParked, state: "promoted" };
  push(
    "non-parked-state-still-excluded",
    isStandingLawCandidate(promoted) === false,
    `isStandingLawCandidate(promoted) = ${isStandingLawCandidate(promoted)}`,
  );

  // Non-decision knowledge-corpus rows stay excluded regardless of
  // adjudicated_by (must not accidentally admit the 28k-row flood).
  const knowledgeRow = { id: "cand_x", entity: "x_like:123", claim_type: "", state: "parked" };
  push(
    "non-decision-parked-row-excluded",
    isStandingLawCandidate(knowledgeRow) === false,
    `isStandingLawCandidate(knowledge) = ${isStandingLawCandidate(knowledgeRow)}`,
  );

  const details = checks.map((c) => `${c.ok ? "ok  " : "FAIL"} ${c.name} — ${c.detail}`);
  return { ok: checks.every((c) => c.ok), details };
}

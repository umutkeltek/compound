package candidates

import (
	"os"
	"strings"
)

// ProducersEnv is the opt-in switch for the AUTOMATIC candidate producers:
// the scheduled or side-effect writers that file candidates nobody asked for
// (knowledge import and extraction, source observations, the distiller
// fan-out, session mining, capability-observation review). They write only
// when this is exactly "1".
//
// Why off by default: eight collect -> propose -> review loops all stopped at
// one unstaffed review step. Within one week the automatic writers added more
// than a thousand memory candidates, and the inbox has seen four
// adjudications ever. A candidate that no consumer will read is cost, not
// learning. The explicit paths stay open: `compound capture`, `compound
// memory candidate` and `compound memory lesson` never consult this switch.
const ProducersEnv = "COMPOUND_CANDIDATE_PRODUCERS"

// AutomaticProducersEnabled reports whether automatic candidate producers may
// write. Explicit, caller-requested candidate writes must not call this.
func AutomaticProducersEnabled() bool {
	return strings.TrimSpace(os.Getenv(ProducersEnv)) == "1"
}

// ProducerDisabledNote is the one-line reason a gated producer reports when it
// skipped its candidate write, so a skipped write is visible, not silent.
const ProducerDisabledNote = "automatic candidate producer off (set " + ProducersEnv + "=1 to enable)"

// BasisLessonOnWrite is the promotion basis stamped on a lesson claim. A lesson
// is written by the agent that tripped on the problem (writer = adjudicator,
// the consumer is the named skill), so it promotes on write. It is
// deliberately NOT in PromotionBases: the promote driver must never promote a
// candidate that merely claims this basis; only the lesson door may mint it.
const BasisLessonOnWrite = "lesson_on_write"

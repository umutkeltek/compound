package distill

import "regexp"

// A decision-turn is a user turn 3–2000 chars that carries decision language.
// Sessions below the worth gate are skipped before spending a reasoning model on
// them — signal, not size (a 30-byte decision beats a 700MB tool-noise session).
var decRe = regexp.MustCompile(`(?i)\b(let'?s|we (should|need|could|can|must|do)|go ahead|yes|no|instead|actually|decided?|lock|i (want|think|prefer|like|don'?t|need)|why|can (we|you)|should we|fix|implement|build|use|skip|stop|continue|proceed|approve|reject|change|rethink|make sure|do it|do that|don'?t|better|correct)\b`)

// IsDecisionTurn reports whether a turn is a user decision-turn (exported so the
// scope resolver can weight alias mentions found inside decisions more heavily).
func IsDecisionTurn(t Turn) bool {
	n := len(t.Text)
	return t.Role == RoleUser && n >= 3 && n <= 2000 && decRe.MatchString(t.Text)
}

// CountDecisionTurns is the worth signal.
func CountDecisionTurns(turns []Turn) int {
	n := 0
	for _, t := range turns {
		if IsDecisionTurn(t) {
			n++
		}
	}
	return n
}

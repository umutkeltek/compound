package reviewartifact

import "strings"

// terminalStatuses is the ONE owner of "this review artifact is finished".
//
// Before this existed the same six-word literal was copied into three
// identically-bodied private predicates under three different names —
// internal/home's terminalReviewStatus, internal/director's
// reviewStatusTerminal, and this package's terminalResolutionStatus — plus
// several inline switches. Every copy omitted "completed", so every consumer
// independently billed finished work to the operator as a pending decision.
//
// The measured cost of that omission on a live home: 107 of 191
// review_required rows were `status: completed` worktree-retirement RESULTS,
// i.e. governed work that had already run to completion. The operator's
// backlog read 56% larger than it was.
//
// A status word belongs here when some producer in this repo writes it to mean
// "there is nothing left to decide". Add to this set rather than to a caller;
// a seventh copy of the literal is how the sixth one drifted.
var terminalStatuses = map[string]bool{
	"ok":                     true,
	"applied":                true,
	"resolved":               true,
	"accepted":               true,
	"recorded":               true,
	"candidate_recorded":     true,
	"authorization_recorded": true,
	"completed":              true,
}

// TerminalStatus reports whether a review artifact status means the work is
// finished and carries no remaining operator decision. Callers in other
// packages must use this instead of re-listing the words.
func TerminalStatus(status string) bool {
	return terminalStatuses[strings.TrimSpace(status)]
}

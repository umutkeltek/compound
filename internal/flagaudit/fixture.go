package flagaudit

import "fmt"

// UnvalidatedFixtureCommand is the RED proof for the flag-validation ratchet:
// a first-party command handler with NO flag validation. Whatever argv it
// receives, it reports success — exactly the `compound work list` defect
// shape (unknown flags swallowed, exit 0, no complaint naming the flag).
// Wired into cmd/compound/command_registry.go as a real registry row
// ("flag-ratchet-fixture"), it must make TestFirstPartyFlagValidationRatchet
// FAIL by landing in the census beyond PinnedSilentAcceptors; the GREEN
// commit removes the row and this file again.
func UnvalidatedFixtureCommand(args []string) {
	fmt.Printf("flag-audit fixture: accepted %d unvalidated argument(s)\n", len(args))
}

package work

import "path/filepath"

// These helpers expose the current loose-engine layout only to same-package
// parity tests. Production code must use internal/eventstore operations.
func (s *Store) eventsLog() string {
	return filepath.Join(s.home, "events", "work.jsonl")
}

func (s *Store) envPath(id string) string {
	return filepath.Join(s.home, "events", id+".json")
}

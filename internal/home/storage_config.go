package home

import (
	"compound/internal/persist"
)

// StorageConfig returns the active storage backend configuration, read from
// COMPOUND_STORE and COMPOUND_DSN environment variables. The default is the
// file backend (zero-change from current behavior). When COMPOUND_STORE=postgres,
// the Postgres+pgvector backend (internal/persist/pg) is selected.
//
// This function is additive infrastructure (ADR-0005 Phase 1): it exposes the
// backend selection so that future Phase 2 code can branch on it. No existing
// home behavior changes — the file layout under COMPOUND_HOME remains authoritative
// until an explicit migration command is run.
func StorageConfig() persist.Config {
	return persist.ConfigFromEnv()
}

// StorageBackend returns just the active backend name ("file" or "postgres").
// Convenience wrapper for callers that only need the branch condition.
func StorageBackend() persist.Backend {
	return StorageConfig().Backend
}

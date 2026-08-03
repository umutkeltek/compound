package home

import "os"

// StorageSettings describes the active storage backend selection, read from
// the COMPOUND_STORE and COMPOUND_DSN environment variables. The default is
// the file backend (zero-change from current behavior). When
// COMPOUND_STORE=postgres, the Postgres+pgvector backend (internal/persist/pg)
// is selected.
//
// This is additive infrastructure (ADR-0005 Phase 1): it exposes the backend
// selection so that future Phase 2 code can branch on it. No existing home
// behavior changes — the file layout under COMPOUND_HOME remains authoritative
// until an explicit migration command is run.
//
// NOTE: this file deliberately does NOT import internal/persist for the typed
// view. persist imports internal/mem, and mem imports home — a persist import
// here closes an import cycle (home → persist → mem → home). The typed
// persist.Config is constructed at the CLI composition root instead.
type StorageSettings struct {
	// Backend is "file" (default) or "postgres".
	Backend string
	// DSN is the Postgres connection string (only meaningful when
	// Backend == "postgres").
	DSN string
}

// StorageConfig returns the active storage backend configuration from the
// environment.
func StorageConfig() StorageSettings {
	if os.Getenv("COMPOUND_STORE") == "postgres" {
		return StorageSettings{Backend: "postgres", DSN: os.Getenv("COMPOUND_DSN")}
	}
	return StorageSettings{Backend: "file"}
}

// StorageBackend returns just the active backend name ("file" or "postgres").
// Convenience wrapper for callers that only need the branch condition.
func StorageBackend() string {
	return StorageConfig().Backend
}

// Package persist owns storage backend selection.
//
// The file backend is the only backend. Compound once carried an experimental
// Postgres+pgvector adapter behind COMPOUND_STORE=postgres (ADR-0005); it was
// removed. What stays here is the refusal: selecting any store other than
// file, or leaving one of the removed adapter's variables set, returns an
// explicit error. A stale selection is never served silently from files.
//
// Classification: leaf (owns no authority object).
package persist

import "os"

// Backend names a storage engine.
type Backend string

// BackendFile is JSON files under COMPOUND_HOME: the default and only backend.
const BackendFile Backend = "file"

// removedPostgresEnv are the variables only the removed Postgres adapter read.
// One of them still set means a configuration from before the removal, and a
// DSN among them is a credential with no consumer left.
var removedPostgresEnv = []string{
	"COMPOUND_EXPERIMENTAL_POSTGRES",
	"COMPOUND_POSTGRES_PROFILE",
	"COMPOUND_DSN",
	"COMPOUND_VECTOR_DIMS",
}

// Config holds the storage backend selection.
type Config struct {
	// Backend is always BackendFile in a Config that ConfigFromEnv returned.
	Backend Backend
}

// ConfigError is a safe, typed backend-selection refusal. Reason and Recovery
// are fixed contract text; rejected environment values are never copied into
// the error.
type ConfigError struct {
	Field    string
	Reason   string
	Recovery string
}

func (e *ConfigError) Error() string {
	if e == nil {
		return ""
	}
	if e.Recovery == "" {
		return e.Field + ": " + e.Reason
	}
	return e.Field + ": " + e.Reason + "; " + e.Recovery
}

// DefaultConfig returns the file backend.
func DefaultConfig() Config {
	return Config{Backend: BackendFile}
}

// ConfigFromEnv reads the backend selection from the environment. Unset or the
// exact value "file" selects the file backend. Everything else fails closed:
// the removed Postgres selection, an unknown or misspelled value, and any
// variable of the removed adapter left set.
func ConfigFromEnv() (Config, error) {
	switch store := os.Getenv("COMPOUND_STORE"); store {
	case "", string(BackendFile):
	case "postgres":
		return Config{}, &ConfigError{
			Field:    "COMPOUND_STORE",
			Reason:   "the experimental Postgres backend was removed",
			Recovery: "unset COMPOUND_STORE; file is the only backend",
		}
	default:
		return Config{}, &ConfigError{
			Field:    "COMPOUND_STORE",
			Reason:   "unsupported COMPOUND_STORE value",
			Recovery: "unset COMPOUND_STORE; file is the only backend",
		}
	}
	for _, key := range removedPostgresEnv {
		if os.Getenv(key) != "" {
			return Config{}, &ConfigError{
				Field:    key,
				Reason:   "this variable configured the removed Postgres backend",
				Recovery: "unset it; file is the only backend",
			}
		}
	}
	return DefaultConfig(), nil
}

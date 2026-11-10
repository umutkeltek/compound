package persist

import (
	"errors"
	"strings"
	"testing"
)

const syntheticSecret = "persist-config-" + "sentinel"

func syntheticURLDSN() string {
	return strings.Join([]string{"postgres", "://operator:", syntheticSecret, "@db.example.invalid/compound"}, "")
}

func clearStoreEnv(t *testing.T) {
	t.Helper()
	t.Setenv("COMPOUND_STORE", "")
	for _, key := range removedPostgresEnv {
		t.Setenv(key, "")
	}
}

func requireConfigError(t *testing.T, err error, field, reason string) {
	t.Helper()
	var configErr *ConfigError
	if !errors.As(err, &configErr) {
		t.Fatalf("error = %T %v, want a typed ConfigError", err, err)
	}
	if configErr.Field != field || !strings.Contains(configErr.Reason, reason) {
		t.Fatalf("ConfigError = %+v, want field %q and reason containing %q", configErr, field, reason)
	}
	for _, forbidden := range []string{syntheticSecret, "db.example.invalid", "local-lab"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("refusal disclosed a rejected value %q: %v", forbidden, err)
		}
	}
}

func TestConfigFromEnvDefaultsToFileBackend(t *testing.T) {
	for _, store := range []string{"", "file"} {
		clearStoreEnv(t)
		t.Setenv("COMPOUND_STORE", store)
		cfg, err := ConfigFromEnv()
		if err != nil {
			t.Fatalf("COMPOUND_STORE=%q: %v", store, err)
		}
		if cfg != DefaultConfig() || cfg.Backend != BackendFile {
			t.Fatalf("COMPOUND_STORE=%q: config = %+v, want the file backend", store, cfg)
		}
	}
}

func TestConfigFromEnvRefusesRemovedPostgresBackend(t *testing.T) {
	// Bare selection, and the complete gate that used to open the adapter.
	for name, env := range map[string]map[string]string{
		"bare": {},
		"complete former gate": {
			"COMPOUND_EXPERIMENTAL_POSTGRES": "1",
			"COMPOUND_POSTGRES_PROFILE":      "local-lab",
			"COMPOUND_DSN":                   syntheticURLDSN(),
			"COMPOUND_VECTOR_DIMS":           "1536",
		},
	} {
		t.Run(name, func(t *testing.T) {
			clearStoreEnv(t)
			t.Setenv("COMPOUND_STORE", "postgres")
			for key, value := range env {
				t.Setenv(key, value)
			}
			_, err := ConfigFromEnv()
			requireConfigError(t, err, "COMPOUND_STORE", "Postgres backend was removed")
		})
	}
}

func TestConfigFromEnvRejectsUnknownBackendWithoutFileFallback(t *testing.T) {
	// Exact match only: a typo, padding or a case variant is a refusal, never
	// a quiet return to files.
	for _, store := range []string{"postgre", " file ", "FILE", " postgres ", "POSTGRES", "sqlite"} {
		t.Run(store, func(t *testing.T) {
			clearStoreEnv(t)
			t.Setenv("COMPOUND_STORE", store)
			_, err := ConfigFromEnv()
			requireConfigError(t, err, "COMPOUND_STORE", "unsupported COMPOUND_STORE value")
		})
	}
}

func TestConfigFromEnvRefusesStrayRemovedPostgresVariables(t *testing.T) {
	values := map[string]string{
		"COMPOUND_EXPERIMENTAL_POSTGRES": "1",
		"COMPOUND_POSTGRES_PROFILE":      "local-lab",
		"COMPOUND_DSN":                   syntheticURLDSN(),
		"COMPOUND_VECTOR_DIMS":           "1536",
	}
	for _, store := range []string{"", "file"} {
		for _, key := range removedPostgresEnv {
			t.Run(store+"/"+key, func(t *testing.T) {
				clearStoreEnv(t)
				t.Setenv("COMPOUND_STORE", store)
				t.Setenv(key, values[key])
				_, err := ConfigFromEnv()
				requireConfigError(t, err, key, "removed Postgres backend")
			})
		}
	}
}

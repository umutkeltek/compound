package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"compound/internal/envelope"
	"compound/internal/eventstore"
)

// Small CLI helpers the organ needs, mirrored from cmd/compound (actIDFor from
// act_cli.go, encodeJSON from bench.go, the flag readers from main.go). The
// cmd-budget ratchet keeps cmd/compound a thin shell, so the organ carries its
// own copies rather than importing the shell. Keep them byte-compatible with
// the canonical versions.

func hasFlag(args []string, name string) bool {
	for _, a := range args {
		if a == name {
			return true
		}
	}
	return false
}

func hasUsageFlag(args []string) bool {
	for _, a := range args {
		if a == "--help" || a == "-h" || a == "help" {
			return true
		}
	}
	return false
}

func valueFlag(args []string, name string) string {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(a, name+"=") {
			return strings.TrimPrefix(a, name+"=")
		}
	}
	return ""
}

func encodeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// actIDFor mints deterministic content-addressed ids, mirroring
// cmd/compound/act_cli.go's actIDFor.
func actIDFor(prefix string, parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "|")))
	return prefix + hex.EncodeToString(sum[:])[:12]
}

// recordEventstoreEnvelope persists a receipt/transition through the canonical
// event store and proves it resolvable via a post-write Read, mirroring the
// helper in cmd/compound/eventstore_rebaseline.go (which mirrors package act's
// own unexported recorder).
func recordEventstoreEnvelope(store *eventstore.Store, value envelope.Envelope) error {
	if err := value.Validate(); err != nil {
		return err
	}
	id := envelopeID(value)
	if id == "" {
		return fmt.Errorf("eventstore envelope id required for %T", value)
	}
	if _, err := store.PutJSON(id, value, " "); err != nil {
		return err
	}
	record, err := store.Read(id)
	if err != nil {
		return fmt.Errorf("prove event %s persistence through Store.Read: %w", id, err)
	}
	if record.ID != id {
		return fmt.Errorf("prove event %s persistence through Store.Read: resolved id %q", id, record.ID)
	}
	return nil
}

func envelopeID(value envelope.Envelope) string {
	switch typed := value.(type) {
	case *envelope.Receipt:
		return typed.ID
	case *envelope.Transition:
		return typed.ID
	default:
		return ""
	}
}

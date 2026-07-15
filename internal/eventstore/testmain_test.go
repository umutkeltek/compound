package eventstore

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
)

const (
	childEnv     = "EVENTSTORE_CHILD"
	childHomeEnv = "EVENTSTORE_CHILD_HOME"
	childIDEnv   = "EVENTSTORE_CHILD_ID"
	childJSONEnv = "EVENTSTORE_CHILD_JSON"
)

func TestMain(m *testing.M) {
	if os.Getenv(childEnv) == "1" {
		os.Exit(runPutChild())
	}
	os.Exit(m.Run())
}

func runPutChild() int {
	store, err := Open(os.Getenv(childHomeEnv))
	if err != nil {
		return 1
	}
	var record map[string]any
	if err := json.Unmarshal([]byte(os.Getenv(childJSONEnv)), &record); err != nil {
		return 1
	}
	_, err = store.PutJSON(os.Getenv(childIDEnv), record, "  ")
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrIDConflict):
		return 3
	default:
		return 1
	}
}
